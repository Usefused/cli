package cmd

import (
	"fmt"
	"os"

	"github.com/Usefused/cli/internal/config"
	"github.com/spf13/cobra"
)

var Version = "dev"

var (
	APIKey    string
	EngineURL string
)

// RootCmd is exported for testing.
var RootCmd = &cobra.Command{
	Use:     "fused-cli",
	Version: Version,
	Short:   "Turn any API into a typed SDK or MCP server — powered by Fused.",
	Long: `Fused CLI lets you register API services, select the endpoints you care about,
and instantly generate type-safe SDKs or MCP servers ready for production.`,
}

func Execute() {
	startUpdateCheck()
	if err := RootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	printUpdateNudge()
}

func init() {
	RootCmd.PersistentFlags().StringVar(&APIKey, "key", "", "API key (overrides config & FUSED_API_KEY)")
	RootCmd.PersistentFlags().StringVar(&EngineURL, "engine-url", "", "Fused Engine URL (overrides config & FUSED_ENGINE_URL)")
}

// GetEngineURL resolves the Engine URL.
// Resolution order: flag -> env -> config file -> error.
func GetEngineURL() (string, error) {
	if EngineURL != "" {
		return EngineURL, nil
	}
	if env := os.Getenv("FUSED_ENGINE_URL"); env != "" {
		return env, nil
	}
	cfgVal, err := config.Get("engine-url")
	if err == nil && cfgVal != "" {
		return cfgVal, nil
	}
	// Give a clear setup hint if missing.
	return "", fmt.Errorf("engine-url is not configured.\n\nRun:\n  fused-cli config set engine-url <url>\n\nOr set FUSED_ENGINE_URL environment variable.")
}

// GetAPIKey resolves the API key.
// Resolution order: flag -> env -> config file -> empty string.
func GetAPIKey() string {
	if APIKey != "" {
		return APIKey
	}
	if env := os.Getenv("FUSED_API_KEY"); env != "" {
		return env
	}
	if cfgVal, err := config.Get("api-key"); err == nil && cfgVal != "" {
		return cfgVal
	}
	return ""
}
