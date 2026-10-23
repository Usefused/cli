package cmd

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

// TestConnectCommaListGrammar permits optional separator spacing without normalizing secret bytes.
func TestConnectCommaListGrammar(t *testing.T) {
	for _, value := range []string{
		"client_id=example,client_secret= value=with;delimiters ",
		"client_id=example, client_secret= value=with;delimiters ",
	} {
		fields, err := parseConnectAssignments([]string{value})
		want := map[string]string{"client_id": "example", "client_secret": " value=with;delimiters "}
		// Whitespace around field names is syntax, while whitespace after equals remains credential data.
		if err != nil || !reflect.DeepEqual(fields, want) {
			t.Fatal("comma-separated credential bytes changed")
		}
	}
}

// TestConnectAssignmentsPreserveCredentialBytes checks shell expansion and delimiter-rich paired credentials.
func TestConnectAssignmentsPreserveCredentialBytes(t *testing.T) {
	t.Setenv("FUSED_TEST_ASSIGNMENT", "synthetic=a;b:$literal\nsecond line")
	// Use only a fixed shell program so test material is expanded as data, never executable code.
	value, err := exec.Command("sh", "-c", `printf '%s' "client_secret=$FUSED_TEST_ASSIGNMENT"`).Output()
	// A failed shell fixture must not turn into an empty-credential test.
	if err != nil {
		t.Fatal(err)
	}
	fields, err := parseConnectAssignments([]string{"client_id=example, " + string(value)})
	// The first equals sign is syntax; all subsequent bytes belong to the credential.
	if err != nil || fields["client_secret"] != "synthetic=a;b:$literal\nsecond line" {
		t.Fatal("shell-expanded credential was changed")
	}
	input, err := connectCredentialInput(&cobra.Command{}, &api.AuthConfig{Type: "oauth"}, fields)
	// OAuth validation must not reinterpret semicolons or dollar signs in secret values.
	if err != nil || input.clientID != "example" || input.clientSecret != fields["client_secret"] {
		t.Fatal("OAuth pair did not retain exact values")
	}
	entry, err := parseBucketEntry("token=" + fields["client_secret"])
	// Generic secret assignments use the same byte-preserving boundary.
	if err != nil || !entry.supplied || entry.value != fields["client_secret"] {
		t.Fatal("bucket assignment did not retain exact values")
	}
}

// TestConnectAssignmentValidation prevents malformed or partial inputs from falling back to prompts.
func TestConnectAssignmentValidation(t *testing.T) {
	for _, args := range [][]string{{"token:private"}, {"=private"}, {"token=first, token=private"}, {"client_id=example", "client_secret=private"}, {"token=private,"}} {
		_, err := parseConnectAssignments(args)
		// Errors must identify the grammar without including credential material.
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("malformed assignment accepted or disclosed")
		}
	}
	for _, test := range []struct {
		auth   api.AuthConfig
		fields map[string]string
	}{
		{api.AuthConfig{Name: "token", Type: "apiKey"}, map[string]string{"token": ""}},
		{api.AuthConfig{Name: "token", Type: "apiKey"}, map[string]string{"wrong": "private"}},
		{api.AuthConfig{Type: "oauth"}, map[string]string{"client_id": "example"}},
		{api.AuthConfig{Type: "basic"}, map[string]string{"username": "example"}},
		{api.AuthConfig{Type: "mtls"}, map[string]string{"cert": "private"}},
	} {
		_, err := connectCredentialInput(&cobra.Command{}, &test.auth, test.fields)
		// Every explicit invalid family input must fail before storage or interaction.
		if err == nil {
			t.Fatal("invalid credential fields accepted")
		}
	}
}

// TestConnectBasicEmptyPassword preserves services that intentionally use a key as the username.
func TestConnectBasicEmptyPassword(t *testing.T) {
	auth := &api.AuthConfig{Type: "basic", BasicPasswordMode: api.BasicPasswordMode("empty")}
	input, err := connectCredentialInput(&cobra.Command{}, auth, map[string]string{"username": "key=with;delimiters", "password": ""})
	// An explicitly empty password remains part of the atomic pair.
	if err != nil || input.username != "key=with;delimiters" || input.password != "" {
		t.Fatal("empty-password Basic input changed")
	}
}
