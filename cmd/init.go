package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

type unifiedInitMode string

const (
	unifiedInitModeSDK unifiedInitMode = "sdk"
	unifiedInitModeMCP unifiedInitMode = "mcp"
	unifiedInitModeAPI unifiedInitMode = "api"
)

type unifiedInitOptions struct {
	sdk         bool
	mcp         bool
	api         bool
	extend      bool
	services    []string
	operations  []string
	selectAll   []string
	version     string
	description string
	language    string
	bucket      string
}

type unifiedInitRunner func(*cobra.Command, unifiedInitMode, scaffoldRequest) error

type unifiedInitPrecommitError struct {
	cause error
}

var selectUnifiedInitMode = promptUnifiedInitMode
var requestUnifiedInitMCPDescription = promptUnifiedInitMCPDescription

// newUnifiedInitCommand creates the single guided entry point while keeping lifecycle implementation replaceable in focused tests.
func newUnifiedInitCommand() *cobra.Command {
	return newUnifiedInitCommandWithRunner(runUnifiedInitLifecycle)
}

// newUnifiedInitCommandWithRunner binds the common app-selection flags before delegating the resolved intent to one lifecycle runner.
func newUnifiedInitCommandWithRunner(runner unifiedInitRunner) *cobra.Command {
	opts := &unifiedInitOptions{version: defaultScaffoldVersion, language: defaultScaffoldLanguage}
	command := &cobra.Command{
		Use:   "init <app-name>",
		Short: "Create and start an SDK, direct API app, or MCP server",
		Long: `Create one working Fused app from Registry services.

Choose --sdk, --api, or --mcp. In a terminal, omitting the mode opens a short
selector. The command resolves services and operations, enables missing
workspace services, writes the durable config, plans, applies, and returns the
runtime outcome.`,
		Args: cobra.ExactArgs(1),
		RunE: WithTelemetry("cli.init", func(cmd *cobra.Command, args []string) error {
			mode, err := resolveUnifiedInitMode(opts)
			// Mode resolution must finish before kind-specific validation or any Engine call.
			if err != nil {
				return err
			}
			// MCP description completion is local and must precede request construction.
			if err := completeUnifiedInitDescription(mode, opts); err != nil {
				return err
			}
			request, err := buildUnifiedInitRequest(cmd, mode, args[0], opts)
			// Local intent errors must leave both the filesystem and remote workspace untouched.
			if err != nil {
				return err
			}
			return runner(cmd, mode, request)
		}),
	}

	command.Flags().BoolVar(&opts.sdk, "sdk", false, "Create a generated typed SDK and download its package")
	command.Flags().BoolVar(&opts.api, "api", false, "Create a direct REST execution app without generating a package")
	command.Flags().BoolVar(&opts.mcp, "mcp", false, "Create and deploy an Engine-hosted MCP server")
	command.Flags().BoolVar(&opts.extend, "extend", false, "Compatibility alias for 'fused-cli extend'; use --version for an applied successor")
	// Existing scripts retain --extend, while help directs new additive workflows to the root extend command.
	_ = command.Flags().MarkHidden("extend")
	command.Flags().StringSliceVar(&opts.services, "service", nil, "Registry service as <service>[=<version>]; repeatable")
	command.Flags().StringSliceVar(&opts.operations, "operation", nil, "Selected operation as <service>=<operationId>; repeatable")
	command.Flags().StringSliceVar(&opts.selectAll, "select-all", nil, "Service whose complete operation surface should be selected; repeatable")
	command.Flags().StringVar(&opts.version, "version", defaultScaffoldVersion, "App version")
	command.Flags().StringVar(&opts.language, "language", defaultScaffoldLanguage, "Generated SDK target language")
	command.Flags().StringVar(&opts.bucket, "bucket", "", "Existing bucket to bind to this app")
	command.Flags().StringVar(&opts.description, "description", "", "User-facing MCP server description")
	return command
}

