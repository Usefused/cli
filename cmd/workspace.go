package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

var workspaceInput io.Reader = os.Stdin

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Manage Fused workspace configuration",
	Long:  `Manage your central workspace policy, including allowed services and versions.`,
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var workspacePlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Plan workspace configuration",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.workspace.plan", func(cmd *cobra.Command, args []string) error {
		return runConfigPlan(planOptions{filter: filterWorkspace, jsonOut: workspacePlanJSON, receiptOut: workspacePlanReceiptOut})
	}),
}

var workspacePlanJSON bool
var workspacePlanReceiptOut string
var workspaceApplyPlanID string
var workspaceApplyReceiptPath string
var workspaceApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply workspace configuration",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.workspace.apply", func(cmd *cobra.Command, args []string) error {
		warnIfProductionEnvironment(cmd)
		return runConfigApply(withApplyAudit(cmd, applyOptions{
			filter:      filterWorkspace,
			planID:      workspaceApplyPlanID,
			receiptPath: workspaceApplyReceiptPath,
		}))
	}),
}

// warnIfProductionEnvironment is a best-effort UX nudge (Task 8,
// engine_workspace_registration_plan.md): `workspace apply` can activate or
// deactivate services workspace-wide, so before running it we check the
// Engine's /health echo of its --environment label and warn if it's
// "production" (the default -- most Engines will hit this unless an
// operator has explicitly labeled a non-production deployment). Never
// blocks or fails the apply: any error here (offline Engine, older Engine
// without the environment field, etc.) just means the warning is silently
// skipped.
func warnIfProductionEnvironment(cmd *cobra.Command) {
	client, err := getAPIClient()
	if err != nil {
		return
	}
	health, err := client.Health()
	if err != nil || health == nil || health.Environment == "" {
		return
	}
	if strings.EqualFold(health.Environment, "production") {
		fmt.Fprintf(cmd.OutOrStdout(), "Warning: applying against a production Engine (environment=%s).\n", health.Environment)
	}
}

var workspaceServicesCmd = &cobra.Command{
	Use:   "services",
	Short: "Manage workspace services",
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var workspaceServicesListInteractive bool
var workspaceServicesListQuery string
var workspaceServicesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workspace services",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.workspace.services.list", func(cmd *cobra.Command, args []string) error {
		if workspaceServicesListInteractive {
			if err := requireInteractive("omit --interactive to print the complete service list"); err != nil {
				return err
			}
		}
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		services, err := client.ListWorkspaceServices()
		if err != nil {
			return err
		}
		services = filterWorkspaceServices(services, workspaceServicesListQuery)
		if wantsJSON(cmd) {
			return writeJSON(cmd, services)
		}
		if len(services) == 0 {
			if strings.TrimSpace(workspaceServicesListQuery) != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "No visible workspace services found matching %q.\n", strings.TrimSpace(workspaceServicesListQuery))
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "No workspace services found.")
			}
			return nil
		}

		if workspaceServicesListInteractive {
			scanner := bufio.NewScanner(workspaceInput)
			for i, service := range services {
				fmt.Fprintf(cmd.OutOrStdout(), "%d. %s\n", i+1, service.ServiceName)
			}
			fmt.Fprint(cmd.OutOrStdout(), "Select service: ")
			if !scanner.Scan() {
				return fmt.Errorf("no service selected")
			}
			choiceStr := scanner.Text()
			choice, err := strconv.Atoi(choiceStr)
			if err != nil || choice < 1 || choice > len(services) {
				return fmt.Errorf("invalid service selection")
			}
			selected := services[choice-1]
			fmt.Fprintf(cmd.OutOrStdout(), "Enabled Versions for %s: %s\n", selected.ServiceName, strings.Join(workspaceServiceVersionNames(selected), ", "))
			return nil
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
		fmt.Fprintln(w, "SERVICE_NAME\tSLUG\tSERVICE_ID\tVERSION\tENABLED_VERSIONS")
		for _, service := range services {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", service.ServiceName, workspaceServiceSlugColumn(service), service.ServiceID, service.Version, strings.Join(workspaceServiceVersionNames(service), ", "))
		}
		w.Flush()
		return nil
	}),
}

// filterWorkspaceServices searches only the access-filtered rows returned by
// Engine. Every whitespace-separated term must occur in the service name or
// actionable slug, case-insensitively, so a query can combine provider and
// product words without implying that hidden workspace services were searched.
func filterWorkspaceServices(services []cliapi.WorkspaceService, query string) []cliapi.WorkspaceService {
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(terms) == 0 {
		return services
	}

	filtered := make([]cliapi.WorkspaceService, 0, len(services))
	for _, service := range services {
		haystack := strings.ToLower(service.ServiceName + " " + service.ServiceSlug)
		matches := true
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				matches = false
				break
			}
		}
		if matches {
			filtered = append(filtered, service)
		}
	}
	return filtered
}

