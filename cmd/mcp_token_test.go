package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestMCPTokenGenerateRequestDefaultsToFullAccessWithoutExpiry(t *testing.T) {
	command := newMCPTokenGenerateTestCommand()
	request, err := mcpTokenGenerateRequest(command, "agent")
	if err != nil {
		t.Fatalf("mcpTokenGenerateRequest: %v", err)
	}
	if request.Name != "agent" || len(request.Allow) != 1 || request.Allow[0] != "*" {
		t.Fatalf("request = %#v", request)
	}
	if request.ExpiresIn != nil {
		t.Fatalf("default expiry = %#v, want nil", request.ExpiresIn)
	}
}

func TestMCPTokenGenerateRequestParsesScopeAndExpiry(t *testing.T) {
	command := newMCPTokenGenerateTestCommand()
	if err := command.Flags().Set("allow", "issues.list,issues.create"); err != nil {
		t.Fatal(err)
	}
	if err := command.Flags().Set("expires-in", "15m"); err != nil {
		t.Fatal(err)
	}

	request, err := mcpTokenGenerateRequest(command, "support-agent")
	if err != nil {
		t.Fatalf("mcpTokenGenerateRequest: %v", err)
	}
	if strings.Join(request.Allow, ",") != "issues.list,issues.create" {
		t.Fatalf("allow = %#v", request.Allow)
	}
	if request.ExpiresIn == nil || *request.ExpiresIn != 900 {
		t.Fatalf("expires_in = %#v, want 900", request.ExpiresIn)
	}
}

func TestMCPTokenExpiryRejectsInvalidDurations(t *testing.T) {
	for _, value := range []string{"0s", "-1s", "1500ms", "tomorrow"} {
		t.Run(value, func(t *testing.T) {
			command := newMCPTokenGenerateTestCommand()
			if err := command.Flags().Set("expires-in", value); err != nil {
				t.Fatal(err)
			}
			if _, err := mcpTokenExpirySeconds(command); err == nil || !strings.Contains(err.Error(), "positive whole-second") {
				t.Fatalf("expiry error = %v", err)
			}
		})
	}
}

func TestMCPTokenCommandSurfaceAndSDKScopeIsolation(t *testing.T) {
	for _, command := range []*cobra.Command{mcpTokenGenerateCmd, mcpTokenListCmd, mcpTokenRevokeCmd} {
		if command.Args == nil || command.RunE == nil {
			t.Fatalf("%s is not an executable command", command.CommandPath())
		}
	}
	if mcpTokenGenerateCmd.Flags().Lookup("allow") == nil || mcpTokenGenerateCmd.Flags().Lookup("expires-in") == nil {
		t.Fatal("MCP token generation must expose scope and expiry flags")
	}
	// Scoped SDK tokens are intentionally API-only for the MVP, so the SDK CLI
	// stays simple until there is a demonstrated SDK-agent use case.
	if sdkTokenGenerateCmd.Flags().Lookup("allow") != nil || sdkTokenGenerateCmd.Flags().Lookup("expires-in") != nil {
		t.Fatal("SDK token generation must not advertise scope or expiry flags")
	}
}

func newMCPTokenGenerateTestCommand() *cobra.Command {
	command := &cobra.Command{Use: "generate"}
	command.Flags().StringSlice("allow", []string{"*"}, "")
	command.Flags().String("expires-in", "", "")
	return command
}
