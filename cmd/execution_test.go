package cmd

import "testing"

// TestUnifiedAppCommandsRegistered keeps the supported hosted App workflow on the public command surface.
func TestUnifiedAppCommandsRegistered(t *testing.T) {
	for _, parts := range [][]string{{"app", "plan"}, {"app", "apply"}, {"app", "bundle", "attach"}} {
		command, remaining, err := RootCmd.Find(parts)
		// Exact subcommands must resolve without falling back to an SDK or root command.
		if err != nil || len(remaining) != 0 || command == nil || command.Name() != parts[len(parts)-1] {
			t.Fatalf("command %v resolved to %v, remaining %v, error %v", parts, command, remaining, err)
		}
	}
	// The YAML extraction command must not remain discoverable through Cobra's parent fallback.
	if command, remaining, _ := RootCmd.Find([]string{"app", "source", "extract"}); command != nil && len(remaining) == 0 {
		t.Fatal("legacy source extraction command remains registered")
	}
}
