package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

func TestAppTokenHelpersShareTransportAndPreserveKindResolution(t *testing.T) {
	var resolvedKinds []string
	var mutationMethods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/engine/graphql":
			handleAppTokenHelperGraphQL(t, w, r, &resolvedKinds)
		case "/workspace/app-tokens":
			mutationMethods = append(mutationMethods, r.Method)
			if r.URL.Query().Get("app_family_id") != "family-1" {
				t.Fatalf("app_family_id = %q", r.URL.Query().Get("app_family_id"))
			}
			if r.Method == http.MethodPost {
				_, _ = w.Write([]byte(`{"id":"token-1","app_family_id":"family-1","name":"agent","allow":["*"],"token":"shown-once","expires_at":"2026-08-10T13:00:00Z","created_at":"2026-08-10T12:00:00Z"}`))
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	oldEngineURL, oldAPIKey := EngineURL, APIKey
	EngineURL, APIKey = server.URL, "fsk_test"
	t.Cleanup(func() { EngineURL, APIKey = oldEngineURL, oldAPIKey })

	output := &bytes.Buffer{}
	command := &cobra.Command{Use: "token"}
	command.SetOut(output)
	if err := issueAppToken(command, appTokenKindSDK, " sdk-name ", api.AppTokenGenerateRequest{Name: "agent"}); err != nil {
		t.Fatalf("issueAppToken: %v", err)
	}
	if !strings.Contains(output.String(), "shown-once") {
		t.Fatalf("issue output = %q", output.String())
	}
	// Human output must make the automatic access cutoff visible when copying the one-time token.
	if !strings.Contains(output.String(), "Expires: 2026-08-10T13:00:00Z") {
		t.Fatalf("issue output missing expiry = %q", output.String())
	}

	tokens, err := loadAppTokens(appTokenKindMCP, "mcp-name")
	if err != nil || len(tokens) != 1 || tokens[0].Name != "agent" {
		t.Fatalf("loadAppTokens = %#v, %v", tokens, err)
	}
	if err := revokeAppToken(command, appTokenKindMCP, "mcp-name", "agent"); err != nil {
		t.Fatalf("revokeAppToken: %v", err)
	}
	if got := strings.Join(resolvedKinds, ","); got != "sdk,mcp,mcp" {
		t.Fatalf("resolved kinds = %q", got)
	}
	if got := strings.Join(mutationMethods, ","); got != "POST,DELETE" {
		t.Fatalf("mutation methods = %q", got)
	}
}

func handleAppTokenHelperGraphQL(t *testing.T, w http.ResponseWriter, r *http.Request, resolvedKinds *[]string) {
	t.Helper()
	var body struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode GraphQL request: %v", err)
	}
	if strings.Contains(body.Query, "appFamilyReference") {
		kind, _ := body.Variables["kind"].(string)
		*resolvedKinds = append(*resolvedKinds, kind)
		_, _ = w.Write([]byte(`{"data":{"appFamilyReference":{"id":"family-1","kind":"` + kind + `"}}}`))
		return
	}
	if strings.Contains(body.Query, "appTokens") {
		_, _ = w.Write([]byte(`{"data":{"appTokens":[{"id":"token-1","app_family_id":"family-1","name":"agent","allow":["*"],"expires_at":"","created_at":"2026-08-10T12:00:00Z","last_used_at":""}]}}`))
		return
	}
	t.Fatalf("unexpected GraphQL query: %s", body.Query)
}
