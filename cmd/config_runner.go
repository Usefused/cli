package cmd

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	summary             map[string]interface{}
	notifications       api.NotificationInbox
	requiredPermissions []api.PermissionRequirement
}

type planResultOutput struct {
	planReceipt
	Summary             map[string]interface{}      `json:"summary"`
	Notifications       api.NotificationInbox       `json:"notifications,omitempty"`
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
	Stage     string
	SDKName   string
	SDKID     string
	VersionID string
	JobID     string
	Err       error
}

// Error reports the failed SDK lifecycle stage without discarding the Engine error.
func (err *sdkApplyStageError) Error() string {
	return fmt.Sprintf("SDK %s stage failed for %s: %v", err.Stage, err.SDKName, err.Err)
}

// Unwrap exposes the underlying Engine or local filesystem failure.
func (err *sdkApplyStageError) Unwrap() error { return err.Err }

// jsonDetails returns stable stage context for the CLI JSON error envelope.
func (err *sdkApplyStageError) jsonDetails() map[string]any {
	details := map[string]any{"stage": err.Stage, "sdk": err.SDKName}
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

// planConfigWithRemediation retries only the explicit interactive SDK
// credential workflow. Every other planner error retains the ordinary
// read-only, single-request behavior expected by CI and automation.
func planConfigWithRemediation(client *api.Client, cfg *configfile.ParsedConfig, engineURL string, opts planOptions) (plannedConfig, error) {
	result, err := planOneConfig(client, cfg, engineURL, opts.ownerTeamSlug)
	if err == nil || !opts.interactive || cfg.Kind != configfile.KindSDK {
		return result, err
	}
	if !isBucketCredentialsMissing(err) {
		return plannedConfig{}, err
	}
	if err := remediateSDKPlanCredentials(client, cfg, err, opts); err != nil {
		return plannedConfig{}, err
	}
	// One bounded retry proves the newly stored material satisfies readiness
	// without allowing a malformed Engine response to create a prompt loop.
	return planOneConfig(client, cfg, engineURL, opts.ownerTeamSlug)
}

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
		if err != nil {
			return plannedConfig{}, fmt.Errorf("failed to plan SDK %s: %w", cfg.SDK.Name, err)
		}
		return plannedConfig{
			receipt:             newPlanReceipt(resp.PlanID, cfg.ConfigKey, cfg.SourceHash, engineURL),
			summary:             resp.Summary,
			notifications:       resp.Notifications,
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

func printPlanResult(planned []plannedConfig, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(planResultOutputs(planned))
	}
	for _, result := range planned {
		receipt := result.receipt
		fmt.Printf("Plan created for %s (Plan ID: %s)\n", receipt.ConfigKey, receipt.PlanID)
		if err := printPlanSummary(os.Stdout, result.summary); err != nil {
			return err
		}
		printRequiredPermissions(os.Stdout, result.requiredPermissions)
		printNotificationInbox(receipt.ConfigKey, result.notifications)
	}
	return nil
}

func planResultOutputs(planned []plannedConfig) []planResultOutput {
	results := make([]planResultOutput, 0, len(planned))
	for _, result := range planned {
		results = append(results, planResultOutput{
			planReceipt:         result.receipt,
			Summary:             result.summary,
			Notifications:       result.notifications,
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

func printPlanSummary(out io.Writer, summary map[string]interface{}) error {
	if len(summary) == 0 {
		fmt.Fprintln(out, "Plan summary: no changes reported.")
		return nil
	}
	data, err := json.MarshalIndent(summary, "  ", "  ")
	if err != nil {
		return fmt.Errorf("failed to render plan summary: %w", err)
	}
	// Why: the Engine owns kind-specific summary fields. Rendering its full
	// JSON prevents new action details from being silently hidden while the
	// CLI evolves richer kind-specific presentation independently.
	fmt.Fprintf(out, "Plan summary:\n  %s\n", data)
	return nil
}

func printNotificationInbox(configKey string, inbox api.NotificationInbox) {
	if len(inbox.Items) == 0 && len(inbox.Warnings) == 0 {
		return
	}
	fmt.Printf("Workspace notifications for %s\n", configKey)
	for _, item := range inbox.Items {
		target := notificationTarget(item)
		if target != "" {
			fmt.Printf("- %s %s %s: %s\n", item.Severity, item.Source, target, item.Message)
			continue
		}
		fmt.Printf("- %s %s %s: %s\n", item.Severity, item.Source, item.Type, item.Message)
	}
	for _, warning := range inbox.Warnings {
		fmt.Printf("- warning: %s\n", warning)
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
	if err != nil {
		return sdkApplyOutput{}, &sdkApplyStageError{Stage: "apply", SDKName: cfg.SDK.Name, Err: err}
	}
	result := sdkApplyOutput{
		ConfigKey: cfg.ConfigKey, PlanID: resp.PlanID, Status: resp.Status,
		SDKID: resp.AppFamilyID, VersionID: resp.AppID, ExecutionToken: resp.ExecutionToken,
		// Apply only enqueues generation; nothing has waited on the job yet, so
		// the stage stays "queued" until the download path confirms it finished.
		Generation: sdkApplyStageOutput{Status: "queued", JobID: resp.JobID},
		Download:   sdkApplyDownloadOutput{Status: "not_requested"},
	}
	// generate: false publishes the version without building a package, so
	// there is no job to wait on and nothing to download. --download is already
	// rejected for this config before apply runs.
	if !sdkGeneratesPackage(cfg.SDK) {
		result.Generation.Status = "skipped"
		return result, nil
	}
	if !download {
		return result, nil
	}
	if err := waitForSDKGeneration(client, resp.JobID); err != nil {
		return sdkApplyOutput{}, &sdkApplyStageError{
			Stage: "generation", SDKName: cfg.SDK.Name, SDKID: resp.AppFamilyID,
			VersionID: resp.AppID, JobID: resp.JobID, Err: err,
		}
	}
	result.Generation.Status = "completed"
	if err := downloadSDKByIDQuiet(client, resp.AppID, cfg.SDK.Name, "."); err != nil {
		return sdkApplyOutput{}, &sdkApplyStageError{
			Stage: "download", SDKName: cfg.SDK.Name, SDKID: resp.AppFamilyID,
			VersionID: resp.AppID, JobID: resp.JobID, Err: err,
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

func applyPreparedSDK(client *api.Client, cfg *configfile.ParsedConfig, receipt planReceipt, download bool) error {
	resp, err := client.ApplySDKConfig(receipt.PlanID, receipt.SourceHash)
	if err != nil {
		return fmt.Errorf("failed to apply SDK %s: %w", cfg.SDK.Name, err)
	}
	fmt.Printf("Successfully applied SDK %s\n", cfg.SDK.Name)
	fmt.Printf("  SDK ID: %s\n  Version ID: %s\n", resp.AppFamilyID, resp.AppID)
	// Engine cannot recover the plaintext token later, so surface it on the
	// successful response path without copying it into CLI state or logs.
	if resp.ExecutionToken != "" {
		fmt.Printf("  SDK token (shown once): %s\n", resp.ExecutionToken)
	}
	if !sdkGeneratesPackage(cfg.SDK) {
		fmt.Printf("  Package: not built (generate: false) -- call it over REST, or describe it with 'fused-cli sdk openapi %s@%s'\n", cfg.SDK.Name, cfg.SDK.Version)
		return nil
	}
	if !download {
		return nil
	}
	if err := waitForSDKGeneration(client, resp.JobID); err != nil {
		return fmt.Errorf("failed to generate SDK %s: %w", cfg.SDK.Name, err)
	}
	return downloadSDKByID(client, resp.AppID, cfg.SDK.Name, ".")
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

func waitForSDKGeneration(client *api.Client, jobID string) error {
	if jobID == "" {
		return nil
	}
	eventChan := make(chan api.SDKEvent)
	errChan := make(chan error)
	go client.StreamSDKGenerationEvents(jobID, eventChan, errChan)
	timeout := time.After(2 * time.Minute)
	for eventChan != nil || errChan != nil {
		select {
		case event, ok := <-eventChan:
			nextEventChan, done, err := handleSDKGenerationEvent(eventChan, event, ok)
			eventChan = nextEventChan
			if done || err != nil {
				return err
			}
		case err, ok := <-errChan:
			nextErrChan, err := handleSDKGenerationStreamError(errChan, err, ok)
			errChan = nextErrChan
			if err != nil {
				return err
			}
		case <-timeout:
			return errors.New("timed out waiting for SDK generation")
		}
	}
	return nil
}

func handleSDKGenerationEvent(ch chan api.SDKEvent, event api.SDKEvent, ok bool) (chan api.SDKEvent, bool, error) {
	if !ok {
		return nil, false, nil
	}
	switch event.Type {
	case "complete", "auth_key_generated":
		return ch, true, nil
	case "error":
		return ch, false, errors.New(event.Message)
	default:
		return ch, false, nil
	}
}

func handleSDKGenerationStreamError(ch chan error, err error, ok bool) (chan error, error) {
	if !ok {
		return nil, nil
	}
	return ch, err
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

func writePlanReceiptFile(path string, receipt planReceipt) error {
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, append(data, '\n'), 0644, validateJSONContent)
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
