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
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

var sdkInput io.Reader = os.Stdin

var sdkCmd = &cobra.Command{
	Use:   "sdk",
	Short: "Manage Fused SDK configuration",
	Long:  `Manage your SDK generation config files, plan changes, and download generated SDKs.`,
	Example: `  fused-cli sdk security-sdk download
  fused-cli sdk security-sdk@1.2.0 download`,
	Args: cobra.ArbitraryArgs,
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.sdk", func(cmd *cobra.Command, args []string) error {
		return runSDKDynamicAction(cmd, args)
	}),
}

var sdkPlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Plan SDK configuration",
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.sdk.plan", func(cmd *cobra.Command, args []string) error {
		return runConfigPlan(planOptions{filter: filterSDK, jsonOut: sdkPlanJSON, receiptOut: sdkPlanReceiptOut, ownerTeamID: sdkPlanOwnerTeam})
	}),
}

var sdkPlanJSON bool
var sdkPlanReceiptOut string
var sdkPlanOwnerTeam string
var sdkApplyDownload bool
var sdkApplyPlanID string
var sdkApplyReceiptPath string
var sdkApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply SDK configuration",
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.sdk.apply", func(cmd *cobra.Command, args []string) error {
		return runConfigApply(withApplyAudit(cmd, applyOptions{
			filter:      filterSDK,
			download:    sdkApplyDownload,
			planID:      sdkApplyPlanID,
			receiptPath: sdkApplyReceiptPath,
		}))
	}),
}

var sdkValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate SDK configuration",
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
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
var sdkListFlags listFlags
var sdkListTarget string
var sdkListLanguage string
var sdkListLatestOnly bool

type sdkDownloadTarget struct {
	Name    string
	Version string
}

func validateSDKDownloadArgs(args []string) error {
	if len(args) > 0 {
		name, version := parseSDKDownloadName(args[0])
		if name == "" || strings.Contains(args[0], "@") && version == "" {
			return fmt.Errorf("sdk download requires name@version when using version suffix")
		}
	}
	return nil
}

func resolveSDKDownloadTargets(args []string, configPath string) ([]sdkDownloadTarget, error) {
	if len(args) > 0 {
		name, version := parseSDKDownloadName(args[0])
		return []sdkDownloadTarget{{Name: name, Version: version}}, nil
	}
	run, err := configfile.LoadRun(configPath)
	if err != nil {
		return nil, err
	}
	targets := make([]sdkDownloadTarget, 0, len(run.Configs))
	for _, cfg := range run.Configs {
		if cfg.Kind == configfile.KindSDK {
			targets = append(targets, sdkDownloadTarget{
				Name:    cfg.SDK.Name,
				Version: strings.TrimSpace(cfg.SDK.Version),
			})
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no sdk configs found")
	}
	return targets, nil
}

func parseSDKDownloadName(raw string) (string, string) {
	name, version, found := strings.Cut(raw, "@")
	if !found {
		return strings.TrimSpace(raw), ""
	}
	return strings.TrimSpace(name), strings.TrimSpace(version)
}

func runSDKDynamicAction(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	if len(args) == 1 && args[0] == "list" {
		return runSDKList(cmd)
	}
	if len(args) == 2 && args[1] == "download" {
		if err := validateSDKDownloadArgs([]string{args[0]}); err != nil {
			return err
		}
		return runSDKDownloadTargets([]sdkDownloadTarget{downloadTargetFromName(args[0])})
	}
	if len(args) == 2 {
		return runSDKReadAction(cmd, args[0], args[1])
	}
	return fmt.Errorf("unknown sdk command %q", strings.Join(args, " "))
}

func runSDKReadAction(cmd *cobra.Command, rawName, action string) error {
	target := downloadTargetFromName(rawName)
	switch action {
	case "show":
		return runSDKShow(cmd, target)
	case "services":
		return runSDKServices(cmd, target)
	case "buckets":
		return runSDKBuckets(cmd, target)
	case "tokens":
		return runSDKTokens(cmd, target)
	default:
		return fmt.Errorf("unknown sdk action %q", action)
	}
}

func runSDKList(cmd *cobra.Command) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	page, err := client.ListSDKs(api.SDKListOptions{
		PageOptions:    sdkListFlags.pageOptions(),
		TargetType:     sdkListTarget,
		TargetLanguage: sdkListLanguage,
		LatestOnly:     sdkListLatestOnly,
	})
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tVERSION\tTARGET_TYPE\tLANGUAGE\tID")
	for _, sdk := range page.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", sdk.Name, sdk.Version, sdk.TargetType, sdk.TargetLanguage, sdk.ID)
	}
	w.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, sdkListFlags)
	return nil
}

