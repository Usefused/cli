package cmd

import (
	"github.com/spf13/cobra"
)

// importCmd is the parent for the non-interactive spec import flow (sprint:
// "Non-Interactive Multi-Format Spec Import via CLI"). Unlike plan/apply
// above (which discover .fused/ config files), import takes a spec directly
// -- there's no declarative config file to point at, since the spec IS the
// input, and it always imports everything the spec describes (no
// endpoint-picking prompt).
var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Import an API spec (OpenAPI, AsyncAPI, or Postman) into the Registry",
	Long: `Import a spec as a Fused service without the conversational import agent --
suitable for a local run or a CI step. Always imports everything the spec
describes (no endpoint-picking prompt). Mirrors this CLI's plan/apply shape:
"import plan" computes what would change and who else relies on the version
being touched; "import apply" commits it.`,
}

var (
	importPlanName       string
	importPlanSlug       string
	importPlanPublic     bool
	importPlanCategory   string
	importPlanReceiptOut string
	importPlanJSON       bool
)

var importPlanCmd = &cobra.Command{
	Use:   "plan <spec-path-or-url>",
	Short: "Plan a non-interactive spec import",
	Long: `Parses the given spec (a local file path or an http(s) URL), resolves
whether it targets a new or existing service (via --slug), diffs it against
the live service if one exists, and auto-selects a publish strategy. Read-only
-- nothing is written except the plan record itself.`,
	Args: cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.import.plan", func(cmd *cobra.Command, args []string) error {
		return runImportPlan(cmd, args[0], importSpecPlanOptions{
			name:       importPlanName,
			slug:       importPlanSlug,
			isPublic:   importPlanPublic,
			category:   importPlanCategory,
			receiptOut: importPlanReceiptOut,
			jsonOut:    importPlanJSON,
		})
	}),
}

var (
	importApplyPlanID      string
	importApplySourceHash  string
	importApplyReceiptPath string
)

var importApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply a previously planned spec import",
	Long: `Commits the plan produced by "import plan": a new service is created live
immediately; an existing service is appended and published in the same
request, with the strategy plan already resolved. Defaults to the most
recent local plan receipt.`,
	RunE: WithTelemetry("cli.import.apply", func(cmd *cobra.Command, args []string) error {
		return runImportApply(cmd, importSpecApplyOptions{
			planID:      importApplyPlanID,
			sourceHash:  importApplySourceHash,
			receiptPath: importApplyReceiptPath,
		})
	}),
}

func init() {
	RootCmd.AddCommand(importCmd)

	importCmd.AddCommand(importPlanCmd)
	importPlanCmd.Flags().StringVar(&importPlanName, "name", "", "Service name (required)")
	importPlanCmd.Flags().StringVar(&importPlanSlug, "slug", "", "Existing service slug to update -- omit to always create a new service")
	importPlanCmd.Flags().BoolVar(&importPlanPublic, "public", false, "Mark a new service public (default: private)")
	importPlanCmd.Flags().StringVar(&importPlanCategory, "category", "", "Category for a new service")
	importPlanCmd.Flags().StringVar(&importPlanReceiptOut, "receipt-out", "", "Write the plan receipt to a specific path")
	importPlanCmd.Flags().BoolVar(&importPlanJSON, "json", false, "Print the raw plan response as JSON instead of a summary")
	importPlanCmd.MarkFlagRequired("name")

	importCmd.AddCommand(importApplyCmd)
	importApplyCmd.Flags().StringVar(&importApplyPlanID, "plan-id", "", "Apply a specific remote plan ID (requires --source-hash)")
	importApplyCmd.Flags().StringVar(&importApplySourceHash, "source-hash", "", "Source hash to pair with --plan-id")
	importApplyCmd.Flags().StringVar(&importApplyReceiptPath, "receipt", "", "Read a specific plan receipt (default: most recent local receipt)")
}
