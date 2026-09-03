package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	noApply     bool
}

type unifiedInitRunner func(*cobra.Command, unifiedInitMode, scaffoldRequest) error

type unifiedInitPrecommitError struct {
	cause error
}

// unifiedInitConfigPublication records the exact local transition that can be undone only with negative commit proof.
type unifiedInitConfigPublication struct {
	path         string
	candidate    []byte
	previous     []byte
	previousMode os.FileMode
	changed      bool
	extended     bool
}

// unifiedInitReceiptPublication records the exact receipt transition paired with one published app candidate.
type unifiedInitReceiptPublication struct {
	path           string
	candidate      []byte
	previous       []byte
	previousMode   os.FileMode
	previousExists bool
}

// unifiedInitLocalApplyState groups the byte-owned local publications with the prepared remote apply request.
type unifiedInitLocalApplyState struct {
	configPublication  unifiedInitConfigPublication
	receiptPublication unifiedInitReceiptPublication
	prepared           preparedConfigApply
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
runtime outcome. Pass --no-apply to write validated local desired state and
retain available plan receipts without applying Engine state.`,
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
	command.Flags().BoolVar(&opts.noApply, "no-apply", false, "Plan initialization without applying, generating, or downloading")
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
		noApply: opts.noApply,
	}, nil
}

// runUnifiedInitLifecycle either publishes validated local desired state or keeps both remote receipt boundaries behind one confirmation.
func runUnifiedInitLifecycle(cmd *cobra.Command, mode unifiedInitMode, request scaffoldRequest) error {
	// Create-only collisions must fail before the composed workspace lifecycle can plan or apply a missing service.
	if !request.extend {
		if err := ensureUnifiedInitTargetAbsent(request.path); err != nil {
			return err
		}
	}
	// Deferred initialization preserves plan receipts but must stop before either apply boundary.
	if request.noApply {
		lifecycle, err := prepareSDKInitLifecycle(cmd, request, resolveScaffoldBucket)
		// Failed resolution or workspace planning cannot publish deferred app intent.
		if err != nil {
			return err
		}
		return createUnifiedInitWithoutApply(cmd, mode, lifecycle, resolveScaffoldRequirements, resolveScaffoldBucket)
	}
	lifecycle, err := prepareSDKInitLifecycle(cmd, request, resolveScaffoldBucket)
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

// createUnifiedInitWithoutApply publishes every currently valid plan receipt and stops before any Engine apply side effect.
func createUnifiedInitWithoutApply(cmd *cobra.Command, mode unifiedInitMode, lifecycle sdkInitLifecycle, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) error {
	appResolver := resolver
	// Engine cannot resolve app runtime snapshots until a missing workspace version is applied, so retain structurally valid app intent for the later app plan.
	if lifecycle.draft != nil {
		appResolver = deferScaffoldRequirements
	}
	result, parsed, candidate, err := prepareUnifiedInitPlanInput(mode, lifecycle.request, appResolver, bucketResolver)
	// App resolution and semantic validation remain a prerequisite even though apply is deferred.
	if err != nil {
		return fmt.Errorf("prepare local %s config: %w", mode, err)
	}
	// Already-enabled dependencies allow the complete app plan and receipt to be created immediately.
	if lifecycle.draft == nil {
		planOpts := planOptions{
			interactive: !nonInteractive(), output: cmd.OutOrStdout(),
			auditCtx: cmd.Context(), auditAction: cmd.CommandPath(),
		}
		plan, err := planUnifiedInitCandidate(cmd, lifecycle.client, mode, lifecycle.request, false, parsed, planOpts)
		// A failed app plan must leave both the candidate config and receipt absent.
		if err != nil {
			return err
		}
		// Staging publishes the validated config and its receipt but deliberately leaves the prepared apply unused.
		if _, err := stageUnifiedInitLocalApply(cmd, lifecycle.client, mode, lifecycle.request, result, parsed, candidate, plan); err != nil {
			return err
		}
		recordGeneratedBindingCount(cmd.Context(), result.GeneratedBindingCount)
		recordAppliedChangeIf(cmd.Context(), cmd.CommandPath(), "config_file", result.Changed)
		printUnifiedInitDeferredNextSteps(cmd, mode, lifecycle.request.path, nil, true)
		return nil
	}
	publication, err := publishUnifiedInitConfig(lifecycle.request, result, candidate)
	// Publication reuses the apply path's collision-safe writer while no remote commit is possible.
	if err != nil {
		return err
	}
	// Missing workspace services retain their already-created plan receipt without activating them in Engine.
	if err := publishSDKInitWorkspacePlan(lifecycle.draft); err != nil {
		// The app publication is safe to undo because no remote apply has started.
		if rollbackErr := rollbackUnifiedInitConfig(publication); rollbackErr != nil {
			return fmt.Errorf("write deferred workspace plan: %w (app config rollback failed: %v)", err, rollbackErr)
		}
		return fmt.Errorf("write deferred workspace plan: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Saved workspace plan receipt for %s without applying it.\n", lifecycle.draft.path)
	recordGeneratedBindingCount(cmd.Context(), result.GeneratedBindingCount)
	recordAppliedChangeIf(cmd.Context(), cmd.CommandPath(), "config_file", result.Changed)
	// Rendering failure remains local and cannot be mistaken for a successful Engine apply.
	if err := printScaffoldResult(cmd, unifiedInitDisplayScaffoldResult(mode, result)); err != nil {
		return err
	}
	printUnifiedInitDeferredNextSteps(cmd, mode, lifecycle.request.path, lifecycle.draft, false)
	return nil
}

// printUnifiedInitDeferredNextSteps shows the receipt-aware commands that complete a planned initialization later.
func printUnifiedInitDeferredNextSteps(cmd *cobra.Command, mode unifiedInitMode, appPath string, workspaceDraft *sdkInitWorkspaceDraft, appPlanned bool) {
	fmt.Fprintln(cmd.OutOrStdout(), "Initialization planned. No Engine changes were applied.")
	fmt.Fprintln(cmd.OutOrStdout(), "When ready, run:")
	// A missing service version must consume its existing receipt before the app can acquire a runtime-backed plan.
	if workspaceDraft != nil {
		fmt.Fprintln(cmd.OutOrStdout(), "  # App planning waits for the selected workspace service version to become active.")
		fmt.Fprintf(cmd.OutOrStdout(), "  fused-cli workspace apply -f %s\n", shellQuoteWorkspaceServiceArg(workspaceDraft.path))
	}
	resource := string(mode)
	// Direct API apps use the existing SDK plan/apply resource with generation disabled.
	if mode == unifiedInitModeAPI {
		resource = "sdk"
	}
	// App planning is deferred only when its selected workspace snapshot does not exist yet.
	if !appPlanned {
		fmt.Fprintf(cmd.OutOrStdout(), "  fused-cli %s plan -f %s\n", resource, shellQuoteWorkspaceServiceArg(appPath))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  fused-cli %s apply -f %s", resource, shellQuoteWorkspaceServiceArg(appPath))
	// A deferred generated SDK still needs an explicit package download after its later apply.
	if mode == unifiedInitModeSDK {
		fmt.Fprint(cmd.OutOrStdout(), " --download")
	}
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "If a saved plan is stale, rerun its plan command before apply.")
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
	// A just-enabled exact version may not have reached Engine's runtime-contract cache, so repair only that typed race and retry once.
	if err != nil && unifiedInitCanRefreshRuntimeSnapshots(err) {
		_, baseParsed, _, baseErr := prepareUnifiedInitPlanInput(mode, request, deferScaffoldRequirements, bucketResolver)
		// Structural or bucket failures are not snapshot races and retain the original dependency diagnosis without a mutation.
		if baseErr == nil {
			refreshed, refreshErr := refreshUnifiedInitRuntimeSnapshots(cmd, client, baseParsed)
			// A failed exact refresh reports completed mutations and never publishes the speculative app file.
			if refreshErr != nil {
				return contextualizeUnifiedInitPrecommitFailureAfterRefresh("runtime contract refresh", mode, request, workspaceApplied, refreshed, refreshErr)
			}
			result, parsed, pendingWrite, err = prepareUnifiedInitPlanInput(mode, request, resolver, bucketResolver)
			// The single retry is authoritative; another failure cannot create a refresh loop.
			if err != nil {
				return contextualizeUnifiedInitPrecommitFailureAfterRefresh("configuration preflight retry", mode, request, workspaceApplied, refreshed, err)
			}
		}
	}
	// Candidate creation or extension must validate fully before app planning.
	if err != nil {
		return contextualizeUnifiedInitPrecommitFailure("configuration preflight", mode, request, workspaceApplied, err)
	}
	planOpts := planOptions{
		interactive: !nonInteractive(), output: cmd.OutOrStdout(),
		auditCtx: cmd.Context(), auditAction: cmd.CommandPath(),
	}
	plan, err := planUnifiedInitCandidate(cmd, client, mode, request, workspaceApplied, parsed, planOpts)
	// Planning and its one bounded repair complete before either local artifact is published.
	if err != nil {
		return err
	}
	localState, err := stageUnifiedInitLocalApply(cmd, client, mode, request, result, parsed, pendingWrite, plan)
	// Local staging owns rollback for every failure before the apply request begins.
	if err != nil {
		return err
	}
	var applyResult sdkApplyOutput
	var applyErr error
	// API init retains the exact apply-returned Version ID so onboarding never depends on an immediately consistent name lookup.
	if mode == unifiedInitModeAPI {
		applyResult, applyErr = applyPreparedSDKWithResult(client, localState.prepared.config, localState.prepared.receipt, downloadPackage)
	} else {
		applyErr = applyPreparedConfig(client, localState.prepared, downloadPackage)
	}
	// Finalization either records retained desired state or restores both artifacts from negative commit proof.
	if err := finalizeUnifiedInitApply(cmd, request, result, localState, applyErr); err != nil {
		return err
	}
	// Direct API onboarding ends with an immediately usable Engine execution request rather than package instructions.
	if mode == unifiedInitModeAPI {
		printUnifiedInitAPINextStep(cmd, client, parsed, applyResult.VersionID)
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), string(parsed.Kind))
	return nil
}

// planUnifiedInitCandidate creates one app plan and performs only the bounded exact-snapshot repair authorized for generated SDKs.
func planUnifiedInitCandidate(cmd *cobra.Command, client *api.Client, mode unifiedInitMode, request scaffoldRequest, workspaceApplied bool, parsed *configfile.ParsedConfig, opts planOptions) (plannedConfig, error) {
	plan, err := planConfigWithRemediation(client, parsed, client.BaseURL, opts)
	// Ordinary plan success needs no repair or contextual error wrapping.
	if err == nil {
		return plan, nil
	}
	// Unrelated plan failures preserve either candidate absence or the exact accepted extension file.
	if !unifiedInitCanRefreshGenerationSnapshot(mode, parsed, err) {
		return plannedConfig{}, contextualizeUnifiedInitPrecommitFailure("app plan", mode, request, workspaceApplied, err)
	}
	refreshed, refreshErr := refreshUnifiedInitGenerationSnapshots(cmd, client, parsed)
	// Refresh failure stops before config publication and reports any exact snapshots already completed.
	if refreshErr != nil {
		return plannedConfig{}, contextualizeUnifiedInitPrecommitFailureAfterRefresh("generation snapshot refresh", mode, request, workspaceApplied, refreshed, refreshErr)
	}
	plan, err = planConfigWithRemediation(client, parsed, client.BaseURL, opts)
	// The one retry is authoritative; a second failure cannot trigger another refresh or publish the candidate.
	if err != nil {
		return plannedConfig{}, contextualizeUnifiedInitPrecommitFailureAfterRefresh("app plan retry", mode, request, workspaceApplied, refreshed, err)
	}
	return plan, nil
}

// stageUnifiedInitLocalApply publishes the reviewed config and receipt, renders review output, and prepares apply with rollback on every local failure.
func stageUnifiedInitLocalApply(cmd *cobra.Command, client *api.Client, mode unifiedInitMode, request scaffoldRequest, result scaffoldResult, parsed *configfile.ParsedConfig, candidate []byte, plan plannedConfig) (unifiedInitLocalApplyState, error) {
	state := unifiedInitLocalApplyState{}
	publication, err := publishUnifiedInitConfig(request, result, candidate)
	// A successful plan is the first boundary allowed to publish the exact local candidate.
	if err != nil {
		return state, err
	}
	state.configPublication = publication
	recordGeneratedBindingCount(cmd.Context(), result.GeneratedBindingCount)
	// Output failure occurs before receipt publication or remote apply, so the speculative config can be safely restored.
	if err := printScaffoldResult(cmd, unifiedInitDisplayScaffoldResult(mode, result)); err != nil {
		return state, rollbackUnifiedInitConfigFailure(err, publication)
	}
	receiptPublication, err := publishUnifiedInitReceipt(parsed, plan.receipt)
	// Receipt publication precedes apply but remains part of the local transaction until Engine supplies commit proof.
	if err != nil {
		return state, rollbackUnifiedInitConfigFailure(err, publication)
	}
	state.receiptPublication = receiptPublication
	displayConfigKey := sdkConfigDisplayKey(parsed.ConfigKey, parsed.SDK)
	fmt.Fprintf(cmd.OutOrStdout(), "%s plan created for %s (Plan ID: %s)\n", strings.ToUpper(string(mode)), displayConfigKey, plan.receipt.PlanID)
	// Human review output must remain complete before the app mutation starts.
	if err := printPlanSummary(cmd.OutOrStdout(), plan.summary); err != nil {
		return state, rollbackUnifiedInitLocalFailure(err, publication, receiptPublication)
	}
	printRequiredPermissions(cmd.OutOrStdout(), plan.requiredPermissions)
	printCredentialReadiness(cmd.OutOrStdout(), displayConfigKey, plan.credentialReadiness)
	printNotificationInbox(cmd.OutOrStdout(), displayConfigKey, plan.notifications)
	prepared, err := prepareConfigApply(parsed, applyOptions{}, client.BaseURL)
	// Receipt and Engine-target preflight must complete before calling the apply endpoint.
	if err != nil {
		return state, rollbackUnifiedInitLocalFailure(err, publication, receiptPublication)
	}
	state.prepared = prepared
	return state, nil
}

// unifiedInitDisplayScaffoldResult maps API mode onto its public resource name
// without changing the SDK-kind candidate written to disk or sent to Engine.
func unifiedInitDisplayScaffoldResult(mode unifiedInitMode, result scaffoldResult) scaffoldResult {
	// SDK and MCP already have matching public and internal kind names.
	if mode != unifiedInitModeAPI {
		return result
	}
	display := result
	display.Kind = configfile.ConfigKind(unifiedInitModeAPI)
	return display
}

// rollbackUnifiedInitConfigFailure restores the byte-owned candidate after a local failure before receipt publication.
func rollbackUnifiedInitConfigFailure(cause error, publication unifiedInitConfigPublication) error {
	// Ownership verification prevents cleanup from replacing a concurrent edit made during local staging.
	if rollbackErr := rollbackUnifiedInitConfig(publication); rollbackErr != nil {
		return fmt.Errorf("%w (local config rollback failed: %v)", cause, rollbackErr)
	}
	return cause
}

// rollbackUnifiedInitLocalFailure restores both byte-owned publications after a local failure proves apply never began.
func rollbackUnifiedInitLocalFailure(cause error, configPublication unifiedInitConfigPublication, receiptPublication unifiedInitReceiptPublication) error {
	// Coordinated verification prevents one artifact from being restored across a concurrent change to the other.
	if rollbackErr := rollbackUnifiedInitPublications(configPublication, receiptPublication); rollbackErr != nil {
		return fmt.Errorf("%w (local config and receipt rollback failed: %v)", cause, rollbackErr)
	}
	return cause
}

// finalizeUnifiedInitApply preserves ambiguous/committed desired state and rolls back only from authoritative negative commit proof.
func finalizeUnifiedInitApply(cmd *cobra.Command, request scaffoldRequest, result scaffoldResult, state unifiedInitLocalApplyState, applyErr error) error {
	// Successful apply retains the candidate config as accepted desired state.
	if applyErr == nil {
		recordAppliedChangeIf(cmd.Context(), cmd.CommandPath(), "config_file", result.Changed)
		return nil
	}
	// Ambiguous or committed failures retain both candidate and receipt for exact state inspection.
	if !appApplyProvenNotCommitted(applyErr) {
		recordAppliedChangeIf(cmd.Context(), cmd.CommandPath(), "config_file", result.Changed)
		return applyErr
	}
	// Negative commit proof authorizes restoring the complete byte-owned local transaction.
	if rollbackErr := rollbackUnifiedInitPublications(state.configPublication, state.receiptPublication); rollbackErr != nil {
		return fmt.Errorf("%w (local config and receipt rollback failed: %v)", applyErr, rollbackErr)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Reverted local config %s and plan receipt because Engine proved the app apply did not commit.\n", request.path)
	return applyErr
}

// publishUnifiedInitReceipt snapshots and atomically replaces the default receipt while retaining exact rollback material.
func publishUnifiedInitReceipt(parsed *configfile.ParsedConfig, receipt planReceipt) (unifiedInitReceiptPublication, error) {
	publication := unifiedInitReceiptPublication{path: defaultReceiptPath(parsed.ConfigKey)}
	candidate, err := marshalPlanReceipt(receipt)
	// Serialization must complete before inspecting or replacing any existing receipt.
	if err != nil {
		return publication, err
	}
	publication.candidate = append([]byte(nil), candidate...)
	previous, err := os.ReadFile(publication.path)
	// Definite absence is a valid prior state that rollback will later restore by removal.
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	} else if err == nil {
		publication.previous = previous
		publication.previousExists = true
		info, statErr := os.Stat(publication.path)
		// Existing receipt permissions are part of the exact extension rollback state.
		if statErr != nil {
			return publication, statErr
		}
		publication.previousMode = info.Mode().Perm()
	}
	// Any read failure other than definite absence makes prior receipt ownership unknowable.
	if err != nil {
		return publication, err
	}
	// The shared atomic writer preserves an existing mode and uses the established 0644 default for value-free receipt metadata.
	if err := atomicWriteFile(publication.path, candidate, 0o644, validateJSONContent); err != nil {
		return publication, err
	}
	return publication, nil
}

// rollbackUnifiedInitPublications verifies ownership of both local artifacts before restoring either one.
func rollbackUnifiedInitPublications(configPublication unifiedInitConfigPublication, receiptPublication unifiedInitReceiptPublication) error {
	// Config verification first prevents a concurrent app-file edit from triggering receipt restoration in isolation.
	if err := verifyUnifiedInitConfigPublication(configPublication); err != nil {
		return err
	}
	// Receipt verification prevents a concurrent plan from being overwritten or removed by this failed apply.
	if err := verifyUnifiedInitReceiptPublication(receiptPublication); err != nil {
		return err
	}
	// Receipt restoration runs first because config rollback re-establishes the source-hash boundary consumed by normal apply.
	if err := rollbackUnifiedInitReceipt(receiptPublication); err != nil {
		return err
	}
	return rollbackUnifiedInitConfig(configPublication)
}

// verifyUnifiedInitConfigPublication proves the app file is still absent or byte-identical to this invocation's candidate.
func verifyUnifiedInitConfigPublication(publication unifiedInitConfigPublication) error {
	// An unchanged extension has no config publication whose ownership needs proof.
	if !publication.changed {
		return nil
	}
	current, err := os.ReadFile(publication.path)
	// Independent removal of a newly created candidate already matches its rollback target.
	if err != nil && !publication.extended && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	// Every other read failure makes destructive rollback unsafe.
	if err != nil {
		return fmt.Errorf("read published config %s: %w", publication.path, err)
	}
	// Byte mismatch is positive evidence that another actor now owns the path.
	if !bytes.Equal(current, publication.candidate) {
		return fmt.Errorf("config %s changed after publication; preserving the newer bytes", publication.path)
	}
	return nil
}

// verifyUnifiedInitReceiptPublication proves the receipt still contains exactly the plan bytes this invocation wrote.
func verifyUnifiedInitReceiptPublication(publication unifiedInitReceiptPublication) error {
	current, err := os.ReadFile(publication.path)
	// Missing receipt is already the rollback target only when this invocation created it from absence.
	if errors.Is(err, os.ErrNotExist) && !publication.previousExists {
		return nil
	}
	// Any other read failure or removal of a replaced receipt prevents a safe restoration.
	if err != nil {
		return fmt.Errorf("read published plan receipt %s: %w", publication.path, err)
	}
	// A different receipt may represent a concurrent plan and must never be overwritten by rollback.
	if !bytes.Equal(current, publication.candidate) {
		return fmt.Errorf("plan receipt %s changed after publication; preserving the newer bytes", publication.path)
	}
	return nil
}

// rollbackUnifiedInitReceipt restores the exact prior receipt bytes/mode or removes the byte-identical receipt created by this invocation.
func rollbackUnifiedInitReceipt(publication unifiedInitReceiptPublication) error {
	// Rechecking immediately before mutation narrows the verification-to-write window and protects a newly published concurrent plan.
	if err := verifyUnifiedInitReceiptPublication(publication); err != nil {
		return err
	}
	// An independently removed newly-created receipt already matches definite prior absence.
	if _, err := os.Stat(publication.path); errors.Is(err, os.ErrNotExist) && !publication.previousExists {
		return nil
	} else if err != nil {
		// Other stat failures make the final mutation unsafe despite the earlier ownership check.
		return fmt.Errorf("inspect published plan receipt %s: %w", publication.path, err)
	}
	// Replacement rollback restores both exact prior content and its private mode.
	if publication.previousExists {
		if err := atomicWriteFile(publication.path, publication.previous, publication.previousMode, nil); err != nil {
			return err
		}
		// Explicit mode restoration prevents a later filesystem default from widening an older private receipt.
		if err := os.Chmod(publication.path, publication.previousMode); err != nil {
			return fmt.Errorf("restore plan receipt mode %s: %w", publication.path, err)
		}
		return nil
	}
	// Creation rollback removes only a receipt already verified as this invocation's exact bytes.
	if err := os.Remove(publication.path); err != nil {
		return fmt.Errorf("remove rejected plan receipt %s: %w", publication.path, err)
	}
	syncDirectory(filepath.Dir(publication.path))
	return nil
}

// publishUnifiedInitConfig atomically publishes one planned candidate while retaining only the bytes needed for a proven-noncommit rollback.
func publishUnifiedInitConfig(request scaffoldRequest, result scaffoldResult, candidate []byte) (unifiedInitConfigPublication, error) {
	publication := unifiedInitConfigPublication{path: request.path, candidate: append([]byte(nil), candidate...), changed: result.Changed, extended: request.extend}
	// An unchanged extension has no local mutation to publish or later undo.
	if !result.Changed {
		return publication, nil
	}
	// Extensions retain the exact accepted bytes and permissions so a rejected remote apply cannot leave speculative desired state behind.
	if request.extend {
		previous, err := os.ReadFile(request.path)
		// Failure to capture the currently accepted bytes prevents a safe speculative replacement.
		if err != nil {
			return publication, err
		}
		info, err := os.Stat(request.path)
		// Original permissions are part of the rollback invariant, not disposable metadata.
		if err != nil {
			return publication, err
		}
		publication.previous = previous
		publication.previousMode = info.Mode().Perm()
		// Atomic replacement ensures observers see either the accepted file or the complete planned candidate.
		if err := atomicWriteFile(request.path, candidate, 0o644, scaffoldValidator(request)); err != nil {
			return publication, err
		}
		return publication, nil
	}
	// New app initialization keeps create-only collision protection at the final publication boundary.
	if err := atomicCreateFile(request.path, candidate, 0o644, scaffoldValidator(request)); err != nil {
		return publication, err
	}
	return publication, nil
}

// rollbackUnifiedInitConfig restores the exact pre-apply file state without overwriting concurrent edits made after publication.
func rollbackUnifiedInitConfig(publication unifiedInitConfigPublication) error {
	// Shared verification keeps direct pre-apply cleanup and two-file apply rollback under the same ownership rule.
	if err := verifyUnifiedInitConfigPublication(publication); err != nil {
		return err
	}
	// No changed candidate means the desired-state filesystem already matches its pre-apply state.
	if !publication.changed {
		return nil
	}
	// Independent removal of a newly-created candidate already completed the required rollback.
	if !publication.extended {
		if _, err := os.Stat(publication.path); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			// Non-absence stat failures make a subsequent removal unsafe.
			return fmt.Errorf("inspect published config %s: %w", publication.path, err)
		}
	}
	// Extension rollback atomically restores both the prior content and its original private mode.
	if publication.extended {
		// Restoring content first keeps the rollback atomic for readers.
		if err := atomicWriteFile(publication.path, publication.previous, publication.previousMode, nil); err != nil {
			return err
		}
		// Explicit chmod reasserts the captured mode even if filesystem metadata changed while apply was in flight.
		if err := os.Chmod(publication.path, publication.previousMode); err != nil {
			return fmt.Errorf("restore config mode %s: %w", publication.path, err)
		}
		return nil
	}
	// Creation rollback removes only the byte-identical file this invocation published.
	if err := os.Remove(publication.path); err != nil {
		return fmt.Errorf("remove rejected config %s: %w", publication.path, err)
	}
	syncDirectory(filepath.Dir(publication.path))
	return nil
}

// appApplyProvenNotCommitted trusts rollback authority only when Engine supplied the stable negative commit proof.
func appApplyProvenNotCommitted(err error) bool {
	var apiErr *api.APIError
	// Missing or ambiguous metadata can never authorize deleting or replacing the local candidate.
	return errors.As(err, &apiErr) && apiErr.CommitState == "not_committed"
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
		message += fmt.Sprintf("; exact runtime contract refresh completed for [%s]", strings.Join(refreshed, ", "))
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
func printUnifiedInitAPINextStep(cmd *cobra.Command, client *api.Client, parsed *configfile.ParsedConfig, appID string) {
	// Older or synthetic apply responses may omit identity, so preserve an actionable exact-name fallback without another network read.
	if strings.TrimSpace(appID) == "" {
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
