package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/config"
	"github.com/spf13/cobra"
)

var Version = "dev"

var (
	APIKey           string
	EngineURL        string
	ConfigFile       string
	showReadme       bool
	ReadmeContent    string
	NoInput          bool
	RequestID        string
	RequestTimeout   = api.DefaultTimeout
	executionContext = context.Background()

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
	Use:           "fused-cli",
	Version:       Version,
	Short:         "Manage Fused Engine, Registry, workspace, and runtime configuration.",
	SilenceErrors: true,
	SilenceUsage:  true,
	Long: `Fused CLI is the config-as-code and operations CLI for the Fused
integration layer. Use it to connect to a Fused Engine, import API services,
apply workspace configuration, manage buckets and secrets, configure webhooks,
generate SDKs, and deploy MCP servers.`,
	Run: func(cmd *cobra.Command, args []string) {
		if showReadme {
			fmt.Print(ReadmeContent)
			os.Exit(0)
		}
		cmd.Help()
	},
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if showReadme {
			fmt.Print(ReadmeContent)
			os.Exit(0)
		}
		return validateExecutionOptions()
	},
}

func NewRootCommand() *cobra.Command {
	return RootCmd
}

func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	executionContext = ctx
	RootCmd.SetContext(ctx)
	updateStarted := startUpdateCheck()

	shutdown := InitTelemetry()
	defer func() {
		// Do not pass a nil context
		_ = shutdown(context.Background())
	}()

	if err := RootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if updateStarted {
		printUpdateNudge()
	}
}

func init() {
	RootCmd.PersistentFlags().StringVar(&APIKey, "key", "", "Engine credential (overrides saved login, FUSED_API_KEY & FUSED_LICENSE_KEY)")
	RootCmd.PersistentFlags().StringVar(&EngineURL, "engine-url", "", "Fused Engine URL (overrides config & FUSED_ENGINE_URL)")
	RootCmd.PersistentFlags().StringVarP(&ConfigFile, "file", "f", "", "Path to a Fused config file (disables .fused/ discovery)")
	RootCmd.PersistentFlags().BoolVar(&NoInput, "no-input", false, "Fail instead of prompting for input (also enabled by CI=true)")
	RootCmd.PersistentFlags().DurationVar(&RequestTimeout, "timeout", api.DefaultTimeout, "Maximum duration for an Engine request")
	RootCmd.PersistentFlags().StringVar(&RequestID, "request-id", "", "Attach an audit correlation ID to Engine requests")
	RootCmd.PersistentFlags().BoolVar(&showReadme, "readme", false, "Print the full CLI README text and exit")
}

func validateExecutionOptions() error {
	if RequestTimeout <= 0 {
		return errors.New("--timeout must be greater than zero")
	}
	if !validRequestID(RequestID) {
		return errors.New("--request-id must contain only letters, numbers, '.', '_', ':', or '-'")
	}
	return nil
}

func validRequestID(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !isRequestIDChar(char) {
			return false
		}
	}
	return true
}

func isRequestIDChar(char rune) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
		char >= '0' && char <= '9' || strings.ContainsRune("._:-", char)
}

func nonInteractive() bool {
	return NoInput || strings.EqualFold(strings.TrimSpace(os.Getenv("CI")), "true")
}

func requireInteractive(remediation string) error {
	if !nonInteractive() {
		return nil
	}
	return fmt.Errorf("interactive input is disabled; %s", remediation)
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

// GetAPIKey resolves the local Engine control credential.
// Resolution order: flag -> saved login/config -> API key env -> license env.
func GetAPIKey() string {
	return resolveAPIKey().value
}

type resolvedCredential struct {
	value  string
	source string
}

func resolveAPIKey() resolvedCredential {
	if APIKey != "" {
		return resolvedCredential{value: APIKey, source: "--key"}
	}
	// A browser login is an explicit local user choice. Prefer its attributable
	// credential over ambient shell variables that may exist only for setup.
	if cfgVal, err := config.Get("api-key"); err == nil && cfgVal != "" {
		return resolvedCredential{value: cfgVal, source: "saved login/config"}
	}
	if env := os.Getenv("FUSED_API_KEY"); env != "" {
		return resolvedCredential{value: env, source: "FUSED_API_KEY"}
	}
	// The workspace license remains a bootstrap fallback for machines that have
	// not established an attributable CLI login yet.
	if env := os.Getenv("FUSED_LICENSE_KEY"); env != "" {
		return resolvedCredential{value: env, source: "FUSED_LICENSE_KEY"}
	}
	return resolvedCredential{}
}

// getAPIClient returns an initialized API client.
func getAPIClient() (*api.Client, error) {
	return getAPIClientWithTimeout(RequestTimeout)
}

func getAPIClientWithTimeout(timeout time.Duration) (*api.Client, error) {
	url, err := GetEngineURL()
	if err != nil {
		return nil, err
	}
	apiKey := GetAPIKey()
	if apiKey == "" {
		return nil, fmt.Errorf("api-key is not configured.\n\nRun:\n  fused-cli config set api-key <key>\n\nOr set FUSED_API_KEY or FUSED_LICENSE_KEY.")
	}
	return api.NewClientWithOptions(url, apiKey, api.ClientOptions{
		Context:         executionContext,
		Timeout:         timeout,
		RequestID:       RequestID,
		DisableProgress: nonInteractive(),
	}), nil
}
