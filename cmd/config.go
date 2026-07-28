package cmd

import (
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
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Update a configuration setting",
	Args:  cobra.ExactArgs(2),
	ValidArgs: config.KnownKeys,
	Run: func(cmd *cobra.Command, args []string) {
		key, value := args[0], args[1]
		if err := config.Set(key, value); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Config %q set successfully.\n", key)
	},
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print the value of a given configuration key",
	Args:  cobra.ExactArgs(1),
	ValidArgs: config.KnownKeys,
	Run: func(cmd *cobra.Command, args []string) {
		key := args[0]
		val, err := config.Get(key)
		if err != nil {
			if err == config.ErrNotConfigured {
				fmt.Printf("Key %q is not set.\n", key)
				os.Exit(1)
			}
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(val)
	},
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "Print all configured settings",
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := config.Load()
		if err != nil {
			fmt.Printf("Failed to load config: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("engine-url =", cfg.EngineURL)

		// Mask the API key if present.
		keyVal := cfg.APIKey
		if len(keyVal) > 4 {
			keyVal = keyVal[:4] + "..."
		}
		fmt.Println("api-key =", keyVal)
	},
}

var configResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Delete the configuration file entirely",
	Run: func(cmd *cobra.Command, args []string) {
		path, _ := config.Path()
		err := os.Remove(path)
		if err != nil && !os.IsNotExist(err) {
			fmt.Printf("Failed to reset config: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✅ Configuration reset.")
	},
}

func init() {
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configListCmd)
	configCmd.AddCommand(configResetCmd)
	RootCmd.AddCommand(configCmd)
}