func runSDKShow(cmd *cobra.Command, target sdkDownloadTarget) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	sdk, err := client.GetSDKByName(target.Name, target.Version)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "name:\t%s\nversion:\t%s\nid:\t%s\nsandbox_url:\t%s\n", sdk.Name, sdk.Version, sdk.ID, sdk.SandboxURL)
	return nil
}

func runSDKServices(cmd *cobra.Command, target sdkDownloadTarget) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	sdk, err := client.GetSDKSelectionsByNameVersion(target.Name, target.Version)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tSERVICE_ID\tVERSION\tSELECT_ALL\tENDPOINTS\tWEBHOOKS")
	for _, service := range sdk.DetailedSelections {
		fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%d\t%d\n", displaySDKServiceSlug(service), service.ServiceID, service.ServiceVersionName, service.SelectAll, len(service.EndpointIDs), len(service.WebhookIDs))
	}
	w.Flush()
	return nil
}

func runSDKBuckets(cmd *cobra.Command, target sdkDownloadTarget) error {
	client, sdk, err := sdkClientAndDetails(target)
	if err != nil {
		return err
	}
	buckets, err := client.ListSDKBuckets(sdk.ID)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tID\tDEFAULT")
	for _, bucket := range buckets {
		fmt.Fprintf(w, "%s\t%s\t%t\n", bucket.Name, bucket.ID, bucket.IsDefault)
	}
	w.Flush()
	return nil
}

func runSDKTokens(cmd *cobra.Command, target sdkDownloadTarget) error {
	client, sdk, err := sdkClientAndDetails(target)
	if err != nil {
		return err
	}
	tokens, err := client.ListSDKTokens(sdk.ID)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tID\tCREATED_AT")
	for _, token := range tokens {
		fmt.Fprintf(w, "%s\t%s\t%s\n", token.Name, token.ID, token.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	w.Flush()
	return nil
}

func sdkClientAndDetails(target sdkDownloadTarget) (*api.Client, *api.SDKBasicDetails, error) {
	client, err := getAPIClient()
	if err != nil {
		return nil, nil, err
	}
	sdk, err := client.GetSDKByName(target.Name, target.Version)
	if err != nil {
		return nil, nil, err
	}
	return client, sdk, nil
}

func displaySDKServiceSlug(service api.SDKSelectionDetail) string {
	if service.ServiceProvider != "" && service.ServiceSlug != "" {
		return "@" + service.ServiceProvider + "/" + service.ServiceSlug
	}
	if service.ServiceSlug != "" {
		return service.ServiceSlug
	}
	return service.ServiceName
}

func downloadTargetFromName(name string) sdkDownloadTarget {
	sdkName, version := parseSDKDownloadName(name)
	return sdkDownloadTarget{Name: sdkName, Version: version}
}

var sdkDownloadCmd = &cobra.Command{
	Use:    "download [name[@version]]",
	Short:  "Download the generated SDK for a config",
	Hidden: true,
	Args:   cobra.MaximumNArgs(1),
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.sdk.download", func(cmd *cobra.Command, args []string) error {
		if err := validateSDKDownloadArgs(args); err != nil {
			return err
		}

		sdksToDownload, err := resolveSDKDownloadTargets(args, effectiveConfigFile())
		if err != nil {
			return err
		}

		return runSDKDownloadTargets(sdksToDownload)
	}),
}

func runSDKDownloadTargets(targets []sdkDownloadTarget) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	for _, target := range targets {
		if err := downloadSDKTarget(client, target); err != nil {
			return err
		}
	}
	return nil
}

