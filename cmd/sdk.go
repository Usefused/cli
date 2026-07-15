package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

var sdkInput io.Reader = os.Stdin

var sdkCmd = &cobra.Command{
	Use:   "sdk",
	Short: "Manage Fused SDK configuration",
	Long:  `Manage your SDK generation config files, plan changes, and download generated SDKs.`,
}

var sdkPlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Plan SDK configuration",
	RunE: WithTelemetry("cli.sdk.plan", func(cmd *cobra.Command, args []string) error {
		return runConfigPlan(planOptions{filter: filterSDK, jsonOut: sdkPlanJSON, receiptOut: sdkPlanReceiptOut})
	}),
}

var sdkPlanJSON bool
var sdkPlanReceiptOut string
var sdkApplyDownload bool
var sdkApplyPlanID string
var sdkApplyReceiptPath string
var sdkApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply SDK configuration",
	RunE: WithTelemetry("cli.sdk.apply", func(cmd *cobra.Command, args []string) error {
		return runConfigApply(applyOptions{
			filter:      filterSDK,
			download:    sdkApplyDownload,
			planID:      sdkApplyPlanID,
			receiptPath: sdkApplyReceiptPath,
		})
	}),
}

var sdkValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate SDK configuration",
	RunE: WithTelemetry("cli.sdk.validate", func(cmd *cobra.Command, args []string) error {
		run, err := configfile.LoadRun(effectiveConfigFile())
		if err != nil {
			return err
		}
		count := 0
		for _, cfg := range run.Configs {
			if cfg.Kind == configfile.KindSDK {
				count++
			}
		}
		if count == 0 {
			return fmt.Errorf("no sdk configs found")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "validated %d sdk config\n", count)
		return nil
	}),
}

var sdkDownloadOutDir string

func validateSDKDownloadArgs(args []string) error {
	if len(args) > 0 && strings.Contains(args[0], "@") {
		return fmt.Errorf("sdk download uses config names only; %q includes a version suffix", args[0])
	}
	return nil
}

var sdkDownloadCmd = &cobra.Command{
	Use:   "download [name]",
	Short: "Download the generated SDK for a config",
	Args:  cobra.MaximumNArgs(1),
	RunE: WithTelemetry("cli.sdk.download", func(cmd *cobra.Command, args []string) error {
		if err := validateSDKDownloadArgs(args); err != nil {
			return err
		}

		client, err := getAPIClient()
		if err != nil {
			return err
		}

		var sdksToDownload []string
		if len(args) > 0 {
			name := args[0]
			sdksToDownload = append(sdksToDownload, "sdk:"+name)
		} else {
			run, err := configfile.LoadRun(ConfigFile)
			if err != nil {
				return err
			}
			for _, cfg := range run.Configs {
				if cfg.Kind == configfile.KindSDK {
					sdksToDownload = append(sdksToDownload, cfg.ConfigKey)
				}
			}
		}

		if len(sdksToDownload) == 0 {
			fmt.Println("No SDKs found to download.")
			return nil
		}

		for _, configKey := range sdksToDownload {
			data, err := client.DownloadGeneratedSDK(configKey)
			if err != nil {
				return fmt.Errorf("failed to download %s: %w", configKey, err)
			}
			outPath := filepath.Join(sdkDownloadOutDir, strings.TrimPrefix(configKey, "sdk:")+".zip")
			if err := os.WriteFile(outPath, data, 0644); err != nil {
				return fmt.Errorf("failed to write %s: %w", outPath, err)
			}
			fmt.Printf("Downloaded %s to %s\n", configKey, outPath)
		}
		return nil
	}),
}

var sdkServiceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage services in SDK config",
}

var sdkAddServiceVersion string
var sdkAddServiceCmd = &cobra.Command{
	Use:   "add <service>",
	Short: "Add a service to SDK config",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.sdk.service.add", func(cmd *cobra.Command, args []string) error {
		if err := addSDKService(ConfigFile, args[0], sdkAddServiceVersion); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Added service %s with version %s\n", args[0], sdkAddServiceVersion)
		return nil
	}),
}

var sdkOperationCmd = &cobra.Command{
	Use:   "operation",
	Short: "Manage operations in SDK config",
}

