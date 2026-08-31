package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type sdkInitResolvedService struct {
	target  workspaceServiceAddTarget
	version string
}

type sdkInitWorkspaceDraft struct {
	path   string
	config *configfile.WorkspaceConfig
	parsed *configfile.ParsedConfig
	plan   plannedConfig
}

type sdkInitLifecycle struct {
	client   *api.Client
	request  scaffoldRequest
	services []sdkInitResolvedService
	draft    *sdkInitWorkspaceDraft
}

var confirmSDKInit = promptSDKInitConfirmation

// runSDKInitWorkflow composes the existing workspace and SDK lifecycle helpers while retaining local-only empty scaffolds.
func runSDKInitWorkflow(cmd *cobra.Command, request scaffoldRequest, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) error {
	// An empty SDK skeleton remains editable offline because it has no service lifecycle to coordinate.
	if len(request.services) == 0 {
		return runLocalScaffold(cmd, request, resolver, bucketResolver)
	}
	// Existing lifecycle printers are human-oriented, so refuse structured output before any remote or local mutation.
	if wantsJSON(cmd) {
		return errors.New("sdk init with services does not support --json; use --no-input for non-interactive lifecycle orchestration")
	}
	return runServiceSDKInit(cmd, request, resolver, bucketResolver)
}

// runServiceSDKInit prepares one reviewed lifecycle and delegates its two ordered commit boundaries.
func runServiceSDKInit(cmd *cobra.Command, request scaffoldRequest, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) error {
	lifecycle, err := prepareSDKInitLifecycle(cmd, request)
	if err != nil {
		return err
	}
	confirmed, err := confirmSDKInitIfNeeded(lifecycle.request, lifecycle.services, lifecycle.draft != nil)
	if err != nil {
		return err
	}
	// A declined combined review must leave both workspace and SDK desired state untouched.
	if !confirmed {
		return errors.New("SDK initialization cancelled")
	}
	return commitSDKInitLifecycle(cmd, lifecycle, resolver, bucketResolver)
}

// prepareSDKInitLifecycle resolves immutable selections and plans any required workspace activation without mutating desired state.
func prepareSDKInitLifecycle(cmd *cobra.Command, request scaffoldRequest) (sdkInitLifecycle, error) {
	client, err := getAPIClient()
	if err != nil {
		return sdkInitLifecycle{}, err
	}
	resolvedRequest, resolvedServices, err := resolveSDKInitServices(request, client)
	if err != nil {
		return sdkInitLifecycle{}, err
	}
	draft, err := planSDKInitWorkspace(client, resolvedServices)
	if err != nil {
		return sdkInitLifecycle{}, err
	}
	if err := printSDKInitWorkspacePlan(cmd, draft); err != nil {
		return sdkInitLifecycle{}, err
	}
	return sdkInitLifecycle{client: client, request: resolvedRequest, services: resolvedServices, draft: draft}, nil
}

// commitSDKInitLifecycle applies workspace intent first and preserves that completed boundary if SDK creation later fails.
func commitSDKInitLifecycle(cmd *cobra.Command, lifecycle sdkInitLifecycle, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) error {
	workspaceApplied, err := applySDKInitWorkspace(cmd, lifecycle.client, lifecycle.draft)
	if err != nil {
		return err
	}
	if err := createAndApplySDKInit(cmd, lifecycle.client, lifecycle.request, resolver, bucketResolver); err != nil {
		// A completed workspace commit is intentionally preserved and reported instead of pretending a cross-boundary rollback occurred.
		if workspaceApplied {
			return fmt.Errorf("workspace activation was applied, but SDK initialization did not complete: %w", err)
		}
		return err
	}
	return nil
}

