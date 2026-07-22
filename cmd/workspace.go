package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

var workspaceInput io.Reader = os.Stdin

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Manage Fused workspace configuration",
	Long:  `Manage your central workspace policy, including allowed services and versions.`,
}

var workspacePlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Plan workspace configuration",
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
	RunE: WithTelemetry("cli.workspace.apply", func(cmd *cobra.Command, args []string) error {
		warnIfProductionEnvironment(cmd)
		return runConfigApply(applyOptions{
			filter:      filterWorkspace,
			planID:      workspaceApplyPlanID,
			receiptPath: workspaceApplyReceiptPath,
		})
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
}

var workspaceServicesListInteractive bool
var workspaceServicesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workspace services",
	RunE: WithTelemetry("cli.workspace.services.list", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		services, err := client.ListWorkspaceServices()
		if err != nil {
			return err
		}
		if len(services) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No workspace services found.")
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

		for _, service := range services {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\n", service.ServiceName, workspaceServiceSlugColumn(service), service.ServiceID, service.Version, strings.Join(workspaceServiceVersionNames(service), ", "))
		}
		return nil
	}),
}

// workspaceServiceSlugColumn prints what a user actually needs to act on a
// listed service -- its Registry slug, the argument `service <slug> show`
// and `workspace service <slug> operations` expect -- as an ADDITIONAL
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
	Use:   "has <service_name>",
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
				fmt.Fprintf(cmd.OutOrStdout(), "Found service %s (Enabled Versions: %s)\n", service.ServiceName, strings.Join(workspaceServiceVersionNames(service), ", "))
				return nil
			}
		}
		return fmt.Errorf("service %s not found in workspace", serviceName)
	}),
}

var workspaceServiceCmd = &cobra.Command{
	Use:   "service <service-slug> [versions|operations|webhooks|add|connect|remove|deprecate|version] [args...]",
	Short: "Manage a specific workspace service",
	Args:  validateWorkspaceServiceArgs,
	// Why: Write to OTEL to audit user/agent-triggered execution of workspace service mutation/reads.
	RunE: WithTelemetry("cli.workspace.service", func(cmd *cobra.Command, args []string) error {
		return runWorkspaceServiceAction(cmd, args)
	}),
	ValidArgsFunction: completeWorkspaceServiceArgs,
}

func validateWorkspaceServiceArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) == 1 {
		return nil
	}
	action := args[1]
	if !isWorkspaceServiceAction(action) {
		return fmt.Errorf("unknown workspace service action %q", action)
	}
	if action == "version" {
		if len(args) < 3 {
			return fmt.Errorf("workspace service version requires an action (add, remove, deprecate)")
		}
		versionAction := args[2]
		if versionAction != "add" && versionAction != "remove" && versionAction != "deprecate" {
			return fmt.Errorf("unknown workspace service version action %q", versionAction)
		}
		if len(args) < 4 {
			return fmt.Errorf("workspace service version %s requires a version argument", versionAction)
		}
	}
	return nil
}

func runWorkspaceServiceAction(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	serviceSlug := args[0]
	if workspaceServiceShowVersions {
		return runWorkspaceServiceVersions(cmd, serviceSlug)
	}
	if len(args) == 1 {
		return cmd.Help()
	}
	if args[1] == "version" {
		return runWorkspaceServiceVersionAction(cmd, serviceSlug, args[2:])
	}
	action, ok := workspaceServiceActionHandlers()[args[1]]
	if !ok {
		return fmt.Errorf("unknown workspace service action %q", args[1])
	}
	return action(cmd, serviceSlug)
}

type workspaceServiceActionHandler func(*cobra.Command, string) error

func workspaceServiceActionHandlers() map[string]workspaceServiceActionHandler {
	return map[string]workspaceServiceActionHandler{
		"versions":   runWorkspaceServiceVersions,
		"operations": runWorkspaceServiceOperationsWithFlagVersion,
		"webhooks":   runWorkspaceServiceWebhooks,
		"add":        runWorkspaceServiceAdd,
		"connect":    runWorkspaceServiceConnectWithRequiredUser,
		"remove":     runWorkspaceServiceRemove,
		"deprecate":  runWorkspaceServiceDeprecateWithRequiredDate,
	}
}