var sdkAddOperationInteractive bool
var sdkAddOperationApply bool
var sdkAddOperationDownload bool
var sdkAddOperationCmd = &cobra.Command{
	Use:   "add <service> [operationId...]",
	Short: "Add one or more operationIds to SDK config",
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 && sdkAddOperationInteractive {
			return nil
		}
		if len(args) < 1 {
			return fmt.Errorf("service is required unless --interactive is set")
		}
		return nil
	},
	RunE: WithTelemetry("cli.sdk.operation.add", func(cmd *cobra.Command, args []string) error {
		serviceName, operations := "", []string{}
		if len(args) > 0 {
			serviceName = args[0]
			operations = append(operations, args[1:]...)
		}
		if len(operations) == 0 && sdkAddOperationInteractive {
			selectedService, selectedOperations, err := selectSDKOperationsInteractively(ConfigFile, serviceName)
			if err != nil {
				return err
			}
			serviceName = selectedService
			operations = append(operations, selectedOperations...)
		}
		if len(operations) == 0 {
			return fmt.Errorf("at least one operationId is required unless --interactive is set")
		}
		if err := addSDKOperations(ConfigFile, serviceName, operations); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Added %d operationId(s) to service %s: %s\n", len(operations), serviceName, strings.Join(operations, ", "))
		if sdkAddOperationDownload {
			sdkAddOperationApply = true
		}
		if sdkAddOperationApply {
			if err := runConfigPlan(planOptions{filter: filterSDK}); err != nil {
				return err
			}
			return runConfigApply(applyOptions{filter: filterSDK, download: sdkAddOperationDownload})
		}
		return nil
	}),
}

var sdkRemoveOperationCmd = &cobra.Command{
	Use:   "remove <service> <operationId...>",
	Short: "Remove one or more operationIds from SDK config",
	Args:  cobra.MinimumNArgs(2),
	RunE: WithTelemetry("cli.sdk.operation.remove", func(cmd *cobra.Command, args []string) error {
		operations := append([]string(nil), args[1:]...)
		if err := removeSDKOperations(ConfigFile, args[0], operations); err != nil {
			return err
		}
		fmt.Printf("Removed %d operationId(s) from service %s: %s\n", len(operations), args[0], strings.Join(operations, ", "))
		return nil
	}),
}

var sdkWebhookCmd = &cobra.Command{
	Use:   "webhook",
	Short: "Manage webhooks in SDK config",
}

var sdkAddWebhookInteractive bool
var sdkAddWebhookCmd = &cobra.Command{
	Use:   "add <service> [webhookId...]",
	Short: "Add one or more webhooks to SDK config",
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 && sdkAddWebhookInteractive {
			return nil
		}
		if len(args) < 1 {
			return fmt.Errorf("service is required unless --interactive is set")
		}
		return nil
	},
	RunE: WithTelemetry("cli.sdk.webhook.add", func(cmd *cobra.Command, args []string) error {
		serviceName, webhooks := "", []string{}
		if len(args) > 0 {
			serviceName = args[0]
			webhooks = append(webhooks, args[1:]...)
		}
		if len(webhooks) == 0 && sdkAddWebhookInteractive {
			selectedService, selectedWebhooks, err := selectSDKWebhooksInteractively(ConfigFile, serviceName)
			if err != nil {
				return err
			}
			serviceName = selectedService
			webhooks = append(webhooks, selectedWebhooks...)
		}
		if len(webhooks) == 0 {
			return fmt.Errorf("at least one webhook is required unless --interactive is set")
		}
		if err := addSDKWebhooks(ConfigFile, serviceName, webhooks); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Added %d webhook(s) to service %s: %s\n", len(webhooks), serviceName, strings.Join(webhooks, ", "))
		return nil
	}),
}

var sdkRemoveWebhookCmd = &cobra.Command{
	Use:   "remove <service> <webhookId...>",
	Short: "Remove one or more webhooks from SDK config",
	Args:  cobra.MinimumNArgs(2),
	RunE: WithTelemetry("cli.sdk.webhook.remove", func(cmd *cobra.Command, args []string) error {
		webhooks := append([]string(nil), args[1:]...)
		if err := removeSDKWebhooks(ConfigFile, args[0], webhooks); err != nil {
			return err
		}
		fmt.Printf("Removed %d webhook(s) from service %s: %s\n", len(webhooks), args[0], strings.Join(webhooks, ", "))
		return nil
	}),
}

