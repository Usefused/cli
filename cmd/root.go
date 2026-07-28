package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"os"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/config"
	"github.com/spf13/cobra"
)

var Version = "dev"

var (
	APIKey        string
	EngineURL     string
	ConfigFile    string
	showReadme    bool
	ReadmeContent string

	// EmbeddedSkillFS holds the whole skills/ tree (every fused-cli skill,
	// every version folder) baked into the binary at build time (see main.go's
	// go:embed). It is only an offline fallback -- `fused-cli skill print`/
	// `skill install` prefer fetching this build's own version folder from
	// GitHub's main branch at runtime (see skill.go) so a doc-only skill fix
	// doesn't require a new CLI release. This embedded copy is used only when
	// that fetch fails (any file, since resolveSkillFiles is all-or-nothing).
	EmbeddedSkillFS fs.FS
)

// RootCmd is exported for testing.
var RootCmd = &cobra.Command{
	Use:     "fused-cli",
	Version: Version,
	Short:   "Manage Fused Engine, Registry, workspace, and runtime configuration.",
	Long: `Fused CLI is the config-as-code and operations CLI for the Fused
integration layer. Use it to connect to a Fused Engine, import API services,
apply workspace configuration, manage buckets and secrets, configure webhooks,
and operate SDK or MCP artifacts when you need them.`,
	Run: func(cmd *cobra.Command, args []string) {
		if showReadme {
			fmt.Print(ReadmeContent)
			os.Exit(0)
		}
		cmd.Help()
	},
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if showReadme {
			fmt.Print(ReadmeContent)
			os.Exit(0)
		}
	},
}

func NewRootCommand() *cobra.Command {
	return RootCmd
}

func Execute() {
	startUpdateCheck()

	shutdown := InitTelemetry()
	defer func() {
		// Do not pass a nil context
		_ = shutdown(context.Background())
	}()

	if err := RootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	printUpdateNudge()
}

func init() {
	RootCmd.PersistentFlags().StringVar(&APIKey, "key", "", "API key (overrides config & FUSED_API_KEY)")
	RootCmd.PersistentFlags().StringVar(&EngineURL, "engine-url", "", "Fused Engine URL (overrides config & FUSED_ENGINE_URL)")
	RootCmd.PersistentFlags().StringVarP(&ConfigFile, "file", "f", "", "Path to a Fused config file (disables .fused/ discovery)")
	RootCmd.PersistentFlags().BoolVar(&showReadme, "readme", false, "Print the full CLI README text and exit")
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

// getAPIClient returns an initialized API client.
func getAPIClient() (*api.Client, error) {
	url, err := GetEngineURL()
	if err != nil {
		return nil, err
	}
	return api.NewClient(url, GetAPIKey()), nil
}
