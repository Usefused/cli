package cmd

import "testing"

// TestExecutionCommandsRegistered keeps the user workflow under the distinct hosted App command.
func TestExecutionCommandsRegistered(t *testing.T) {
	for _, parts := range [][]string{{"execution", "plan"}, {"execution", "apply"}, {"execution", "bundle", "attach"}} {
		command, remaining, err := RootCmd.Find(parts)
		// Exact subcommands must resolve without falling back to an SDK or root command.
		if err != nil || len(remaining) != 0 || command == nil || command.Name() != parts[len(parts)-1] {
			t.Fatalf("command %v resolved to %v, remaining %v, error %v", parts, command, remaining, err)
		}
	}
}
