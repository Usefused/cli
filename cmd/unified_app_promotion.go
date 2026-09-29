package cmd

import (
	"fmt"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"

	"github.com/spf13/cobra"
)

// unifiedPromoteCmd switches traffic explicitly, without recompiling or editing saved source.
var unifiedPromoteCmd = &cobra.Command{
	Use:   "promote [app-name] --version <version>",
	Short: "Switch traffic to a named Unified App version",
	Example: `  fused-cli unified-app promote "Customer lookup" --version 1.0.0
  fused-cli unified-app promote -f .fused/unified_app/customer-app.yaml --version 1.0.0`,
	Long: "Use the optional app name, or read it from the Unified App config, and switch traffic to the explicit --version. An explicit name takes precedence over config selection. The config and linked source are not modified. Historical results remain available. Clients using an exact version URL must call the selected version; the hosted MCP family URL follows the active version.",
	Args: cobra.MaximumNArgs(1),
	// An explicit promote command is the caller's authorization for this audited mutation.
	RunE: WithTelemetry("cli.unified.promote", func(cmd *cobra.Command, args []string) error {
		version, _ := cmd.Flags().GetString("version")
		version = strings.TrimSpace(version)
		// An explicit destination label prevents an accidental switch to an inferred latest version.
		if version == "" {
			return fmt.Errorf("specify --version, for example: unified-app promote -f .fused/unified_app/customer-app.yaml --version 1.0.0")
		}
		name, err := unifiedPromotionName(args, ConfigFile)
		// Resolve exactly one app before sending any management request.
		if err != nil {
			return err
		}
		client, err := getAPIClient()
		// Missing management credentials cannot authorize runtime changes.
		if err != nil {
			return err
		}
		appID, err := client.ResolveUnifiedAppReference(name, version)
		// Only an exact authorized match may become the destination of the traffic switch.
		if err != nil {
			return err
		}
		current, err := client.GetUnifiedAppTraffic(appID)
		// Read failure must not degrade into an unconditional promotion.
		if err != nil {
			return err
		}
		result, err := client.PromoteUnifiedApp(appID, current.ActiveAppID)
		// Conflicts and uncertain responses require review instead of an automatic retry.
		if err != nil {
			return err
		}
		recordAppliedChange(cmd.Context(), "unified.promote", "unified_app")
		// Structured output shares the Engine receipt with automation clients.
		if wantsJSON(cmd) {
			return writeJSON(cmd, unifiedPromotionOutput{UnifiedAppTraffic: *result, Name: name, Version: version})
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Unified App %q version %s is now receiving new traffic.\n", name, version)
		return err
	}),
}

// unifiedPromotionName honors an explicit app name and otherwise uses the selected config.
func unifiedPromotionName(args []string, path string) (string, error) {
	// Explicit command intent takes precedence and does not require a local declaration.
	if len(args) > 0 {
		name := strings.TrimSpace(args[0])
		// A blank argument must not silently fall back to an unrelated local app.
		if name == "" {
			return "", fmt.Errorf("app name must not be blank")
		}
		return name, nil
	}
	return unifiedPromotionConfigName(path)
}

// unifiedPromotionConfigName uses the shared config loader without rewriting source or guessing between apps.
func unifiedPromotionConfigName(path string) (string, error) {
	run, err := configfile.LoadRun(path)
	// Invalid or missing local config must stop before remote identity discovery.
	if err != nil {
		return "", err
	}
	names := make([]string, 0)
	for _, cfg := range run.Configs {
		// Other declarations do not identify a hosted Unified App.
		if cfg.Kind == configfile.KindUnifiedApp {
			names = append(names, cfg.UnifiedApp.Name)
		}
	}
	// Traffic is a single-app mutation; a workspace with multiple apps needs an explicit file.
	if len(names) != 1 {
		return "", fmt.Errorf("select exactly one Unified App config with -f")
	}
	return names[0], nil
}

// unifiedPromotionOutput retains stable IDs for automation alongside the labels selected by the user.
type unifiedPromotionOutput struct {
	api.UnifiedAppTraffic
	Name    string `json:"name"`
	Version string `json:"version"`
}

// init registers traffic switching alongside the existing plan/apply commands.
func init() {
	unifiedCmd.AddCommand(unifiedPromoteCmd)
	unifiedPromoteCmd.Flags().String("version", "", "Exact version label to receive traffic (for example 1.0.0)")
	_ = unifiedPromoteCmd.MarkFlagRequired("version")
	unifiedPromoteCmd.Flags().Bool(jsonOutputFlag, false, "Print traffic destination as JSON")
}