func selectSDKOperationsInteractively(path, requestedService string) (string, []string, error) {
	cfg, err := loadSDKConfigForEdit(path)
	if err != nil {
		return "", nil, err
	}
	scanner := bufio.NewScanner(sdkInput)
	serviceName, serviceVersion, err := chooseSDKService(cfg, requestedService, scanner)
	if err != nil {
		return "", nil, err
	}
	client, err := getAPIClient()
	if err != nil {
		return "", nil, err
	}
	serviceID, err := workspaceServiceID(client, serviceName)
	if err != nil {
		return "", nil, err
	}
	endpoints, err := client.SearchEndpoints(serviceID, serviceVersion, "")
	if err != nil {
		return "", nil, err
	}
	if len(endpoints) == 0 {
		return "", nil, fmt.Errorf("no operations found for service %s version %s", serviceName, serviceVersion)
	}
	for i, endpoint := range endpoints {
		fmt.Printf("%d. %s %s %s\n", i+1, endpoint.Name, endpoint.Method, endpoint.Path)
	}
	fmt.Print("Select operations (for example 1,3-4 or all): ")
	if !scanner.Scan() {
		return "", nil, fmt.Errorf("no operation selected")
	}
	operations, err := operationsFromSelection(endpoints, scanner.Text())
	if err != nil {
		return "", nil, err
	}
	return serviceName, operations, nil
}

func selectSDKWebhooksInteractively(path, requestedService string) (string, []string, error) {
	cfg, err := loadSDKConfigForEdit(path)
	if err != nil {
		return "", nil, err
	}
	scanner := bufio.NewScanner(sdkInput)
	serviceName, serviceVersion, err := chooseSDKService(cfg, requestedService, scanner)
	if err != nil {
		return "", nil, err
	}
	client, err := getAPIClient()
	if err != nil {
		return "", nil, err
	}
	serviceID, err := workspaceServiceID(client, serviceName)
	if err != nil {
		return "", nil, err
	}
	webhooks, err := client.FetchWebhooks(serviceID, serviceVersion)
	if err != nil {
		return "", nil, err
	}
	if len(webhooks) == 0 {
		return "", nil, fmt.Errorf("no webhooks found for service %s version %s", serviceName, serviceVersion)
	}
	for i, webhook := range webhooks {
		fmt.Printf("%d. %s\n", i+1, webhook.Name)
	}
	fmt.Print("Select webhooks (for example 1,3-4 or all): ")
	if !scanner.Scan() {
		return "", nil, fmt.Errorf("no webhook selected")
	}
	selectedNames, err := webhooksFromSelection(webhooks, scanner.Text())
	if err != nil {
		return "", nil, err
	}
	return serviceName, selectedNames, nil
}

func chooseSDKService(cfg *configfile.SDKConfig, requested string, scanner *bufio.Scanner) (string, string, error) {
	if requested != "" {
		return configuredServiceVersion(cfg, requested)
	}
	if len(cfg.Services) == 0 {
		return "", "", fmt.Errorf("sdk config has no services")
	}
	if len(cfg.Services) == 1 {
		for name, service := range cfg.Services {
			return name, service.Version, nil
		}
	}
	names := sortedSDKServiceNames(cfg.Services)
	for i, name := range names {
		fmt.Printf("%d. %s %s\n", i+1, name, cfg.Services[name].Version)
	}
	fmt.Print("Select service: ")
	if !scanner.Scan() {
		return "", "", fmt.Errorf("no service selected")
	}
	choice, err := selectedIndex(scanner.Text(), len(names))
	if err != nil {
		return "", "", fmt.Errorf("invalid service selection")
	}
	name := names[choice]
	return name, cfg.Services[name].Version, nil
}

func configuredServiceVersion(cfg *configfile.SDKConfig, serviceName string) (string, string, error) {
	service, ok := cfg.Services[serviceName]
	if !ok {
		return "", "", fmt.Errorf("service %s is not in this SDK config", serviceName)
	}
	return serviceName, service.Version, nil
}

func sortedSDKServiceNames(services map[string]configfile.SDKService) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func workspaceServiceID(client interface {
	ListWorkspaceServices(names ...string) ([]api.WorkspaceService, error)
}, serviceName string) (string, error) {
	services, err := client.ListWorkspaceServices()
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

func operationsFromSelection(endpoints []api.Integration, rawChoice string) ([]string, error) {
	rawChoice = strings.TrimSpace(rawChoice)
	if strings.EqualFold(rawChoice, "all") {
		return operationNames(endpoints), nil
	}
	indices, err := selectedIndices(rawChoice, len(endpoints))
	if err != nil {
		return nil, err
	}
	operations := make([]string, 0, len(indices))
	for _, index := range indices {
		operations = append(operations, endpoints[index].Name)
	}
	return operations, nil
}

func webhooksFromSelection(webhooks []api.Webhook, rawChoice string) ([]string, error) {
	rawChoice = strings.TrimSpace(rawChoice)
	if strings.EqualFold(rawChoice, "all") {
		return webhookNames(webhooks), nil
	}
	indices, err := selectedIndices(rawChoice, len(webhooks))
	if err != nil {
		return nil, err
	}
	selected := make([]string, 0, len(indices))
	for _, index := range indices {
		selected = append(selected, webhooks[index].Name)
	}
	return selected, nil
}

func operationNames(endpoints []api.Integration) []string {
	operations := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		operations = append(operations, endpoint.Name)
	}
	return operations
}

