package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	apiKey string
	apiURL string
)

var rootCmd = &cobra.Command{
	Use:   "fused-cli",
	Short: "A CLI for discovering and generating Fused SDKs",
	Long: `fused-cli allows you to discover API services and endpoints
and generate ready-to-use SDKs.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
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
