package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

var sdkInput io.Reader = os.Stdin

var sdkDownloadOutDir string
var sdkListFlags listFlags
var sdkPlanJSON bool
var sdkPlanReceiptOut string
var sdkPlanOwnerTeam string
var sdkApplyDownload bool
var sdkApplyJSON bool
var sdkApplyPlanID string
var sdkApplyReceiptPath string

var sdkCmd = &cobra.Command{
	Use:   "sdk",
	Short: "Manage generated SDKs",
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var sdkListCmd = &cobra.Command{
	Use:   "list",
	Short: "List generated SDKs",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.sdk.list", func(cmd *cobra.Command, _ []string) error {
		return runSDKList(cmd)
	}),
}

var sdkPlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Plan SDK configuration",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.sdk.plan", func(_ *cobra.Command, _ []string) error {
		return runConfigPlan(planOptions{filter: filterSDK, jsonOut: sdkPlanJSON, receiptOut: sdkPlanReceiptOut, ownerTeamSlug: sdkPlanOwnerTeam})
	}),
}

var sdkApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply SDK configuration",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.sdk.apply", func(cmd *cobra.Command, _ []string) error {
		return runConfigApply(withApplyAudit(cmd, applyOptions{
			filter: filterSDK, download: sdkApplyDownload, jsonOut: sdkApplyJSON,
			planID: sdkApplyPlanID, receiptPath: sdkApplyReceiptPath, output: cmd.OutOrStdout(),
		}))
	}),
}

var sdkValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate SDK configuration",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.sdk.validate", func(cmd *cobra.Command, _ []string) error {
		run, err := configfile.LoadRun(effectiveConfigFile())
		if err != nil {
			return err
		}
		count := 0
		for _, config := range run.Configs {
			if config.Kind == configfile.KindSDK {
				count++
			}
		}
		if count == 0 {
			return fmt.Errorf("no sdk configs found")
		}
		if wantsJSON(cmd) {
			return writeJSON(cmd, validationResult("sdk", count))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "validated %d sdk config\n", count)
		return nil
	}),
}

type sdkDownloadTarget struct {
	Name    string
	Version string
}

func validateSDKDownloadArgs(args []string) error {
	if len(args) > 0 {
		return validateExactAppReference(args[0], "sdk download")
	}
	return nil
}

func resolveSDKDownloadTargets(args []string, configPath string) ([]sdkDownloadTarget, error) {
	if len(args) > 0 {
		name, version := parseSDKDownloadName(args[0])
		if err := validateExactAppReference(args[0], "sdk download"); err != nil {
			return nil, err
		}
		return []sdkDownloadTarget{{Name: name, Version: version}}, nil
	}
	run, err := configfile.LoadRun(configPath)
	if err != nil {
		return nil, err
	}
	targets := make([]sdkDownloadTarget, 0, len(run.Configs))
	for _, cfg := range run.Configs {
		if cfg.Kind == configfile.KindSDK {
			if strings.TrimSpace(cfg.SDK.Version) == "" {
				return nil, fmt.Errorf("sdk %q must declare a version before download", cfg.SDK.Name)
			}
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

func validateExactAppReference(raw, action string) error {
	name, version := parseSDKDownloadName(raw)
	if name == "" || strings.Contains(raw, "@") && version == "" {
		return fmt.Errorf("%s requires name@version or an app UUID", action)
	}
	if version != "" {
		return nil
	}
	if _, err := uuid.Parse(name); err != nil {
		// A family name is intentionally insufficient: lifecycle and download
		// operations must identify one immutable app version.
		return fmt.Errorf("%s requires name@version or an app UUID", action)
	}
	return nil
}

func parseSDKDownloadName(raw string) (string, string) {
	name, version, found := strings.Cut(raw, "@")
	if !found {
		return strings.TrimSpace(raw), ""
	}
	return strings.TrimSpace(name), strings.TrimSpace(version)
}

func runSDKList(cmd *cobra.Command) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	page, err := client.ListApps("sdk", sdkListFlags.pageOptions())
	if err != nil {
		return err
	}
	if wantsJSON(cmd) {
		return writeJSONPage(cmd, page.Items, page.Total, sdkListFlags)
	}
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tVERSION\tSTATUS\tSDK_ID\tVERSION_ID")
	for _, sdk := range page.Items {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", sdk.Name, sdk.Version, sdk.Status, sdk.AppFamilyID, sdk.AppID)
	}
	_ = writer.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, sdkListFlags)
	return nil
}

var sdkShowCmd = newSDKVersionReadCommand(
	"show <sdk-name@version-or-version-id>",
	"Show one exact SDK version",
	"cli.sdk.show",
	runSDKShow,
)

var sdkServicesCmd = newSDKVersionReadCommand(
	"services <sdk-name@version-or-version-id>",
	"List services scoped to one exact SDK version",
	"cli.sdk.services",
	runSDKServices,
)

var sdkBucketsCmd = &cobra.Command{
	Use:   "buckets <sdk-name-or-id>",
	Short: "List buckets available to all versions of an SDK",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.sdk.buckets", func(cmd *cobra.Command, args []string) error {
		return runSDKBuckets(cmd, downloadTargetFromName(args[0]))
	}),
}