// runLocalScaffold preserves the original init behavior for SDK skeletons that declare no services.
func runLocalScaffold(cmd *cobra.Command, request scaffoldRequest, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) error {
	result, err := writeScaffold(request, resolver, bucketResolver)
	if err != nil {
		return err
	}
	recordGeneratedBindingCount(cmd.Context(), result.GeneratedBindingCount)
	recordAppliedChangeIf(cmd.Context(), cmd.CommandPath(), "config_file", result.Changed)
	return printScaffoldResult(cmd, result)
}

// resolveSDKInitServices selects canonical service identities, defaults omitted versions, and rewrites operation references consistently.
func resolveSDKInitServices(request scaffoldRequest, client *api.Client) (scaffoldRequest, []sdkInitResolvedService, error) {
	queries := make([]string, 0, len(request.services))
	for _, service := range request.services {
		queries = append(queries, service.name)
	}
	interactive := !nonInteractive()
	targets, err := resolveWorkspaceServiceAddTargetsWithOptions(queries, "", workspaceServiceResolutionOptions{
		interactive: interactive, confirmRegistry: false,
	})
	if err != nil {
		return scaffoldRequest{}, nil, err
	}
	resolved := make([]sdkInitResolvedService, 0, len(targets))
	aliases := make(map[string]string, len(request.services)+len(targets))
	for _, target := range targets {
		version, versionErr := resolveSDKInitServiceVersion(request.services, target, client)
		if versionErr != nil {
			return scaffoldRequest{}, nil, versionErr
		}
		resolved = append(resolved, sdkInitResolvedService{target: target, version: version})
		aliases[target.slug] = target.slug
		for _, requested := range target.requestedRefs {
			aliases[requested] = target.slug
		}
	}
	request.services = make([]scaffoldService, 0, len(resolved))
	for _, service := range resolved {
		request.services = append(request.services, scaffoldService{name: service.target.slug, version: service.version})
	}
	request.operations = rewriteSDKInitOperations(request.operations, aliases)
	request.selectAll = rewriteSDKInitNames(request.selectAll, aliases)
	return request, resolved, nil
}

// resolveSDKInitServiceVersion keeps explicit versions authoritative and otherwise pins one concrete existing or Registry version.
func resolveSDKInitServiceVersion(requested []scaffoldService, target workspaceServiceAddTarget, client *api.Client) (string, error) {
	version, err := explicitSDKInitServiceVersion(requested, target)
	if err != nil {
		return "", err
	}
	// The workspace's latest enabled version is deterministic and needs no Registry round-trip.
	if version == "" {
		version = strings.TrimSpace(target.version)
	}
	// Explicit and enabled versions are already concrete immutable selections.
	if version != "" {
		return version, nil
	}
	return latestSDKInitServiceVersion(target.slug, client)
}

// explicitSDKInitServiceVersion folds repeated aliases into one authoritative explicit version choice.
func explicitSDKInitServiceVersion(requested []scaffoldService, target workspaceServiceAddTarget) (string, error) {
	version := ""
	for _, service := range requested {
		// Only aliases resolved to this immutable service identity contribute a version choice.
		if service.name != target.slug && !containsString(target.requestedRefs, service.name) {
			continue
		}
		// Repeated aliases may not smuggle conflicting versions into one deduplicated service selection.
		if service.version != "" && version != "" && service.version != version {
			return "", fmt.Errorf("service %s was requested with conflicting versions %s and %s", target.slug, version, service.version)
		}
		if service.version != "" {
			version = service.version
		}
	}
	return version, nil
}

// latestSDKInitServiceVersion resolves and validates Registry latest before either desired-state document is written.
func latestSDKInitServiceVersion(slug string, client *api.Client) (string, error) {
	latest, err := client.GetServiceLatestVersion(slug)
	if err != nil {
		return "", fmt.Errorf("resolve latest version for service %s: %w", slug, err)
	}
	version := strings.TrimSpace(latest)
	// An empty Registry version would create a mutable or invalid SDK declaration.
	if version == "" {
		return "", fmt.Errorf("service %s has no public version to select", slug)
	}
	return version, nil
}

