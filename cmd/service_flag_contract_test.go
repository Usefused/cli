package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestEveryServiceFlagSharesTheMultiSelectorContract verifies grouped app selectors and legacy service-only command selectors retain their distinct grammars.
func TestEveryServiceFlagSharesTheMultiSelectorContract(t *testing.T) {
	found := make([]string, 0)
	var inspect func(*cobra.Command)
	// Traverse registered commands rather than maintaining a second list that can drift as commands are added.
	inspect = func(command *cobra.Command) {
		flag := command.Flags().Lookup("service")
		// Only commands that define --service locally own its parsing contract.
		if flag != nil && command != webhookListCmd {
			found = append(found, command.CommandPath())
			wantType, wantHelp := "stringSlice", "comma-separated or repeatable"
			// App selectors own comma-separated operation lists and must bypass Cobra's CSV splitting.
			if command.Parent() == RootCmd && (command.Name() == "init" || command.Name() == "extend") {
				wantType, wantHelp = "stringArray", "bare services select all operations; quote semicolons"
			}
			// The flag type must preserve the delimiters owned by that command's grammar.
			if flag.Value.Type() != wantType {
				t.Errorf("%s --service uses %s, want %s", command.CommandPath(), flag.Value.Type(), wantType)
			}
			// Help must expose both canonical version syntax and the two supported multi-value forms.
			if !strings.Contains(flag.Usage, "<service>") || !strings.Contains(flag.Usage, "@<version>") || !strings.Contains(flag.Usage, wantHelp) {
				t.Errorf("%s --service help does not describe the shared selector contract: %q", command.CommandPath(), flag.Usage)
			}
		}
		for _, child := range command.Commands() {
			inspect(child)
		}
	}
	inspect(RootCmd)
	// The audit itself must fail if command registration changes so radically that it checks nothing.
	if len(found) == 0 {
		t.Fatal("no --service flags were registered")
	}
}