// workspaceServiceSlugColumn prints what a user actually needs to act on a
// listed service -- its Registry slug, the argument `service show <slug>`
// and `workspace service operations <slug>` expect -- as an ADDITIONAL
// column alongside the existing UUID (never replacing it: several e2e flows
// assert the raw service ID appears in this command's output, and other
// tooling may already parse this column position). Falls back to "-" when
// the Engine couldn't resolve a slug for this row (e.g. Registry was
// unreachable at list time), so the column is never confused with an empty
// string meaning "this service genuinely has no slug".
func workspaceServiceSlugColumn(service cliapi.WorkspaceService) string {
	if service.ServiceSlug != "" {
		return service.ServiceSlug
	}
	return "-"
}

var workspaceHasCmd = &cobra.Command{
	Use:   "has <service-name>",
	Short: "Check if a service is available in the workspace",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.workspace.has", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		serviceName := args[0]
		services, err := client.ListWorkspaceServices(serviceName)
		if err != nil {
			return err
		}
		for _, service := range services {
			if service.ServiceName == serviceName {
				if wantsJSON(cmd) {
					return writeJSON(cmd, service)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Found service %s (Enabled Versions: %s)\n", service.ServiceName, strings.Join(workspaceServiceVersionNames(service), ", "))
				return nil
			}
		}
		return fmt.Errorf("service %s not found in workspace", serviceName)
	}),
}

var workspaceServiceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage a specific workspace service",
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var workspaceServiceVersionsCmd = newWorkspaceServiceCommand("versions <service-slug>", "List enabled service versions", "cli.workspace.service.versions", runWorkspaceServiceVersions)
var workspaceServiceOperationsCmd = newWorkspaceServiceCommand("operations <service-slug>", "List or search enabled service operations", "cli.workspace.service.operations", runWorkspaceServiceOperationsWithFlagVersion)
var workspaceServiceWebhooksCmd = newWorkspaceServiceCommand("webhooks <service-slug>", "List workspace webhook registrations", "cli.workspace.service.webhooks", runWorkspaceServiceWebhooks)
var workspaceServiceAddCmd = &cobra.Command{
	Use:   "add <service-query-or-slug> [service-query-or-slug...]",
	Short: "Find and add services to workspace configuration",
	Args:  cobra.MinimumNArgs(1),
	RunE: WithTelemetry("cli.workspace.service.add", func(cmd *cobra.Command, args []string) error {
		return runWorkspaceServiceAdd(cmd, args)
	}),
}
var workspaceServiceConnectCmd = newWorkspaceServiceCommand("connect <service-slug>", "Start an end-user connection", "cli.workspace.service.connect", runWorkspaceServiceConnectWithRequiredUser)
var workspaceServiceDeleteCmd = newWorkspaceServiceCommand("delete <service-slug>", "Delete a service from workspace configuration", "cli.workspace.service.delete", runWorkspaceServiceDelete)
var workspaceServiceDeprecateCmd = newWorkspaceServiceCommand("deprecate <service-slug>", "Schedule service deprecation", "cli.workspace.service.deprecate", runWorkspaceServiceDeprecateWithRequiredDate)

var workspaceServiceVersionCmd = &cobra.Command{
	Use:   "version",
	Short: "Manage enabled service versions",
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var workspaceServiceVersionAddCmd = newWorkspaceServiceVersionCommand("add <service-slug> <version>", "Enable a service version", "cli.workspace.service.version.add", runWorkspaceServiceVersionAdd)
var workspaceServiceVersionDeleteCmd = newWorkspaceServiceVersionCommand("delete <service-slug> <version>", "Delete an enabled service version", "cli.workspace.service.version.delete", runWorkspaceServiceVersionDelete)
var workspaceServiceVersionDeprecateCmd = newWorkspaceServiceVersionCommand("deprecate <service-slug> <version>", "Schedule service-version deprecation", "cli.workspace.service.version.deprecate", runWorkspaceServiceVersionDeprecate)

func newWorkspaceServiceCommand(use, short, spanName string, run func(*cobra.Command, string) error) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Args: cobra.ExactArgs(1),
		RunE: WithTelemetry(spanName, func(cmd *cobra.Command, args []string) error {
			return run(cmd, args[0])
		}),
	}
}

func newWorkspaceServiceVersionCommand(use, short, spanName string, run func(*cobra.Command, string, string) error) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Args: cobra.ExactArgs(2),
		RunE: WithTelemetry(spanName, func(cmd *cobra.Command, args []string) error {
			return run(cmd, args[0], args[1])
		}),
	}
}

func runWorkspaceServiceOperationsWithFlagVersion(cmd *cobra.Command, serviceSlug string) error {
	return runWorkspaceServiceOperations(cmd, serviceSlug, workspaceServiceOperationsVersion)
}

func runWorkspaceServiceConnectWithRequiredUser(cmd *cobra.Command, serviceSlug string) error {
	if workspaceServiceConnectUserRef == "" {
		return fmt.Errorf("flag --user-ref is required")
	}
	if workspaceServiceConnectBucket == "" {
		return fmt.Errorf("flag --bucket is required")
	}
	return runWorkspaceServiceConnect(cmd, serviceSlug)
}