// rewriteSDKInitOperations maps user-entered aliases onto the canonical keys persisted in the SDK config.
func rewriteSDKInitOperations(operations []scaffoldOperation, aliases map[string]string) []scaffoldOperation {
	rewritten := make([]scaffoldOperation, 0, len(operations))
	for _, operation := range operations {
		// Unknown keys remain unchanged so the existing merge validator produces its established actionable error.
		if canonical, ok := aliases[operation.service]; ok {
			operation.service = canonical
		}
		rewritten = append(rewritten, operation)
	}
	return rewritten
}

// rewriteSDKInitNames maps select-all aliases onto the same canonical SDK service keys.
func rewriteSDKInitNames(names []string, aliases map[string]string) []string {
	rewritten := make([]string, 0, len(names))
	for _, name := range names {
		// Unknown keys remain unchanged for the existing selection validator.
		if canonical, ok := aliases[name]; ok {
			name = canonical
		}
		rewritten = append(rewritten, name)
	}
	return rewritten
}

// planSDKInitWorkspace prepares and plans only service versions that are not already enabled.
func planSDKInitWorkspace(client *api.Client, services []sdkInitResolvedService) (*sdkInitWorkspaceDraft, error) {
	additions := sdkInitWorkspaceAdditions(services)
	// No workspace commit or receipt is needed when every exact service version is already enabled.
	if len(additions) == 0 {
		return nil, nil
	}
	path, config, err := loadWorkspaceConfigForSync("")
	if err != nil {
		return nil, err
	}
	if err := mergeWorkspaceServiceAdditions(config, additions); err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return nil, err
	}
	parsed, err := configfile.Parse(data, path)
	if err != nil {
		return nil, err
	}
	plan, err := planOneConfig(client, parsed, client.BaseURL, "")
	if err != nil {
		return nil, err
	}
	return &sdkInitWorkspaceDraft{path: path, config: config, parsed: parsed, plan: plan}, nil
}

// sdkInitWorkspaceAdditions reuses the standalone workspace command DTO for versions that require activation.
func sdkInitWorkspaceAdditions(services []sdkInitResolvedService) []workspaceServiceConfigAddition {
	additions := make([]workspaceServiceConfigAddition, 0, len(services))
	for _, service := range services {
		// An exact enabled version already has execution authority and must not create another workspace plan.
		if containsString(service.target.enabledVersions, service.version) {
			continue
		}
		additions = append(additions, workspaceServiceConfigAddition{
			serviceName: service.target.slug, expectedServiceID: service.target.serviceID,
			identityKeys:     appendUniqueWorkspaceServiceRefs([]string{service.target.slug}, service.target.requestedRefs...),
			persistServiceID: service.target.configServiceID, version: service.version,
		})
	}
	return additions
}

