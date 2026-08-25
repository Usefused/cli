package cmd

import (
	"github.com/spf13/cobra"
)

// importCmd groups deterministic spec planning, reviewed provider discovery,
// and the one explicit apply boundary for Registry service mutation.
var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Discover, plan, and apply Registry service contracts",
	Long: `Use "import plan" when the exact machine-readable source is already known.
Use "import discover" for a provider URL: Fused validates specifications first,
falls back to a bounded documentation crawl, and presents operations and optional
enrichment for review. Both commands produce the same receipt. Only "import apply"
creates or updates a Registry service.`,
	Args: cobra.NoArgs,
	RunE: requireSubcommand,
}

var (
	importPlanName       string
	importPlanSlug       string
	importPlanURL        string
	importPlanVersion    string
	importPlanTarget     string
	importPlanPublic     bool
	importPlanCategory   string
	importPlanOverlay    string
	importPlanReceiptOut string
	importPlanJSON       bool
	importPlanStrict     bool
)

var importPlanCmd = &cobra.Command{
	Use:   "plan [spec-path]",
	Short: "Plan a non-interactive spec import",
	Long: `Parses the given spec (a local file path or an http(s) URL), resolves
whether it targets a new or existing service, and diffs it against the exact
provider version. Use --url for an online source. Read-only -- nothing is
written except the plan record itself. Use --overlay for a local correction
file that Registry will validate and canonicalize with the source.`,
	Args: cobra.MaximumNArgs(1),
	RunE: WithTelemetry("cli.import.plan", func(cmd *cobra.Command, args []string) error {
		specPath := ""
		if len(args) == 1 {
			specPath = args[0]
		}
		// isPublic is nil unless --public was explicitly passed, so the
		// Registry can tell "not specified" apart from "explicitly false"
		// and apply the right default depending on whether this targets a
		// new service or a new version of one that already exists.
		var isPublic *bool
		if cmd.Flags().Changed("public") {
			isPublic = &importPlanPublic
		}
		return runImportPlan(cmd, specPath, importSpecPlanOptions{
			name:       importPlanName,
			slug:       importPlanSlug,
			url:        importPlanURL,
			version:    importPlanVersion,
			target:     importPlanTarget,
			isPublic:   isPublic,
			category:   importPlanCategory,
			overlay:    importPlanOverlay,
			receiptOut: importPlanReceiptOut,
			jsonOut:    importPlanJSON,
			strict:     importPlanStrict,
		})
	}),
}

var (
	importApplyPlanID      string
	importApplyReviewHash  string
	importApplyReceiptPath string
)

var importApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply a plan from import plan or import discover",
	Long: `Commits the plan produced by "import plan" or "import discover": a new
service is created live immediately; an existing provider version is replaced
by a new internal revision, while a different provider version is created
alongside it. Defaults to the most recent local receipt from either command.
Import plan/apply requests allow 20 minutes unless --timeout is explicitly set.`,
	Args: cobra.NoArgs,
	RunE: WithTelemetry("cli.import.apply", func(cmd *cobra.Command, args []string) error {
		return runImportApply(cmd, importSpecApplyOptions{
			planID:      importApplyPlanID,
			reviewHash:  importApplyReviewHash,
			receiptPath: importApplyReceiptPath,
		})
	}),
}

var importStatusJSON bool

var importStatusCmd = &cobra.Command{
	Use:   "status <operation-id>",
	Short: "Read a durable import apply outcome",
	Long: `Reads the operation recorded for an import plan. Use this after an
apply timeout or lost response; status never retries the import mutation.`,
	Args: cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.import.status", func(cmd *cobra.Command, args []string) error {
		return runImportStatus(cmd, args[0], importStatusJSON)
	}),
}

// init registers import commands and their command-scoped receipt flags once.
func init() {
	RootCmd.AddCommand(importCmd)

	importCmd.AddCommand(importPlanCmd)
	importPlanCmd.Flags().StringVar(&importPlanName, "name", "", "Service name (required)")
	importPlanCmd.Flags().StringVar(&importPlanSlug, "slug", "", "Service slug to create or update (required; unique within your account)")
	importPlanCmd.MarkFlagRequired("slug")
	importPlanCmd.Flags().StringVar(&importPlanURL, "url", "", "Import from an online http(s) source")
	importPlanCmd.Flags().StringVar(&importPlanVersion, "version", "", "Provider version when the source does not declare one")
	importPlanCmd.Flags().StringVar(&importPlanTarget, "target", "endpoints", "Contract content to import: all, endpoints, or webhooks")
	importPlanCmd.Flags().BoolVar(&importPlanPublic, "public", false, "Registry visibility: for a brand-new service, marks the service (and its first version) public -- default private if omitted. For a new version of an existing service, stages just that version's visibility -- default public (matching prior versions) if omitted, so existing automation that never passes this flag is unaffected.")
	importPlanCmd.Flags().StringVar(&importPlanCategory, "category", "", "Category for a new service")
	importPlanCmd.Flags().StringVar(&importPlanOverlay, "overlay", "", "Local overlay file applied by the Registry during planning")
	importPlanCmd.Flags().StringVar(&importPlanReceiptOut, "receipt-out", "", "Write the plan receipt to a specific path")
	importPlanCmd.Flags().BoolVar(&importPlanJSON, "json", false, "Print the raw plan response as JSON instead of a summary")
	importPlanCmd.Flags().BoolVar(&importPlanStrict, "strict", false, "Reject plans containing warning or error diagnostics")
	importPlanCmd.MarkFlagRequired("name")

	importCmd.AddCommand(importApplyCmd)
	importApplyCmd.Flags().StringVar(&importApplyPlanID, "plan-id", "", "Apply a specific remote plan ID (requires --review-hash)")
	importApplyCmd.Flags().StringVar(&importApplyReviewHash, "review-hash", "", "Combined review hash to pair with --plan-id")
	importApplyCmd.Flags().StringVar(&importApplyReceiptPath, "receipt", "", "Read a specific plan or discovery receipt (default: most recent local receipt)")

	importCmd.AddCommand(importStatusCmd)
	importStatusCmd.Flags().BoolVar(&importStatusJSON, "json", false, "Print the raw operation status as JSON")
}