func downloadSDKTarget(client *api.Client, target sdkDownloadTarget) error {
	data, err := downloadSDKByName(client, target)
	if err != nil {
		return fmt.Errorf("failed to download sdk:%s: %w", target.Name, err)
	}
	extractDir := filepath.Join(sdkDownloadOutDir, "fused-sdks", target.Name)
	if err := extractSDKZip(data, extractDir); err != nil {
		return fmt.Errorf("failed to extract sdk:%s: %w", target.Name, err)
	}
	fmt.Printf("Downloaded and extracted sdk:%s to %s\n", target.Name, extractDir)
	return nil
}

func downloadSDKByName(client *api.Client, target sdkDownloadTarget) ([]byte, error) {
	sdk, err := client.GetSDKByName(target.Name, target.Version)
	if err != nil {
		return nil, err
	}
	if sdk == nil || strings.TrimSpace(sdk.ID) == "" {
		return nil, fmt.Errorf("generated SDK not found")
	}
	return client.DownloadSDK(sdk.ID)
}

var sdkServiceCmd = &cobra.Command{
	Use:   "service <service-slug> [add|remove] [operationId...]",
	Short: "Manage services in SDK config",
	Args:  validateSDKServiceArgs,
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.sdk.service", func(cmd *cobra.Command, args []string) error {
		return runSDKServiceAction(cmd, args)
	}),
	ValidArgsFunction: completeSDKServiceArgs,
}

var sdkAddServiceVersion string
var sdkServiceActionInteractive bool
var sdkServiceActionApply bool
var sdkServiceActionDownload bool

func validateSDKServiceArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) < 2 {
		return fmt.Errorf("sdk service action is required")
	}
	if !isSDKServiceAction(args[1]) {
		return fmt.Errorf("unknown sdk service action %q", args[1])
	}
	return nil
}

func runSDKServiceAction(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	serviceName := args[0]
	action := args[1]
	operations := append([]string(nil), args[2:]...)
	switch action {
	case "add":
		return runSDKServiceAddAction(cmd, serviceName, operations)
	case "remove":
		return runSDKServiceRemoveAction(cmd, serviceName, operations)
	default:
		return fmt.Errorf("unknown sdk service action %q", action)
	}
}

func runSDKServiceAddAction(cmd *cobra.Command, serviceName string, operations []string) error {
	if len(operations) == 0 && sdkServiceActionInteractive {
		selectedService, selectedOperations, err := selectSDKOperationsInteractively(ConfigFile, serviceName)
		if err != nil {
			return err
		}
		serviceName = selectedService
		operations = append(operations, selectedOperations...)
	}
	if len(operations) > 0 {
		if err := addSDKOperations(ConfigFile, serviceName, operations); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Added %d operationId(s) to service %s: %s\n", len(operations), serviceName, strings.Join(operations, ", "))
		return maybeApplySDKServiceAction(cmd)
	}
	if err := addSDKService(ConfigFile, serviceName, sdkAddServiceVersion); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Added service %s with version %s\n", serviceName, sdkAddServiceVersion)
	return nil
}

func runSDKServiceRemoveAction(cmd *cobra.Command, serviceName string, operations []string) error {
	if len(operations) > 0 {
		if err := removeSDKOperations(ConfigFile, serviceName, operations); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Removed %d operationId(s) from service %s: %s\n", len(operations), serviceName, strings.Join(operations, ", "))
		return nil
	}
	if err := removeSDKService(ConfigFile, serviceName); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Removed service %s\n", serviceName)
	return nil
}

func maybeApplySDKServiceAction(cmd *cobra.Command) error {
	if sdkServiceActionDownload {
		sdkServiceActionApply = true
	}
	if !sdkServiceActionApply {
		return nil
	}
	if err := runConfigPlan(planOptions{filter: filterSDK}); err != nil {
		return err
	}
	return runConfigApply(withApplyAudit(cmd, applyOptions{filter: filterSDK, download: sdkServiceActionDownload}))
}

func isSDKServiceAction(action string) bool {
	switch action {
	case "add", "remove":
		return true
	default:
		return false
	}
}

func completeSDKServiceArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return completeSDKConfigServices(toComplete), cobra.ShellCompDirectiveNoFileComp
	case 1:
		return filteredSDKServiceActions(toComplete), cobra.ShellCompDirectiveNoFileComp
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