func runWorkspaceServiceDeprecateWithRequiredDate(cmd *cobra.Command, serviceSlug string) error {
	if workspaceServiceDeprecateAt == "" {
		return fmt.Errorf("flag --at is required")
	}
	return runWorkspaceServiceDeprecate(cmd, serviceSlug)
}

// runWorkspaceServiceAdd composes batched resolution, one atomic config edit,
// and optional scoped activations without invoking full workspace mirroring.
func runWorkspaceServiceAdd(cmd *cobra.Command, serviceQueries []string) error {
	// Interactive selection is intentionally unavailable to agents and CI so an
	// ambiguous provider identity can never be guessed during automation.
	if workspaceServiceAddInteractive {
		if err := requireInteractive("omit --interactive to add a unique or exact service match automatically"); err != nil {
			return err
		}
	}
	targets, err := resolveWorkspaceServiceAddTargets(serviceQueries, workspaceServiceAddID, workspaceServiceAddInteractive)
	if err != nil {
		return err
	}
	version := strings.TrimSpace(workspaceServiceAddVersion)
	span := trace.SpanFromContext(cmd.Context())
	span.SetAttributes(attribute.Int("service_count", len(targets)), attribute.Bool("apply", workspaceServiceAddApply))
	recordWorkspaceServiceResolution(span, targets)
	if err := addWorkspaceServices(ConfigFile, workspaceServiceConfigAdditions(targets, version)); err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "workspace_config")
	printWorkspaceServiceAddTargets(cmd, targets, version)
	// Omitting --apply intentionally preserves the established config-as-code
	// workflow; only an explicit composite request crosses the mutation boundary.
	if !workspaceServiceAddApply {
		return nil
	}
	// Reuse the existing production safeguard for the composite's immediate
	// mutation path, even though the scoped endpoint cannot remove services.
	warnIfProductionEnvironment(cmd)
	return applyWorkspaceServiceAddTargets(cmd, targets, version)
}

// workspaceServiceConfigAdditions projects resolution details into the narrow
// config-edit DTO so authoring remains separate from discovery and activation.
func workspaceServiceConfigAdditions(targets []workspaceServiceAddTarget, version string) []workspaceServiceConfigAddition {
	additions := make([]workspaceServiceConfigAddition, 0, len(targets))
	for _, target := range targets {
		additions = append(additions, workspaceServiceConfigAddition{
			serviceName: target.slug, expectedServiceID: target.serviceID,
			persistServiceID: target.configServiceID, version: version,
		})
	}
	return additions
}

// recordWorkspaceServiceResolution emits bounded aggregate provenance without
// attaching user queries, provider names, service slugs, or credential context.
func recordWorkspaceServiceResolution(span trace.Span, targets []workspaceServiceAddTarget) {
	explicitCount, workspaceCount, registryCount := 0, 0, 0
	source := "none"
	for index, target := range targets {
		// The first source is the candidate aggregate value; any later difference
		// converts it to a bounded mixed marker instead of a user-derived list.
		if index == 0 {
			source = target.resolutionSource
		} else if source != target.resolutionSource {
			source = "mixed"
		}
		switch target.resolutionSource {
		case "explicit":
			explicitCount++
		case "workspace":
			workspaceCount++
		case "registry":
			registryCount++
		}
	}
	span.SetAttributes(
		attribute.String("service_resolution_source", source),
		attribute.Int("service_resolution.explicit_count", explicitCount),
		attribute.Int("service_resolution.workspace_count", workspaceCount),
		attribute.Int("service_resolution.registry_count", registryCount),
	)
}

// printWorkspaceServiceAddTargets retains the existing per-service result and
// direct UI link while supporting any number of resolved additions.
func printWorkspaceServiceAddTargets(cmd *cobra.Command, targets []workspaceServiceAddTarget, version string) {
	engineURL, engineURLErr := GetEngineURL()
	for _, target := range targets {
		fmt.Fprintln(cmd.OutOrStdout(), workspaceServiceAddResult(target, version, workspaceServiceAddApply))
		// UI links are best-effort output; config authoring remains successful
		// when the Engine URL is unavailable or cannot produce a safe route.
		if engineURLErr != nil {
			continue
		}
		viewURL := workspaceServiceViewURL(engineURL, target.serviceID)
		if viewURL != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "View %s: %s\n", target.slug, viewURL)
		}
	}
}

