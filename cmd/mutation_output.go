package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func printMutationOutcome(cmd *cobra.Command, changed bool, changedMessage, unchangedMessage string) {
	if changed {
		fmt.Fprintln(cmd.OutOrStdout(), changedMessage)
		return
	}
	fmt.Fprintln(cmd.OutOrStdout(), unchangedMessage)
}
