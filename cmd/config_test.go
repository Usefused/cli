package cmd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Usefused/cli/cmd"
)

func executeConfigCommand(t *testing.T, args ...string) string {
	t.Helper()
	output := new(bytes.Buffer)
	cmd.RootCmd.SetOut(output)
	cmd.RootCmd.SetErr(output)
	cmd.RootCmd.SetArgs(args)
	if err := cmd.RootCmd.Execute(); err != nil {
		t.Fatalf("execute %v: %v", args, err)
	}
	return output.String()
}

// TestConfigCommands_RoundTrip verifies that the config subcommands integrate
// with the config package properly: set -> list/get -> reset.
func TestConfigCommands_RoundTrip(t *testing.T) {
	// Isolate the config file.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// 1. `config set` should save the engine-url.
	executeConfigCommand(t, "config", "set", "engine-url", "http://test-engine")

	// 2. `config set` should save the api-key.
	executeConfigCommand(t, "config", "set", "api-key", "sk-topsecretkey")

	// 3. `config get` should print the specific value.
	getOutput := executeConfigCommand(t, "config", "get", "engine-url")
	if !strings.Contains(getOutput, "http://test-engine") {
		t.Errorf("get engine-url: expected to find %q, got: %q", "http://test-engine", getOutput)
	}

	// 4. `config list` should print both, but mask the API key.
	listOutput := executeConfigCommand(t, "config", "list")
	if !strings.Contains(listOutput, "engine-url = http://test-engine") {
		t.Errorf("list: expected engine-url, got: %s", listOutput)
	}
	if strings.Contains(listOutput, "sk-topsecretkey") {
		t.Errorf("list: API key was NOT masked: %s", listOutput)
	}
	if !strings.Contains(listOutput, "api-key = sk-t...") {
		t.Errorf("list: expected masked API key 'sk-t...', got: %s", listOutput)
	}

	// 5. `config reset` should clear the file.
	executeConfigCommand(t, "config", "reset")

	// 6. After reset, `list` should show empty fields.
	resetListOutput := executeConfigCommand(t, "config", "list")
	if !strings.Contains(resetListOutput, "engine-url = \n") {
		t.Errorf("list after reset: expected empty engine-url, got: %q", resetListOutput)
	}
}
