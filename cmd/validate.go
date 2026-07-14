package cmd

import (
	"fmt"

	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate Fused configurations",
	Long:  `Validates the syntax and references for all Fused configurations in the target directory or file.`,
	RunE: WithTelemetry("cli.validate", func(cmd *cobra.Command, args []string) error {
		run, err := configfile.LoadRun(effectiveConfigFile())
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "validated %d config\n", len(run.Configs))
		return nil
	}),
}

func init() {
	RootCmd.AddCommand(validateCmd)
}
