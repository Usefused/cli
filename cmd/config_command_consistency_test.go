package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestConfigPlanAndApplyCommandsShareContract(t *testing.T) {
	plans := []*cobra.Command{planCmd, workspacePlanCmd, sdkPlanCmd, mcpPlanCmd, webhookPlanCmd}
	applies := []*cobra.Command{applyCmd, workspaceApplyCmd, sdkApplyCmd, mcpApplyCmd, webhookApplyCmd}
	assertConfigCommandContract(t, plans, []string{"receipt-out", "json"})
	assertConfigCommandContract(t, applies, []string{"receipt", "plan-id"})

	for _, command := range append(plans, applies...) {
		if err := command.Args(command, []string{"unexpected"}); err == nil {
			t.Errorf("%s accepts an unexpected positional argument", command.CommandPath())
		}
	}
	for _, command := range applies {
		if command.Flags().Lookup("owner-team") != nil {
			t.Errorf("%s can change ownership after planning", command.CommandPath())
		}
	}
}

func assertConfigCommandContract(t *testing.T, commands []*cobra.Command, requiredFlags []string) {
	t.Helper()
	for _, command := range commands {
		for _, name := range requiredFlags {
			if command.Flags().Lookup(name) == nil {
				t.Errorf("%s is missing --%s", command.CommandPath(), name)
			}
		}
	}
}
