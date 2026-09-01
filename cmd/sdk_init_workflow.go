package cmd

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
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

var stableAppVersionPattern = regexp.MustCompile(`^(v?)(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

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
	lifecycle, err := prepareSDKInitLifecycle(cmd, request, resolver, bucketResolver)
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
func prepareSDKInitLifecycle(cmd *cobra.Command, request scaffoldRequest, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) (sdkInitLifecycle, error) {
	client, err := getAPIClient()
	if err != nil {
		return sdkInitLifecycle{}, err
	}
	resolvedRequest, resolvedServices, err := resolveSDKInitServices(request, client)
	if err != nil {
		return sdkInitLifecycle{}, err
	}
	resolvedRequest, err = completeSDKInitOperationSelections(cmd, client, resolvedRequest, resolvedServices)
	// Operation discovery must finish before a workspace plan can imply that the full SDK intent is reviewable.
	if err != nil {
		return sdkInitLifecycle{}, err
	}
	resolvedRequest, err = completeSDKInitVersionExtension(client, resolvedRequest, resolver, bucketResolver)
	// Applied immutable versions must receive an explicit new identity before workspace or app planning can begin.
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

// completeSDKInitVersionExtension keeps one config file while assigning new immutable identity whenever its authored scope changes.
func completeSDKInitVersionExtension(client *api.Client, request scaffoldRequest, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) (scaffoldRequest, error) {
	// New files and ordinary creation require no inferred lifecycle decision.
	if !request.extend {
		return request, nil
	}
	data, err := os.ReadFile(request.path)
	// Extension must prove the current file before consulting remote app identity.
	if err != nil {
		return scaffoldRequest{}, err
	}
	current := &configfile.AppConfig{}
	// The existing document supplies the stable app name and currently authored immutable version.
	if err := decodeScaffoldDraft(data, request.path, request.kind, current); err != nil {
		return scaffoldRequest{}, err
	}
	_, changed, _, err := extendAppScaffoldData(request, data, resolver, bucketResolver)
	// Local conflicts and enrichment failures must stop before any version prompt or write.
	if err != nil {
		return scaffoldRequest{}, err
	}
	// An idempotent extension can safely plan the current version without manufacturing a successor.
	if !changed {
		return request, nil
	}
	// Repeating the file's immutable identity cannot authorize any real scope change.
	if request.versionSet && request.version == current.Version {
		return scaffoldRequest{}, fmt.Errorf("%s %s@%s already identifies the current config; use --version <new-version> for the successor", request.kind, current.Name, current.Version)
	}
	// An explicit different version overrides automatic successor inference after checking for a visible collision.
	if request.versionSet {
		exists, existsErr := sdkInitAppVersionExists(client, request.kind, current.Name, request.version)
		if existsErr != nil {
			return scaffoldRequest{}, existsErr
		}
		// A visible exact successor already owns immutable scope and must fail before the config write.
		if exists {
			return scaffoldRequest{}, fmt.Errorf("%s %s@%s already exists; choose another --version", request.kind, current.Name, request.version)
		}
		return request, nil
	}
	version, err := nextMinorAppVersion(current.Version)
	// Every real implicit extension advances stable SemVer; prerelease, build, overflow, or non-SemVer identities require an exact override.
	if err != nil {
		return scaffoldRequest{}, fmt.Errorf("cannot infer successor for %s %s@%s: %w; pass --version <new-version>", request.kind, current.Name, current.Version, err)
	}
	request.version = version
	request.versionSet = true
	successorExists, successorErr := sdkInitAppVersionExists(client, request.kind, current.Name, version)
	if successorErr != nil {
		return scaffoldRequest{}, successorErr
	}
	// Automatic inference must not select a visible immutable version that already exists.
	if successorExists {
		return scaffoldRequest{}, fmt.Errorf("inferred successor %s@%s already exists; pass --version <new-version>", current.Name, version)
	}
	return request, nil
}

// sdkInitAppVersionExists reports visible exact versions while treating not-found as unknown for later pre-write plan proof.
func sdkInitAppVersionExists(client *api.Client, kind configfile.ConfigKind, name, version string) (bool, error) {
	var err error
	// MCP and SDK/API app references use distinct Engine kind selectors despite sharing the extension workflow.
	if kind == configfile.KindMCP {
		_, err = client.ResolveMCPAppReference(name, version)
	} else {
		_, err = client.ResolveSDKAppReference(name, version)
	}
	// Engine deliberately conflates absence and inaccessibility, so false is not mutation authority and callers must preflight plan before writing.
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.Code == "resource_not_found" {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// nextMinorAppVersion deterministically advances one stable SemVer while preserving an optional v prefix.
func nextMinorAppVersion(current string) (string, error) {
	matches := stableAppVersionPattern.FindStringSubmatch(strings.TrimSpace(current))
	// Only stable three-part SemVer is safe to advance automatically.
	if matches == nil {
		return "", errors.New("current version is not a stable three-part SemVer")
	}
	minor, err := strconv.ParseUint(matches[3], 10, 64)
	if err != nil || minor == ^uint64(0) {
		return "", errors.New("current minor version cannot be incremented")
	}
	return matches[1] + matches[2] + "." + strconv.FormatUint(minor+1, 10) + ".0", nil
}

// commitSDKInitLifecycle applies workspace intent first and preserves that completed boundary if SDK creation later fails.
func commitSDKInitLifecycle(cmd *cobra.Command, lifecycle sdkInitLifecycle, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) error {
	workspaceApplied, err := applySDKInitWorkspace(cmd, lifecycle.client, lifecycle.draft)
	if err != nil {
		return err
	}
	if err := createAndApplySDKInit(cmd, lifecycle.client, lifecycle.request, workspaceApplied, resolver, bucketResolver); err != nil {
		var precommitErr *unifiedInitPrecommitError
		// Shared precommit failures already state the exact workspace receipt and local-file outcome.
		if errors.As(err, &precommitErr) {
			return err
		}
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

// completeSDKInitOperationSelections prompts only for services whose operation scope was not supplied explicitly.
func completeSDKInitOperationSelections(cmd *cobra.Command, client *api.Client, request scaffoldRequest, services []sdkInitResolvedService) (scaffoldRequest, error) {
	input := sdkInput
	for _, service := range services {
		// Explicit --operation and --select-all selections remain authoritative and bypass discovery prompts.
		if sdkInitServiceHasOperationSelection(request, service.target.slug) {
			continue
		}
		// Automation must declare its capability boundary rather than silently accepting a Registry-wide operation set.
		if nonInteractive() {
			return scaffoldRequest{}, fmt.Errorf("--no-input requires --operation '%s=<operationId>' or --select-all '%s'", service.target.slug, service.target.slug)
		}
		operations, selectAll, err := selectSDKOperationsForService(client, service.target.serviceID, service.target.slug, service.version, input, cmd.OutOrStdout())
		if err != nil {
			return scaffoldRequest{}, err
		}
		// The complete-surface choice is preserved as intent instead of expanding it into a hand-authored allowlist.
		if selectAll {
			request.selectAll = append(request.selectAll, service.target.slug)
			continue
		}
		for _, operation := range operations {
			request.operations = append(request.operations, scaffoldOperation{service: service.target.slug, operation: operation})
		}
	}
	return request, nil
}

// sdkInitServiceHasOperationSelection reports whether one resolved service already has an explicit capability boundary.
func sdkInitServiceHasOperationSelection(request scaffoldRequest, serviceName string) bool {
	for _, operation := range request.operations {
		// Any explicit operation for this service satisfies the initial selection requirement.
		if operation.service == serviceName {
			return true
		}
	}
	for _, selected := range request.selectAll {
		// Explicit select-all is a complete alternative to enumerating operation IDs.
		if selected == serviceName {
			return true
		}
	}
	return false
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
	action := "Create"
	actionInSentence := "create"
	// Extension confirmation language makes the immutable successor visible as an additive app action.
	if request.extend {
		action = "Extend"
		actionInSentence = "extend"
	}
	selection := "the selected operations"
	// A single operation is more useful than a count in the common init path.
	if len(request.operations) == 1 && len(request.selectAll) == 0 {
		selection = request.operations[0].operation
	} else if len(request.selectAll) > 0 {
		// Preserve the user's explicit complete-surface choice in the final confirmation language.
		selection = "all operations for the selected services"
	}
	appIdentity := request.name
	// An explicit or prompted successor must remain visible in the same final authorization as its expanded scope.
	if request.versionSet {
		appIdentity = fmt.Sprintf("%s version %s", request.name, request.version)
	}
	// The common single-service activation receives the concise combined wording requested by terminal users.
	if workspaceChange && len(services) == 1 {
		service := services[0]
		return fmt.Sprintf("%s %s isn't in your workspace yet — enable it and %s %s with %s?", service.target.slug, service.version, actionInSentence, appIdentity, selection)
	}
	// Already-enabled and multi-service flows still receive one app-level confirmation.
	return fmt.Sprintf("%s %s with %s?", action, appIdentity, selection)
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

// createAndApplySDKInit keeps the hidden compatibility alias on the same plan-before-publish lifecycle as root init.
func createAndApplySDKInit(cmd *cobra.Command, client *api.Client, request scaffoldRequest, workspaceApplied bool, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) error {
	return createPlanApplyUnifiedInit(cmd, client, unifiedInitModeSDK, request, workspaceApplied, false, resolver, bucketResolver)
}