// resolveUnifiedInitMode enforces one app outcome and prompts only when terminal input is available.
func resolveUnifiedInitMode(opts *unifiedInitOptions) (unifiedInitMode, error) {
	selected := make([]unifiedInitMode, 0, 3)
	// Each explicit flag is a complete mode choice; collecting them first produces one stable conflict error.
	if opts.sdk {
		selected = append(selected, unifiedInitModeSDK)
	}
	// API is an SDK execution app with code generation disabled, not a separate config kind.
	if opts.api {
		selected = append(selected, unifiedInitModeAPI)
	}
	// MCP remains a hosted runtime even though its onboarding lifecycle shares the same orchestration.
	if opts.mcp {
		selected = append(selected, unifiedInitModeMCP)
	}
	// Competing outcome flags would make package and runtime behavior ambiguous.
	if len(selected) > 1 {
		return "", errors.New("choose exactly one of --sdk, --api, or --mcp")
	}
	// One explicit mode is deterministic for both terminals and automation.
	if len(selected) == 1 {
		return selected[0], nil
	}
	// Automation must declare its output because it cannot answer the mode selector.
	if nonInteractive() {
		return "", errors.New("--no-input requires exactly one of --sdk, --api, or --mcp")
	}
	return selectUnifiedInitMode()
}

// promptUnifiedInitMode presents the three user outcomes without exposing their shared internal config shape.
func promptUnifiedInitMode() (unifiedInitMode, error) {
	selected := unifiedInitModeSDK
	err := huh.NewSelect[unifiedInitMode]().
		Title("What do you want to build?").
		Options(
			huh.NewOption("Typed SDK", unifiedInitModeSDK),
			huh.NewOption("MCP server", unifiedInitModeMCP),
			huh.NewOption("Direct API", unifiedInitModeAPI),
		).
		Value(&selected).
		Run()
	return selected, err
}

// completeUnifiedInitDescription obtains MCP identity prose before any remote plan or mutation is attempted.
func completeUnifiedInitDescription(mode unifiedInitMode, opts *unifiedInitOptions) error {
	// Description has protocol meaning only for an MCP initialize response.
	if mode != unifiedInitModeMCP {
		// Rejecting stray prose avoids implying it changes SDK or API runtime behavior.
		if strings.TrimSpace(opts.description) != "" {
			return errors.New("--description can only be used with --mcp")
		}
		return nil
	}
	// An explicit non-empty description remains authoritative and bypasses prompting.
	if strings.TrimSpace(opts.description) != "" {
		opts.description = strings.TrimSpace(opts.description)
		return nil
	}
	// Additive MCP extension can preserve the existing immutable description without prompting for it again.
	if opts.extend {
		return nil
	}
	// Non-interactive MCP creation must carry all immutable server identity fields explicitly.
	if nonInteractive() {
		return errors.New("--no-input with --mcp requires --description")
	}
	description, err := requestUnifiedInitMCPDescription()
	// Prompt cancellation or terminal failure must stop before service discovery.
	if err != nil {
		return err
	}
	description = strings.TrimSpace(description)
	// A blank result cannot produce a valid MCP config even if a custom prompt implementation returns it.
	if description == "" {
		return errors.New("MCP server description must not be empty")
	}
	opts.description = description
	return nil
}

// promptUnifiedInitMCPDescription collects the bounded user-facing purpose shown to MCP clients during initialization.
func promptUnifiedInitMCPDescription() (string, error) {
	var description string
	err := huh.NewInput().
		Title("Describe what this MCP server should help users do").
		CharLimit(500).
		Validate(func(value string) error {
			// Whitespace-only prose cannot serve as useful model routing context.
			if strings.TrimSpace(value) == "" {
				return errors.New("description is required")
			}
			return nil
		}).
		Value(&description).
		Run()
	return strings.TrimSpace(description), err
}

