package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWorkspaceIdentityDiscovery requires canonical workspace routes and rejects removed root spellings.
func TestWorkspaceIdentityDiscovery(t *testing.T) {
	for _, name := range []string{"team", "user"} {
		canonical, _, err := RootCmd.Find([]string{"workspace", name})
		// Canonical groups must be visible and attached to workspace for help and completion.
		if err != nil || canonical.Parent() != workspaceCmd || canonical.Hidden {
			t.Fatalf("workspace %s unavailable: %v", name, err)
		}
		legacy, _, err := RootCmd.Find([]string{name})
		// Removed roots must fail instead of forwarding to the canonical command.
		if err == nil && legacy != RootCmd {
			t.Fatalf("removed root %s remains registered", name)
		}

		message := runCommandInDirExpectError(t, t.TempDir(), "http://unused.invalid", []string{"workspace", name})
		// Group-only invocations should guide the user to a concrete action rather than contacting Engine.
		if !strings.Contains(message, "requires a subcommand") {
			t.Fatalf("workspace %s usage: %s", name, message)
		}
	}
}

// TestWorkspaceIdentityRoutesReuseEngineContracts verifies canonical commands retain the Engine contract without forwarding aliases.
func TestWorkspaceIdentityRoutesReuseEngineContracts(t *testing.T) {
	var requests []teamCommandGraphQLRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request teamCommandGraphQLRequest
		// Malformed requests must fail the test instead of masking a routing regression.
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, request)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"teams":{"total":0,"items":[]},"users":{"total":0,"items":[]},"setTeamWorkspaceRole":{"binding":null,"authorization_revision":5,"changed":true}}}`))
	}))
	defer server.Close()
	for _, args := range [][]string{
		{"team", "list", "--search", "platform", "--limit", "5", "--json"},
		{"user", "list", "--search", "ada@example.com", "--include-suspended", "--json"},
		{"team", "access", "workspace", "set", "team-1", "builder"},
	} {
		requests = nil
		canonical := runCommandInDirOutput(t, t.TempDir(), server.URL, append([]string{"workspace"}, args...))
		// Canonical execution must produce one request and successful output.
		if len(requests) != 1 || strings.TrimSpace(canonical) == "" {
			t.Fatalf("command %v: output=%q requests=%d", args, canonical, len(requests))
		}

	}
}
