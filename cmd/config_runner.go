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
	if _, err := os.Stat(".fused"); err == nil {
		return ""
	}
	return ConfigFile
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
		if _, err := client.ApplyWorkspaceConfig(receipt.PlanID, receipt.SourceHash); err != nil {
			return fmt.Errorf("failed to apply workspace %s: %w", cfg.ConfigKey, err)
		}
		fmt.Printf("Successfully applied workspace config\n")
	case configfile.KindSDK:
		resp, err := client.ApplySDKConfig(receipt.PlanID, receipt.SourceHash)
		if err != nil {
			return fmt.Errorf("failed to apply SDK %s: %w", cfg.SDK.Name, err)
		}
		fmt.Printf("Successfully applied SDK %s (SDK ID: %s)\n", cfg.SDK.Name, resp.SDKID)
		if download {
			return downloadSDKConfig(client, cfg.ConfigKey, ".")
		}
	}
	return nil
}

func downloadSDKConfig(client *api.Client, configKey, outDir string) error {
	data, err := client.DownloadGeneratedSDK(configKey)
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
