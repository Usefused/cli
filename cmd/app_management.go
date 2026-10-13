package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

var appCmd = commandGroup("app", "Manage SDK, REST, MCP, and Unified Apps")
var appListFlags, appVersionFlags listFlags

// appClientAndVersion resolves one immutable target across kinds; ambiguous names are rejected by Engine.
func appClientAndVersion(target string) (*api.Client, string, error) {
	// Never resolve an unversioned name to an implicit latest version for execution or mutations.
	if err := validateExactAppReference(target, "app"); err != nil {
		return nil, "", err
	}
	client, err := getAPIClient()
	if err != nil {
		return nil, "", err
	} // Authentication failures cannot trigger another identity strategy.
	name, version := parseSDKDownloadName(strings.TrimSpace(target))
	id, err := client.ResolveAppReference(name, version)
	return client, id, err
}

// appReadCommand provides an exact-version boundary shared by app inspection and lifecycle actions.
func appReadCommand(use, short string, run func(*cobra.Command, *api.Client, string) error) *cobra.Command {
	c := &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(1)}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		client, id, err := appClientAndVersion(args[0])
		if err != nil {
			return err
		} // A failed lookup must never fall back to a sibling app kind.
		return run(cmd, client, id)
	}
	addJSONOutputFlag(c)
	return c
}

// appListCommand uses one server-paginated cross-kind collection instead of merging independently paginated lists.
func appListCommand() *cobra.Command {
	c := &cobra.Command{Use: "list", Short: "List apps across all delivery types", Args: cobra.NoArgs}
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		} // No Engine lookup may run with invalid configuration.
		kind, err := appTypeFilter(cmd)
		if err != nil {
			return err
		} // Invalid types must not silently broaden the query.
		page, err := client.ListApplications(kind, appListFlags.pageOptions())
		if err != nil {
			return err
		} // Failed pages must not appear empty.
		if wantsJSON(cmd) {
			return writeJSONPage(cmd, page.Items, page.Total, appListFlags)
		} // Preserve pagination for agents.
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tTYPE\tAPP_ID\tVERSIONS\tLATEST\tSTABLE")
		for _, a := range page.Items {
			fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n", a.Name, applicationType(a), a.AppFamilyID, a.VersionCount, a.LatestVersion, a.StableVersion)
		}
		if err := w.Flush(); err != nil {
			return err
		} // Output failures cannot report a successful page.
		printPageSummary(cmd.OutOrStdout(), page.Total, appListFlags)
		return nil
	}
	c.Flags().String("type", "", "Filter by sdk, api, mcp, or unified-app")
	addListFlags(c, &appListFlags)
	addJSONOutputFlag(c)
	return c
}

// appVersionListCommand keeps exact version discovery separate from family listing.
func appVersionListCommand() *cobra.Command {
	c := &cobra.Command{Use: "list [app-name-or-id]", Short: "List app versions across all delivery types", Args: cobra.MaximumNArgs(1)}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		} // Local configuration is required for a scoped read.
		target := ""
		if len(args) > 0 {
			target = args[0]
		} // An omitted family intentionally lists all visible versions.
		page, err := client.ListAppVersions("", target, appVersionFlags.pageOptions())
		if err != nil {
			return err
		} // Resolution errors must not broaden the collection.
		if wantsJSON(cmd) {
			return writeJSONPage(cmd, page.Items, page.Total, appVersionFlags)
		} // Keep the complete immutable identities.
		writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
		fmt.Fprintln(writer, "NAME\tVERSION\tAPP_ID\tVERSION_ID\tSTABLE\tSTABLE_VERSION_ID\tSTATUS\tCREATED\tDEFAULT_TRANSPORT\tSTREAMABLE HTTP (STABLE, RECOMMENDED)\tSTREAMABLE HTTP (VERSION-PINNED)\tSSE (STABLE, LEGACY)\tSSE (VERSION-PINNED, LEGACY)")
		for _, app := range page.Items {
			streamableHTTP, versionedStreamableHTTP, sse, versionedSSE := "", "", "", ""
			// A hard-deactivated promoted version leaves stable endpoints empty until
			// an explicit apply selects the next immutable target.
			if app.TransportURLs != nil {
				streamableHTTP, versionedStreamableHTTP = app.TransportURLs.StreamableHTTP, app.TransportURLs.VersionedStreamableHTTP
				sse, versionedSSE = app.TransportURLs.SSE, app.TransportURLs.VersionedSSE
			}
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%t\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				app.Name, app.Version, app.AppFamilyID, app.AppID, app.Stable, app.StableVersionID, app.Status, app.CreatedAt,
				app.DefaultTransport, streamableHTTP, versionedStreamableHTTP, sse, versionedSSE)
		}
		if err := writer.Flush(); err != nil {
			return err
		} // Report incomplete output.
		printPageSummary(cmd.OutOrStdout(), page.Total, appVersionFlags)
		return nil
	}
	addListFlags(c, &appVersionFlags)
	addJSONOutputFlag(c)
	return c
}