func newSDKVersionReadCommand(use, short, spanName string, run func(*cobra.Command, sdkDownloadTarget) error) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(cmd, args); err != nil {
				return err
			}
			return validateExactAppReference(args[0], cmd.CommandPath())
		},
		RunE: WithTelemetry(spanName, func(cmd *cobra.Command, args []string) error {
			return run(cmd, downloadTargetFromName(args[0]))
		}),
	}
}

func runSDKShow(cmd *cobra.Command, target sdkDownloadTarget) error {
	if err := validateExactAppTarget(target, "sdk show"); err != nil {
		return err
	}
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	appID, err := client.ResolveSDKAppReference(target.Name, target.Version)
	if err != nil {
		return err
	}
	sdk, err := client.GetApp(appID)
	if err != nil {
		return err
	}
	if wantsJSON(cmd) {
		return writeJSON(cmd, sdk)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "name:\t%s\nversion:\t%s\nstatus:\t%s\nsdk_id:\t%s\nversion_id:\t%s\n", sdk.Name, sdk.Version, sdk.Status, sdk.AppFamilyID, sdk.AppID)
	return nil
}

func runSDKServices(cmd *cobra.Command, target sdkDownloadTarget) error {
	if err := validateExactAppTarget(target, "sdk services"); err != nil {
		return err
	}
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	appID, err := client.ResolveSDKAppReference(target.Name, target.Version)
	if err != nil {
		return err
	}
	services, err := client.ListAppServices(appID)
	if err != nil {
		return err
	}
	if wantsJSON(cmd) {
		return writeJSON(cmd, services)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tSERVICE_ID\tVERSION\tSELECT_ALL\tENDPOINTS\tWEBHOOKS")
	for _, service := range services {
		name := service.ServiceSlug
		if name == "" {
			name = service.ServiceName
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%d\t%d\n", name, service.ServiceID, service.Version, service.SelectAll, service.EndpointCount, service.WebhookCount)
	}
	w.Flush()
	return nil
}

func runSDKBuckets(cmd *cobra.Command, target sdkDownloadTarget) error {
	client, appFamilyID, err := sdkClientAndFamilyID(target)
	if err != nil {
		return err
	}
	buckets, err := client.ListSDKBuckets(appFamilyID)
	if err != nil {
		return err
	}
	if wantsJSON(cmd) {
		return writeJSON(cmd, buckets)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tID\tDEFAULT")
	for _, bucket := range buckets {
		fmt.Fprintf(w, "%s\t%s\t%t\n", bucket.Name, bucket.ID, bucket.IsDefault)
	}
	w.Flush()
	return nil
}

func sdkClientAndFamilyID(target sdkDownloadTarget) (*api.Client, string, error) {
	client, err := getAPIClient()
	if err != nil {
		return nil, "", err
	}
	appFamilyID, err := client.ResolveSDKFamilyReference(target.Name)
	if err != nil {
		return nil, "", err
	}
	return client, appFamilyID, nil
}

func downloadTargetFromName(name string) sdkDownloadTarget {
	sdkName, version := parseSDKDownloadName(name)
	return sdkDownloadTarget{Name: sdkName, Version: version}
}

func validateExactAppTarget(target sdkDownloadTarget, action string) error {
	reference := target.Name
	if target.Version != "" {
		reference += "@" + target.Version
	}
	return validateExactAppReference(reference, action)
}

var sdkDownloadCmd = &cobra.Command{
	Use:   "download [sdk-name@version-or-version-id]",
	Short: "Download the generated SDK for a config",
	Args:  cobra.MaximumNArgs(1),
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.sdk.download", func(cmd *cobra.Command, args []string) error {
		if err := validateSDKDownloadArgs(args); err != nil {
			return err
		}

		sdksToDownload, err := resolveSDKDownloadTargets(args, effectiveConfigFile())
		if err != nil {
			return err
		}

		return runSDKDownloadTargets(cmd, sdksToDownload)
	}),
}

type sdkDownloadOutput struct {
	SDK       string `json:"sdk"`
	VersionID string `json:"version_id"`
	Status    string `json:"status"`
	Path      string `json:"path"`
}

// runSDKDownloadTargets downloads exact SDK versions and renders one output mode.
func runSDKDownloadTargets(cmd *cobra.Command, targets []sdkDownloadTarget) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	results := make([]sdkDownloadOutput, 0, len(targets))
	for _, target := range targets {
		result, err := downloadSDKTarget(client, target)
		if err != nil {
			return err
		}
		results = append(results, result)
		if !wantsJSON(cmd) {
			fmt.Fprintf(cmd.OutOrStdout(), "Downloaded and extracted sdk:%s to %s\n", result.SDK, result.Path)
		}
	}
	if wantsJSON(cmd) {
		return writeJSON(cmd, results)
	}
	return nil
}

