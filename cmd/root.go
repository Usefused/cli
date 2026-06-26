package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var Version = "dev"

var (
	apiKey string
	apiURL string
)

var rootCmd = &cobra.Command{
	Use:   "fused-cli",
	Short: "Turn any API into a typed SDK or MCP server — powered by Fused.",
	Long: `Fused CLI lets you register API services, select the endpoints you care about,
and instantly generate type-safe SDKs or MCP servers ready for production.`,
}

func Execute() {
	startUpdateCheck()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	printUpdateNudge()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&apiKey, "key", "", "FUSED_API_KEY (can also be set via FUSED_API_KEY env var)")
	rootCmd.PersistentFlags().StringVar(&apiURL, "api-url", "https://api.usefused.com", "Fused backend URL")
}

// GetAPIKey resolves the API key from flag or environment variable
func GetAPIKey() string {
	if apiKey != "" {
		return apiKey
	}
	return os.Getenv("FUSED_API_KEY")
}
