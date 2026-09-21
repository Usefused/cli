package configfile_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/configfile"
)

// TestManagedAuthReferenceAdmission proves both CLI app kinds preserve valid managed refs and reject arbitrary secret namespaces.
func TestManagedAuthReferenceAdmission(t *testing.T) {
	cases := []struct {
		name, authType, authName, ref string
		valid                         bool
	}{
		{"oauth", "oauth", "oauth2", "${fused.bucket.auth.slack.oauth2}", true},
		{"oidc", "oidc", "login", "${fused.bucket.auth.login.login}", true},
		{"api key", "api_key", "oauth2", "${fused.bucket.auth.slack.oauth2}", false},
		{"missing target", "oauth", "", "${fused.bucket.auth.slack.oauth2}", false},
		{"private secret", "oauth", "oauth2", "${fused.bucket.secret.api_key}", false},
		{"extra segment", "oauth", "oauth2", "${fused.bucket.auth.slack.oauth2.secret}", false},
		{"missing scheme", "oauth", "oauth2", "${fused.bucket.auth.slack}", false},
		{"surrounding text", "oauth", "oauth2", "prefix ${fused.bucket.auth.slack.oauth2}", false},
		{"trailing expression", "oauth", "oauth2", "${fused.bucket.auth.slack.oauth2}${other}", false},
	}
	// SDK and MCP must enforce the same application-reference boundary before network planning.
	for _, kind := range []string{"sdk", "mcp"} {
		for _, tc := range cases {
			// Exercise the public parser and its JSON transport, not the private validation helper alone.
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				fields := "language: typescript"
				// MCP carries authored server metadata instead of a generated package language.
				if kind == "mcp" {
					fields = "description: Check the connected account."
				}
				body := fmt.Sprintf("apiVersion: fused/v1\nkind: %s\nname: managed\nversion: 1.0.0\n%s\nservices:\n  slack:\n    operations: [auth.test]\n    auth: {type: %q, name: %q, ref: %q}\n", kind, fields, tc.authType, tc.authName, tc.ref)
				parsed, err := configfile.Parse([]byte(body), kind+".yaml")
				// Invalid namespaces and selectors must fail locally rather than becoming broker requests.
				if !tc.valid {
					if err == nil {
						t.Fatal("invalid managed reference admitted")
					}
					return
				}
				// Valid managed references must be usable by the same CLI workflow as local references.
				if err != nil {
					t.Fatal(err)
				}
				payload, err := json.Marshal(parsed)
				// The exact reference must survive serialization without dropping the managed namespace.
				if err != nil || !strings.Contains(string(payload), tc.ref) {
					t.Fatalf("managed reference lost during serialization: %v", err)
				}
			})
		}
	}
}
