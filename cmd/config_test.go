package cmd_test

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Usefused/cli/cmd"
)

// captureOutput intercepts os.Stdout during function execution.
func captureOutput(f func()) string {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)
	return string(out)
}

// TestConfigCommands_RoundTrip verifies that the config subcommands integrate
// with the config package properly: set -> list/get -> reset.
func TestConfigCommands_RoundTrip(t *testing.T) {
	// Isolate the config file.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	// 1. `config set` should save the engine-url.
	cmd.RootCmd.SetArgs([]string{"config", "set", "engine-url", "http://test-engine"})
	cmd.RootCmd.Execute()

	// 2. `config set` should save the api-key.
	cmd.RootCmd.SetArgs([]string{"config", "set", "api-key", "sk-topsecretkey"})
	cmd.RootCmd.Execute()

	// 3. `config get` should print the specific value.
	cmd.RootCmd.SetArgs([]string{"config", "get", "engine-url"})
	getOutput := captureOutput(func() {
		cmd.RootCmd.Execute()
	})
	if !strings.Contains(getOutput, "http://test-engine") {
		t.Errorf("get engine-url: expected to find %q, got: %q", "http://test-engine", getOutput)
	}

	// 4. `config list` should print both, but mask the API key.
	cmd.RootCmd.SetArgs([]string{"config", "list"})
	listOutput := captureOutput(func() {
		cmd.RootCmd.Execute()
	})
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
	cmd.RootCmd.SetArgs([]string{"config", "reset"})
	cmd.RootCmd.Execute()

	// 6. After reset, `list` should show empty fields.
	cmd.RootCmd.SetArgs([]string{"config", "list"})
	resetListOutput := captureOutput(func() {
		cmd.RootCmd.Execute()
	})
	if !strings.Contains(resetListOutput, "engine-url = \n") {
		t.Errorf("list after reset: expected empty engine-url, got: %q", resetListOutput)
	}
}