// buildUnifiedInitRequest maps every public mode onto the existing SDK/MCP scaffold contract without introducing an API resource kind.
func buildUnifiedInitRequest(cmd *cobra.Command, mode unifiedInitMode, name string, opts *unifiedInitOptions) (scaffoldRequest, error) {
	kind := configfile.KindSDK
	// MCP is the only mode whose durable config and target directory differ from SDK.
	if mode == unifiedInitModeMCP {
		kind = configfile.KindMCP
	}
	services, err := parseScaffoldServices(opts.services, false)
	// Service syntax is validated locally before any Registry resolution.
	if err != nil {
		return scaffoldRequest{}, err
	}
	// Top-level init promises a working runtime, so an empty editable skeleton belongs to the compatibility commands instead.
	if len(services) == 0 {
		return scaffoldRequest{}, errors.New("init requires at least one --service")
	}
	operations, err := parseScaffoldOperations(opts.operations)
	// Explicit operation syntax remains the same contract as resource-scoped init.
	if err != nil {
		return scaffoldRequest{}, err
	}
	selectAll, err := parseScaffoldNames("--select-all", opts.selectAll)
	// Duplicate or malformed select-all values must fail before path creation.
	if err != nil {
		return scaffoldRequest{}, err
	}
	path, err := scaffoldTargetPath(kind, strings.TrimSpace(name), ConfigFile)
	// Target validation prevents unsafe or ambiguous config writes.
	if err != nil {
		return scaffoldRequest{}, err
	}
	language := opts.language
	// Only generated SDKs select a package emitter; direct API and MCP outcomes must not silently ignore this flag.
	if mode != unifiedInitModeSDK && cmd.Flags().Changed("language") {
		return scaffoldRequest{}, errors.New("--language can only be used with --sdk")
	}
	// Hosted MCP configs omit the language field, while direct API keeps the SDK schema default with generation disabled.
	if mode == unifiedInitModeMCP {
		language = ""
	}
	return scaffoldRequest{
		kind: kind, name: strings.TrimSpace(name), path: path, extend: opts.extend,
		services: services, operations: operations, selectAll: selectAll,
		version: opts.version, description: opts.description, language: language, bucket: strings.TrimSpace(opts.bucket),
		versionSet: cmd.Flags().Changed("version"), languageSet: cmd.Flags().Changed("language"),
		descriptionSet: mode == unifiedInitModeMCP && strings.TrimSpace(opts.description) != "", bucketSet: cmd.Flags().Changed("bucket"),
		generate: mode == unifiedInitModeSDK, generateSet: mode == unifiedInitModeSDK || mode == unifiedInitModeAPI,
	}, nil
}

// runUnifiedInitLifecycle keeps the workspace and app receipt boundaries ordered behind one user confirmation.
func runUnifiedInitLifecycle(cmd *cobra.Command, mode unifiedInitMode, request scaffoldRequest) error {
	lifecycle, err := prepareSDKInitLifecycle(cmd, request, resolveScaffoldRequirements, resolveScaffoldBucket)
	// Resolution and read-only workspace planning must succeed before the combined review.
	if err != nil {
		return err
	}
	confirmed, err := confirmSDKInitIfNeeded(lifecycle.request, lifecycle.services, lifecycle.draft != nil)
	// Prompt failures cannot authorize either receipt boundary.
	if err != nil {
		return err
	}
	// A declined combined review must leave both desired-state files and remote state untouched.
	if !confirmed {
		return errors.New("initialization cancelled")
	}
	workspaceApplied, err := applySDKInitWorkspace(cmd, lifecycle.client, lifecycle.draft)
	// The app boundary must not start after a failed workspace commit.
	if err != nil {
		return err
	}
	if err := createPlanApplyUnifiedInit(cmd, lifecycle.client, mode, lifecycle.request, workspaceApplied, mode == unifiedInitModeSDK, resolveScaffoldRequirements, resolveScaffoldBucket); err != nil {
		var precommitErr *unifiedInitPrecommitError
		// Preparation and plan failures already carry precise workspace and local-state context.
		if errors.As(err, &precommitErr) {
			return err
		}
		// A committed workspace receipt remains valid even when the later app boundary fails.
		if workspaceApplied {
			return fmt.Errorf("workspace activation was applied, but %s initialization did not complete: %w", mode, err)
		}
		return err
	}
	return nil
}

// Error exposes the human precommit context while retaining the typed Engine cause through Unwrap.
func (err *unifiedInitPrecommitError) Error() string {
	return err.cause.Error()
}

