package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/Usefused/cli/internal/config"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage Fused CLI configuration",
	Long: `Set or view Fused CLI configuration.
Settings are saved to the config file and apply to all future commands.`,
	Args: cobra.NoArgs,
	RunE: requireSubcommand,
}

var configSetCmd = &cobra.Command{
	Use:       "set <key> <value>",
	Short:     "Update a configuration setting",
	Args:      cobra.ExactArgs(2),
	ValidArgs: config.KnownKeys,
	RunE: WithTelemetry("cli.config.set", func(cmd *cobra.Command, args []string) error {
		key, value := args[0], args[1]
		if err := config.Set(key, value); err != nil {
			return err
		}
		recordAppliedChange(cmd.Context(), cmd.CommandPath(), "cli_config")
		fmt.Fprintf(cmd.OutOrStdout(), "✅ Config %q set successfully.\n", key)

		if key == "engine-url" && os.Getenv("FUSED_ENGINE_URL") != "" {
			fmt.Fprintln(cmd.OutOrStdout(), "\n⚠️  Warning: FUSED_ENGINE_URL is currently set in your environment.")
			fmt.Fprintln(cmd.OutOrStdout(), "   The environment variable will override this configured value until it is unset.")
		}
		return nil
	}),
}

var configGetCmd = &cobra.Command{
	Use:       "get <key>",
	Short:     "Print the value of a given configuration key",
	Args:      cobra.ExactArgs(1),
	ValidArgs: config.KnownKeys,
	RunE: WithTelemetry("cli.config.get", func(cmd *cobra.Command, args []string) error {
		key := args[0]
		val, err := config.Get(key)
		if err != nil {
			if errors.Is(err, config.ErrNotConfigured) {
				return fmt.Errorf("key %q is not set", key)
			}
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), val)
		return nil
	}),
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "Print all configured settings",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.config.list", func(cmd *cobra.Command, _ []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		fmt.Fprintln(cmd.OutOrStdout(), "engine-url =", cfg.EngineURL)
		if os.Getenv("FUSED_ENGINE_URL") != "" {
			fmt.Fprintln(cmd.OutOrStdout(), "             (⚠️  overridden by FUSED_ENGINE_URL environment variable)")
		}

		// Mask the API key if present.
		keyVal := cfg.APIKey
		if len(keyVal) > 4 {
			keyVal = keyVal[:4] + "..."
		}
		fmt.Fprintln(cmd.OutOrStdout(), "api-key =", keyVal)
		fmt.Fprintln(cmd.OutOrStdout(), "api-key-expires-at =", cfg.APIKeyExpiresAt)
		if cfg.APIKey == "" && os.Getenv("FUSED_API_KEY") != "" {
			fmt.Fprintln(cmd.OutOrStdout(), "             (using FUSED_API_KEY environment fallback)")
		} else if cfg.APIKey == "" && os.Getenv("FUSED_LICENSE_KEY") != "" {
			fmt.Fprintln(cmd.OutOrStdout(), "             (using FUSED_LICENSE_KEY bootstrap fallback)")
		}
		return nil
	}),
}

var configResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Delete the configuration file entirely",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.config.reset", func(cmd *cobra.Command, _ []string) error {
		path, _ := config.Path()
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("reset config: %w", err)
		}
		recordAppliedChange(cmd.Context(), cmd.CommandPath(), "cli_config")
		fmt.Fprintln(cmd.OutOrStdout(), "✅ Configuration reset.")
		return nil
	}),
}

func init() {
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configListCmd)
	configCmd.AddCommand(configResetCmd)
	RootCmd.AddCommand(configCmd)
}