// applyWorkspaceServiceAddTargets reuses Engine's scoped additive mutation for
// each selected service, so unrelated workspace services can never be removed.
func applyWorkspaceServiceAddTargets(cmd *cobra.Command, targets []workspaceServiceAddTarget, version string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	committed := make([]string, 0, len(targets))
	for index, target := range targets {
		err := client.AddWorkspaceService(cliapi.AddWorkspaceServiceRequest{
			ServiceID: target.serviceID, ServiceName: target.slug, VersionTag: version,
		})
		// Stop on the first rejected scoped activation, but return every known state
		// and an exact idempotent retry for the failed and unattempted suffix.
		if err != nil {
			unattempted := workspaceServiceTargetSlugs(targets[index+1:])
			commitState := workspaceServiceFailedCommitState(err)
			span := trace.SpanFromContext(cmd.Context())
			span.SetAttributes(
				attribute.Int("workspace_service_apply.committed_count", len(committed)),
				attribute.Int("workspace_service_apply.unattempted_count", len(unattempted)),
				attribute.String("workspace_service_apply.failed_commit_state", commitState),
			)
			return &workspaceServiceApplyOutcomeError{
				committed: committed, failed: target.slug, failedCommitState: commitState,
				unattempted: unattempted,
				recovery:    workspaceServiceApplyRecoveryCommand(targets[index:], version, ConfigFile),
				cause:       err,
			}
		}
		recordAppliedChange(cmd.Context(), cmd.CommandPath(), "workspace_service_activation")
		fmt.Fprintf(cmd.OutOrStdout(), "Activated service %s in workspace\n", target.slug)
		committed = append(committed, target.slug)
	}
	return nil
}

// workspaceServiceTargetSlugs projects safe canonical refs for outcome display.
func workspaceServiceTargetSlugs(targets []workspaceServiceAddTarget) []string {
	slugs := make([]string, 0, len(targets))
	for _, target := range targets {
		slugs = append(slugs, target.slug)
	}
	return slugs
}

// workspaceServiceFailedCommitState is conservative across the HTTP boundary:
// authoritative client rejections are uncommitted, while 5xx/lost responses are unknown.
func workspaceServiceFailedCommitState(err error) string {
	var apiError *cliapi.APIError
	// Only 4xx proves validation or policy rejected the request before commit;
	// a 5xx can occur after persistence and must not invite an unsafe blind retry.
	if errors.As(err, &apiError) && apiError.HTTPStatus >= http.StatusBadRequest && apiError.HTTPStatus < http.StatusInternalServerError {
		return "not_committed"
	}
	return "unknown"
}

func runWorkspaceServiceDelete(cmd *cobra.Command, serviceSlug string) error {
	targetServiceID := ""
	if workspaceServiceRemoveForce {
		var err error
		targetServiceID, err = resolveForceRemoveServiceID(serviceSlug)
		if err != nil {
			return err
		}
	}
	if err := removeWorkspaceService(ConfigFile, serviceSlug); err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "workspace_config")
	if workspaceServiceRemoveForce {
		if err := runForceRemoveWorkspace(targetServiceID, ""); err != nil {
			return err
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Deleted service %s\n", serviceSlug)
	return nil
}

func runWorkspaceServiceDeprecate(cmd *cobra.Command, serviceSlug string) error {
	if err := addWorkspaceDeprecation(ConfigFile, serviceSlug, "", workspaceServiceDeprecateAt, workspaceServiceDeprecateReason); err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "workspace_config")
	fmt.Fprintf(cmd.OutOrStdout(), "Added deprecation for service %s at %s\n", serviceSlug, workspaceServiceDeprecateAt)
	return nil
}

func runWorkspaceServiceVersionAdd(cmd *cobra.Command, serviceSlug, version string) error {
	ctx := cmd.Context()
	span := trace.SpanFromContext(ctx)

	if version == "latest" {
		// Why: Resolving "latest" eagerly before writing to the workspace config ensures
		// that the workspace remains deterministic. If we stored "latest" in the YAML directly,
		// the same workspace configuration would drift silently as new registry versions are published.
		client, err := getAPIClient()
		if err != nil {
			return err
		}

		latestVersion, err := client.GetServiceLatestVersion(serviceSlug)
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Resolved 'latest' to version %s for service %s\n", latestVersion, serviceSlug)

		span.AddEvent("cli.workspace.service.version.add.latest_resolved", trace.WithAttributes(
			attribute.String("service", serviceSlug),
			attribute.String("resolved_version", latestVersion),
		))

		version = latestVersion
	}

	if err := addWorkspaceVersion(ConfigFile, serviceSlug, version); err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "workspace_config")
	fmt.Fprintf(cmd.OutOrStdout(), "Added version %s to service %s\n", version, serviceSlug)
	return nil
}

func runWorkspaceServiceVersionDelete(cmd *cobra.Command, serviceSlug, version string) error {
	targetServiceID := ""
	if workspaceServiceVersionRemoveForce {
		var err error
		targetServiceID, err = resolveForceRemoveServiceID(serviceSlug)
		if err != nil {
			return err
		}
	}
	if err := removeWorkspaceVersion(ConfigFile, serviceSlug, version); err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "workspace_config")
	if workspaceServiceVersionRemoveForce {
		if err := runForceRemoveWorkspace(targetServiceID, version); err != nil {
			return err
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Deleted version %s from service %s\n", version, serviceSlug)
	return nil
}

func runWorkspaceServiceVersionDeprecate(cmd *cobra.Command, serviceSlug, version string) error {
	if workspaceServiceVersionDeprecateAt == "" {
		return fmt.Errorf("flag --at is required")
	}
	if err := addWorkspaceDeprecation(ConfigFile, serviceSlug, version, workspaceServiceVersionDeprecateAt, workspaceServiceVersionDeprecateReason); err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "workspace_config")
	fmt.Fprintf(cmd.OutOrStdout(), "Added deprecation for version %s of service %s at %s\n", version, serviceSlug, workspaceServiceVersionDeprecateAt)
	return nil
}