// printSDKInitWorkspacePlan exposes the existing Engine-owned workspace summary before the combined confirmation.
func printSDKInitWorkspacePlan(cmd *cobra.Command, draft *sdkInitWorkspaceDraft) error {
	// Already-enabled selections have no workspace plan to review.
	if draft == nil {
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Workspace plan created for %s (Plan ID: %s)\n", draft.parsed.ConfigKey, draft.plan.receipt.PlanID)
	if err := printPlanSummary(cmd.OutOrStdout(), draft.plan.summary); err != nil {
		return err
	}
	printRequiredPermissions(cmd.OutOrStdout(), draft.plan.requiredPermissions)
	return nil
}

// confirmSDKInitIfNeeded asks once for the combined workspace and SDK intent while automation proceeds from exact flags.
func confirmSDKInitIfNeeded(request scaffoldRequest, services []sdkInitResolvedService, workspaceChange bool) (bool, error) {
	// --no-input and CI require exact resolvable flags and authorize no terminal interaction.
	if nonInteractive() {
		return true, nil
	}
	return confirmSDKInit(sdkInitConfirmationMessage(request, services, workspaceChange))
}

// sdkInitConfirmationMessage summarizes the one common service/operation case without hiding broader selections.
func sdkInitConfirmationMessage(request scaffoldRequest, services []sdkInitResolvedService, workspaceChange bool) string {
	selection := "the selected operations"
	// A single operation is more useful than a count in the common init path.
	if len(request.operations) == 1 && len(request.selectAll) == 0 {
		selection = request.operations[0].operation
	} else if len(request.selectAll) > 0 {
		selection = "the selected service operations"
	}
	// The common single-service activation receives the concise combined wording requested by terminal users.
	if workspaceChange && len(services) == 1 {
		service := services[0]
		return fmt.Sprintf("%s %s isn't in your workspace yet — enable it and create %s with %s?", service.target.slug, service.version, request.name, selection)
	}
	// Already-enabled and multi-service flows still receive one app-level confirmation.
	return fmt.Sprintf("Create %s with %s?", request.name, selection)
}

// promptSDKInitConfirmation renders the composite review as one affirmative lifecycle decision.
func promptSDKInitConfirmation(message string) (bool, error) {
	confirmed := true
	err := huh.NewConfirm().Title(message).Affirmative("Enable and create").Negative("Cancel").Value(&confirmed).Run()
	return confirmed, err
}

// applySDKInitWorkspace writes the reviewed draft, records its receipt, and invokes the existing workspace apply path.
func applySDKInitWorkspace(cmd *cobra.Command, client *api.Client, draft *sdkInitWorkspaceDraft) (bool, error) {
	// Already-enabled selections deliberately skip the workspace commit boundary.
	if draft == nil {
		return false, nil
	}
	if err := maybeWritePlanReceipt(draft.parsed, draft.plan.receipt, planOptions{}, 1); err != nil {
		return false, err
	}
	if err := writeWorkspaceConfig(draft.path, draft.config); err != nil {
		return false, err
	}
	parsed, err := configfile.ParseFile(draft.path)
	if err != nil {
		return false, err
	}
	prepared, err := prepareConfigApply(parsed, applyOptions{}, client.BaseURL)
	if err != nil {
		return false, err
	}
	warnIfProductionEnvironment(cmd)
	if err := applyPreparedConfig(client, prepared, false); err != nil {
		return false, err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "workspace")
	return true, nil
}

// createAndApplySDKInit writes the SDK draft, plans with credential remediation, records its receipt, and applies it.
func createAndApplySDKInit(cmd *cobra.Command, client *api.Client, request scaffoldRequest, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) error {
	result, err := writeScaffold(request, resolver, bucketResolver)
	if err != nil {
		return err
	}
	recordGeneratedBindingCount(cmd.Context(), result.GeneratedBindingCount)
	recordAppliedChangeIf(cmd.Context(), cmd.CommandPath(), "config_file", result.Changed)
	if err := printScaffoldResult(cmd, result); err != nil {
		return err
	}
	parsed, err := configfile.ParseFile(request.path)
	if err != nil {
		return err
	}
	planOpts := planOptions{
		interactive: !nonInteractive(), output: cmd.OutOrStdout(),
		auditCtx: cmd.Context(), auditAction: cmd.CommandPath(),
	}
	plan, err := planConfigWithRemediation(client, parsed, client.BaseURL, planOpts)
	if err != nil {
		return err
	}
	if err := maybeWritePlanReceipt(parsed, plan.receipt, planOpts, 1); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "SDK plan created for %s (Plan ID: %s)\n", parsed.ConfigKey, plan.receipt.PlanID)
	if err := printPlanSummary(cmd.OutOrStdout(), plan.summary); err != nil {
		return err
	}
	printRequiredPermissions(cmd.OutOrStdout(), plan.requiredPermissions)
	prepared, err := prepareConfigApply(parsed, applyOptions{}, client.BaseURL)
	if err != nil {
		return err
	}
	if err := applyPreparedConfig(client, prepared, false); err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "sdk")
	return nil
}