func runWorkspaceServiceOperationsWithFlagVersion(cmd *cobra.Command, serviceSlug string) error {
	return runWorkspaceServiceOperations(cmd, serviceSlug, workspaceServiceOperationsVersion)
}

func runWorkspaceServiceConnectWithRequiredUser(cmd *cobra.Command, serviceSlug string) error {
	if workspaceServiceConnectUserRef == "" {
		return fmt.Errorf("flag --user-ref is required for connect action")
	}
	return runWorkspaceServiceConnect(cmd, serviceSlug)
}

func runWorkspaceServiceDeprecateWithRequiredDate(cmd *cobra.Command, serviceSlug string) error {
	if workspaceServiceDeprecateAt == "" {
		return fmt.Errorf("flag --at is required for deprecate action")
	}
	return runWorkspaceServiceDeprecate(cmd, serviceSlug)
}

// Why: Separation of concern. Version-specific commands are routed independently to prevent N+1 nesting.
func runWorkspaceServiceVersionAction(cmd *cobra.Command, serviceSlug string, args []string) error {
	versionAction := args[0]
	version := args[1]
	switch versionAction {
	case "add":
		return runWorkspaceServiceVersionAdd(cmd, serviceSlug, version)
	case "remove":
		return runWorkspaceServiceVersionRemove(cmd, serviceSlug, version)
	case "deprecate":
		if workspaceServiceVersionDeprecateAt == "" {
			return fmt.Errorf("flag --at is required for version deprecate action")
		}
		return runWorkspaceServiceVersionDeprecate(cmd, serviceSlug, version)
	default:
		return fmt.Errorf("unknown workspace service version action %q", versionAction)
	}
}

func runWorkspaceServiceAdd(cmd *cobra.Command, serviceSlug string) error {
	if err := addWorkspaceService(ConfigFile, serviceSlug, workspaceServiceAddID, workspaceServiceAddVersion); err != nil {
		return err
	}
	fmt.Printf("Added service %s with version %s\n", serviceSlug, workspaceServiceAddVersion)
	return nil
}

func runWorkspaceServiceRemove(cmd *cobra.Command, serviceSlug string) error {
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
	if workspaceServiceRemoveForce {
		if err := runForceRemoveWorkspace(targetServiceID, ""); err != nil {
			return err
		}
	}
	fmt.Printf("Removed service %s\n", serviceSlug)
	return nil
}

func runWorkspaceServiceDeprecate(cmd *cobra.Command, serviceSlug string) error {
	if err := addWorkspaceDeprecation(ConfigFile, serviceSlug, "", workspaceServiceDeprecateAt, workspaceServiceDeprecateReason); err != nil {
		return err
	}
	fmt.Printf("Added deprecation for service %s at %s\n", serviceSlug, workspaceServiceDeprecateAt)
	return nil
}

func runWorkspaceServiceVersionAdd(cmd *cobra.Command, serviceSlug, version string) error {
	if err := addWorkspaceVersion(ConfigFile, serviceSlug, version); err != nil {
		return err
	}
	fmt.Printf("Added version %s to service %s\n", version, serviceSlug)
	return nil
}

func runWorkspaceServiceVersionRemove(cmd *cobra.Command, serviceSlug, version string) error {
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
	if workspaceServiceVersionRemoveForce {
		if err := runForceRemoveWorkspace(targetServiceID, version); err != nil {
			return err
		}
	}
	fmt.Printf("Removed version %s from service %s\n", version, serviceSlug)
	return nil
}

func runWorkspaceServiceVersionDeprecate(cmd *cobra.Command, serviceSlug, version string) error {
	if err := addWorkspaceDeprecation(ConfigFile, serviceSlug, version, workspaceServiceVersionDeprecateAt, workspaceServiceVersionDeprecateReason); err != nil {
		return err
	}
	fmt.Printf("Added deprecation for version %s of service %s at %s\n", version, serviceSlug, workspaceServiceVersionDeprecateAt)
	return nil
}

func isWorkspaceServiceAction(action string) bool {
	switch action {
	case "versions", "operations", "webhooks", "add", "connect", "remove", "deprecate", "version":
		return true
	default:
		return false
	}
}

func completeWorkspaceServiceArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return completeWorkspaceServiceCandidates(toComplete), cobra.ShellCompDirectiveNoFileComp
	case 1:
		return filteredWorkspaceServiceActions(toComplete), cobra.ShellCompDirectiveNoFileComp
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