// Unwrap preserves API error codes and transport causes for callers and structured telemetry.
func (err *unifiedInitPrecommitError) Unwrap() error {
	return err.cause
}

// createPlanApplyUnifiedInit plans one in-memory app candidate before publishing its config and applying the selected runtime outcome.
func createPlanApplyUnifiedInit(cmd *cobra.Command, client *api.Client, mode unifiedInitMode, request scaffoldRequest, workspaceApplied bool, downloadPackage bool, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) error {
	result, parsed, pendingWrite, err := prepareUnifiedInitPlanInput(mode, request, resolver, bucketResolver)
	// Candidate creation or extension must validate fully before app planning.
	if err != nil {
		return contextualizeUnifiedInitPrecommitFailure("configuration preflight", mode, request, workspaceApplied, err)
	}
	planOpts := planOptions{
		interactive: !nonInteractive(), output: cmd.OutOrStdout(),
		auditCtx: cmd.Context(), auditAction: cmd.CommandPath(),
	}
	plan, err := planConfigWithRemediation(client, parsed, client.BaseURL, planOpts)
	// Generated SDK onboarding may deterministically repair only the typed missing-pin condition, then replay the identical plan once.
	if err != nil && unifiedInitCanRefreshGenerationSnapshot(mode, parsed, err) {
		refreshed, refreshErr := refreshUnifiedInitGenerationSnapshots(cmd, client, parsed)
		// Refresh failure stops before config publication and reports any exact snapshots already completed.
		if refreshErr != nil {
			return contextualizeUnifiedInitPrecommitFailureAfterRefresh("generation snapshot refresh", mode, request, workspaceApplied, refreshed, refreshErr)
		}
		plan, err = planConfigWithRemediation(client, parsed, client.BaseURL, planOpts)
		// The one retry is authoritative; a second failure cannot trigger another refresh or publish the candidate.
		if err != nil {
			return contextualizeUnifiedInitPrecommitFailureAfterRefresh("app plan retry", mode, request, workspaceApplied, refreshed, err)
		}
	}
	// A failed app plan preserves either absence for creation or the exact existing extension target.
	if err != nil {
		return contextualizeUnifiedInitPrecommitFailure("app plan", mode, request, workspaceApplied, err)
	}
	// A successful plan proves exact successor availability and permissions before any local candidate is published.
	if result.Changed {
		// Extensions replace one accepted file atomically, while new apps retain create-only collision protection.
		if request.extend {
			if err := atomicWriteFile(request.path, pendingWrite, 0o644, scaffoldValidator(request)); err != nil {
				return err
			}
		} else {
			if err := atomicCreateFile(request.path, pendingWrite, 0o644, scaffoldValidator(request)); err != nil {
				return err
			}
		}
	}
	recordGeneratedBindingCount(cmd.Context(), result.GeneratedBindingCount)
	recordAppliedChangeIf(cmd.Context(), cmd.CommandPath(), "config_file", result.Changed)
	if err := printScaffoldResult(cmd, result); err != nil {
		return err
	}
	// Apply is authorized only after the exact app receipt is durable.
	if err := maybeWritePlanReceipt(parsed, plan.receipt, planOpts, 1); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s plan created for %s (Plan ID: %s)\n", strings.ToUpper(string(mode)), parsed.ConfigKey, plan.receipt.PlanID)
	// Human review output must remain complete before the app mutation starts.
	if err := printPlanSummary(cmd.OutOrStdout(), plan.summary); err != nil {
		return err
	}
	printRequiredPermissions(cmd.OutOrStdout(), plan.requiredPermissions)
	prepared, err := prepareConfigApply(parsed, applyOptions{}, client.BaseURL)
	// Receipt and Engine-target preflight must complete before calling the apply endpoint.
	if err != nil {
		return err
	}
	// Unified SDK creation requests package completion, while compatibility callers can preserve their historical apply-only outcome.
	if err := applyPreparedConfig(client, prepared, downloadPackage); err != nil {
		return err
	}
	// Direct API onboarding ends with an immediately usable Engine execution request rather than package instructions.
	if mode == unifiedInitModeAPI {
		printUnifiedInitAPINextStep(cmd, client, parsed)
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), string(parsed.Kind))
	return nil
}