func webhookNames(webhooks []api.Webhook) []string {
	names := make([]string, 0, len(webhooks))
	for _, webhook := range webhooks {
		names = append(names, webhook.Name)
	}
	return names
}

func selectedIndices(rawChoice string, size int) ([]int, error) {
	seen := map[int]bool{}
	var indices []int
	for _, token := range strings.Split(rawChoice, ",") {
		added, err := indicesFromToken(strings.TrimSpace(token), size)
		if err != nil {
			return nil, err
		}
		for _, index := range added {
			if !seen[index] {
				indices = append(indices, index)
				seen[index] = true
			}
		}
	}
	if len(indices) == 0 {
		return nil, fmt.Errorf("invalid selection")
	}
	return indices, nil
}

func indicesFromToken(token string, size int) ([]int, error) {
	if token == "" {
		return nil, fmt.Errorf("invalid selection")
	}
	if strings.Contains(token, "-") {
		parts := strings.Split(token, "-")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid selection")
		}
		return indexRange(parts[0], parts[1], size)
	}
	index, err := selectedIndex(token, size)
	if err != nil {
		return nil, fmt.Errorf("invalid selection")
	}
	return []int{index}, nil
}

func indexRange(startRaw, endRaw string, size int) ([]int, error) {
	start, err := selectedIndex(startRaw, size)
	if err != nil {
		return nil, fmt.Errorf("invalid selection")
	}
	end, err := selectedIndex(endRaw, size)
	if err != nil || end < start {
		return nil, fmt.Errorf("invalid selection")
	}
	indices := make([]int, 0, end-start+1)
	for index := start; index <= end; index++ {
		indices = append(indices, index)
	}
	return indices, nil
}

func selectedIndex(rawChoice string, size int) (int, error) {
	choice, err := strconv.Atoi(strings.TrimSpace(rawChoice))
	if err != nil || choice < 1 || choice > size {
		return 0, fmt.Errorf("selection out of range")
	}
	return choice - 1, nil
}

func init() {
	RootCmd.AddCommand(sdkCmd)

	sdkCmd.AddCommand(sdkPlanCmd)
	sdkPlanCmd.Flags().BoolVar(&sdkPlanJSON, "json", false, "Print plan receipt JSON instead of writing default receipt")
	sdkPlanCmd.Flags().StringVar(&sdkPlanReceiptOut, "receipt-out", "", "Write the plan receipt to a specific path")

	sdkCmd.AddCommand(sdkApplyCmd)
	sdkApplyCmd.Flags().BoolVar(&sdkApplyDownload, "download", false, "Download generated SDK after apply")
	sdkApplyCmd.Flags().StringVar(&sdkApplyPlanID, "plan-id", "", "Apply a specific remote plan ID for a single SDK config")
	sdkApplyCmd.Flags().StringVar(&sdkApplyReceiptPath, "receipt", "", "Read a specific plan receipt for a single SDK config")

	sdkCmd.AddCommand(sdkValidateCmd)

	sdkCmd.AddCommand(sdkDownloadCmd)
	sdkDownloadCmd.Flags().StringVarP(&sdkDownloadOutDir, "out", "o", ".", "Output directory for the SDK")

	sdkCmd.AddCommand(sdkServiceCmd)
	sdkServiceCmd.AddCommand(sdkAddServiceCmd)
	sdkAddServiceCmd.Flags().StringVar(&sdkAddServiceVersion, "version", "", "Specific version to use for the service")

	sdkCmd.AddCommand(sdkOperationCmd)
	sdkOperationCmd.AddCommand(sdkAddOperationCmd)
	sdkAddOperationCmd.Flags().BoolVarP(&sdkAddOperationInteractive, "interactive", "i", false, "Interactive operation selection")
	sdkAddOperationCmd.Flags().BoolVar(&sdkAddOperationApply, "apply", false, "Apply changes after adding operation")
	sdkAddOperationCmd.Flags().BoolVar(&sdkAddOperationDownload, "download", false, "Download SDK after apply (implies --apply)")
	sdkOperationCmd.AddCommand(sdkRemoveOperationCmd)

	sdkCmd.AddCommand(sdkWebhookCmd)
	sdkWebhookCmd.AddCommand(sdkAddWebhookCmd)
	sdkAddWebhookCmd.Flags().BoolVarP(&sdkAddWebhookInteractive, "interactive", "i", false, "Interactive webhook selection")
	sdkWebhookCmd.AddCommand(sdkRemoveWebhookCmd)
}