func completeWorkspaceServiceCandidates(toComplete string) []string {
	client, err := getAPIClient()
	if err != nil {
		return nil
	}
	services, err := client.ListWorkspaceServices()
	if err != nil {
		return nil
	}
	candidates := make([]string, 0, len(services))
	for _, service := range services {
		if service.ServiceID != "" && strings.HasPrefix(service.ServiceID, toComplete) {
			candidates = append(candidates, service.ServiceID+"\t"+service.ServiceName)
		}
	}
	return candidates
}

func filteredWorkspaceServiceActions(toComplete string) []string {
	actions := []string{"versions", "operations", "webhooks", "add", "connect", "remove", "deprecate", "version"}
	if toComplete == "" {
		return actions
	}
	out := make([]string, 0, len(actions))
	for _, action := range actions {
		if strings.HasPrefix(action, toComplete) {
			out = append(out, action)
		}
	}
	return out
}

var workspaceServiceAddVersion string
var workspaceServiceAddID string
var workspaceServiceRemoveForce bool
var workspaceServiceDeprecateAt string
var workspaceServiceDeprecateReason string
var workspaceServiceVersionRemoveForce bool
var workspaceServiceVersionDeprecateAt string
var workspaceServiceVersionDeprecateReason string
var workspaceServiceShowVersions bool
var workspaceServiceOperationsVersion string
var workspaceServiceOperationsQuery string
var workspaceServiceListFlags listFlags

func runWorkspaceServiceVersions(cmd *cobra.Command, serviceSlug string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	serviceID, err := resolveServiceIDFromSlug(client, serviceSlug)
	if err != nil {
		return err
	}
	services, err := client.ListWorkspaceServices()
	if err != nil {
		return err
	}
	for _, workspaceService := range services {
		if workspaceService.ServiceID == serviceID {
			printWorkspaceServiceVersions(cmd.OutOrStdout(), workspaceService)
			return nil
		}
	}
	return fmt.Errorf("service %s not found in workspace", serviceSlug)
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
	for _, endpoint := range endpoints {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", endpoint.Name, endpoint.Method, endpoint.Path)
	}
	return nil
}

func readWorkspaceServiceOperations(cmd *cobra.Command, client *cliapi.Client, serviceID, version string) ([]cliapi.Integration, error) {
	if strings.TrimSpace(workspaceServiceOperationsQuery) != "" {
		return client.SearchEndpointsPage(serviceID, version, workspaceServiceOperationsQuery, workspaceServiceListFlags.pageOptions())
	}
	if cmd.Flags().Changed("limit") || cmd.Flags().Changed("offset") {
		return nil, fmt.Errorf("--limit and --offset require --q for workspace service operations because only endpoint search is paginated server-side")
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
	if len(webhooks) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No webhook registrations for service %s.\n", serviceSlug)
		return nil
	}
	for _, wh := range webhooks {
		url := appliedWebhookURL(client.BaseURL, cliapi.AppliedWebhookConfig{ServiceKey: serviceSlug, Label: wh.Label, Slug: wh.Slug})
		fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\n", wh.Label, url, wh.CreatedAt)
	}
	return nil
}

var workspaceServiceConnectBucket string
var workspaceServiceConnectUserRef string
var workspaceServiceConnectSDKID string
var workspaceServiceConnectResourceInput []string

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
	bucketID, err := resolveBucketID(client, connectBucketName())
	if err != nil {
		return err
	}
	resourceInput, err := parseResourceInputFlags(workspaceServiceConnectResourceInput)
	if err != nil {
		return err
	}
	session, err := client.StartConnectSession(bucketID, serviceID, workspaceServiceConnectUserRef, workspaceServiceConnectSDKID, resourceInput)
	if err != nil {
		return err
	}
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

// connectBucketName keeps the CLI default aligned with config defaults while
// still allowing explicit bucket names/IDs for multi-tenant user stores.
func connectBucketName() string {
	if strings.TrimSpace(workspaceServiceConnectBucket) == "" {
		return "default"
	}
	return workspaceServiceConnectBucket
}

