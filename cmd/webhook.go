package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Usefused/cli/internal/configfile"
)

// webhookCmd remains separate from apps because kind: webhook
// has no operations/webhooks-selection surface of its own (that lives on
// whichever kind: sdk/kind: mcp app declares webhook_attachment) and no
// generated package or deployed runtime -- it only reconciles rows in
// fused_workspace_webhooks, so plan/apply/validate is the whole surface. See
// plans/plan-webhook-kind.md's CLI section.
var webhookCmd = &cobra.Command{
	Use:   "webhook",
	Short: "Manage Fused webhook registration configuration",
	Long:  `Manage kind: webhook config files (named bundles of webhook ingress registrations spanning one or more services) and plan/apply their changes.`,
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var webhookPlanJSON bool
var webhookPlanReceiptOut string
var webhookPlanOwnerTeam string
var webhookApplyPlanID string
var webhookApplyReceiptPath string

var webhookPlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Plan webhook configuration",
	Args:  cobra.NoArgs,
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.webhook.plan", func(cmd *cobra.Command, args []string) error {
		return runConfigPlan(planOptions{filter: filterWebhook, jsonOut: webhookPlanJSON, receiptOut: webhookPlanReceiptOut, ownerTeamSlug: webhookPlanOwnerTeam})
	}),
}

var webhookApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply webhook configuration",
	Args:  cobra.NoArgs,
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.webhook.apply", func(cmd *cobra.Command, args []string) error {
		return runConfigApply(withApplyAudit(cmd, applyOptions{filter: filterWebhook, planID: webhookApplyPlanID, receiptPath: webhookApplyReceiptPath}))
	}),
}

var webhookValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate webhook configuration",
	Args:  cobra.NoArgs,
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.webhook.validate", func(cmd *cobra.Command, args []string) error {
		run, err := configfile.LoadRun(effectiveConfigFile())
		if err != nil {
			return err
		}
		count := 0
		for _, cfg := range run.Configs {
			if cfg.Kind == configfile.KindWebhook {
				count++
			}
		}
		if count == 0 {
			return fmt.Errorf("no webhook configs found")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "validated %d webhook config\n", count)
		return nil
	}),
}

func init() {
	RootCmd.AddCommand(webhookCmd)

	webhookCmd.AddCommand(webhookPlanCmd)
	webhookPlanCmd.Flags().BoolVar(&webhookPlanJSON, "json", false, "Print plan result JSON, including summary and notifications")
	webhookPlanCmd.Flags().StringVar(&webhookPlanReceiptOut, "receipt-out", "", "Write the plan receipt to a specific path")
	webhookPlanCmd.Flags().StringVar(&webhookPlanOwnerTeam, "owner-team", "", "Optional owning team slug; defaults to the authenticated person")

	webhookCmd.AddCommand(webhookApplyCmd)
	webhookApplyCmd.Flags().StringVar(&webhookApplyPlanID, "plan-id", "", "Apply a specific remote plan ID for a single webhook config")
	webhookApplyCmd.Flags().StringVar(&webhookApplyReceiptPath, "receipt", "", "Read a specific plan receipt for a single webhook config")

	webhookCmd.AddCommand(webhookValidateCmd)
}