func completeSDKConfigServices(toComplete string) []string {
	cfg, err := loadSDKConfigForEdit(effectiveConfigFile())
	if err != nil {
		return nil
	}
	names := sortedSDKServiceNames(cfg.Services)
	out := make([]string, 0, len(names))
	for _, name := range names {
		if strings.HasPrefix(name, toComplete) {
			out = append(out, name)
		}
	}
	return out
}

func filteredSDKServiceActions(toComplete string) []string {
	actions := []string{"add", "remove"}
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

var sdkAddServiceCmd = &cobra.Command{
	Use:   "add <service>",
	Short: "Add a service to SDK config",
	Args:  cobra.ExactArgs(1),
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.sdk.service.add", func(cmd *cobra.Command, args []string) error {
		if err := addSDKService(ConfigFile, args[0], sdkAddServiceVersion); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Added service %s with version %s\n", args[0], sdkAddServiceVersion)
		return nil
	}),
}

var sdkOperationCmd = &cobra.Command{
	Use:   "operation <service-slug> [add|remove] [operationId...]",
	Short: "Manage operations in SDK config",
	Args:  validateSDKOperationArgs,
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.sdk.operation", func(cmd *cobra.Command, args []string) error {
		return runSDKOperationAction(cmd, args)
	}),
	ValidArgsFunction: completeSDKOperationArgs,
}

var sdkAddOperationInteractive bool
var sdkAddOperationApply bool
var sdkAddOperationDownload bool

func validateSDKOperationArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) < 2 {
		return fmt.Errorf("sdk operation action is required (e.g. add, remove)")
	}
	action := args[1]
	if action != "add" && action != "remove" {
		return fmt.Errorf("unknown sdk operation action %q", action)
	}
	return nil
}

func runSDKOperationAction(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	serviceName := args[0]
	action := args[1]
	operations := append([]string(nil), args[2:]...)
	switch action {
	case "add":
		return runSDKAddOperationAction(cmd, serviceName, operations)
	case "remove":
		return runSDKRemoveOperationAction(cmd, serviceName, operations)
	default:
		return fmt.Errorf("unknown sdk operation action %q", action)
	}
}

func runSDKAddOperationAction(cmd *cobra.Command, serviceName string, operations []string) error {
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
		return runConfigApply(withApplyAudit(cmd, applyOptions{filter: filterSDK, download: sdkAddOperationDownload}))
	}
	return nil
}

func runSDKRemoveOperationAction(cmd *cobra.Command, serviceName string, operations []string) error {
	if err := removeSDKOperations(ConfigFile, serviceName, operations); err != nil {
		return err
	}
	fmt.Printf("Removed %d operationId(s) from service %s: %s\n", len(operations), serviceName, strings.Join(operations, ", "))
	return nil
}

func completeSDKOperationArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return completeSDKConfigServices(toComplete), cobra.ShellCompDirectiveNoFileComp
	case 1:
		return filteredSDKServiceActions(toComplete), cobra.ShellCompDirectiveNoFileComp
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

var sdkWebhookCmd = &cobra.Command{
	Use:   "webhook <service-slug> [add|remove] [webhookId...]",
	Short: "Manage webhooks in SDK config",
	Args:  validateSDKWebhookArgs,
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.sdk.webhook", func(cmd *cobra.Command, args []string) error {
		return runSDKWebhookAction(cmd, args)
	}),
	ValidArgsFunction: completeSDKWebhookArgs,
}

var sdkAddWebhookInteractive bool

func validateSDKWebhookArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) < 2 {
		return fmt.Errorf("sdk webhook action is required (e.g. add, remove)")
	}
	action := args[1]
	if action != "add" && action != "remove" {
		return fmt.Errorf("unknown sdk webhook action %q", action)
	}
	return nil
}

func runSDKWebhookAction(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	serviceName := args[0]
	action := args[1]
	webhooks := append([]string(nil), args[2:]...)
	switch action {
	case "add":
		return runSDKAddWebhookAction(cmd, serviceName, webhooks)
	case "remove":
		return runSDKRemoveWebhookAction(cmd, serviceName, webhooks)
	default:
		return fmt.Errorf("unknown sdk webhook action %q", action)
	}
}

func runSDKAddWebhookAction(cmd *cobra.Command, serviceName string, webhooks []string) error {
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
}