// downloadSDKTarget resolves, downloads, and extracts one exact SDK version.
func downloadSDKTarget(client *api.Client, target sdkDownloadTarget) (sdkDownloadOutput, error) {
	appID, err := client.ResolveSDKAppReference(target.Name, target.Version)
	if err != nil {
		return sdkDownloadOutput{}, &sdkApplyStageError{Stage: "resolve", SDKName: target.Name, Err: err}
	}
	if strings.TrimSpace(appID) == "" {
		return sdkDownloadOutput{}, &sdkApplyStageError{Stage: "resolve", SDKName: target.Name, Err: errors.New("generated SDK not found")}
	}
	data, err := client.DownloadSDK(appID)
	if err != nil {
		return sdkDownloadOutput{}, &sdkApplyStageError{Stage: "download", SDKName: target.Name, VersionID: appID, Err: err}
	}
	extractDir := filepath.Join(sdkDownloadOutDir, "fused-sdks", target.Name)
	if err := extractSDKZip(data, extractDir); err != nil {
		return sdkDownloadOutput{}, &sdkApplyStageError{Stage: "extract", SDKName: target.Name, VersionID: appID, Err: err}
	}
	return sdkDownloadOutput{SDK: target.Name, VersionID: appID, Status: "completed", Path: extractDir}, nil
}

var sdkServiceCmd = commandGroup("service", "Manage services in SDK config")

var sdkAddServiceVersion string

func runSDKServiceAddAction(cmd *cobra.Command, serviceName string) error {
	if err := addSDKService(ConfigFile, serviceName, sdkAddServiceVersion); err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "sdk_config")
	fmt.Fprintf(cmd.OutOrStdout(), "Added service %s with version %s\n", serviceName, sdkAddServiceVersion)
	return nil
}

func runSDKServiceRemoveAction(cmd *cobra.Command, serviceName string) error {
	if err := removeSDKService(ConfigFile, serviceName); err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "sdk_config")
	fmt.Fprintf(cmd.OutOrStdout(), "Removed service %s\n", serviceName)
	return nil
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

var sdkAddServiceCmd = &cobra.Command{
	Use:   "add <service-slug>",
	Short: "Add a service to SDK config",
	Args:  cobra.ExactArgs(1),
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.sdk.service.add", func(cmd *cobra.Command, args []string) error {
		return runSDKServiceAddAction(cmd, args[0])
	}),
	ValidArgsFunction: completeSDKServiceNames,
}

var sdkRemoveServiceCmd = &cobra.Command{
	Use:   "remove <service-slug>",
	Short: "Remove a service from SDK config",
	Args:  cobra.ExactArgs(1),
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.sdk.service.remove", func(cmd *cobra.Command, args []string) error {
		return runSDKServiceRemoveAction(cmd, args[0])
	}),
	ValidArgsFunction: completeSDKServiceNames,
}

var sdkOperationCmd = commandGroup("operation", "Manage operations in SDK config")

var sdkAddOperationInteractive bool
var sdkAddOperationApply bool
var sdkAddOperationDownload bool

var sdkAddOperationCmd = &cobra.Command{
	Use:   "add <service-slug> [operation-id...]",
	Short: "Add operations to a service in SDK config",
	Args:  cobra.MinimumNArgs(1),
	RunE: WithTelemetry("cli.sdk.operation.add", func(cmd *cobra.Command, args []string) error {
		return runSDKAddOperationAction(cmd, args[0], args[1:])
	}),
	ValidArgsFunction: completeSDKServiceNames,
}

var sdkRemoveOperationCmd = &cobra.Command{
	Use:   "remove <service-slug> <operation-id...>",
	Short: "Remove operations from a service in SDK config",
	Args:  cobra.MinimumNArgs(2),
	RunE: WithTelemetry("cli.sdk.operation.remove", func(cmd *cobra.Command, args []string) error {
		return runSDKRemoveOperationAction(cmd, args[0], args[1:])
	}),
	ValidArgsFunction: completeSDKServiceNames,
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
		return fmt.Errorf("at least one operation ID is required unless --interactive is set")
	}
	if err := addSDKOperations(ConfigFile, serviceName, operations); err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "sdk_config")
	fmt.Fprintf(cmd.OutOrStdout(), "Added %d operation ID(s) to service %s: %s\n", len(operations), serviceName, strings.Join(operations, ", "))
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
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "sdk_config")
	fmt.Printf("Removed %d operation ID(s) from service %s: %s\n", len(operations), serviceName, strings.Join(operations, ", "))
	return nil
}