// runAppShow retains the complete Engine-owned app metadata, including kind-specific transport details.
func runAppShow(cmd *cobra.Command, client *api.Client, id string) error {
	result, err := client.GetApp(id)
	if err != nil {
		return err
	} // No synthetic metadata replaces a failed read.
	if result.Kind == "sdk" { // Generated packages expose their asynchronous build outcome.
		generation, generationErr := client.GetSDKGenerationStatus(id)
		if generationErr != nil {
			result.GenerationStatus = "unavailable"
		} else {
			result.GenerationStatus, result.GenerationJobID = generation.Status, generation.JobID
		} // Inspection remains useful when the auxiliary status read fails.
	}
	if wantsJSON(cmd) {
		return writeJSON(cmd, result)
	} // Machine output retains all metadata.
	fmt.Fprintf(cmd.OutOrStdout(), "name:\t%s\nkind:\t%s\nversion:\t%s\nstatus:\t%s\napp_id:\t%s\nversion_id:\t%s\n", result.Name, result.Kind, result.Version, result.Status, result.AppFamilyID, result.AppID)
	if result.Kind == "sdk" {
		fmt.Fprintf(cmd.OutOrStdout(), "generation_status:\t%s\ngeneration_job_id:\t%s\n", result.GenerationStatus, result.GenerationJobID)
	} // Keep build diagnostics visible.
	return nil
}

// runAppServices reads immutable selections for any app kind.
func runAppServices(cmd *cobra.Command, client *api.Client, id string) error {
	result, err := client.ListAppServices(id)
	if err != nil {
		return err
	} // A denied selection read must not look empty.
	return writeJSON(cmd, result)
}

// runAppDeactivate uses the exact version ID and never retries the mutation.
func runAppDeactivate(cmd *cobra.Command, client *api.Client, id string) error {
	if err := client.DeactivateApp(id); err != nil {
		return err
	} // Unknown outcomes must remain errors.
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "app")
	if wantsJSON(cmd) {
		return writeJSON(cmd, map[string]string{"version_id": id, "status": "deactivated"})
	} // Automation receives only confirmed metadata.
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Deactivated app version %s.\n", id)
	return err
}

// appTokenCommand shares family token management while leaving policy validation authoritative in Engine.
func appTokenCommand() *cobra.Command {
	parent := commandGroup("token", "Manage execution tokens shared by an app's versions")
	for _, action := range []string{"generate", "list", "revoke"} {
		action := action
		use := action + " <app-name-or-id>"
		arity := 1
		if action != "list" {
			use += " <token-name>"
			arity = 2
		} // Mutations always identify a named token explicitly.
		c := &cobra.Command{Use: use, Short: strings.ToUpper(action[:1]) + action[1:] + " app execution tokens", Args: cobra.ExactArgs(arity)}
		c.RunE = func(cmd *cobra.Command, args []string) error {
			request := api.AppTokenGenerateRequest{}
			var err error
			if action == "generate" { // Validate all local policy before resolving or issuing a credential.
				request, err = mcpTokenGenerateRequest(cmd, args[1])
				if err != nil {
					return err
				}
				if !cmd.Flags().Changed("allow") {
					request.Allow = nil
				} // Omission preserves kind-specific Engine defaults.
			}
			client, err := getAPIClient()
			if err != nil {
				return err
			} // Do not issue credentials using an alternate account.
			id, err := client.ResolveAppFamilyReference(args[0])
			if err != nil {
				return err
			} // Ambiguous cross-kind names need an explicit family ID.
			switch action { // Dispatch exactly one authorized token operation without automatic retries.
			case "generate":
				result, err := client.GenerateAppToken(id, request)
				if err != nil {
					return err
				} // Preserve uncertain issuance outcomes.
				recordAppliedChange(cmd.Context(), cmd.CommandPath(), "app_token")
				return writeJSON(cmd, result)
			case "list":
				result, err := client.ListAppTokens(id)
				if err != nil {
					return err
				} // Never return a partial credential inventory.
				return writeJSON(cmd, result)
			default:
				if err := client.RevokeAppToken(id, args[1]); err != nil {
					return err
				} // Confirm revocation before recording it.
				recordAppliedChange(cmd.Context(), cmd.CommandPath(), "app_token")
				return writeJSON(cmd, map[string]string{"status": "revoked", "name": args[1]})
			}
		}
		if action == "generate" { // Policy flags belong only to creation; Engine enforces each app kind's capabilities.
			addAppTokenExpiryFlag(c)
			c.Flags().StringSlice("allow", nil, "Exact operation IDs to allow (requires an app supporting scoped tokens)")
			c.Flags().StringArray("fixed-binding", nil, "Bind service-slug,auth-name,end-user-ref[,resource-id]; repeat per auth pair")
		}
		addJSONOutputFlag(c)
		parent.AddCommand(c)
	}
	return parent
}

