package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

var unifiedCmd = &cobra.Command{Use: "unified-app", Short: "Manage hosted Unified Apps", Args: cobra.NoArgs, RunE: requireSubcommand}

var unifiedPlanCmd = &cobra.Command{
	Use: "plan", Short: "Plan a Unified App", Args: cobra.NoArgs,
	RunE: WithTelemetry("cli.unified.plan", func(cmd *cobra.Command, _ []string) error {
		changed, err := syncUnifiedAppSources(effectiveConfigFile(), false)
		if err != nil {
			return err
		}
		// Local source extraction is a user-triggered write and belongs in the same audit trail as apply.
		if changed > 0 {
			recordAppliedChange(cmd.Context(), cmd.CommandPath(), "unified_app.source")
		}
		jsonOut, _ := cmd.Flags().GetBool("json")
		receiptOut, _ := cmd.Flags().GetString("receipt-out")
		ownerTeam, _ := cmd.Flags().GetString("owner-team")
		return runConfigPlan(planOptions{filter: filterUnified, jsonOut: jsonOut, receiptOut: receiptOut, ownerTeamSlug: ownerTeam,
			output: cmd.OutOrStdout(), auditCtx: cmd.Context(), auditAction: cmd.CommandPath()})
	}),
}

// unifiedSyncCmd externalizes local authoring source without changing the deployed App version.
var unifiedSyncCmd = &cobra.Command{
	Use: "sync", Short: "Create editable TypeScript files for local Unified App configs", Args: cobra.NoArgs,
	RunE: WithTelemetry("cli.unified.sync", func(cmd *cobra.Command, _ []string) error {
		changed, err := syncUnifiedAppSources(effectiveConfigFile(), true)
		if err != nil {
			return err
		}
		// Sync records only actual local mutations so no-op reads do not appear as changes.
		if changed > 0 {
			recordAppliedChange(cmd.Context(), cmd.CommandPath(), "unified_app.source")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Synchronized %d Unified App source file(s).\n", changed)
		return nil
	}),
}

var unifiedApplyCmd = &cobra.Command{
	Use: "apply", Short: "Apply a Unified App plan", Args: cobra.NoArgs,
	RunE: WithTelemetry("cli.unified.apply", func(cmd *cobra.Command, _ []string) error {
		jsonOut, _ := cmd.Flags().GetBool("json")
		planID, _ := cmd.Flags().GetString("plan-id")
		receiptPath, _ := cmd.Flags().GetString("receipt")
		return runConfigApply(withApplyAudit(cmd, applyOptions{filter: filterUnified, jsonOut: jsonOut,
			planID: planID, receiptPath: receiptPath, output: cmd.OutOrStdout()}))
	}),
}

// unifiedApplyOutput reports App identity and one-time token without SDK package terminology.
type unifiedApplyOutput struct {
	ConfigKey        string                `json:"config_key"`
	PlanID           string                `json:"plan_id"`
	Status           string                `json:"status"`
	AppFamilyID      string                `json:"app_family_id"`
	AppID            string                `json:"app_id"`
	ExecutionToken   string                `json:"execution_token,omitempty"`
	HostedMCP        bool                  `json:"hosted_mcp,omitempty"`
	MCPTransportURLs *api.MCPTransportURLs `json:"mcp_transport_urls,omitempty"`
}

// applyExecutionVersion sends the exact plan receipt to Engine and retains the one-time token only in output.
func applyExecutionVersion(client *api.Client, cfg *configfile.ParsedConfig, receipt planReceipt) (unifiedApplyOutput, error) {
	resp, err := client.ApplyUnifiedAppConfig(receipt.PlanID, receipt.SourceHash, receipt.NoToken)
	// An uncertain mutation cannot be reported as a safe retry.
	if err != nil {
		return unifiedApplyOutput{}, fmt.Errorf("failed to apply Unified App %s: %w", cfg.UnifiedApp.Name, err)
	}
	result := unifiedApplyOutput{ConfigKey: cfg.ConfigKey, PlanID: resp.PlanID, Status: resp.Status,
		AppFamilyID: resp.AppFamilyID, AppID: resp.AppID, ExecutionToken: resp.ExecutionToken, HostedMCP: resp.HostedMCP}
	// The optional transport shares this exact App version and family token.
	if resp.HostedMCP {
		result.MCPTransportURLs = &resp.MCPTransportURLs
	}
	return result, nil
}

// applyPreparedExecution prints only deployment metadata and the token Engine returns once.
func applyPreparedExecution(client *api.Client, cfg *configfile.ParsedConfig, receipt planReceipt) error {
	result, err := applyExecutionVersion(client, cfg, receipt)
	if err != nil {
		return err
	}
	fmt.Printf("Applied Unified App %s (version %s).\n", cfg.UnifiedApp.Name, result.AppID)
	// Plaintext tokens cannot be recovered from Engine after this response.
	if result.ExecutionToken != "" {
		fmt.Printf("  Execution token (shown once): %s\n", result.ExecutionToken)
	}
	return nil
}

// applyUnifiedAppConfigsJSON keeps structured deployment output independent of SDK package fields.
func applyUnifiedAppConfigsJSON(client *api.Client, prepared []preparedConfigApply, opts applyOptions) error {
	results := make([]unifiedApplyOutput, 0, len(prepared))
	for _, item := range prepared {
		// A mixed apply must not quietly produce one kind's JSON schema for another.
		if item.config.Kind != configfile.KindUnifiedApp {
			return fmt.Errorf("structured unified-app apply requires only unified_app configs")
		}
		result, err := applyExecutionVersion(client, item.config, item.receipt)
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

// init registers the dedicated hosted App workflow beside SDK and MCP commands.
func init() {
	RootCmd.AddCommand(unifiedCmd)
	unifiedCmd.AddCommand(unifiedPlanCmd, unifiedApplyCmd, unifiedSyncCmd)
	unifiedPlanCmd.Flags().Bool("json", false, "Print plan result JSON")
	unifiedPlanCmd.Flags().String("receipt-out", "", "Write the plan receipt to this path")
	unifiedPlanCmd.Flags().String("owner-team", "", "Optional owning team slug")
	unifiedApplyCmd.Flags().Bool("json", false, "Print apply receipt as JSON")
	unifiedApplyCmd.Flags().String("plan-id", "", "Apply a specific remote plan ID")
	unifiedApplyCmd.Flags().String("receipt", "", "Read a plan receipt from this path")
}
