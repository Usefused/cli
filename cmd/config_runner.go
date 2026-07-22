package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

type planReceipt struct {
	ConfigKey  string `json:"config_key"`
	PlanID     string `json:"plan_id"`
	SourceHash string `json:"source_hash"`
	EngineURL  string `json:"engine_url,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
}

type plannedConfig struct {
	receipt       planReceipt
	notifications api.NotificationInbox
}

type configKindFilter string

const (
	filterAll       configKindFilter = ""
	filterSDK       configKindFilter = "sdk"
	filterWorkspace configKindFilter = "workspace"
)

var (
	sdkDownloadRetryAttempts = 10
	sdkDownloadRetryDelay    = 500 * time.Millisecond
)

type planOptions struct {
	filter     configKindFilter
	jsonOut    bool
	receiptOut string
}

type applyOptions struct {
	filter      configKindFilter
	download    bool
	planID      string
	receiptPath string
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
		result, err := planOneConfig(client, cfg, engineURL)
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

func planOneConfig(client *api.Client, cfg *configfile.ParsedConfig, engineURL string) (plannedConfig, error) {
	switch cfg.Kind {
	case configfile.KindWorkspace:
		raw, _ := json.Marshal(cfg.Workspace)
		resp, err := client.PlanWorkspaceConfig(cfg.SourceHash, cfg.ConfigKey, raw)
		if err != nil {
			return plannedConfig{}, fmt.Errorf("failed to plan workspace %s: %w", cfg.ConfigKey, err)
		}
		return plannedConfig{receipt: newPlanReceipt(resp.PlanID, cfg.ConfigKey, cfg.SourceHash, engineURL)}, nil
	case configfile.KindSDK:
		raw, _ := json.Marshal(cfg.SDK)
		resp, err := client.PlanSDKConfig(cfg.SourceHash, cfg.ConfigKey, raw)
		if err != nil {
			return plannedConfig{}, fmt.Errorf("failed to plan SDK %s: %w", cfg.SDK.Name, err)
		}
		return plannedConfig{
			receipt:       newPlanReceipt(resp.PlanID, cfg.ConfigKey, cfg.SourceHash, engineURL),
			notifications: resp.Notifications,
		}, nil
	default:
		return plannedConfig{}, fmt.Errorf("unsupported config kind %q", cfg.Kind)
	}
}

func newPlanReceipt(planID, configKey, sourceHash, engineURL string) planReceipt {
	return planReceipt{
		ConfigKey:  configKey,
		PlanID:     planID,
		SourceHash: sourceHash,
		EngineURL:  engineURL,
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
	if opts.jsonOut {
		return nil
	}
	return writePlanReceiptFile(defaultReceiptPath(cfg.ConfigKey), receipt)
}

func printPlanResult(planned []plannedConfig, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(planReceipts(planned))
	}
	for _, result := range planned {
		receipt := result.receipt
		fmt.Printf("Plan created for %s (Plan ID: %s)\n", receipt.ConfigKey, receipt.PlanID)
		printNotificationInbox(receipt.ConfigKey, result.notifications)
	}
	return nil
}

func planReceipts(planned []plannedConfig) []planReceipt {
	receipts := make([]planReceipt, 0, len(planned))
	for _, result := range planned {
		receipts = append(receipts, result.receipt)
	}
	return receipts
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
	printResolvedConfigPaths(configs)
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

func applyConfigs(client *api.Client, configs []*configfile.ParsedConfig, opts applyOptions) error {
	for _, cfg := range configs {
		receipt, err := receiptForApply(cfg, opts)
		if err != nil {
			return err
		}
		if err := applyOneConfig(client, cfg, receipt, opts.download); err != nil {
			return err
		}
	}
	return nil
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

func receiptForApply(cfg *configfile.ParsedConfig, opts applyOptions) (planReceipt, error) {
	if opts.planID != "" {
		return planReceipt{ConfigKey: cfg.ConfigKey, PlanID: opts.planID, SourceHash: cfg.SourceHash}, nil
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
	return receipt, nil
}

func applyOneConfig(client *api.Client, cfg *configfile.ParsedConfig, receipt planReceipt, download bool) error {
	switch cfg.Kind {
	case configfile.KindWorkspace:
		connectMaterials, err := workspaceConnectMaterials(cfg)
		if err != nil {
			return err
		}
		authMaterials, err := workspaceAuthMaterials(cfg)
		if err != nil {
			return err
		}
		resp, err := client.ApplyWorkspaceConfig(receipt.PlanID, receipt.SourceHash, connectMaterials, authMaterials)
		if err != nil {
			return fmt.Errorf("failed to apply workspace %s: %w", cfg.ConfigKey, err)
		}
		fmt.Printf("Successfully applied workspace config\n")
		printAppliedWebhooks(client.BaseURL, resp.Webhooks)
	case configfile.KindSDK:
		resp, err := client.ApplySDKConfig(receipt.PlanID, receipt.SourceHash)
		if err != nil {
			return fmt.Errorf("failed to apply SDK %s: %w", cfg.SDK.Name, err)
		}
		fmt.Printf("Successfully applied SDK %s (SDK ID: %s)\n", cfg.SDK.Name, resp.SDKID)
		if download {
			if err := waitForSDKGeneration(client, resp.JobID); err != nil {
				return fmt.Errorf("failed to generate SDK %s: %w", cfg.SDK.Name, err)
			}
			return downloadSDKByID(client, resp.SDKID, cfg.SDK.Name, ".")
		}
	}
	return nil
}

// workspaceAuthMaterials adapts configfile's apply-only static auth material to
// the API payload shape without writing it into plan receipts or config files.
func workspaceAuthMaterials(cfg *configfile.ParsedConfig) (map[string]api.AuthMaterial, error) {
	materials, err := cfg.WorkspaceAuthMaterials()
	if err != nil {
		return nil, err
	}
	out := make(map[string]api.AuthMaterial, len(materials))
	for key, material := range materials {
		out[key] = api.AuthMaterial{
			Username: material.Username,
			Password: material.Password,
			Token:    material.Token,
			APIKey:   material.APIKey,
			Cert:     material.Cert,
			Key:      material.Key,
		}
	}
	return out, nil
}

func workspaceConnectMaterials(cfg *configfile.ParsedConfig) (map[string]api.ConnectMaterial, error) {
	materials, err := cfg.WorkspaceConnectMaterials()
	if err != nil {
		return nil, err
	}
	out := make(map[string]api.ConnectMaterial, len(materials))
	for key, material := range materials {
		out[key] = api.ConnectMaterial{ClientID: material.ClientID, ClientSecret: material.ClientSecret, BindingValues: material.BindingValues}
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

func downloadSDKByID(client *api.Client, sdkID, sdkName, outDir string) error {
	if strings.TrimSpace(sdkID) == "" {
		return fmt.Errorf("sdk ID is required for download")
	}
	data, err := client.DownloadSDK(sdkID)
	if err != nil {
		return fmt.Errorf("failed to download sdk:%s: %w", sdkName, err)
	}
	outPath := filepath.Join(outDir, sdkName+".zip")
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", outPath, err)
	}
	fmt.Printf("Downloaded sdk:%s to %s\n", sdkName, outPath)
	return nil
}

func downloadSDKConfig(client *api.Client, configKey, outDir string) error {
	data, err := downloadSDKConfigWithRetry(client, configKey)
	if err != nil {
		return fmt.Errorf("failed to download %s: %w", configKey, err)
	}
	outPath := filepath.Join(outDir, strings.TrimPrefix(configKey, "sdk:")+".zip")
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", outPath, err)
	}
	fmt.Printf("Downloaded %s to %s\n", configKey, outPath)
	return nil
}

func downloadSDKConfigWithRetry(client *api.Client, configKey string) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= sdkDownloadRetryAttempts; attempt++ {
		data, err := client.DownloadGeneratedSDK(configKey)
		if err == nil {
			return data, nil
		}
		lastErr = err
		if !isPendingSDKDownload(err) || attempt == sdkDownloadRetryAttempts {
			return nil, err
		}
		time.Sleep(sdkDownloadRetryDelay)
	}
	return nil, lastErr
}

func isPendingSDKDownload(err error) bool {
	return err != nil && strings.Contains(err.Error(), "status 404")
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
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
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