func workspaceServiceByID(client *cliapi.Client, serviceID, serviceSlug string) (cliapi.WorkspaceService, error) {
	services, err := client.ListWorkspaceServices()
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

func resolveServiceIDFromSlug(client *cliapi.Client, serviceSlug string) (string, error) {
	versions, err := client.ServiceVersions(serviceSlug)
	if err != nil {
		return "", err
	}
	for _, version := range versions {
		if version.ServiceID != "" {
			return version.ServiceID, nil
		}
	}
	return "", fmt.Errorf("service %s has no visible versions", serviceSlug)
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
	connectMaterials, err := workspaceConnectMaterials(cfg)
	if err != nil {
		return err
	}
	authMaterials, err := workspaceAuthMaterials(cfg)
	if err != nil {
		return err
	}
	_, err = client.ApplyWorkspaceConfig(planResp.PlanID, sourceHash, connectMaterials, authMaterials)
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

func init() {
	RootCmd.AddCommand(workspaceCmd)

	workspaceCmd.AddCommand(workspacePlanCmd)
	workspacePlanCmd.Flags().BoolVar(&workspacePlanJSON, "json", false, "Print plan receipt JSON instead of writing default receipt")
	workspacePlanCmd.Flags().StringVar(&workspacePlanReceiptOut, "receipt-out", "", "Write the plan receipt to a specific path")
	workspaceCmd.AddCommand(workspaceApplyCmd)
	workspaceApplyCmd.Flags().StringVar(&workspaceApplyPlanID, "plan-id", "", "Apply a specific remote plan ID")
	workspaceApplyCmd.Flags().StringVar(&workspaceApplyReceiptPath, "receipt", "", "Read a specific plan receipt")

	workspaceCmd.AddCommand(workspaceServicesCmd)
	workspaceServicesListCmd.Flags().BoolVarP(&workspaceServicesListInteractive, "interactive", "i", false, "Interactive service selection")
	workspaceServicesCmd.AddCommand(workspaceServicesListCmd)
	workspaceCmd.AddCommand(workspaceServiceCmd)
	workspaceCmd.AddCommand(workspaceHasCmd)

	workspaceServiceCmd.Flags().BoolVar(&workspaceServiceShowVersions, "versions", false, "List versions enabled in the workspace for this service slug; supports @provider/slug")
	workspaceServiceCmd.Flags().StringVar(&workspaceServiceOperationsVersion, "version", "", "Enabled workspace service version for the operations action; omitted uses the latest enabled version")
	workspaceServiceCmd.Flags().StringVar(&workspaceServiceOperationsQuery, "q", "", "Search query for the operations action")
	addListFlags(workspaceServiceCmd, &workspaceServiceListFlags)

	workspaceServiceCmd.Flags().StringVar(&workspaceServiceAddVersion, "add-version", "", "Version to enable; omitted resolves latest during plan")
	workspaceServiceCmd.Flags().StringVar(&workspaceServiceAddID, "service-id", "", "Registry service UUID to store in workspace config")

	workspaceServiceCmd.Flags().StringVar(&workspaceServiceConnectBucket, "bucket", "default", "Workspace credential bucket name or ID")
	workspaceServiceCmd.Flags().StringVar(&workspaceServiceConnectUserRef, "user-ref", "", "Stable user reference to attach to the connected provider account")
	workspaceServiceCmd.Flags().StringVar(&workspaceServiceConnectSDKID, "sdk-id", "", "Optional SDK UUID for audit attribution")
	workspaceServiceCmd.Flags().StringSliceVar(&workspaceServiceConnectResourceInput, "resource-input", nil, "Tenant input as key=value; repeat for multiple declared fields")

	workspaceServiceCmd.Flags().BoolVar(&workspaceServiceRemoveForce, "force", false, "Force removal when the generated plan action is applied")

	workspaceServiceCmd.Flags().StringVar(&workspaceServiceDeprecateAt, "at", "", "Deprecation effective date in YYYY-MM-DD")
	workspaceServiceCmd.Flags().StringVar(&workspaceServiceDeprecateReason, "reason", "", "Reason for deprecation")

	workspaceServiceCmd.Flags().BoolVar(&workspaceServiceVersionRemoveForce, "version-force", false, "Force removal")
	workspaceServiceCmd.Flags().StringVar(&workspaceServiceVersionDeprecateAt, "version-at", "", "Deprecation effective date in YYYY-MM-DD")
	workspaceServiceCmd.Flags().StringVar(&workspaceServiceVersionDeprecateReason, "version-reason", "", "Reason for deprecation")
}
