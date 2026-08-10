package cmd

import (
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

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
