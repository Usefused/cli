package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestConnectRejectsMalformedInputBeforeRequests keeps malformed credential lists away from the network.
func TestConnectRejectsMalformedInputBeforeRequests(t *testing.T) {
	requests := 0
	// Count requests without accepting any supplied material.
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()
	out := runCommandInDirExpectError(t, t.TempDir(), server.URL, []string{
		"connect", "jira", "username=alice, password", "--type", "basic",
	})
	// Diagnostics describe the grammar without echoing credential values.
	if !strings.Contains(out, "credentials must use name=value, name=value") {
		t.Fatalf("expected actionable local validation error, got %q", out)
	}
	// Invalid lists must fail before service resolution or credential mutation.
	if requests != 0 {
		t.Fatalf("malformed input sent %d request(s)", requests)
	}
}

// TestValidateOAuthApplicationSecretInputRequiresClosedPair distinguishes app registration from token material.
func TestValidateOAuthApplicationSecretInputRequiresClosedPair(t *testing.T) {
	clientID, clientSecret, err := validateOAuthApplicationSecretInput("app-id", "app-secret", 2)
	if err != nil || clientID != "app-id" || clientSecret != "app-secret" {
		t.Fatalf("OAuth application input = %q/%q, err=%v", clientID, clientSecret, err)
	}
	// Extra fields such as redirect_uri are Engine-owned and must fail locally.
	_, _, err = validateOAuthApplicationSecretInput("app-id", "app-secret", 3)
	if err == nil || !strings.Contains(err.Error(), "exactly") {
		t.Fatalf("extra OAuth field error = %v", err)
	}
	// Missing client_secret must fail before any service mutation.
	_, _, err = validateOAuthApplicationSecretInput("app-id", "", 2)
	if err == nil || !strings.Contains(err.Error(), "exactly") {
		t.Fatalf("missing OAuth secret error = %v", err)
	}
}

func TestValidateBasicSecretInputHonoursPasswordMode(t *testing.T) {
	tests := []struct {
		name     string
		mode     api.BasicPasswordMode
		password string
		wantErr  bool
	}{
		{name: "default requires password", password: "", wantErr: true},
		{name: "required accepts password", mode: "required", password: "secret"},
		{name: "optional accepts empty", mode: "optional", password: ""},
		{name: "empty accepts empty", mode: "empty", password: ""},
		{name: "empty rejects nonempty", mode: "empty", password: "secret", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			username, password, err := validateBasicSecretInput(test.mode, "api-key", test.password)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateBasicSecretInput() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && (username != "api-key" || password != test.password) {
				t.Fatalf("resolved Basic input = %q/%q", username, password)
			}
		})
	}
}
