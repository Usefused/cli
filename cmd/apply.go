package cmd

import "github.com/spf13/cobra"

var (
	applyDownload    bool
	applyPlanID      string
	applyReceiptPath string
)

var applyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply changes for Fused configurations",
	Long:  `Applies planned changes for all Fused configurations in the target directory or file.`,
	RunE: WithTelemetry("cli.apply", func(cmd *cobra.Command, args []string) error {
		return runConfigApply(applyOptions{download: applyDownload, planID: applyPlanID, receiptPath: applyReceiptPath})
	}),
}

func init() {
	RootCmd.AddCommand(applyCmd)
	applyCmd.Flags().BoolVar(&applyDownload, "download", false, "Download generated SDKs after apply")
	applyCmd.Flags().StringVar(&applyPlanID, "plan-id", "", "Apply a specific remote plan ID for a single config")
	applyCmd.Flags().StringVar(&applyReceiptPath, "receipt", "", "Read a specific plan receipt for a single config")
}
