package cmd

import (
	"github.com/spf13/cobra"
)

var (
	planJSON       bool
	planReceiptOut string
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Plan changes for Fused configurations",
	Long:  `Discovers and plans changes for all Fused configurations in the target directory or file.`,
	RunE: WithTelemetry("cli.plan", func(cmd *cobra.Command, args []string) error {
		return runConfigPlan(planOptions{jsonOut: planJSON, receiptOut: planReceiptOut})
	}),
}

func init() {
	RootCmd.AddCommand(planCmd)
	planCmd.Flags().BoolVar(&planJSON, "json", false, "Print plan result JSON, including summary and notifications")
	planCmd.Flags().StringVar(&planReceiptOut, "receipt-out", "", "Write the plan receipt to a specific path")
}
