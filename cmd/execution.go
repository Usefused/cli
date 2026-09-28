package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

var executionCmd = &cobra.Command{Use: "execution", Short: "Manage hosted Execution Apps", Args: cobra.NoArgs, RunE: requireSubcommand}

var executionPlanCmd = &cobra.Command{
	Use: "plan", Short: "Plan an Execution App", Args: cobra.NoArgs,
	RunE: WithTelemetry("cli.execution.plan", func(cmd *cobra.Command, _ []string) error {
		jsonOut, _ := cmd.Flags().GetBool("json")
		receiptOut, _ := cmd.Flags().GetString("receipt-out")
		ownerTeam, _ := cmd.Flags().GetString("owner-team")
		return runConfigPlan(planOptions{filter: filterExecution, jsonOut: jsonOut, receiptOut: receiptOut, ownerTeamSlug: ownerTeam,
			output: cmd.OutOrStdout(), auditCtx: cmd.Context(), auditAction: cmd.CommandPath()})
	}),
}

var executionApplyCmd = &cobra.Command{
	Use: "apply", Short: "Apply an Execution App plan", Args: cobra.NoArgs,
	RunE: WithTelemetry("cli.execution.apply", func(cmd *cobra.Command, _ []string) error {
		jsonOut, _ := cmd.Flags().GetBool("json")
		planID, _ := cmd.Flags().GetString("plan-id")
		receiptPath, _ := cmd.Flags().GetString("receipt")
		return runConfigApply(withApplyAudit(cmd, applyOptions{filter: filterExecution, jsonOut: jsonOut,
			planID: planID, receiptPath: receiptPath, output: cmd.OutOrStdout()}))
	}),
}

// executionApplyOutput reports App identity and one-time token without SDK package terminology.
type executionApplyOutput struct {
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
func applyExecutionVersion(client *api.Client, cfg *configfile.ParsedConfig, receipt planReceipt) (executionApplyOutput, error) {
	resp, err := client.ApplyExecutionConfig(receipt.PlanID, receipt.SourceHash, receipt.NoToken)
	// An uncertain mutation cannot be reported as a safe retry.
	if err != nil {
		return executionApplyOutput{}, fmt.Errorf("failed to apply Execution App %s: %w", cfg.Execution.Name, err)
	}
	result := executionApplyOutput{ConfigKey: cfg.ConfigKey, PlanID: resp.PlanID, Status: resp.Status,
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
	fmt.Printf("Applied Execution App %s (version %s).\n", cfg.Execution.Name, result.AppID)
	// Plaintext tokens cannot be recovered from Engine after this response.
	if result.ExecutionToken != "" {
		fmt.Printf("  Execution token (shown once): %s\n", result.ExecutionToken)
	}
	return nil
}

// applyExecutionConfigsJSON keeps structured deployment output independent of SDK package fields.
func applyExecutionConfigsJSON(client *api.Client, prepared []preparedConfigApply, opts applyOptions) error {
	results := make([]executionApplyOutput, 0, len(prepared))
	for _, item := range prepared {
		// A mixed apply must not quietly produce one kind's JSON schema for another.
		if item.config.Kind != configfile.KindExecution {
			return fmt.Errorf("structured execution apply requires only execution configs")
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
	RootCmd.AddCommand(executionCmd)
	executionCmd.AddCommand(executionPlanCmd, executionApplyCmd)
	executionPlanCmd.Flags().Bool("json", false, "Print plan result JSON")
	executionPlanCmd.Flags().String("receipt-out", "", "Write the plan receipt to this path")
	executionPlanCmd.Flags().String("owner-team", "", "Optional owning team slug")
	executionApplyCmd.Flags().Bool("json", false, "Print apply receipt as JSON")
	executionApplyCmd.Flags().String("plan-id", "", "Apply a specific remote plan ID")
	executionApplyCmd.Flags().String("receipt", "", "Read a plan receipt from this path")
}
