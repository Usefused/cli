package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestEveryServiceFlagSharesTheMultiSelectorContract prevents singular service flags from bypassing comma, repetition, or version syntax.
func TestEveryServiceFlagSharesTheMultiSelectorContract(t *testing.T) {
	found := make([]string, 0)
	var inspect func(*cobra.Command)
	// Traverse registered commands rather than maintaining a second list that can drift as commands are added.
	inspect = func(command *cobra.Command) {
		flag := command.Flags().Lookup("service")
		// Only commands that define --service locally own its parsing contract.
		if flag != nil {
			found = append(found, command.CommandPath())
			// Cobra's stringSlice value is what accepts both comma-separated and repeated flag forms.
			if flag.Value.Type() != "stringSlice" {
				t.Errorf("%s --service uses %s, want stringSlice", command.CommandPath(), flag.Value.Type())
			}
			// Help must expose both canonical version syntax and the two supported multi-value forms.
			if !strings.Contains(flag.Usage, "<service>") || !strings.Contains(flag.Usage, "@<version>") || !strings.Contains(flag.Usage, "comma-separated or repeatable") {
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
