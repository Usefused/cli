package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestSecretSetRejectsMalformedInlineInputBeforeRequests verifies Basic and
// mTLS callers share command-level validation before service resolution.
func TestSecretSetRejectsMalformedInlineInputBeforeRequests(t *testing.T) {
	previousInteractive, previousStdin := secretSetInteractive, secretSetValueStdin
	previousType := secretSetType
	t.Cleanup(func() {
		secretSetInteractive, secretSetValueStdin = previousInteractive, previousStdin
		secretSetType = previousType
		secretSetCmd.SetIn(nil)
	})
	secretSetInteractive, secretSetValueStdin = false, true

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()

	secretSetCmd.SetIn(strings.NewReader("username=alice;password"))
	out := runCommandInDirExpectError(t, t.TempDir(), server.URL, []string{
		"secret", "set", "jira", "--type", "basic", "--value-stdin",
	})
	// The local diagnostic should identify the exact malformed segment.
	if !strings.Contains(out, "segment 2 must contain '='") {
		t.Fatalf("expected actionable local validation error, got %q", out)
	}
	// No metadata or mutation request may occur after malformed credential input.
	if requests != 0 {
		t.Fatalf("malformed secret input sent %d request(s)", requests)
	}
}

// TestMultiFieldSecretResolversRejectMalformedAssignments verifies both
// parser consumers preserve the actionable shared diagnostic.
func TestMultiFieldSecretResolversRejectMalformedAssignments(t *testing.T) {
	tests := []struct {
		name    string
		resolve func() error
	}{
		{name: "basic", resolve: func() error {
			_, _, err := resolveBasicSecretInput(api.BasicPasswordMode("required"), "username=alice;password=")
			return err
		}},
		{name: "mtls", resolve: func() error {
			_, _, err := resolveMTLSSecretInput("cert=certificate;key")
			return err
		}},
		{name: "oauth", resolve: func() error {
			_, _, err := resolveOAuthApplicationSecretInput("client_id=id;client_secret")
			return err
		}},
	}
	// Both multi-field credential families must expose the same parser contract.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.resolve()
			// Each resolver must retain the parser's precise local failure instead of replacing it with missing-field text.
			if err == nil || !strings.Contains(err.Error(), "invalid inline key=value input") {
				t.Fatalf("expected shared inline validation error, got %v", err)
			}
		})
	}
}

// TestResolveOAuthApplicationSecretInputRequiresClosedPair distinguishes app registration from token material.
func TestResolveOAuthApplicationSecretInputRequiresClosedPair(t *testing.T) {
	clientID, clientSecret, err := resolveOAuthApplicationSecretInput("client_id=app-id;client_secret=app-secret")
	if err != nil || clientID != "app-id" || clientSecret != "app-secret" {
		t.Fatalf("OAuth application input = %q/%q, err=%v", clientID, clientSecret, err)
	}
	// Redirects are Engine-owned and extra fields must fail before service lookup or mutation.
	_, _, err = resolveOAuthApplicationSecretInput("client_id=app-id;client_secret=app-secret;redirect_uri=https://attacker.example")
	if err == nil || !strings.Contains(err.Error(), "exactly") {
		t.Fatalf("extra OAuth field error = %v", err)
	}
}

// TestBasicEmptyPasswordModeRetainsDeliberateEmptyValue verifies strict inline
// parsing does not erase the reviewed passwordless Basic-auth contract.
func TestBasicEmptyPasswordModeRetainsDeliberateEmptyValue(t *testing.T) {
	username, password, err := resolveBasicSecretInput(api.BasicPasswordMode("empty"), "username=alice;password=")
	// The explicit empty field is valid only because the selected auth contract requires it.
	if err != nil || username != "alice" || password != "" {
		t.Fatalf("empty-password Basic input = %q/%q, err=%v", username, password, err)
	}
	_, _, err = resolveBasicSecretInput(api.BasicPasswordMode("required"), "username=alice;password=")
	// Required-password Basic auth still rejects the same blank assignment locally.
	if err == nil || !strings.Contains(err.Error(), "empty value") {
		t.Fatalf("required-password blank error = %v", err)
	}
}

func TestSelectSecretAuthByTypeRequiresNameForSameFamilySchemes(t *testing.T) {
	previousType, previousName := secretSetType, secretSetAuthName
	t.Cleanup(func() { secretSetType, secretSetAuthName = previousType, previousName })
	secretSetType, secretSetAuthName = "oauth", ""
	info := &api.ServiceInfo{AuthConfigs: []api.AuthConfig{
		{Name: "adminOAuth", Type: "oauth2"},
		{Name: "userOAuth", Type: "oauth2"},
	}}

	_, err := selectSecretAuthByType(info, "acme")
	if err == nil || !strings.Contains(err.Error(), "--auth-name") || !strings.Contains(err.Error(), "adminOAuth, userOAuth") {
		t.Fatalf("expected deterministic same-family ambiguity error, got %v", err)
	}
}

func TestSelectSecretAuthByTypeUsesExactNameWithinFamily(t *testing.T) {
	previousType, previousName := secretSetType, secretSetAuthName
	t.Cleanup(func() { secretSetType, secretSetAuthName = previousType, previousName })
	secretSetType, secretSetAuthName = "oauth", "userOAuth"
	info := &api.ServiceInfo{AuthConfigs: []api.AuthConfig{
		{Name: "adminOAuth", Type: "oauth2"},
		{Name: "userOAuth", Type: "oauth2"},
	}}

	auth, err := selectSecretAuthByType(info, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if auth.Name != "userOAuth" {
		t.Fatalf("selected auth = %#v, want userOAuth", auth)
	}
}

func TestValidateSecretSetArgsRequiresTypeWithAuthName(t *testing.T) {
	previousInteractive, previousStdin := secretSetInteractive, secretSetValueStdin
	previousType, previousName := secretSetType, secretSetAuthName
	t.Cleanup(func() {
		secretSetInteractive, secretSetValueStdin = previousInteractive, previousStdin
		secretSetType, secretSetAuthName = previousType, previousName
	})
	secretSetInteractive, secretSetValueStdin = false, true
	secretSetType, secretSetAuthName = "", "userOAuth"

	err := validateSecretSetArgs(secretSetCmd, []string{"acme"})
	if err == nil || !strings.Contains(err.Error(), "--auth-name requires --type") {
		t.Fatalf("expected auth-name/type validation error, got %v", err)
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