// runSharedAppInvoke preserves SDK/MCP execution validation and Unified App's distinct success envelope.
func runSharedAppInvoke(cmd *cobra.Command, args []string) error {
	request, err := buildSDKInvokeRequest(args[1], cmd.InOrStdin())
	if err != nil {
		return err
	} // Malformed input must not reach a phone or provider.
	token, err := readSDKInvokeToken(cmd.InOrStdin())
	if err != nil {
		return err
	} // Runtime tokens remain distinct from management credentials.
	client, id, err := appClientAndVersion(args[0])
	if err != nil {
		return err
	} // Exact identity is required before runtime access.
	info, err := client.GetApp(id)
	if err != nil {
		return err
	} // Kind must be authoritative, not inferred from the app's name.
	// Only authored execution may select another explicitly permitted bucket.
	request.BucketID, err = runtimeBucketInvocationFlag(cmd, info.Kind, request.Operation)
	if err != nil {
		return err
	}
	if info.Kind != "unified_app" || request.Operation != "execute" { // Existing physical/unified-operation response validation remains intact for SDK and MCP apps.
		prepared := preparedSDKInvocation{EngineURL: client.BaseURL, AppID: id, Token: token, IdempotencyKey: effectiveSDKInvokeIdempotencyKey(), Request: request}
		start := time.Now()
		response, endpoint, err := executeSDKInvocation(cmd.Context(), prepared)
		if err != nil {
			return err
		} // Never repeat an uncertain execution.
		return writeSDKInvocationOutput(cmd, sdkInvokeOutput{AppID: id, Operation: args[1], Kind: response.Kind, StatusCode: response.StatusCode, Results: response.Results, ElapsedMS: float64(time.Since(start).Microseconds()) / 1000, EngineEndpoint: endpoint})
	}
	// The family route follows traffic, so retain the exact CLI version as an atomic dispatch fence.
	request.ExpectedAppID = id
	return invokeUnifiedAppHTTP(cmd, client.BaseURL, info.AppFamilyID, token, request)
}

// configureAppSurface centralizes shared lifecycle commands without retaining type-specific aliases.
func configureAppSurface() {
	RootCmd.AddCommand(appCmd)
	RootCmd.RemoveCommand(sdkCmd, mcpCmd, unifiedCmd)
	sdkCmd.RemoveCommand(sdkListCmd, sdkVersionsCmd, sdkShowCmd, sdkServicesCmd, sdkBucketsCmd, sdkDeactivateCmd, sdkTokenCmd, sdkOpenAPICmd, sdkInvokeCmd, sdkActivityCmd)
	mcpCmd.RemoveCommand(mcpListCmd, mcpVersionsCmd, mcpDeactivateCmd, mcpTokenCmd)
	appCmd.AddCommand(appListCommand(), appReadCommand("show <app-name@version-or-version-id>", "Show one exact app version", runAppShow), appReadCommand("deactivate <app-name@version-or-version-id>", "Permanently deactivate one exact app version", runAppDeactivate), appTokenCommand())
	version := commandGroup("versions", "Inspect app versions")
	version.AddCommand(appVersionListCommand())
	appCmd.AddCommand(version)
	service := commandGroup("service", "Inspect app services or edit SDK/API service selections")
	service.AddCommand(appReadCommand("list <app-name@version-or-version-id>", "List services selected by one app version", runAppServices))
	appCmd.AddCommand(service)
	relocateCommand(appCmd, sdkOpenAPICmd, "openapi <app-name@version-or-version-id>", "Export one exact app version as OpenAPI")
	relocateCommand(appCmd, sdkInvokeCmd, "invoke <app-name@version-or-version-id> <operation>", "Invoke one operation on an exact app version")
	sdkInvokeCmd.RunE = runSharedAppInvoke
	sdkInvokeCmd.Flags().String("bucket-id", "", "Select a permitted runtime bucket UUID for Unified App execute")
	sdkInvokeTokenEnv = "FUSED_APP_TOKEN"
	sdkInvokeCmd.Flags().Lookup("token-env").DefValue = "FUSED_APP_TOKEN"
	sdkCmd.Short = "Build, configure, and download generated SDKs"
	buckets := commandGroup("bucket", "Inspect app bucket bindings")
	buckets.AddCommand(appBucketListCommand())
	appCmd.AddCommand(buckets)
	relocateCommand(appCmd, sdkActivityCmd, "activity <app-name@version-or-version-id>", "List app execution receipts")
	// MCP capability inspection remains type-specific because it includes resources and prompts.
	operations := commandGroup("operation", "Inspect MCP capabilities or edit SDK/API operation selections")
	appCmd.AddCommand(operations)
	relocateCommand(operations, mcpOperationsCmd, "list <app-name@version-or-version-id>", "List exact MCP app capabilities")
	configureAppAuthoring(service, operations)
}