// prepareUnifiedInitPlanInput returns a fully validated candidate without publishing either creation or extension bytes.
func prepareUnifiedInitPlanInput(mode unifiedInitMode, request scaffoldRequest, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) (scaffoldResult, *configfile.ParsedConfig, []byte, error) {
	if err := validateUnifiedInitModeRequest(mode, request); err != nil {
		return scaffoldResult{}, nil, nil, err
	}
	// Create-only intent must reject a collision before enrichment or the remote app plan can observe the candidate.
	if !request.extend {
		if err := ensureUnifiedInitTargetAbsent(request.path); err != nil {
			return scaffoldResult{}, nil, nil, err
		}
		candidate, generated, err := newScaffoldData(request, resolver, bucketResolver)
		if err != nil {
			return scaffoldResult{}, nil, nil, err
		}
		// The same semantic validator used by atomic publication proves the in-memory candidate before planning.
		if err := scaffoldValidator(request)(candidate); err != nil {
			return scaffoldResult{}, nil, nil, err
		}
		parsed, err := configfile.Parse(candidate, request.path)
		if err != nil {
			return scaffoldResult{}, nil, nil, err
		}
		result := scaffoldResult{Action: "created", Kind: request.kind, Path: request.path, Changed: true, GeneratedBindingCount: generated}
		return result, parsed, candidate, nil
	}
	data, err := os.ReadFile(request.path)
	// Extension cannot manufacture a missing target or plan against unknown source bytes.
	if err != nil {
		return scaffoldResult{}, nil, nil, err
	}
	updated, changed, generated, err := extendAppScaffoldData(request, data, resolver, bucketResolver)
	if err != nil {
		return scaffoldResult{}, nil, nil, err
	}
	candidate := data
	result := scaffoldResult{Action: "unchanged", Kind: request.kind, Path: request.path, Changed: changed, GeneratedBindingCount: generated}
	// A changed merge plans the exact candidate bytes that will later replace the file atomically.
	if changed {
		candidate = updated
		result.Action = "extended"
	}
	parsed, err := configfile.Parse(candidate, request.path)
	// Full semantic validation catches successor SemVer, language, and operation invariants before plan or write.
	if err != nil {
		return scaffoldResult{}, nil, nil, err
	}
	return result, parsed, candidate, nil
}

// ensureUnifiedInitTargetAbsent rejects every existing filesystem entry before any remote app plan is attempted.
func ensureUnifiedInitTargetAbsent(path string) error {
	_, err := os.Lstat(path)
	// Any existing file, directory, or symlink is a collision that unified init must never replace.
	if err == nil {
		return fmt.Errorf("config %s already exists; use 'fused-cli extend' to add to it: %w", path, os.ErrExist)
	}
	// Only definite absence authorizes construction of a new candidate.
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect config target %s: %w", path, err)
	}
	return nil
}

// contextualizeUnifiedInitPrecommitFailure adds actionable app-boundary state while preserving the exact underlying error.
func contextualizeUnifiedInitPrecommitFailure(stage string, mode unifiedInitMode, request scaffoldRequest, workspaceApplied bool, cause error) error {
	return contextualizeUnifiedInitPrecommitFailureAfterRefresh(stage, mode, request, workspaceApplied, nil, cause)
}

