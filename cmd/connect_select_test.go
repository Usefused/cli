package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Usefused/cli/internal/api"
)

// TestConnectPickerStatuses distinguishes existing-but-unusable material from complete credentials.
func TestConnectPickerStatuses(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	auth := api.AuthConfig{Name: "oauthScheme", Type: "oauth"}
	for _, test := range []struct {
		name, serviceID, family string
		keys                    []string
		expiry                  *time.Time
		want                    string
	}{
		{"complete", "service", "oauth", []string{"oauthScheme_client_id", "oauthScheme_client_secret"}, nil, "Configured"},
		{"partial", "service", "oauth", []string{"oauthScheme_client_id"}, nil, "Incomplete"},
		{"expired", "service", "oauth", []string{"oauthScheme_client_id", "oauthScheme_client_secret"}, &past, "Expired"},
		{"other service", "other", "oauth", nil, nil, ""},
		{"other family", "service", "basic", nil, nil, ""},
	} {
		metadata := api.SecretMetaResponse{ServiceID: test.serviceID, KeyName: "oauthScheme", CredentialType: test.family, KeyNames: test.keys, ExpiresAt: test.expiry}
		got := connectAuthStatus(metadata, "service", auth, now)
		// A visible status must reflect the exact selected service and credential family.
		if got != test.want {
			t.Fatalf("%s: status=%s want=%s", test.name, got, test.want)
		}
	}
	label := connectAuthOptionLabel(connectAuthOption{auth: auth, status: "Configured"})
	// The picker highlights existing values with metadata and an editing action, never a value preview.
	if !strings.Contains(label, "✓") || !strings.Contains(label, "Configured") || !strings.Contains(label, "edit") {
		t.Fatal("configured method was not highlighted as editable")
	}
}

// TestConnectPickerLoadsEveryConfiguredMethod guards against stopping at the first existing credential.
func TestConnectPickerLoadsEveryConfiguredMethod(t *testing.T) {
	calls := 0
	// Each page contains a different complete method, so both pages must contribute to the menu.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := decodeGraphQLTestRequest(t, r)
		name := "first"
		// Page two describes a separate method even though page one already established a usable credential.
		if request.Variables["offset"] == float64(1) {
			name = "second"
		}
		calls++
		fmtJSON(t, w, map[string]any{"data": map[string]any{"secretMetaPage": map[string]any{"total": 2, "items": []map[string]any{{"service_id": "service", "key_name": name, "credential_type": "api_key"}}}}})
	}))
	defer server.Close()
	choices := []api.AuthConfig{{Name: "first", Type: "apiKey"}, {Name: "second", Type: "apiKey"}, {Name: "missing", Type: "apiKey"}}
	options, err := loadConnectAuthOptions(api.NewClient(server.URL, "test"), entryTestBucketID, "service", choices)
	// A complete menu includes both stored methods and the missing one without fetching secret values.
	if err != nil || calls != 2 || len(options) != 3 || options[0].status != "Configured" || options[1].status != "Configured" || options[2].status != "Not configured" {
		t.Fatalf("incomplete picker metadata: calls=%d err=%v", calls, err)
	}
}

// TestConnectPickerCancellationLeavesExistingCredentialsUnchanged proves highlighting a method is not an edit.
func TestConnectPickerCancellationLeavesExistingCredentialsUnchanged(t *testing.T) {
	writes := 0
	server := httptest.NewServer(connectSetupTestHandler(t, true, &writes))
	defer server.Close()
	t.Cleanup(configureBucketSecretTestGlobals(server.URL))
	NoInput = false
	t.Setenv("CI", "false")
	previous := selectConnectAuthInteractively
	// Restore the production picker after simulating terminal cancellation.
	t.Cleanup(func() { selectConnectAuthInteractively = previous })
	cancelled := errors.New("selection cancelled")
	// Cancellation is simulated after metadata inspection and before credential collection.
	selectConnectAuthInteractively = func(options []connectAuthOption) (*connectAuthOption, error) {
		return nil, cancelled
	}
	command := newConnectCommand()
	output := &bytes.Buffer{}
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs([]string{"crm"})
	err := command.Execute()
	// No persistence may occur unless the user actually selects a method and supplies valid material.
	if !errors.Is(err, cancelled) || writes != 0 {
		t.Fatalf("cancel err=%v writes=%d", err, writes)
	}
}