// decodeSharedAppReceipt preserves Unified App output as raw JSON rather than dropping fields through the SDK schema.
func decodeSharedAppReceipt(data []byte) (map[string]json.RawMessage, error) {
	var result map[string]json.RawMessage
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	} // Malformed runtime output is never a successful execution.
	var status string
	if err := json.Unmarshal(result["status"], &status); err != nil {
		return nil, fmt.Errorf("missing Unified App execution status")
	}
	if status != "succeeded" {
		return nil, fmt.Errorf("Unified App execution status: %s", status)
	} // Failed runs are not replayed.
	return result, nil
}

// invokeUnifiedAppHTTP sends one authenticated request and preserves the hosted execution receipt.
func invokeUnifiedAppHTTP(cmd *cobra.Command, engine, id, token string, input sdkInvokeRequest) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	} // Direct helper calls still require a valid request context.
	request, _, err := newSDKInvokeHTTPRequest(ctx, preparedSDKInvocation{EngineURL: engine, AppID: id, Token: token, Request: input, IdempotencyKey: effectiveSDKInvokeIdempotencyKey()})
	if err != nil {
		return err
	} // No request is sent when serialization fails.
	client := &http.Client{Timeout: RequestTimeout, CheckRedirect: rejectSDKInvokeRedirect}
	response, err := client.Do(request)
	if err != nil {
		return sdkInvokeTransportError(err)
	} // Unknown outcomes are never retried.
	defer response.Body.Close()
	body, err := readBoundedSDKInvokeResponse(response.Body)
	if err != nil {
		return err
	} // Bound memory even for provider-generated output.
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return decodeSDKInvokeHTTPError(response.StatusCode, body)
	} // Honor runtime denial and failure status.
	receipt, err := decodeSharedAppReceipt(body)
	if err != nil {
		return err
	} // An unsuccessful receipt cannot be reported as completed work.
	return writeJSON(cmd, receipt)
}

// appBucketListCommand lists family bindings without assuming SDK delivery.
func appBucketListCommand() *cobra.Command {
	c := &cobra.Command{Use: "list <app-name-or-id>", Short: "List buckets available to an app", Args: cobra.ExactArgs(1)}
	c.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		} // Reads require valid management credentials.
		id, err := client.ResolveAppFamilyReference(args[0])
		if err != nil {
			return err
		} // Reject ambiguous families before retrieving bindings.
		result, err := client.ListSDKBuckets(id)
		if err != nil {
			return err
		} // Do not fabricate an empty binding list on failure.
		return writeJSON(cmd, result)
	}
	addJSONOutputFlag(c)
	return c
}

// applicationType distinguishes direct REST delivery from generated SDK packages.
func applicationType(a api.ApplicationSummary) string {
	if a.Kind == "sdk" && a.DeliveryMode == "api" {
		return "api"
	} // REST apps share SDK-kind configuration in Engine.
	return strings.ReplaceAll(a.Kind, "_", "-")
}

// appTypeFilter maps public type spelling to the Engine's authoritative catalog filter.
func appTypeFilter(cmd *cobra.Command) (string, error) {
	kind, _ := cmd.Flags().GetString("type")
	switch kind { // Reject typos instead of returning an unrelated inventory.
	case "", "sdk", "api", "mcp":
		return kind, nil
	case "unified-app":
		return "unified_app", nil
	default:
		return "", fmt.Errorf("unsupported app type %q: choose sdk, api, mcp, or unified-app", kind)
	}
}