// contextualizeUnifiedInitPrecommitFailureAfterRefresh records completed exact snapshot repairs without obscuring the final typed cause.
func contextualizeUnifiedInitPrecommitFailureAfterRefresh(stage string, mode unifiedInitMode, request scaffoldRequest, workspaceApplied bool, refreshed []string, cause error) error {
	localState := fmt.Sprintf("no %s version was created and no config file was written", unifiedInitModeShortLabel(mode))
	// Extension failure preserves the accepted desired-state file instead of leaving no file.
	if request.extend {
		localState = fmt.Sprintf("no %s version was created and the existing config is unchanged", unifiedInitModeShortLabel(mode))
	} else if errors.Is(cause, os.ErrExist) {
		// A create collision preserves the pre-existing path even though the command was not an extension.
		localState = fmt.Sprintf("no %s version was created and the pre-existing config is unchanged", unifiedInitModeShortLabel(mode))
	}
	workspaceState := "no workspace activation receipt was applied by this run"
	// Workspace activation is a separately auditable boundary and is intentionally not rolled back with the app candidate.
	if workspaceApplied {
		workspaceState = "workspace activation was already applied under its separate receipt"
	}
	recovery := unifiedInitFailureRecoveryAfterRefresh(cause, len(refreshed) > 0)
	message := fmt.Sprintf("%s for %s failed during %s for selected services %s; %s; %s", unifiedInitModeLabel(mode), request.name, stage, unifiedInitSelectedServices(request), localState, workspaceState)
	// A completed refresh is a durable Engine-local snapshot mutation even when the subsequent app plan remains unsuccessful.
	if len(refreshed) > 0 {
		message += fmt.Sprintf("; exact generation snapshot refresh completed for [%s]", strings.Join(refreshed, ", "))
	}
	// Targeted recovery is appended only when the underlying structured code justifies it.
	if recovery != "" {
		message += ". Recovery: " + recovery
	}
	return &unifiedInitPrecommitError{cause: fmt.Errorf("%s: %w", message, cause)}
}

// unifiedInitFailureRecoveryAfterRefresh avoids recommending a refresh that this same lifecycle already completed.
func unifiedInitFailureRecoveryAfterRefresh(cause error, refreshCompleted bool) string {
	// Before any completed repair, the existing typed recovery remains the accurate next action.
	if !refreshCompleted {
		return unifiedInitFailureRecovery(cause)
	}
	var apiErr *api.APIError
	// Only a repeated missing-pin response needs different guidance after a proven exact refresh.
	if errors.As(cause, &apiErr) && apiErr.Code == "generation_contract_pin_unavailable" {
		return "the exact selected snapshot refresh completed, but Engine still did not retain the immutable API contract required for generation; inspect this workspace's Engine and Registry logs, or select another enabled version, then retry. This is not a credential or operation-selection failure"
	}
	return unifiedInitFailureRecovery(cause)
}

// unifiedInitModeLabel renders the user-facing outcome without leaking its shared SDK config representation.
func unifiedInitModeLabel(mode unifiedInitMode) string {
	// Public mode labels are deliberately uppercase to make the failed outcome scannable.
	switch mode {
	case unifiedInitModeSDK:
		return "SDK initialization"
	case unifiedInitModeAPI:
		return "API initialization"
	case unifiedInitModeMCP:
		return "MCP initialization"
	default:
		return "app initialization"
	}
}

// unifiedInitModeShortLabel returns the concrete resource noun used in local-state guarantees.
func unifiedInitModeShortLabel(mode unifiedInitMode) string {
	// Direct API remains an outcome label even though its durable config kind is SDK.
	switch mode {
	case unifiedInitModeSDK:
		return "SDK"
	case unifiedInitModeAPI:
		return "API"
	case unifiedInitModeMCP:
		return "MCP"
	default:
		return "app"
	}
}

// unifiedInitSelectedServices returns deterministic slug@version context for support and retry decisions.
func unifiedInitSelectedServices(request scaffoldRequest) string {
	selected := make([]string, 0, len(request.services))
	for _, service := range request.services {
		version := strings.TrimSpace(service.version)
		// A missing version is kept explicit rather than implying that Registry latest was resolved.
		if version == "" {
			version = "<unresolved>"
		}
		selected = append(selected, service.name+"@"+version)
	}
	sort.Strings(selected)
	// Operation-only extensions may carry no newly resolved service selection in the request.
	if len(selected) == 0 {
		return "[none explicitly added]"
	}
	return "[" + strings.Join(selected, ", ") + "]"
}