var workspaceServiceAddVersion string
var workspaceServiceAddID string
var workspaceServiceAddInteractive bool
var workspaceServiceAddApply bool
var workspaceServiceRemoveForce bool
var workspaceServiceDeprecateAt string
var workspaceServiceDeprecateReason string
var workspaceServiceVersionRemoveForce bool
var workspaceServiceVersionDeprecateAt string
var workspaceServiceVersionDeprecateReason string
var workspaceServiceOperationsVersion string
var workspaceServiceOperationsQuery string
var workspaceServiceListFlags listFlags

// runWorkspaceServiceVersions resolves public identity in Registry, then
// requires the canonical ID to be present in Engine's approved workspace set.
func runWorkspaceServiceVersions(cmd *cobra.Command, serviceSlug string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	serviceID, err := resolveServiceIDFromSlug(client, serviceSlug)
	if err != nil {
		return err
	}
	workspaceService, err := workspaceServiceByID(client, serviceID, serviceSlug)
	if err != nil {
		return err
	}
	if wantsJSON(cmd) {
		return writeJSON(cmd, workspaceService.EnabledVersions)
	}
	printWorkspaceServiceVersions(cmd.OutOrStdout(), workspaceService)
	return nil
}

func runWorkspaceServiceOperations(cmd *cobra.Command, serviceSlug, version string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	serviceID, err := resolveServiceIDFromSlug(client, serviceSlug)
	if err != nil {
		return err
	}
	workspaceService, err := workspaceServiceByID(client, serviceID, serviceSlug)
	if err != nil {
		return err
	}
	resolvedVersion, err := resolveWorkspaceOperationVersion(workspaceService, version)
	if err != nil {
		return err
	}
	endpoints, err := readWorkspaceServiceOperations(cmd, client, serviceID, resolvedVersion)
	if err != nil {
		return err
	}
	if len(endpoints) == 0 {
		return fmt.Errorf("no operations found for service %s version %s", serviceSlug, resolvedVersion)
	}
	if wantsJSON(cmd) {
		return writeJSON(cmd, endpoints)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tMETHOD\tPATH")
	for _, endpoint := range endpoints {
		fmt.Fprintf(w, "%s\t%s\t%s\n", endpoint.Name, endpoint.Method, endpoint.Path)
	}
	w.Flush()
	return nil
}

func readWorkspaceServiceOperations(cmd *cobra.Command, client *cliapi.Client, serviceID, version string) ([]cliapi.Integration, error) {
	if strings.TrimSpace(workspaceServiceOperationsQuery) != "" || cmd.Flags().Changed("limit") || cmd.Flags().Changed("offset") {
		return client.SearchEndpointsPage(serviceID, version, workspaceServiceOperationsQuery, workspaceServiceListFlags.pageOptions())
	}
	// Why: Workspace membership/version validation happens above; this call
	// preserves the existing full-list behavior when the user is browsing
	// rather than searching a server-paginated endpoint set.
	return client.ServiceOperations(serviceID, version)
}

// runWorkspaceServiceWebhooks is the read-only visibility command
// (engine_owned_webhooks_plan.md, Task 8): it looks up a service's webhook
// registrations without requiring a workspace apply, and reconstructs each
// display URL the same way applyOneConfig's output does (Task 5) --
// appliedWebhookURL -- since the server only ever returns the opaque slug,
// never a full URL.
func runWorkspaceServiceWebhooks(cmd *cobra.Command, serviceSlug string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	serviceID, err := resolveServiceIDFromSlug(client, serviceSlug)
	if err != nil {
		return err
	}
	// Confirms the service is actually enabled in this workspace (not just
	// visible in the Registry) before asking the Engine for its webhooks --
	// same membership check runWorkspaceServiceOperations already does.
	if _, err := workspaceServiceByID(client, serviceID, serviceSlug); err != nil {
		return err
	}
	webhooks, err := client.ListWorkspaceWebhooks(serviceID)
	if err != nil {
		return err
	}
	results := workspaceWebhookResults(client.BaseURL, serviceSlug, webhooks)
	if wantsJSON(cmd) {
		return writeJSON(cmd, results)
	}
	if len(results) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No webhook registrations for service %s.\n", serviceSlug)
		return nil
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	// SIGNATURE is "set"/"none" only -- never the secret value -- so a user
	// can tell at a glance whether a registration verifies its provider's
	// signature without a second lookup.
	fmt.Fprintln(w, "LABEL\tURL\tSIGNATURE\tCREATED_AT")
	for _, webhook := range results {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", webhook.Label, webhook.URL, webhook.Signature, webhook.CreatedAt)
	}
	w.Flush()
	return nil
}

type workspaceWebhookResult struct {
	Label     string `json:"label"`
	URL       string `json:"url"`
	Signature string `json:"signature"`
	CreatedAt string `json:"created_at"`
}

func workspaceWebhookResults(baseURL, serviceSlug string, webhooks []cliapi.WorkspaceWebhook) []workspaceWebhookResult {
	results := make([]workspaceWebhookResult, 0, len(webhooks))
	for _, webhook := range webhooks {
		url := appliedWebhookURL(baseURL, cliapi.AppliedWebhookConfig{ServiceKey: serviceSlug, Label: webhook.Label, Slug: webhook.Slug})
		results = append(results, workspaceWebhookResult{Label: webhook.Label, URL: url, Signature: webhook.Signature, CreatedAt: webhook.CreatedAt})
	}
	return results
}

var workspaceServiceConnectBucket string
var workspaceServiceConnectUserRef string
var workspaceServiceConnectSDKReference string
var workspaceServiceConnectResourceInput []string
var workspaceServiceConnectScopes []string

// runWorkspaceServiceConnect starts auth from the workspace service boundary
// so connected credentials attach to the same bucket/scope runtime will use.
func runWorkspaceServiceConnect(cmd *cobra.Command, serviceSlug string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	serviceID, err := resolveServiceIDFromSlug(client, serviceSlug)
	if err != nil {
		return err
	}
	// Connect sessions are only valid for workspace-enabled services; this
	// keeps auth material attached to the same allowlist/bucket boundary the
	// Engine will enforce later at execution time.
	if _, err := workspaceServiceByID(client, serviceID, serviceSlug); err != nil {
		return err
	}
	// Buckets are user-facing by name in config/CLI, while the Engine route is
	// ID-scoped so a renamed or duplicate-looking label cannot target the
	// wrong credential container.
	bucketID, err := resolveExplicitBucketID(workspaceServiceConnectBucket)
	if err != nil {
		return err
	}
	sdkID := strings.TrimSpace(workspaceServiceConnectSDKReference)
	if sdkID != "" {
		if err := validateExactAppReference(sdkID, "workspace service connect --sdk"); err != nil {
			return err
		}
		// Exact version resolution prevents audit attribution from drifting when
		// another version is published under the same SDK family.
		sdkName, sdkVersion := parseSDKDownloadName(sdkID)
		sdkID, err = client.ResolveSDKAppReference(sdkName, sdkVersion)
		if err != nil {
			return err
		}
	}
	resourceInput, err := parseResourceInputFlags(workspaceServiceConnectResourceInput)
	if err != nil {
		return err
	}
	session, err := client.StartConnectSession(bucketID, serviceID, workspaceServiceConnectUserRef, sdkID, resourceInput, workspaceServiceConnectScopes)
	if err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "connect_session")
	fmt.Fprintf(cmd.OutOrStdout(), "%s\n", session.AuthorizeURL)
	return nil
}