// selectedAppConfigKind derives the app workflow from local config without including workspace or webhook changes.
func selectedAppConfigKind() (configKindFilter, int, error) {
	run, err := configfile.LoadRun(effectiveConfigFile())
	if err != nil {
		return "", 0, err
	} // Invalid configs must fail before remote requests.
	var kind configKindFilter
	count := 0
	for _, cfg := range run.Configs {
		switch cfg.Kind { // Other resource kinds are outside the app workflow.
		case configfile.KindSDK, configfile.KindMCP, configfile.KindUnifiedApp:
			next := configKindFilter(cfg.Kind)
			if count > 0 && kind != next {
				return "", 0, fmt.Errorf("multiple app types found; select an app config with --file")
			} // Avoid ambiguous apply output and unrelated mutations.
			kind = next
			count++
		}
	}
	if count == 0 {
		return "", 0, fmt.Errorf("no app configs found; select an SDK, REST, MCP, or Unified App config with --file")
	} // Never fall back to all resources.
	return kind, count, nil
}

// runAppPlan dispatches from the config kind while retaining SDK credential review and Unified App source extraction.
func runAppPlan(cmd *cobra.Command, args []string) error {
	kind, _, err := selectedAppConfigKind()
	if err != nil {
		return err
	} // Determine scope before planning.
	if kind == filterUnified {
		return unifiedPlanCmd.RunE(cmd, args)
	} // Unified source preparation precedes its plan.
	if err := validateSDKPlanInteraction(); err != nil {
		return err
	} // Conflicting interaction flags cannot produce mixed JSON.
	return runConfigPlan(planOptions{filter: kind, jsonOut: sdkPlanJSON, receiptOut: sdkPlanReceiptOut, ownerTeamSlug: sdkPlanOwnerTeam, interactive: kind == filterSDK && sdkPlanUsesInteractiveCredentialRemediation(), output: cmd.OutOrStdout(), auditCtx: cmd.Context(), auditAction: cmd.CommandPath()})
}

// runAppApply applies only the type selected by the config, rejecting unsupported package requests before mutation.
func runAppApply(cmd *cobra.Command, _ []string) error {
	kind, _, err := selectedAppConfigKind()
	if err != nil {
		return err
	} // Config validation precedes remote requests.
	if sdkApplyDownload && kind != filterSDK {
		return fmt.Errorf("--download is supported only for generated SDK apps")
	} // A capability error must precede apply.
	return runConfigApply(withApplyAudit(cmd, applyOptions{filter: kind, download: sdkApplyDownload, jsonOut: sdkApplyJSON, planID: sdkApplyPlanID, receiptPath: sdkApplyReceiptPath, output: cmd.OutOrStdout()}))
}

// runAppValidate validates whichever supported app type the selected file declares.
func runAppValidate(cmd *cobra.Command, _ []string) error {
	kind, count, err := selectedAppConfigKind()
	if err != nil {
		return err
	} // Loading already performs schema validation.
	if wantsJSON(cmd) {
		return writeJSON(cmd, validationResult(string(kind), count))
	} // Keep structured validation available across kinds.
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "validated %d %s app config(s)\n", count, kind)
	return err
}

// configureAppAuthoring moves each supported capability under app without retaining typed root aliases.
func configureAppAuthoring(service, operations *cobra.Command) {
	for _, c := range []*cobra.Command{sdkPlanCmd, sdkApplyCmd, sdkValidateCmd, sdkDownloadCmd, sdkSyncCmd, authClientCmd, adminClientCmd, sdkWebhookCmd} {
		relocateCommand(appCmd, c, c.Use, c.Short)
	}
	sdkPlanCmd.Short, sdkApplyCmd.Short, sdkValidateCmd.Short = "Plan the app type declared by config", "Apply the app type declared by config", "Validate the selected app config"
	sdkPlanCmd.RunE, sdkApplyCmd.RunE, sdkValidateCmd.RunE = runAppPlan, runAppApply, runAppValidate
	sdkDownloadCmd.Use = "download [app-name@version-or-version-id]"
	sdkDownloadCmd.Short = "Download a generated SDK app package"
	for _, c := range sdkServiceCmd.Commands() {
		relocateCommand(service, c, c.Use, c.Short)
	}
	for _, c := range sdkOperationCmd.Commands() {
		relocateCommand(operations, c, c.Use, c.Short)
	}
	for _, c := range unifiedCmd.Commands() {
		switch c.Name() { // Keep only capabilities absent from the shared workflow.
		case "source", "schedule", "bundle", "promote":
			relocateCommand(appCmd, c, c.Use, c.Short)
		}
	}
}