func runSDKRemoveWebhookAction(cmd *cobra.Command, serviceName string, webhooks []string) error {
	if err := removeSDKWebhooks(ConfigFile, serviceName, webhooks); err != nil {
		return err
	}
	fmt.Printf("Removed %d webhook(s) from service %s: %s\n", len(webhooks), serviceName, strings.Join(webhooks, ", "))
	return nil
}

func completeSDKWebhookArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return completeSDKConfigServices(toComplete), cobra.ShellCompDirectiveNoFileComp
	case 1:
		return filteredSDKServiceActions(toComplete), cobra.ShellCompDirectiveNoFileComp
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

func selectSDKOperationsInteractively(path, requestedService string) (string, []string, error) {
	if err := requireInteractive("pass the service and operation IDs explicitly"); err != nil {
		return "", nil, err
	}
	return selectSDKOperations(path, requestedService)
}

func selectSDKOperations(path, requestedService string) (string, []string, error) {
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
	if err := requireInteractive("pass the service and webhook IDs explicitly"); err != nil {
		return "", nil, err
	}
	return selectSDKWebhooks(path, requestedService)
}

func selectSDKWebhooks(path, requestedService string) (string, []string, error) {
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
	sdkCmd.Flags().StringVarP(&sdkDownloadOutDir, "out", "o", ".", "Output directory for SDK download")
	addListFlags(sdkCmd, &sdkListFlags)
	sdkCmd.Flags().StringVar(&sdkListTarget, "target", "sdk", "Target type for sdk list")
	sdkCmd.Flags().StringVar(&sdkListLanguage, "language", "", "Target language for sdk list")
	sdkCmd.Flags().BoolVar(&sdkListLatestOnly, "latest-only", true, "Only show the latest SDK per name")

	sdkCmd.AddCommand(sdkPlanCmd)
	sdkPlanCmd.Flags().BoolVar(&sdkPlanJSON, "json", false, "Print plan result JSON, including summary and notifications")
	sdkPlanCmd.Flags().StringVar(&sdkPlanReceiptOut, "receipt-out", "", "Write the plan receipt to a specific path")
	sdkPlanCmd.Flags().StringVar(&sdkPlanOwnerTeam, "owner-team", "", "Owning team ID (required when creating a new SDK)")

	sdkCmd.AddCommand(sdkApplyCmd)
	sdkApplyCmd.Flags().BoolVar(&sdkApplyDownload, "download", false, "Download generated SDK after apply")
	sdkApplyCmd.Flags().StringVar(&sdkApplyPlanID, "plan-id", "", "Apply a specific remote plan ID for a single SDK config")
	sdkApplyCmd.Flags().StringVar(&sdkApplyReceiptPath, "receipt", "", "Read a specific plan receipt for a single SDK config")

	sdkCmd.AddCommand(sdkValidateCmd)

	sdkCmd.AddCommand(sdkDownloadCmd)
	sdkDownloadCmd.Flags().StringVarP(&sdkDownloadOutDir, "out", "o", ".", "Output directory for the SDK")

	sdkCmd.AddCommand(sdkServiceCmd)
	sdkServiceCmd.Flags().StringVar(&sdkAddServiceVersion, "version", "", "Specific version to use when adding a service")
	sdkServiceCmd.Flags().BoolVarP(&sdkServiceActionInteractive, "interactive", "i", false, "Interactively select operations for the service add action")
	sdkServiceCmd.Flags().BoolVar(&sdkServiceActionApply, "apply", false, "Apply SDK config after adding operations")
	sdkServiceCmd.Flags().BoolVar(&sdkServiceActionDownload, "download", false, "Download SDK after apply (implies --apply)")

	sdkCmd.AddCommand(sdkOperationCmd)
	sdkOperationCmd.Flags().BoolVarP(&sdkAddOperationInteractive, "interactive", "i", false, "Interactive operation selection")
	sdkOperationCmd.Flags().BoolVar(&sdkAddOperationApply, "apply", false, "Apply changes after adding operation")
	sdkOperationCmd.Flags().BoolVar(&sdkAddOperationDownload, "download", false, "Download SDK after apply (implies --apply)")

	sdkCmd.AddCommand(sdkWebhookCmd)
	sdkWebhookCmd.Flags().BoolVarP(&sdkAddWebhookInteractive, "interactive", "i", false, "Interactive webhook selection")
}