// parseResourceInputFlags converts repeatable key=value flags into the map the
// Engine validates against the service's declared resource_input fields.
func parseResourceInputFlags(flags []string) (map[string]string, error) {
	values := make(map[string]string, len(flags))
	for _, flag := range flags {
		key, value, ok := strings.Cut(flag, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("resource input must use key=value")
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return values, nil
}

func workspaceServiceByID(client *cliapi.Client, serviceID, serviceSlug string) (cliapi.WorkspaceService, error) {
	// Engine applies the name filter before enrichment, which keeps this
	// membership check bounded even in workspaces with many services.
	services, err := client.ListWorkspaceServices(workspaceServiceLookupName(serviceSlug))
	if err != nil {
		return cliapi.WorkspaceService{}, err
	}
	for _, service := range services {
		if service.ServiceID == serviceID {
			return service, nil
		}
	}
	return cliapi.WorkspaceService{}, fmt.Errorf("service %s is not enabled in this workspace", serviceSlug)
}

// workspaceServiceLookupName keeps existing command call sites narrow while
// delegating qualified-reference grammar to the shared API helper.
func workspaceServiceLookupName(serviceSlug string) string {
	return cliapi.ServiceLookupName(serviceSlug)
}

func resolveWorkspaceOperationVersion(service cliapi.WorkspaceService, requested string) (string, error) {
	if requested != "" {
		if workspaceServiceHasVersion(service, requested) {
			return requested, nil
		}
		return "", fmt.Errorf("version %s for service %s is not enabled in this workspace", requested, service.ServiceName)
	}
	version := latestWorkspaceServiceVersion(service)
	if version == "" {
		return "", fmt.Errorf("service %s has no enabled versions", service.ServiceName)
	}
	return version, nil
}

func workspaceServiceVersionNames(service cliapi.WorkspaceService) []string {
	names := make([]string, len(service.EnabledVersions))
	for i, v := range service.EnabledVersions {
		names[i] = v.Version
	}
	return names
}

func workspaceServiceHasVersion(service cliapi.WorkspaceService, version string) bool {
	if service.Version == version {
		return true
	}
	for _, enabled := range service.EnabledVersions {
		if enabled.Version == version {
			return true
		}
	}
	return false
}

func latestWorkspaceServiceVersion(service cliapi.WorkspaceService) string {
	bestVersion := service.Version
	bestStamp := ""
	for _, enabled := range service.EnabledVersions {
		stamp := enabled.EnabledAt
		if stamp == "" {
			stamp = enabled.CreatedAt
		}
		if bestVersion == "" || stamp >= bestStamp {
			bestVersion = enabled.Version
			bestStamp = stamp
		}
	}
	return bestVersion
}

// resolveServiceIDFromSlug asks the Registry because provider-qualified slugs
// are public identities; callers still enforce workspace approval in Engine.
func resolveServiceIDFromSlug(client *cliapi.Client, serviceSlug string) (string, error) {
	service, err := client.GetServiceInfo(serviceSlug)
	if err != nil {
		return "", err
	}
	if service == nil || strings.TrimSpace(service.ID) == "" {
		return "", fmt.Errorf("service %s not found", serviceSlug)
	}
	// Exact lookup avoids loading every version merely to recover the stable
	// service ID shared by those versions.
	return service.ID, nil
}

func printWorkspaceServiceVersions(out io.Writer, service cliapi.WorkspaceService) {
	if len(service.EnabledVersions) == 0 {
		fmt.Fprintf(out, "No enabled versions for service %s.\n", service.ServiceName)
		return
	}
	for _, version := range service.EnabledVersions {
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", version.Version, version.ServiceVersionID, version.Status, version.EnabledAt)
	}
}

func resolveForceRemoveServiceID(serviceName string) (string, error) {
	if cfg, err := loadWorkspaceConfigForEdit(ConfigFile); err == nil {
		if service := cfg.Services[serviceName]; service.ServiceID != "" {
			return service.ServiceID, nil
		}
	}
	if _, err := uuid.Parse(serviceName); err == nil {
		return serviceName, nil
	}
	client, err := getAPIClient()
	if err != nil {
		return "", err
	}
	services, err := client.ListWorkspaceServices(serviceName)
	if err != nil {
		return "", err
	}
	for _, service := range services {
		if service.ServiceName == serviceName {
			return service.ServiceID, nil
		}
	}
	return "", fmt.Errorf("service %s is not enabled in this workspace", serviceName)
}

func runForceRemoveWorkspace(serviceID string, version string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	cfg, err := configfile.ParseFile(ConfigFile)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(cfg.Workspace)
	if err != nil {
		return err
	}
	planResp, err := client.PlanWorkspaceConfig(cfg.SourceHash, cfg.ConfigKey, raw)
	if err != nil {
		return err
	}
	actions, actionID, err := forceRemovePlanAction(planResp.Summary, serviceID, version)
	if err != nil {
		return err
	}
	if actionID != "" {
		if err := client.UpdateWorkspacePlanAction(planResp.PlanID, actions, actionID, "force_remove"); err != nil {
			return err
		}
	}

	return applyForceRemoveWorkspacePlan(client, cfg, planResp)
}

// applyForceRemoveWorkspacePlan reuses the normal apply material collection so
// force-remove cannot accidentally skip encrypted runtime credential writes.
func applyForceRemoveWorkspacePlan(client *cliapi.Client, cfg *configfile.ParsedConfig, planResp *cliapi.ConfigPlanResponse) error {
	sourceHash := planResp.SourceHash
	if sourceHash == "" {
		sourceHash = cfg.SourceHash
	}
	authMaterials, err := workspaceAuthMaterials(cfg)
	if err != nil {
		return err
	}
	profileMaterials, err := workspaceProfileMaterials(cfg)
	if err != nil {
		return err
	}
	bucketSecretMaterials, err := cfg.WorkspaceBucketSecretMaterials()
	if err != nil {
		return err
	}
	_, err = client.ApplyWorkspaceConfig(planResp.PlanID, sourceHash, authMaterials, profileMaterials, bucketSecretMaterials)
	return err
}

func forceRemovePlanAction(summary map[string]interface{}, serviceID string, version string) ([]map[string]any, string, error) {
	actions, err := workspacePlanActions(summary)
	if err != nil {
		return nil, "", err
	}
	for _, action := range actions {
		actionID, _ := action["id"].(string)
		if actionID != "" && actionRequiresForce(action, serviceID, version) {
			return actions, actionID, nil
		}
	}
	return actions, "", nil
}

func workspacePlanActions(summary map[string]interface{}) ([]map[string]any, error) {
	rawActions, _ := summary["actions"].([]interface{})
	actions := make([]map[string]any, 0, len(rawActions))
	for _, raw := range rawActions {
		action, ok := raw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("workspace plan action has unexpected shape")
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func actionRequiresForce(action map[string]any, serviceID string, version string) bool {
	requiresDecision, _ := action["requires_decision"].(bool)
	actionType, _ := action["type"].(string)
	actionServiceID, _ := action["service_id"].(string)
	actionVersion, _ := action["version"].(string)
	if !requiresDecision || actionServiceID != serviceID {
		return false
	}
	if version == "" {
		return actionType == "remove_service"
	}
	return actionType == "disable_service_version" && actionVersion == version
}

// init registers workspace commands and their command-scoped flags once.
func init() {
	RootCmd.AddCommand(workspaceCmd)

	workspaceCmd.AddCommand(workspacePlanCmd)
	workspacePlanCmd.Flags().BoolVar(&workspacePlanJSON, "json", false, "Print plan result JSON, including summary and notifications")
	workspacePlanCmd.Flags().StringVar(&workspacePlanReceiptOut, "receipt-out", "", "Write the plan receipt to a specific path")
	workspaceCmd.AddCommand(workspaceApplyCmd)
	workspaceApplyCmd.Flags().StringVar(&workspaceApplyPlanID, "plan-id", "", "Apply a specific remote plan ID")
	workspaceApplyCmd.Flags().StringVar(&workspaceApplyReceiptPath, "receipt", "", "Read a specific plan receipt")

	workspaceCmd.AddCommand(workspaceServicesCmd)
	workspaceServicesListCmd.Flags().BoolVarP(&workspaceServicesListInteractive, "interactive", "i", false, "Interactive service selection")
	workspaceServicesListCmd.Flags().StringVarP(&workspaceServicesListQuery, "q", "q", "", "Filter visible workspace services by name or slug")
	addJSONOutputFlag(workspaceServicesListCmd, workspaceHasCmd, workspaceServiceVersionsCmd, workspaceServiceOperationsCmd, workspaceServiceWebhooksCmd)
	workspaceServicesCmd.AddCommand(workspaceServicesListCmd)
	workspaceCmd.AddCommand(workspaceServiceCmd)
	workspaceCmd.AddCommand(workspaceHasCmd)

	workspaceServiceCmd.AddCommand(workspaceServiceVersionsCmd, workspaceServiceOperationsCmd, workspaceServiceWebhooksCmd, workspaceServiceAddCmd, workspaceServiceConnectCmd, workspaceServiceDeleteCmd, workspaceServiceDeprecateCmd, workspaceServiceVersionCmd)
	workspaceServiceVersionCmd.AddCommand(workspaceServiceVersionAddCmd, workspaceServiceVersionDeleteCmd, workspaceServiceVersionDeprecateCmd)

	workspaceServiceOperationsCmd.Flags().StringVar(&workspaceServiceOperationsVersion, "version", "", "Enabled version; omitted uses the latest enabled version")
	workspaceServiceOperationsCmd.Flags().StringVar(&workspaceServiceOperationsQuery, "q", "", "Search query")
	addListFlags(workspaceServiceOperationsCmd, &workspaceServiceListFlags)

	workspaceServiceAddCmd.Flags().StringVar(&workspaceServiceAddVersion, "version", "", "Version to enable; omitted resolves latest during plan or scoped activation")
	workspaceServiceAddCmd.Flags().StringVar(&workspaceServiceAddID, "service-id", "", "Registry service UUID to store in workspace config")
	workspaceServiceAddCmd.Flags().BoolVarP(&workspaceServiceAddInteractive, "interactive", "i", false, "Select and confirm a Registry service when it is not already enabled")
	workspaceServiceAddCmd.Flags().BoolVar(&workspaceServiceAddApply, "apply", false, "Activate only the added services after updating the config")

	workspaceServiceConnectCmd.Flags().StringVar(&workspaceServiceConnectBucket, "bucket", "", "Workspace bucket name or UUID (required)")
	workspaceServiceConnectCmd.Flags().StringVar(&workspaceServiceConnectUserRef, "user-ref", "", "Stable user reference (required)")
	workspaceServiceConnectCmd.Flags().StringVar(&workspaceServiceConnectSDKReference, "sdk", "", "Optional SDK name@version or app UUID for audit attribution")
	workspaceServiceConnectCmd.Flags().StringSliceVar(&workspaceServiceConnectResourceInput, "resource-input", nil, "Tenant input as key=value; repeat for multiple declared fields")
	workspaceServiceConnectCmd.Flags().StringArrayVar(&workspaceServiceConnectScopes, "scope", nil, "OAuth/OIDC scope to request; repeat to reduce provider consent")

	workspaceServiceDeleteCmd.Flags().BoolVar(&workspaceServiceRemoveForce, "force", false, "Force removal when the generated plan action is applied")
	workspaceServiceDeprecateCmd.Flags().StringVar(&workspaceServiceDeprecateAt, "at", "", "Deprecation effective date in YYYY-MM-DD (required)")
	workspaceServiceDeprecateCmd.Flags().StringVar(&workspaceServiceDeprecateReason, "reason", "", "Reason for deprecation")

	workspaceServiceVersionDeleteCmd.Flags().BoolVar(&workspaceServiceVersionRemoveForce, "force", false, "Force removal")
	workspaceServiceVersionDeprecateCmd.Flags().StringVar(&workspaceServiceVersionDeprecateAt, "at", "", "Deprecation effective date in YYYY-MM-DD (required)")
	workspaceServiceVersionDeprecateCmd.Flags().StringVar(&workspaceServiceVersionDeprecateReason, "reason", "", "Reason for deprecation")
}
