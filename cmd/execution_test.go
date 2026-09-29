package cmd

import "testing"

// TestUnifiedAppCommandsRegistered keeps the user workflow under the distinct hosted App command.
func TestUnifiedAppCommandsRegistered(t *testing.T) {
	for _, parts := range [][]string{{"unified-app", "plan"}, {"unified-app", "apply"}, {"unified-app", "sync"}, {"unified-app", "bundle", "attach"}} {
		command, remaining, err := RootCmd.Find(parts)
		// Exact subcommands must resolve without falling back to an SDK or root command.
		if err != nil || len(remaining) != 0 || command == nil || command.Name() != parts[len(parts)-1] {
			t.Fatalf("command %v resolved to %v, remaining %v, error %v", parts, command, remaining, err)
		}
	}
}