func completeSDKServiceNames(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return completeSDKConfigServices(toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

var sdkWebhookCmd = commandGroup("webhook", "Manage webhooks in SDK config")

var sdkAddWebhookInteractive bool

var sdkAddWebhookCmd = &cobra.Command{
	Use:   "add <service-slug> [webhook-id...]",
	Short: "Add webhooks to a service in SDK config",
	Args:  cobra.MinimumNArgs(1),
	RunE: WithTelemetry("cli.sdk.webhook.add", func(cmd *cobra.Command, args []string) error {
		return runSDKAddWebhookAction(cmd, args[0], args[1:])
	}),
	ValidArgsFunction: completeSDKServiceNames,
}

var sdkRemoveWebhookCmd = &cobra.Command{
	Use:   "remove <service-slug> <webhook-id...>",
	Short: "Remove webhooks from a service in SDK config",
	Args:  cobra.MinimumNArgs(2),
	RunE: WithTelemetry("cli.sdk.webhook.remove", func(cmd *cobra.Command, args []string) error {
		return runSDKRemoveWebhookAction(cmd, args[0], args[1:])
	}),
	ValidArgsFunction: completeSDKServiceNames,
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
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "sdk_config")
	fmt.Fprintf(cmd.OutOrStdout(), "Added %d webhook(s) to service %s: %s\n", len(webhooks), serviceName, strings.Join(webhooks, ", "))
	return nil
}

func runSDKRemoveWebhookAction(cmd *cobra.Command, serviceName string, webhooks []string) error {
	if err := removeSDKWebhooks(ConfigFile, serviceName, webhooks); err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "sdk_config")
	fmt.Printf("Removed %d webhook(s) from service %s: %s\n", len(webhooks), serviceName, strings.Join(webhooks, ", "))
	return nil
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
	sdkCmd.AddCommand(sdkListCmd, sdkPlanCmd, sdkApplyCmd, sdkValidateCmd, sdkDownloadCmd, sdkShowCmd, sdkServicesCmd, sdkBucketsCmd)
	addJSONOutputFlag(sdkListCmd, sdkValidateCmd, sdkShowCmd, sdkServicesCmd, sdkBucketsCmd)
	addListFlags(sdkListCmd, &sdkListFlags)
	sdkPlanCmd.Flags().BoolVar(&sdkPlanJSON, "json", false, "Print plan result JSON")
	sdkPlanCmd.Flags().StringVar(&sdkPlanReceiptOut, "receipt-out", "", "Write the plan receipt to this path")
	sdkPlanCmd.Flags().StringVar(&sdkPlanOwnerTeam, "owner-team", "", "Optional owning team slug; defaults to the authenticated person")
	sdkApplyCmd.Flags().BoolVar(&sdkApplyDownload, "download", false, "Download generated SDKs after apply")
	sdkApplyCmd.Flags().BoolVar(&sdkApplyJSON, "json", false, "Print structured apply, generation, and download outcomes as JSON")
	sdkApplyCmd.Flags().StringVar(&sdkApplyPlanID, "plan-id", "", "Apply a specific remote plan ID")
	sdkApplyCmd.Flags().StringVar(&sdkApplyReceiptPath, "receipt", "", "Read a plan receipt from this path")
	sdkDownloadCmd.Flags().StringVarP(&sdkDownloadOutDir, "out", "o", ".", "Output directory for the SDK")
	addJSONOutputFlag(sdkDownloadCmd)

	sdkCmd.AddCommand(sdkServiceCmd)
	sdkServiceCmd.AddCommand(sdkAddServiceCmd, sdkRemoveServiceCmd)
	sdkAddServiceCmd.Flags().StringVar(&sdkAddServiceVersion, "version", "", "Specific version to use when adding a service")

	sdkCmd.AddCommand(sdkOperationCmd)
	sdkOperationCmd.AddCommand(sdkAddOperationCmd, sdkRemoveOperationCmd)
	sdkAddOperationCmd.Flags().BoolVarP(&sdkAddOperationInteractive, "interactive", "i", false, "Interactive operation selection")
	sdkAddOperationCmd.Flags().BoolVar(&sdkAddOperationApply, "apply", false, "Apply changes after adding operation")
	sdkAddOperationCmd.Flags().BoolVar(&sdkAddOperationDownload, "download", false, "Download SDK after apply (implies --apply)")

	sdkCmd.AddCommand(sdkWebhookCmd)
	sdkWebhookCmd.AddCommand(sdkAddWebhookCmd, sdkRemoveWebhookCmd)
	sdkAddWebhookCmd.Flags().BoolVarP(&sdkAddWebhookInteractive, "interactive", "i", false, "Interactive webhook selection")
}
