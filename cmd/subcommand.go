package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func requireSubcommand(cmd *cobra.Command, _ []string) error {
	// A successful no-op is ambiguous to scripts and agents; an explicit
	// usage failure makes a missing action observable without a network call.
	return fmt.Errorf("%s requires a subcommand; run '%s --help'", cmd.CommandPath(), cmd.CommandPath())
}