// unifiedInitFailureRecovery adds targeted operator guidance for the two dependency failures seen during onboarding.
func unifiedInitFailureRecovery(cause error) string {
	var apiErr *api.APIError
	// Non-API failures retain their own message without speculative remote remediation.
	if !errors.As(cause, &apiErr) {
		return ""
	}
	// A missing generation pin requires a Registry contract fix or a different immutable provider version.
	if apiErr.Code == "generation_contract_pin_unavailable" {
		return "the selected enabled service snapshot does not retain the immutable API contract required to generate a typed SDK; refresh or publish that Registry version, or select another enabled version, then retry. This is not a credential or operation-selection failure"
	}
	// GraphQL dependency failures belong in the mono-workspace Engine/Registry path, where workspace owners can inspect complete logs safely.
	if apiErr.Code == "graphql_dependency_failed" {
		return "confirm the Engine can reach Registry, inspect this workspace's Engine logs for the dependency failure, and retry"
	}
	return ""
}

// writeUnifiedInitScaffold delegates every mode to the scaffold's single validated atomic create-or-extend write.
func writeUnifiedInitScaffold(mode unifiedInitMode, request scaffoldRequest, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) (scaffoldResult, error) {
	if err := validateUnifiedInitModeRequest(mode, request); err != nil {
		return scaffoldResult{}, err
	}
	return writeScaffold(request, resolver, bucketResolver)
}

// validateUnifiedInitModeRequest prevents SDK and API orchestration from crossing package-generation policy.
func validateUnifiedInitModeRequest(mode unifiedInitMode, request scaffoldRequest) error {
	// Direct API must reach the shared writer with its no-codegen policy already encoded in the candidate bytes.
	if mode == unifiedInitModeAPI && (!request.generateSet || request.generate) {
		return errors.New("direct API init requires generate: false")
	}
	// Generated SDK mode admits the compatibility alias's absent-means-generate default but rejects an explicit suppression.
	if mode == unifiedInitModeSDK && request.generateSet && !request.generate {
		return errors.New("SDK init requires package generation")
	}
	return nil
}

// printUnifiedInitAPINextStep renders one concrete REST call with the resolved immutable app ID and configured Engine URL.
func printUnifiedInitAPINextStep(cmd *cobra.Command, client *api.Client, parsed *configfile.ParsedConfig) {
	appID, err := client.ResolveSDKAppReference(parsed.SDK.Name, parsed.SDK.Version)
	// Apply already succeeded, so a follow-up lookup failure must not recast the committed app as a failed mutation.
	if err != nil || strings.TrimSpace(appID) == "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Direct API ready. Export its REST contract with:\n  fused-cli sdk openapi %s@%s\n", parsed.SDK.Name, parsed.SDK.Version)
		return
	}
	operation := firstUnifiedInitAPIOperation(parsed.SDK)
	endpoint := strings.TrimRight(client.BaseURL, "/") + "/v1/apps/" + appID + "/executions"
	payload, _ := json.Marshal(map[string]any{"operation": operation, "input": map[string]any{}})
	fmt.Fprintf(cmd.OutOrStdout(), "Direct API ready. Set FUSED_SDK_TOKEN to the token shown above, then adapt this REST request template with the operation's required input:\n")
	fmt.Fprintf(cmd.OutOrStdout(), "  curl -X POST '%s' -H \"Authorization: Bearer $FUSED_SDK_TOKEN\" -H 'Content-Type: application/json' -d %s\n", endpoint, shellQuoteWorkspaceServiceArg(string(payload)))
}

// firstUnifiedInitAPIOperation selects one deterministic configured operation or leaves an explicit template placeholder.
func firstUnifiedInitAPIOperation(config *configfile.SDKConfig) string {
	names := make([]string, 0, len(config.Services))
	for name := range config.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		// Explicit operation lists can provide a concrete template; select-all cannot choose one without schema discovery.
		if len(config.Services[name].Operations) > 0 {
			return config.Services[name].Operations[0]
		}
	}
	return "<operationId>"
}

// init registers the unified creation surface at the root command.
func init() {
	RootCmd.AddCommand(newUnifiedInitCommand())
}
