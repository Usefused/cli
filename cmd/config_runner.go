package cmd

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"gopkg.in/yaml.v3"
)

type planReceipt struct {
	ConfigKey  string `json:"config_key"`
	PlanID     string `json:"plan_id"`
	SourceHash string `json:"source_hash"`
	EngineURL  string `json:"engine_url,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
}

type plannedConfig struct {
	receipt             planReceipt
	displayConfigKey    string
	summary             map[string]interface{}
	notifications       api.NotificationInbox
	credentialReadiness *api.CredentialReadiness
	requiredPermissions []api.PermissionRequirement
}

type planResultOutput struct {
	planReceipt
	Summary             map[string]interface{}      `json:"summary"`
	Notifications       api.NotificationInbox       `json:"notifications,omitempty"`
	CredentialReadiness *api.CredentialReadiness    `json:"credential_readiness,omitempty"`
	RequiredPermissions []api.PermissionRequirement `json:"required_permissions"`
}

type configKindFilter string

const (
	filterAll       configKindFilter = ""
	filterSDK       configKindFilter = "sdk"
	filterMCP       configKindFilter = "mcp"
	filterWorkspace configKindFilter = "workspace"
	filterWebhook   configKindFilter = "webhook"
)

type planOptions struct {
	filter        configKindFilter
	jsonOut       bool
	receiptOut    string
	ownerTeamSlug string
	interactive   bool
	output        io.Writer
	auditCtx      context.Context
	auditAction   string
}

type applyOptions struct {
	filter      configKindFilter
	download    bool
	jsonOut     bool
	planID      string
	receiptPath string
	output      io.Writer
	auditCtx    context.Context
	auditAction string
}

type sdkApplyOutput struct {
	ConfigKey      string                 `json:"config_key"`
	PlanID         string                 `json:"plan_id"`
	Status         string                 `json:"status"`
	SDKID          string                 `json:"sdk_id"`
	VersionID      string                 `json:"version_id"`
	ExecutionToken string                 `json:"execution_token,omitempty"`
	Generation     sdkApplyStageOutput    `json:"generation"`
	Download       sdkApplyDownloadOutput `json:"download"`
}

type sdkApplyStageOutput struct {
	Status string `json:"status"`
	JobID  string `json:"job_id,omitempty"`
}

type sdkApplyDownloadOutput struct {
	Status string `json:"status"`
	Path   string `json:"path,omitempty"`
}

type sdkApplyStageError struct {
	Stage          string
	ResourceLabel  string
	SDKName        string
	SDKID          string
	VersionID      string
	JobID          string
	ExecutionToken string
	Err            error
}

const (
	// Polling uses short Engine-owned reads while leaving ample time for durable background generation.
	sdkGenerationPollInterval = time.Second
	sdkGenerationWaitTimeout  = 10 * time.Minute
)

// sdkApplyOutcomeUnknownError prevents a lost SDK/API apply response from being presented as a safely retryable dependency failure.
type sdkApplyOutcomeUnknownError struct {
	cause     error
	planID    string
	configKey string
	sdkName   string
	version   string
	resource  string
}

// Error explains the ambiguous mutation boundary and directs the operator to an exact read-only target.
func (err *sdkApplyOutcomeUnknownError) Error() string {
	label := normalizedSDKResourceLabel(err.resource)
	return fmt.Sprintf(
		"%s apply outcome is unknown for plan %s and target %s; the response did not prove whether the version committed. Do not retry this apply until inspecting state with `%s`.",
		label, safeWorkspaceOutcomeToken(err.planID, "unavailable"), safeSDKApplyRecoveryTarget(err.sdkName, err.version), err.recoveryCommand(),
	)
}

// Unwrap retains the transport or HTTP cause for logs without changing the non-retryable command contract.
func (err *sdkApplyOutcomeUnknownError) Unwrap() error {
	return err.cause
}

// recoveryCommand builds one inert shared-lifecycle state-inspection command for the immutable candidate version.
func (err *sdkApplyOutcomeUnknownError) recoveryCommand() string {
	return "fused-cli sdk show " + shellQuoteWorkspaceServiceArg(safeSDKApplyRecoveryTarget(err.sdkName, err.version))
}

// jsonDetails preserves the reviewed plan and local config identity needed to correlate an ambiguous apply.
func (err *sdkApplyOutcomeUnknownError) jsonDetails() map[string]any {
	return map[string]any{
		"plan_id": err.planID, "config_key": err.configKey,
		"sdk": err.sdkName, "version": err.version,
	}
}

var safeSDKApplyRecoveryPartPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// safeSDKApplyRecoveryTarget admits only app identity characters that cannot alter a copied shell command or target split.
func safeSDKApplyRecoveryTarget(name, version string) string {
	// Both identity parts must remain exact and unambiguous before they are included in recovery output.
	if !safeSDKApplyRecoveryPartPattern.MatchString(name) || !safeSDKApplyRecoveryPartPattern.MatchString(version) {
		return "<sdk-name>@<version>"
	}
	return name + "@" + version
}

// classifySDKApplyFailure upgrades only unproven SDK/API write-boundary failures while preserving authoritative commit evidence.
func classifySDKApplyFailure(cause error, cfg *configfile.ParsedConfig, receipt planReceipt) error {
	var apiErr *api.APIError
	// Non-API preparation failures happen before this classifier's known write boundary and retain their original local semantics.
	if !errors.As(cause, &apiErr) {
		return cause
	}
	// Only positive or negative commit proof is authoritative; an explicit unknown state still requires safe read-only recovery.
	if apiErr.CommitState == "committed" || apiErr.CommitState == "not_committed" {
		return cause
	}
	// A deadline, connection loss, or generic 5xx crossed the apply boundary without proving whether the immutable version exists.
	if apiErr.CommitState != "unknown" && apiErr.Code != "request_timed_out" && apiErr.Code != "request_cancelled" && apiErr.Code != "engine_unavailable" && apiErr.Code != "sdk_apply_response_invalid" && apiErr.HTTPStatus < 500 {
		return cause
	}
	return &sdkApplyOutcomeUnknownError{
		cause: cause, planID: receipt.PlanID, configKey: cfg.ConfigKey,
		sdkName: cfg.SDK.Name, version: cfg.SDK.Version, resource: sdkApplyResourceLabel(cfg.SDK),
	}
}

// Error reports the failed SDK/API lifecycle stage without discarding the Engine error.
func (err *sdkApplyStageError) Error() string {
	return fmt.Sprintf("%s %s stage failed for %s: %v", normalizedSDKResourceLabel(err.ResourceLabel), err.Stage, err.SDKName, err.Err)
}

// Unwrap exposes the underlying Engine or local filesystem failure.
func (err *sdkApplyStageError) Unwrap() error { return err.Err }

// jsonDetails returns stable stage context for the CLI JSON error envelope.
func (err *sdkApplyStageError) jsonDetails() map[string]any {
	details := map[string]any{"stage": err.Stage, "sdk": err.SDKName}
	// A post-commit JSON failure is the caller's only opportunity to recover the Engine's one-time plaintext token.
	if err.ExecutionToken != "" {
		details["execution_token"] = err.ExecutionToken
	}
	// Stable app identities let automation continue recovery without repeating the committed apply.
	if err.SDKID != "" {
		details["sdk_id"] = err.SDKID
	}
	if err.VersionID != "" {
		details["version_id"] = err.VersionID
	}
	if err.JobID != "" {
		details["job_id"] = err.JobID
	}
	return details
}

type workspaceApplyPayload struct {
	profileMaterials      map[string]api.ConnectMaterial
	bucketSecretMaterials map[string]string
}

type preparedConfigApply struct {
	config           *configfile.ParsedConfig
	receipt          planReceipt
	workspacePayload *workspaceApplyPayload
}

// runConfigPlan loads and validates local desired state before using the shared planner and optional SDK credential remediation.
func runConfigPlan(opts planOptions) error {
	run, err := configfile.LoadRun(effectiveConfigFile())
	if err != nil {
		return err
	}
	configs := filteredConfigs(run.Configs, opts.filter)
	if len(configs) == 0 {
		return fmt.Errorf("no %s configs found", configFilterName(opts.filter))
	}
	if !opts.jsonOut {
		printResolvedConfigPaths(configs)
	}
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	engineURL, _ := GetEngineURL()
	var planned []plannedConfig
	for _, cfg := range configs {
		result, err := planConfigWithRemediation(client, cfg, engineURL, opts)
		if err != nil {
			return err
		}
		planned = append(planned, result)
		if err := maybeWritePlanReceipt(cfg, result.receipt, opts, len(configs)); err != nil {
			return err
		}
	}
	return printPlanResult(planned, opts.jsonOut)
}

// planConfigWithRemediation optionally fills successful SDK readiness warnings and replans once.
func planConfigWithRemediation(client *api.Client, cfg *configfile.ParsedConfig, engineURL string, opts planOptions) (plannedConfig, error) {
	result, err := planOneConfig(client, cfg, engineURL, opts.ownerTeamSlug)
	// A failed plan is authoritative and cannot be reinterpreted as mutable credential absence.
	if err != nil {
		return plannedConfig{}, err
	}
	// Non-interactive and non-SDK planning publish without credential mutation.
	if !opts.interactive || cfg.Kind != configfile.KindSDK || result.credentialReadiness == nil {
		return result, nil
	}
	remediationErr := remediateSDKPlanReadiness(client, cfg, result.credentialReadiness, opts)
	// Declining an optional convenience keeps the already-created valid plan.
	if errors.Is(remediationErr, errCredentialStorageDeclined) {
		fmt.Fprintln(opts.output, "Credential setup skipped; affected calls will return an actionable setup command until credentials are configured.")
		return result, nil
	}
	// Collection or mutation failures are real operator-visible failures rather than silent skips.
	if remediationErr != nil {
		return plannedConfig{}, remediationErr
	}
	// One bounded retry refreshes readiness after the optional write without creating a prompt loop.
	return planOneConfig(client, cfg, engineURL, opts.ownerTeamSlug)
}

// planOneConfig preserves each kind's Engine response, including app
// credential readiness, without performing any mutation itself.
func planOneConfig(client *api.Client, cfg *configfile.ParsedConfig, engineURL, ownerTeamSlug string) (plannedConfig, error) {
	switch cfg.Kind {
	case configfile.KindWorkspace:
		raw, _ := json.Marshal(cfg.Workspace)
		resp, err := client.PlanWorkspaceConfig(cfg.SourceHash, cfg.ConfigKey, raw)
		if err != nil {
			return plannedConfig{}, fmt.Errorf("failed to plan workspace %s: %w", cfg.ConfigKey, err)
		}
		return plannedConfig{
			receipt:             newPlanReceipt(resp.PlanID, cfg.ConfigKey, cfg.SourceHash, engineURL),
			summary:             resp.Summary,
			notifications:       resp.Notifications,
			requiredPermissions: resp.RequiredPermissions,
		}, nil
	case configfile.KindSDK:
		raw, _ := json.Marshal(cfg.SDK)
		resp, err := client.PlanSDKConfig(desiredConfigPlanIntent(cfg, raw, ownerTeamSlug))
		// Package-free SDK configs are public APIs, even though they share the Engine adapter.
		if err != nil {
			return plannedConfig{}, fmt.Errorf("failed to plan %s %s: %w", sdkApplyResourceLabel(cfg.SDK), cfg.SDK.Name, err)
		}
		return plannedConfig{
			receipt:             newPlanReceipt(resp.PlanID, cfg.ConfigKey, cfg.SourceHash, engineURL),
			displayConfigKey:    sdkConfigDisplayKey(cfg.ConfigKey, cfg.SDK),
			summary:             resp.Summary,
			notifications:       resp.Notifications,
			credentialReadiness: resp.CredentialReadiness,
			requiredPermissions: resp.RequiredPermissions,
		}, nil
	case configfile.KindMCP:
		raw, _ := json.Marshal(cfg.MCP)
		resp, err := client.PlanMCPConfig(desiredConfigPlanIntent(cfg, raw, ownerTeamSlug))
		if err != nil {
			return plannedConfig{}, fmt.Errorf("failed to plan MCP %s: %w", cfg.MCP.Name, err)
		}
		return plannedConfig{
			receipt:             newPlanReceipt(resp.PlanID, cfg.ConfigKey, cfg.SourceHash, engineURL),
			summary:             resp.Summary,
			notifications:       resp.Notifications,
			credentialReadiness: resp.CredentialReadiness,
			requiredPermissions: resp.RequiredPermissions,
		}, nil
	case configfile.KindWebhook:
		raw, _ := json.Marshal(cfg.Webhook)
		resp, err := client.PlanWebhookConfig(desiredConfigPlanIntent(cfg, raw, ownerTeamSlug))
		if err != nil {
			return plannedConfig{}, fmt.Errorf("failed to plan webhook %s: %w", cfg.Webhook.Name, err)
		}
		// No notifications field -- kind: webhook never touches another
		// app's state, unlike workspace/SDK/MCP applies.
		return plannedConfig{
			receipt:             newPlanReceipt(resp.PlanID, cfg.ConfigKey, cfg.SourceHash, engineURL),
			summary:             resp.Summary,
			requiredPermissions: resp.RequiredPermissions,
		}, nil
	default:
		return plannedConfig{}, fmt.Errorf("unsupported config kind %q", cfg.Kind)
	}
}

func desiredConfigPlanIntent(cfg *configfile.ParsedConfig, raw json.RawMessage, ownerTeamSlug string) api.DesiredConfigPlanIntent {
	return api.DesiredConfigPlanIntent{SourceHash: cfg.SourceHash, ConfigKey: cfg.ConfigKey, OwnerTeamSlug: ownerTeamSlug, Config: raw}
}

func newPlanReceipt(planID, configKey, sourceHash, engineURL string) planReceipt {
	return planReceipt{
		ConfigKey:  configKey,
		PlanID:     planID,
		SourceHash: sourceHash,
		EngineURL:  canonicalEngineURLOrRaw(engineURL),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
}

func maybeWritePlanReceipt(cfg *configfile.ParsedConfig, receipt planReceipt, opts planOptions, count int) error {
	if opts.receiptOut != "" {
		if count > 1 {
			return errors.New("--receipt-out can only be used with one config")
		}
		return writePlanReceiptFile(opts.receiptOut, receipt)
	}
	return writePlanReceiptFile(defaultReceiptPath(cfg.ConfigKey), receipt)
}

// printPlanResult renders complete human plan guidance or the equivalent stable structured receipt array.
func printPlanResult(planned []plannedConfig, jsonOut bool) error {
	// Structured output preserves canonical receipt fields for automation and backward compatibility.
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(planResultOutputs(planned))
	}
	for _, result := range planned {
		receipt := result.receipt
		displayConfigKey := result.displayConfigKey
		// Older and non-SDK planned results retain their canonical receipt identity when no presentation alias exists.
		if displayConfigKey == "" {
			displayConfigKey = receipt.ConfigKey
		}
		fmt.Printf("Plan created for %s (Plan ID: %s)\n", displayConfigKey, receipt.PlanID)
		// Summary rendering failures stop before later guidance produces an incomplete review.
		if err := printPlanSummary(os.Stdout, result.summary); err != nil {
			return err
		}
		printRequiredPermissions(os.Stdout, result.requiredPermissions)
		printCredentialReadiness(os.Stdout, displayConfigKey, result.credentialReadiness)
		printNotificationInbox(os.Stdout, displayConfigKey, result.notifications)
	}
	return nil
}

// planResultOutputs keeps structured readiness beside the exact plan receipt
// so automation can configure credentials later without blocking publication.
func planResultOutputs(planned []plannedConfig) []planResultOutput {
	results := make([]planResultOutput, 0, len(planned))
	for _, result := range planned {
		results = append(results, planResultOutput{
			planReceipt:         result.receipt,
			Summary:             result.summary,
			Notifications:       result.notifications,
			CredentialReadiness: result.credentialReadiness,
			RequiredPermissions: result.requiredPermissions,
		})
	}
	return results
}

func printRequiredPermissions(out io.Writer, requirements []api.PermissionRequirement) {
	if len(requirements) == 0 {
		return
	}
	fmt.Fprintln(out, "Required permissions:")
	for _, requirement := range requirements {
		fmt.Fprintf(out, "- Ability to %s\n", requirement.ProductDescription())
	}
}

// printPlanSummary renders Engine-owned change data and treats output failure as a pre-apply stop condition.
func printPlanSummary(out io.Writer, summary map[string]interface{}) error {
	// An empty Engine summary remains an explicit no-change statement, and writer failure must stop apply before mutation.
	if len(summary) == 0 {
		if _, err := fmt.Fprintln(out, "Plan summary: no changes reported."); err != nil {
			return fmt.Errorf("failed to render plan summary: %w", err)
		}
		return nil
	}
	data, err := json.MarshalIndent(summary, "  ", "  ")
	// Unsupported local values must fail before a misleading partial summary can authorize apply.
	if err != nil {
		return fmt.Errorf("failed to render plan summary: %w", err)
	}
	// Why: the Engine owns kind-specific summary fields. Rendering its full
	// JSON prevents new action details from being silently hidden while the
	// CLI evolves richer kind-specific presentation independently.
	if _, err := fmt.Fprintf(out, "Plan summary:\n  %s\n", data); err != nil {
		return fmt.Errorf("failed to render plan summary: %w", err)
	}
	return nil
}

// printCredentialReadiness makes successful-but-not-callable app plans visible without blocking publication.
func printCredentialReadiness(out io.Writer, configKey string, readiness *api.CredentialReadiness) {
	// Absence and an empty requirement list both mean there is no actionable credential warning to render.
	if readiness == nil || len(readiness.MissingCredentials) == 0 {
		return
	}
	bucket := "the selected bucket"
	// A named Engine-resolved bucket is useful display context; %q keeps terminal controls escaped.
	if readiness.Bucket != nil && strings.TrimSpace(readiness.Bucket.Name) != "" {
		bucket = fmt.Sprintf("bucket %q", strings.TrimSpace(readiness.Bucket.Name))
	}
	fmt.Fprintf(out, "Credential readiness for %s: %d authentication requirement(s) are missing from %s.\n", configKey, len(readiness.MissingCredentials), bucket)
	// Exact value-free IDs let non-interactive users enter the secure prompt later without another discovery request.
	if readiness.Bucket != nil {
		bucketID := safeWorkspaceServiceID(readiness.Bucket.ID)
		// Each missing auth family gets its own secure prompt because secret set resolves and validates that family independently.
		for _, requirement := range readiness.MissingCredentials {
			serviceID := safeWorkspaceServiceID(requirement.ServiceID)
			// Malformed remote identity cannot be promoted into a copy-ready mutation command.
			if serviceID == workspaceServiceSafeID || bucketID == workspaceServiceSafeID {
				continue
			}
			fmt.Fprintf(out, "- %q (%q): `fused-cli secret set %s --bucket %s --interactive`\n",
				strings.TrimSpace(requirement.Service), strings.TrimSpace(requirement.AuthType),
				shellQuoteWorkspaceServiceArg(serviceID), shellQuoteWorkspaceServiceArg(bucketID),
			)
		}
	}
	fmt.Fprintln(out, "Publication can continue, but affected calls will fail until credentials are set.")
}

// printNotificationInbox renders the Engine-filtered workspace notification set to the caller's selected output stream.
func printNotificationInbox(out io.Writer, configKey string, inbox api.NotificationInbox) {
	// An empty inbox should not add a heading to otherwise concise plan output.
	if len(inbox.Items) == 0 && len(inbox.Warnings) == 0 {
		return
	}
	fmt.Fprintf(out, "Workspace notifications for %s\n", configKey)
	for _, item := range inbox.Items {
		target := notificationTarget(item)
		// Targeted notifications identify the exact affected config or service version instead of a generic event type.
		if target != "" {
			fmt.Fprintf(out, "- %s %s %s: %s\n", item.Severity, item.Source, target, item.Message)
			continue
		}
		fmt.Fprintf(out, "- %s %s %s: %s\n", item.Severity, item.Source, item.Type, item.Message)
	}
	for _, warning := range inbox.Warnings {
		fmt.Fprintf(out, "- warning: %s\n", warning)
	}
}

func notificationTarget(item api.NotificationItem) string {
	if item.ConfigKey != "" {
		return item.ConfigKey
	}
	if item.Version != "" && item.ServiceID != "" {
		return item.ServiceID + "@" + item.Version
	}
	if item.ServiceID != "" {
		return item.ServiceID
	}
	return item.Type
}

func runConfigApply(opts applyOptions) error {
	run, err := configfile.LoadRun(effectiveConfigFile())
	if err != nil {
		return err
	}
	configs := filteredConfigs(run.Configs, opts.filter)
	if err := validateApplyOptions(opts, len(configs)); err != nil {
		return err
	}
	if err := validateDownloadableConfigs(configs, opts.download); err != nil {
		return err
	}
	if !opts.jsonOut {
		printResolvedConfigPaths(configs)
	}
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	return applyConfigs(client, configs, opts)
}

func validateApplyOptions(opts applyOptions, configCount int) error {
	if configCount == 0 {
		return fmt.Errorf("no %s configs found", configFilterName(opts.filter))
	}
	if opts.planID != "" && configCount != 1 {
		return errors.New("--plan-id can only be used with one config")
	}
	if opts.receiptPath != "" && configCount != 1 {
		return errors.New("--receipt can only be used with one config")
	}
	return nil
}

// validateDownloadableConfigs rejects --download against a config that asked
// for no package, before anything is applied. Letting the apply through and
// failing at the transfer would leave a published version behind a command the
// user has to reason about twice.
func validateDownloadableConfigs(configs []*configfile.ParsedConfig, download bool) error {
	if !download {
		return nil
	}
	for _, cfg := range configs {
		if cfg.Kind != configfile.KindSDK || cfg.SDK == nil || sdkGeneratesPackage(cfg.SDK) {
			continue
		}
		return fmt.Errorf(
			"%s sets generate: false, so no package is built and --download has nothing to fetch; drop --download, or set generate: true and bump the version",
			cfg.SDK.Name,
		)
	}
	return nil
}

// sdkGeneratesPackage reports whether this config builds a downloadable
// package. Absent means yes, preserving the behaviour of every config written
// before generate: existed.
func sdkGeneratesPackage(cfg *configfile.SDKConfig) bool {
	return cfg.Generate == nil || *cfg.Generate
}

func applyConfigs(client *api.Client, configs []*configfile.ParsedConfig, opts applyOptions) error {
	prepared, err := prepareConfigApplies(configs, opts, client.BaseURL)
	if err != nil {
		return err
	}
	if opts.jsonOut {
		return applySDKConfigsJSON(client, prepared, opts)
	}
	for _, item := range prepared {
		if err := applyPreparedConfig(client, item, opts.download); err != nil {
			return err
		}
		recordAppliedChange(opts.auditCtx, opts.auditAction, string(item.config.Kind))
	}
	return nil
}

// applySDKConfigsJSON applies SDK configs and emits one stable JSON result array.
func applySDKConfigsJSON(client *api.Client, prepared []preparedConfigApply, opts applyOptions) error {
	results := make([]sdkApplyOutput, 0, len(prepared))
	for _, item := range prepared {
		if item.config.Kind != configfile.KindSDK {
			return errors.New("structured apply output is currently available only for sdk apply")
		}
		result, err := applyPreparedSDKJSON(client, item.config, item.receipt, opts.download)
		if err != nil {
			return err
		}
		results = append(results, result)
		recordAppliedChange(opts.auditCtx, opts.auditAction, string(item.config.Kind))
	}
	output := opts.output
	if output == nil {
		output = os.Stdout
	}
	return json.NewEncoder(output).Encode(results)
}

// applyPreparedSDKJSON preserves apply identity and one-time token data across later stages.
func applyPreparedSDKJSON(client *api.Client, cfg *configfile.ParsedConfig, receipt planReceipt, download bool) (sdkApplyOutput, error) {
	resp, err := client.ApplySDKConfig(receipt.PlanID, receipt.SourceHash)
	// SDK/API apply is a one-shot mutation boundary, so ambiguous failures need read-only recovery rather than generic retry guidance.
	if err != nil {
		classified := classifySDKApplyFailure(err, cfg, receipt)
		var unknown *sdkApplyOutcomeUnknownError
		// Returning the outcome-specific error directly keeps its plan and commit metadata at the top-level JSON boundary.
		if errors.As(classified, &unknown) {
			return sdkApplyOutput{}, classified
		}
		return sdkApplyOutput{}, &sdkApplyStageError{Stage: "apply", ResourceLabel: sdkApplyResourceLabel(cfg.SDK), SDKName: cfg.SDK.Name, Err: classified}
	}
	result := sdkApplyOutput{
		ConfigKey: cfg.ConfigKey, PlanID: resp.PlanID, Status: resp.Status,
		SDKID: resp.AppFamilyID, VersionID: resp.AppID, ExecutionToken: resp.ExecutionToken,
		// Engine distinguishes a durable queue from an immediate cache-hit completion.
		Generation: sdkApplyStageOutput{Status: sdkApplyGenerationStageStatus(resp, sdkGeneratesPackage(cfg.SDK)), JobID: resp.JobID},
		Download:   sdkApplyDownloadOutput{Status: "not_requested"},
	}
	// generate: false publishes the version without building a package, so
	// there is no job to wait on and nothing to download. --download is already
	// rejected for this config before apply runs.
	if !sdkGeneratesPackage(cfg.SDK) {
		result.Generation.Status = "skipped"
		result.Generation.JobID = ""
		return result, nil
	}
	if !download {
		return result, nil
	}
	generation, err := waitForSDKGeneration(client, resp.AppID)
	// Preserve the latest Engine job identity even when generation fails after apply committed.
	if generation != nil && strings.TrimSpace(generation.JobID) != "" {
		result.Generation.JobID = generation.JobID
	}
	// Generation is a separate post-commit stage, so its failure retains the successful app identity and one-time token response semantics.
	if err != nil {
		return sdkApplyOutput{}, &sdkApplyStageError{
			Stage: "generation", ResourceLabel: sdkApplyResourceLabel(cfg.SDK), SDKName: cfg.SDK.Name, SDKID: resp.AppFamilyID,
			VersionID: resp.AppID, JobID: result.Generation.JobID,
			ExecutionToken: resp.ExecutionToken, Err: err,
		}
	}
	result.Generation.Status = "completed"
	if err := downloadSDKByIDQuiet(client, resp.AppID, cfg.SDK.Name, "."); err != nil {
		return sdkApplyOutput{}, &sdkApplyStageError{
			Stage: "download", ResourceLabel: sdkApplyResourceLabel(cfg.SDK), SDKName: cfg.SDK.Name, SDKID: resp.AppFamilyID,
			VersionID: resp.AppID, JobID: resp.JobID,
			ExecutionToken: resp.ExecutionToken, Err: err,
		}
	}
	result.Download = sdkApplyDownloadOutput{Status: "completed", Path: filepath.Join("fused-sdks", cfg.SDK.Name)}
	return result, nil
}

func prepareConfigApplies(configs []*configfile.ParsedConfig, opts applyOptions, engineURL string) ([]preparedConfigApply, error) {
	prepared := make([]preparedConfigApply, 0, len(configs))
	for _, cfg := range configs {
		item, err := prepareConfigApply(cfg, opts, engineURL)
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, item)
	}
	return prepared, nil
}

func prepareConfigApply(cfg *configfile.ParsedConfig, opts applyOptions, engineURL string) (preparedConfigApply, error) {
	receipt, err := receiptForApply(cfg, opts, engineURL)
	if err != nil {
		return preparedConfigApply{}, err
	}
	item := preparedConfigApply{config: cfg, receipt: receipt}
	if cfg.Kind != configfile.KindWorkspace {
		return item, nil
	}
	payload, err := prepareWorkspaceApplyPayload(cfg)
	if err != nil {
		return preparedConfigApply{}, err
	}
	item.workspacePayload = payload
	return item, nil
}

// prepareWorkspaceApplyPayload resolves profile bindings and generic webhook-style secrets outside YAML.
func prepareWorkspaceApplyPayload(cfg *configfile.ParsedConfig) (*workspaceApplyPayload, error) {
	profileMaterials, err := workspaceProfileMaterials(cfg)
	// Binding resolution must complete before any apply request is sent.
	if err != nil {
		return nil, err
	}
	bucketSecretMaterials, err := cfg.WorkspaceBucketSecretMaterials()
	// Generic bucket secrets share the same all-or-nothing local preparation boundary.
	if err != nil {
		return nil, err
	}
	return &workspaceApplyPayload{
		profileMaterials:      profileMaterials,
		bucketSecretMaterials: bucketSecretMaterials,
	}, nil
}

func effectiveConfigFile() string {
	if ConfigFile == "" {
		return ""
	}
	if _, err := os.Stat(ConfigFile); err == nil {
		return ConfigFile
	}
	if resolved := resolveFusedConfigShortcut(ConfigFile); resolved != "" {
		return resolved
	}
	if _, err := os.Stat(".fused"); err == nil {
		return ""
	}
	return ConfigFile
}

func resolveFusedConfigShortcut(path string) string {
	if _, err := os.Stat(".fused"); err == nil {
		candidates := fusedConfigShortcutCandidates(path)
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	return ""
}

func fusedConfigShortcutCandidates(path string) []string {
	name := filepath.Base(path)
	// A short -f value is common during local work; resolve it into the
	// declarative .fused layout so the CLI loads exactly what the user named.
	return []string{
		filepath.Join(".fused", name),
		filepath.Join(".fused", "sdks", name),
		filepath.Join(".fused", "mcps", name),
		filepath.Join(".fused", "webhooks", name),
	}
}

func printResolvedConfigPaths(configs []*configfile.ParsedConfig) {
	for _, cfg := range configs {
		if cfg.Path == "" {
			continue
		}
		fmt.Printf("Using config: %s\n", cfg.Path)
	}
}

func receiptForApply(cfg *configfile.ParsedConfig, opts applyOptions, engineURL string) (planReceipt, error) {
	if opts.planID != "" {
		return planReceipt{
			ConfigKey:  cfg.ConfigKey,
			PlanID:     opts.planID,
			SourceHash: cfg.SourceHash,
			EngineURL:  canonicalEngineURLOrRaw(engineURL),
		}, nil
	}
	path := opts.receiptPath
	if path == "" {
		path = defaultReceiptPath(cfg.ConfigKey)
	}
	receipt, err := readPlanReceiptFile(path)
	if err != nil {
		return receipt, err
	}
	if receipt.ConfigKey != cfg.ConfigKey {
		return receipt, fmt.Errorf("receipt config_key %q does not match %q", receipt.ConfigKey, cfg.ConfigKey)
	}
	if receipt.SourceHash != cfg.SourceHash {
		return receipt, fmt.Errorf("config changed since plan was created for %s", cfg.ConfigKey)
	}
	if err := validateReceiptEngineURL(receipt.EngineURL, engineURL); err != nil {
		return receipt, fmt.Errorf("receipt target invalid for %s: %w", cfg.ConfigKey, err)
	}
	return receipt, nil
}

func canonicalEngineURLOrRaw(raw string) string {
	canonical, err := canonicalEngineURL(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return canonical
}

func canonicalEngineURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid Engine URL %q: %w", raw, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid Engine URL %q: absolute http(s) URL required", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("invalid Engine URL %q: http(s) URL required", raw)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
	parsed.Fragment = ""
	return parsed.String(), nil
}

func validateReceiptEngineURL(receiptURL, engineURL string) error {
	if strings.TrimSpace(receiptURL) == "" {
		return errors.New("receipt has no engine_url; run plan again against the intended Engine")
	}
	receiptTarget, err := canonicalEngineURL(receiptURL)
	if err != nil {
		return err
	}
	activeTarget, err := canonicalEngineURL(engineURL)
	if err != nil {
		return err
	}
	if receiptTarget != activeTarget {
		return fmt.Errorf("receipt targets %s, active Engine is %s; run plan again", receiptTarget, activeTarget)
	}
	return nil
}

func applyPreparedConfig(client *api.Client, item preparedConfigApply, download bool) error {
	cfg, receipt := item.config, item.receipt
	switch cfg.Kind {
	case configfile.KindWorkspace:
		return applyWorkspaceConfig(client, cfg, receipt, item.workspacePayload)
	case configfile.KindSDK:
		return applyPreparedSDK(client, cfg, receipt, download)
	case configfile.KindMCP:
		return applyPreparedMCP(client, cfg, receipt)
	case configfile.KindWebhook:
		return applyPreparedWebhook(client, cfg, receipt)
	}
	return nil
}

// applyPreparedSDK publishes one immutable SDK/API version through the shared lifecycle and preserves the existing error-only caller contract.
func applyPreparedSDK(client *api.Client, cfg *configfile.ParsedConfig, receipt planReceipt, download bool) error {
	_, err := applyPreparedSDKWithResult(client, cfg, receipt, download)
	return err
}

// applyPreparedSDKWithResult publishes one immutable SDK/API version while retaining apply-returned identity for composed workflows.
func applyPreparedSDKWithResult(client *api.Client, cfg *configfile.ParsedConfig, receipt planReceipt, download bool) (sdkApplyOutput, error) {
	resp, err := client.ApplySDKConfig(receipt.PlanID, receipt.SourceHash)
	// Human apply output shares the same ambiguity classifier as structured automation.
	if err != nil {
		return sdkApplyOutput{}, fmt.Errorf("failed to apply %s %s: %w", sdkApplyResourceLabel(cfg.SDK), cfg.SDK.Name, classifySDKApplyFailure(err, cfg, receipt))
	}
	result := sdkApplyOutput{
		ConfigKey: cfg.ConfigKey, PlanID: resp.PlanID, Status: resp.Status,
		SDKID: resp.AppFamilyID, VersionID: resp.AppID, ExecutionToken: resp.ExecutionToken,
		Generation: sdkApplyStageOutput{Status: sdkApplyGenerationStageStatus(resp, sdkGeneratesPackage(cfg.SDK)), JobID: resp.JobID},
		Download:   sdkApplyDownloadOutput{Status: "not_requested"},
	}
	label := sdkApplyResourceLabel(cfg.SDK)
	fmt.Printf("Successfully applied %s %s\n", label, cfg.SDK.Name)
	fmt.Printf("  %s ID: %s\n  Version ID: %s\n", label, resp.AppFamilyID, resp.AppID)
	// Engine cannot recover the plaintext token later, so surface it on the
	// successful response path without copying it into CLI state or logs.
	if resp.ExecutionToken != "" {
		fmt.Printf("  %s token (shown once): %s\n", label, resp.ExecutionToken)
	}
	// A direct API has no package job or download stage, but its exact identity remains useful to unified init.
	if !sdkGeneratesPackage(cfg.SDK) {
		result.Generation.JobID = ""
		fmt.Printf("  Package: not built (generate: false) -- call it over REST, or describe it with 'fused-cli sdk openapi %s@%s'\n", cfg.SDK.Name, cfg.SDK.Version)
		return result, nil
	}
	// A normal apply returns after durable publication and reports whether generation queued or completed immediately.
	if !download {
		fmt.Printf("  Package generation: %s\n", sdkApplyGenerationStageStatus(resp, true))
		return result, nil
	}
	generation, err := waitForSDKGeneration(client, resp.AppID)
	// Polling may return a more precise durable job identity than the initial apply response.
	if generation != nil && strings.TrimSpace(generation.JobID) != "" {
		result.Generation.JobID = generation.JobID
	}
	// Package transfer starts only after Engine reports the immutable version ready.
	if err != nil {
		return sdkApplyOutput{}, fmt.Errorf("failed to generate SDK %s: %w", cfg.SDK.Name, err)
	}
	result.Generation.Status = "completed"
	// A completed generation is not a completed apply workflow until the requested package transfer also succeeds.
	if err := downloadSDKByID(client, resp.AppID, cfg.SDK.Name, "."); err != nil {
		return sdkApplyOutput{}, err
	}
	result.Download = sdkApplyDownloadOutput{Status: "completed", Path: filepath.Join("fused-sdks", cfg.SDK.Name)}
	return result, nil
}

// sdkApplyResourceLabel presents package-free SDK configs as APIs without creating a competing lifecycle kind.
func sdkApplyResourceLabel(cfg *configfile.SDKConfig) string {
	// The generate flag is the single authored distinction between a direct API and a downloadable SDK.
	if !sdkGeneratesPackage(cfg) {
		return "API"
	}
	return "SDK"
}

// normalizedSDKResourceLabel keeps legacy constructed errors SDK-labelled
// while allowing generate:false workflows to identify themselves as APIs.
func normalizedSDKResourceLabel(label string) string {
	// API is the only public alias for the shared SDK lifecycle.
	if label == "API" {
		return label
	}
	return "SDK"
}

// sdkConfigDisplayKey aliases only package-free SDK keys for human output;
// receipts and Engine requests continue to use the canonical sdk prefix.
func sdkConfigDisplayKey(configKey string, cfg *configfile.SDKConfig) string {
	// Generated SDKs and malformed nil fixtures retain the canonical key.
	if cfg == nil || sdkGeneratesPackage(cfg) {
		return configKey
	}
	suffix, found := strings.CutPrefix(configKey, string(configfile.KindSDK)+":")
	// An unexpected key cannot be safely rewritten as a related API identity.
	if !found {
		return configKey
	}
	return "api:" + suffix
}

// sdkApplyGenerationStageStatus maps Engine lifecycle state into stable CLI stage vocabulary while preserving older Engine compatibility.
func sdkApplyGenerationStageStatus(resp *api.SDKConfigApplyResponse, generatesPackage bool) string {
	// A package-free SDK has no asynchronous work regardless of response version.
	if !generatesPackage {
		return "skipped"
	}
	// Current Engines explicitly report the Registry-owned terminal or pending state.
	switch strings.TrimSpace(resp.GenerationStatus) {
	case "complete":
		return "completed"
	case "skipped":
		return "skipped"
	case "pending":
		return "queued"
	}
	// Older Engines used top-level pending for queued work and applied only after generation completed.
	if strings.TrimSpace(resp.Status) == "pending" {
		return "queued"
	}
	return "completed"
}

// applyPreparedMCP publishes one immutable version and surfaces its stable and pinned connection routes.
func applyPreparedMCP(client *api.Client, cfg *configfile.ParsedConfig, receipt planReceipt) error {
	resp, err := client.ApplyMCPConfig(receipt.PlanID, receipt.SourceHash)
	if err != nil {
		return fmt.Errorf("failed to apply MCP %s: %w", cfg.MCP.Name, err)
	}
	fmt.Printf("Successfully applied MCP %s@%s\n", cfg.MCP.Name, cfg.MCP.Version)
	// Engine owns public URL projection so reverse-proxy origins and transport
	// recommendations stay identical across apply, GraphQL, UI, and CLI.
	fmt.Printf("  MCP ID: %s\n  Version ID: %s\n  Default transport: %s\n", resp.AppFamilyID, resp.AppID, resp.DefaultTransport)
	fmt.Printf("  Streamable HTTP (stable, recommended): %s\n", resp.TransportURLs.StreamableHTTP)
	fmt.Printf("  Streamable HTTP (version-pinned): %s\n", resp.TransportURLs.VersionedStreamableHTTP)
	fmt.Printf("  SSE (stable, legacy): %s\n  SSE (version-pinned, legacy): %s\n", resp.TransportURLs.SSE, resp.TransportURLs.VersionedSSE)
	if resp.ExecutionToken != "" {
		fmt.Printf("  Token (shown once): %s\n", resp.ExecutionToken)
	}
	return nil
}

func applyPreparedWebhook(client *api.Client, cfg *configfile.ParsedConfig, receipt planReceipt) error {
	resp, err := client.ApplyWebhookConfig(receipt.PlanID, receipt.SourceHash)
	if err != nil {
		return fmt.Errorf("failed to apply webhook %s: %w", cfg.Webhook.Name, err)
	}
	fmt.Printf("Successfully applied webhook %s\n", resp.Name)
	printAppliedWebhookRegistrations(client.BaseURL, resp.Name, resp.Registrations)
	return nil
}

// printAppliedWebhookRegistrations mirrors printAppliedWebhooks' URL
// reconstruction, but reads from kind: webhook's own apply response
// (WebhookConfigRegistration) instead of workspace apply's
// AppliedWebhookConfig -- the two shapes both carry service+slug, but come
// from different endpoints and will keep diverging (this one has no per-call
// Label since the whole webhook configuration is one label -- resp.Name).
func printAppliedWebhookRegistrations(baseURL, label string, registrations []api.WebhookConfigRegistration) {
	for _, reg := range registrations {
		fmt.Printf("  webhook %q service %q -> %s\n", label, reg.Service, strings.TrimRight(baseURL, "/")+"/webhook/"+reg.Slug+"-"+reg.Service)
	}
}

// applyWorkspaceConfig sends profile bindings and generic named secrets out-of-band, never provider credentials.
func applyWorkspaceConfig(client *api.Client, cfg *configfile.ParsedConfig, receipt planReceipt, payload *workspaceApplyPayload) error {
	// A missing prepared payload is a local invariant failure, so it must not be
	// presented as an Engine rejection with remote recovery metadata.
	if payload == nil {
		return errors.New("workspace apply payload was not prepared")
	}
	resp, err := client.ApplyWorkspaceConfig(
		receipt.PlanID,
		receipt.SourceHash,
		payload.profileMaterials,
		payload.bucketSecretMaterials,
	)
	// Engine errors remain authoritative because CLI no longer owns workspace credential recovery.
	if err != nil {
		return fmt.Errorf("failed to apply workspace %s: %w", cfg.ConfigKey, err)
	}
	// The Engine owns per-service completion; a decoded HTTP result is not itself proof of full success.
	if resp.Status == "partially_applied" {
		for _, service := range resp.Services {
			fmt.Printf("  %s: %s", safeWorkspaceRecoveryValue(service.Key), safeWorkspaceOutcomeToken(service.Status, "unknown"))
			// Recovery codes distinguish a retryable local failure from an uncertain external write.
			if service.ErrorCode != "" {
				fmt.Printf(" (%s)", safeWorkspaceOutcomeToken(service.ErrorCode, "unknown"))
			}
			fmt.Println()
		}
		return fmt.Errorf("workspace partially applied (plan %s); rerun workspace apply with the same receipt to resume; uncertain Registry actions require reconciliation", receipt.PlanID)
	}
	// Unknown success shapes must not be mistaken for a completed plan.
	if resp.Status != "applied" {
		return fmt.Errorf("workspace apply returned unconfirmed status %q", safeWorkspaceOutcomeToken(resp.Status, "unknown"))
	}
	fmt.Printf("Successfully applied workspace config\n")
	printAppliedWebhooks(client.BaseURL, resp.Webhooks)
	return nil
}

// workspaceProfileMaterials carries dynamic binding values separately from
// workspace YAML because profiles are service-scoped policy rather than credentials.
func workspaceProfileMaterials(cfg *configfile.ParsedConfig) (map[string]api.ConnectMaterial, error) {
	materials, err := cfg.WorkspaceProfileMaterials()
	if err != nil {
		return nil, err
	}
	out := make(map[string]api.ConnectMaterial, len(materials))
	for key, material := range materials {
		out[key] = api.ConnectMaterial{BindingValues: material.BindingValues}
	}
	return out, nil
}

// printAppliedWebhooks surfaces each webhook registration's URL right after
// a workspace apply so a user setting one up for the first time doesn't need
// a separate lookup command just to find the address they have to paste into
// the provider's dashboard. baseURL is the Engine host the CLI just talked
// to; the server only returns the opaque slug and service key, not a full
// URL, since it has no reason to know which host-facing address the caller
// used to reach it.
func printAppliedWebhooks(baseURL string, webhooks []api.AppliedWebhookConfig) {
	for _, w := range webhooks {
		fmt.Printf("  webhook %q -> %s\n", w.Label, appliedWebhookURL(baseURL, w))
	}
}

func appliedWebhookURL(baseURL string, w api.AppliedWebhookConfig) string {
	return strings.TrimRight(baseURL, "/") + "/webhook/" + w.Slug + "-" + w.ServiceKey
}

// waitForSDKGeneration follows Engine-owned generation state for one immutable SDK version.
func waitForSDKGeneration(client *api.Client, appID string) (*api.SDKGenerationStatusResponse, error) {
	return waitForSDKGenerationWithTiming(client, appID, sdkGenerationPollInterval, sdkGenerationWaitTimeout)
}

// waitForSDKGenerationWithTiming keeps polling deterministic and independently testable without exposing timing flags to users.
func waitForSDKGenerationWithTiming(client *api.Client, appID string, pollInterval, timeout time.Duration) (*api.SDKGenerationStatusResponse, error) {
	appID = strings.TrimSpace(appID)
	// Apply must return an immutable version identity before CLI can safely observe its background work.
	if appID == "" {
		return nil, errors.New("SDK apply response omitted the Version ID required to follow generation")
	}
	// Non-positive test or caller timing would otherwise create a tight polling loop.
	if pollInterval <= 0 {
		return nil, errors.New("SDK generation poll interval must be positive")
	}
	// A bounded wait keeps --download interruptible even if a background worker is unavailable.
	if timeout <= 0 {
		return nil, errors.New("SDK generation wait timeout must be positive")
	}
	return pollSDKGeneration(client, appID, pollInterval, timeout)
}

// pollSDKGeneration performs immediate and interval reads until Engine proves one terminal version state.
func pollSDKGeneration(client *api.Client, appID string, pollInterval, timeout time.Duration) (*api.SDKGenerationStatusResponse, error) {
	pollTimer := time.NewTicker(pollInterval)
	defer pollTimer.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		status, err := client.GetSDKGenerationStatus(appID)
		// Transient status-read failures do not prove generation failure, so retry them without replaying apply.
		if err != nil {
			// Authoritative client and authorization errors cannot improve by polling the same target.
			if !retryableSDKGenerationStatusRead(err) {
				return nil, err
			}
			select {
			// A later local read may observe progress after a proxy or restart interruption.
			case <-pollTimer.C:
				continue
			// The bounded timeout reports the last read failure without changing committed app state.
			case <-deadline.C:
				return nil, fmt.Errorf("timed out reading SDK generation status: %w", err)
			}
		}
		// Engine must echo the exact immutable identity to prevent readiness from crossing versions.
		if strings.TrimSpace(status.AppID) != appID {
			return status, fmt.Errorf("Engine returned SDK generation status for unexpected Version ID %q", status.AppID)
		}
		switch status.Status {
		// Pending work remains durable in Engine; wait before the next bounded read.
		case "pending":
			select {
			// The next read occurs only after the configured interval to avoid busy-polling Engine.
			case <-pollTimer.C:
				continue
			// Timing out never replays apply because its commit already succeeded.
			case <-deadline.C:
				return status, fmt.Errorf("timed out waiting for SDK generation; inspect `%s`", "fused-cli sdk show "+appID)
			}
		// Complete and skipped are the only terminal states safe for the download stage to consume.
		case "complete", "skipped":
			return status, nil
		// Failed is terminal but Engine deliberately exposes no upstream error prose at this boundary.
		case "failed":
			return status, fmt.Errorf("Engine reported SDK generation failed; inspect `%s` before retrying package download", "fused-cli sdk show "+appID)
		// Unknown states fail closed so CLI never downloads a package whose readiness it cannot prove.
		default:
			return status, fmt.Errorf("Engine returned invalid SDK generation status %q", status.Status)
		}
	}
}

// retryableSDKGenerationStatusRead distinguishes transient Engine availability from authoritative request rejection.
func retryableSDKGenerationStatusRead(err error) bool {
	var apiErr *api.APIError
	// Network and malformed-success errors have no authoritative 4xx proof and may recover on the next local read.
	if !errors.As(err, &apiErr) {
		return true
	}
	// Explicit retryability and server failures are safe to poll because apply is never replayed.
	return apiErr.Retryable || apiErr.HTTPStatus == 0 || apiErr.HTTPStatus >= http.StatusInternalServerError
}

func downloadSDKByID(client *api.Client, appID, sdkName, outDir string) error {
	if err := downloadSDKByIDQuiet(client, appID, sdkName, outDir); err != nil {
		return err
	}
	extractDir := filepath.Join(outDir, "fused-sdks", sdkName)
	fmt.Printf("Downloaded and extracted sdk:%s to %s\n", sdkName, extractDir)
	return nil
}

// downloadSDKByIDQuiet downloads and extracts an exact SDK version without human output.
func downloadSDKByIDQuiet(client *api.Client, appID, sdkName, outDir string) error {
	if strings.TrimSpace(appID) == "" {
		return fmt.Errorf("sdk ID is required for download")
	}
	data, err := client.DownloadSDK(appID)
	if err != nil {
		return fmt.Errorf("failed to download sdk:%s: %w", sdkName, err)
	}
	extractDir := filepath.Join(outDir, "fused-sdks", sdkName)
	if err := extractSDKZip(data, extractDir); err != nil {
		return fmt.Errorf("failed to extract sdk:%s: %w", sdkName, err)
	}
	return nil
}

func extractSDKZip(zipData []byte, outDir string) error {
	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("read zip: %w", err)
	}
	skillName, hasRootSkill, err := generatedSDKSkillName(zipReader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("create extract dir: %w", err)
	}

	for _, f := range zipReader.File {
		fpath := filepath.Join(outDir, f.Name)

		if !strings.HasPrefix(fpath, filepath.Clean(outDir)+string(os.PathSeparator)) {
			continue
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	if hasRootSkill {
		if err := installExtractedSDKSkill(outDir, skillName); err != nil {
			return err
		}
	}
	return nil
}

const maxGeneratedSDKSkillBytes = 1 << 20

var generatedSDKSkillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// generatedSDKSkillName validates the package-root Agent Skill before any
// archive content is extracted. This prevents malformed frontmatter from
// selecting a path outside the shared SDK skill directory.
func generatedSDKSkillName(reader *zip.Reader) (string, bool, error) {
	var foundName string
	found := false
	for _, file := range reader.File {
		name := strings.TrimPrefix(path.Clean(filepath.ToSlash(file.Name)), "/")
		if name != "SKILL.md" || file.FileInfo().IsDir() {
			continue
		}
		if found {
			return "", true, errors.New("generated SDK archive contains multiple root SKILL.md files")
		}
		found = true
		if file.UncompressedSize64 > maxGeneratedSDKSkillBytes {
			return "", true, fmt.Errorf("generated SDK SKILL.md exceeds %d bytes", maxGeneratedSDKSkillBytes)
		}
		rc, err := file.Open()
		if err != nil {
			return "", true, fmt.Errorf("open generated SDK SKILL.md: %w", err)
		}
		content, readErr := io.ReadAll(io.LimitReader(rc, maxGeneratedSDKSkillBytes+1))
		closeErr := rc.Close()
		if readErr != nil {
			return "", true, fmt.Errorf("read generated SDK SKILL.md: %w", readErr)
		}
		if closeErr != nil {
			return "", true, fmt.Errorf("close generated SDK SKILL.md: %w", closeErr)
		}
		if len(content) > maxGeneratedSDKSkillBytes {
			return "", true, fmt.Errorf("generated SDK SKILL.md exceeds %d bytes", maxGeneratedSDKSkillBytes)
		}
		skillName, err := parseGeneratedSDKSkillName(content)
		if err != nil {
			return "", true, err
		}
		foundName = skillName
	}
	return foundName, found, nil
}

func parseGeneratedSDKSkillName(content []byte) (string, error) {
	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) < 3 || lines[0] != "---" {
		return "", errors.New("generated SDK SKILL.md is missing YAML frontmatter")
	}
	closing := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			closing = i
			break
		}
	}
	if closing < 2 {
		return "", errors.New("generated SDK SKILL.md has malformed YAML frontmatter")
	}
	var frontmatter struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:closing], "\n")), &frontmatter); err != nil {
		return "", fmt.Errorf("parse generated SDK SKILL.md frontmatter: %w", err)
	}
	name := frontmatter.Name
	if name != strings.TrimSpace(name) || len(name) == 0 || len(name) > 64 || !generatedSDKSkillNamePattern.MatchString(name) {
		return "", fmt.Errorf("generated SDK SKILL.md has unsafe name %q", name)
	}
	return name, nil
}

func installExtractedSDKSkill(sdkDir, skillName string) error {
	source := filepath.Join(sdkDir, "SKILL.md")
	info, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("locate extracted generated SDK Agent Skill: %w", err)
	}
	content, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read extracted generated SDK Agent Skill: %w", err)
	}
	skillDir := filepath.Join(filepath.Dir(sdkDir), ".agents", "skills", skillName)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return fmt.Errorf("create generated SDK Agent Skill directory: %w", err)
	}
	destination := filepath.Join(skillDir, "SKILL.md")
	if err := atomicWriteFile(destination, content, info.Mode().Perm(), nil); err != nil {
		return fmt.Errorf("install generated SDK Agent Skill: %w", err)
	}
	if err := os.Remove(source); err != nil {
		return fmt.Errorf("remove package-root generated SDK Agent Skill: %w", err)
	}
	return nil
}

func filteredConfigs(configs []*configfile.ParsedConfig, filter configKindFilter) []*configfile.ParsedConfig {
	var out []*configfile.ParsedConfig
	for _, cfg := range configs {
		if filter == "" || string(cfg.Kind) == string(filter) {
			out = append(out, cfg)
		}
	}
	return out
}

func configFilterName(filter configKindFilter) string {
	if filter == "" {
		return "fused"
	}
	return string(filter)
}

func defaultReceiptPath(configKey string) string {
	name := strings.ReplaceAll(configKey, ":", ".")
	return filepath.Join(".fused", ".state", name+".plan.json")
}

// writePlanReceiptFile atomically publishes the canonical bytes shared by normal plan and unified-init rollback tracking.
func writePlanReceiptFile(path string, receipt planReceipt) error {
	data, err := marshalPlanReceipt(receipt)
	if err != nil {
		return err
	}
	return atomicWriteFile(path, data, 0644, validateJSONContent)
}

// marshalPlanReceipt produces one deterministic representation so rollback can prove ownership of the receipt it is about to replace.
func marshalPlanReceipt(receipt planReceipt) ([]byte, error) {
	data, err := json.MarshalIndent(receipt, "", "  ")
	// Serialization failure must occur before any receipt path is changed.
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func readPlanReceiptFile(path string) (planReceipt, error) {
	var receipt planReceipt
	data, err := os.ReadFile(path)
	if err != nil {
		return receipt, fmt.Errorf("failed to read plan receipt %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		return receipt, fmt.Errorf("failed to parse plan receipt %s: %w", path, err)
	}
	return receipt, nil
}
