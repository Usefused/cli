package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const credentialReadinessFixture = `{"error":{"code":"bucket_credentials_missing","message":"The selected credential set is missing required authentication material.","category":"validation","retryable":false,"details":{"bucket":{"id":"11111111-1111-4111-8111-111111111111","name":"default"},"missing_credentials":[{"service_id":"22222222-2222-4222-8222-222222222222","service":"Jira","auth_type":"basic","auth_name":"basicAuth","required_fields":[{"name":"username","secret_key":"basicAuth_username"},{"name":"password","secret_key":"basicAuth_password","value":"never-print-this-value"}]},{"service_id":"22222222-2222-4222-8222-222222222222","service":"Jira","auth_type":"oauth","auth_name":"oauth2","required_fields":[{"name":"client_id","secret_key":"oauth2_client_id"},{"name":"client_secret","secret_key":"oauth2_client_secret"}]}]},"remediation":"Add the required credentials to the credential set and create the plan again."}}`

// TestPlanCredentialErrorNamesEveryRequirement exercises actual plan HTTP
// wrapping and proves two requirements for Jira are not mistaken for two services.
func TestPlanCredentialErrorNamesEveryRequirement(t *testing.T) {
	// The local server returns only a fixture, never querying or storing credentials.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(credentialReadinessFixture))
	}))
	defer server.Close()
	client := NewClient(server.URL, "fixture-key")
	// SDK and MCP share the same reviewed human error renderer.
	for _, plan := range []func(DesiredConfigPlanIntent) (*SDKConfigPlanResponse, error){client.PlanMCPConfig, client.PlanSDKConfig} {
		_, err := plan(DesiredConfigPlanIntent{Config: json.RawMessage(`{}`)})
		var apiErr *APIError
		// The formatter must preserve the typed cause for JSON and interactive remediation.
		if !errors.As(err, &apiErr) {
			t.Fatalf("untyped plan error: %v", err)
		}
		assertPlanCredentialDiagnostic(t, apiErr, err.Error())
	}
}

// assertPlanCredentialDiagnostic checks useful human metadata while verifying
// rendering does not mutate the typed JSON/interactive remediation contract.
func assertPlanCredentialDiagnostic(t *testing.T, apiErr *APIError, message string) {
	t.Helper()
	before, err := json.Marshal(apiErr.Details)
	// Serialization must succeed before comparing the untouched typed details.
	if err != nil {
		t.Fatal(err)
	}
	_ = apiErr.Error()
	after, err := json.Marshal(apiErr.Details)
	// A display-only change must not truncate or rewrite interactive targets.
	if err != nil || string(before) != string(after) {
		t.Fatal("rendering changed typed details")
	}
	want := []string{"HTTP 400", "bucket_credentials_missing", `Missing credential requirements: 2 in bucket "default"`,
		`"Jira" (basic, auth "basicAuth")`, `username (secret key "basicAuth_username")`, `password (secret key "basicAuth_password")`,
		`"Jira" (oauth, auth "oauth2")`, `client_id (secret key "oauth2_client_id")`, `client_secret (secret key "oauth2_client_secret")`, "create the plan again"}
	// Exact field and scheme names tell the operator which material to supply.
	for _, fragment := range want {
		if !strings.Contains(message, fragment) {
			t.Errorf("missing %q in %s", fragment, message)
		}
	}
	// Arbitrary response values are never part of the typed requirement projection.
	if strings.Contains(message, "never-print-this-value") {
		t.Fatal("credential value reached output")
	}
}

// TestCredentialRequirementDisplayFallbacks covers older unnamed errors and
// malformed display metadata without requiring additional service lookups.
func TestCredentialRequirementDisplayFallbacks(t *testing.T) {
	const serviceID = "22222222-2222-4222-8222-222222222222"
	cases := []struct {
		name        string
		requirement MissingCredentialRequirement
		want        string
	}{
		{name: "unnamed", requirement: MissingCredentialRequirement{ServiceID: serviceID, AuthType: "api_key", RequiredFields: []MissingCredentialField{{Name: "api_key", SecretKey: "GoveeAPIKey"}}}, want: "service " + serviceID + " (api_key): missing api_key"},
		{name: "empty metadata", want: "the selected service (authentication): missing required authentication material (field details unavailable)"},
		{name: "mtls", requirement: MissingCredentialRequirement{Service: "Internal API", AuthType: "mtls", RequiredFields: []MissingCredentialField{{Name: "certificate", SecretKey: "mtls_cert"}, {Name: "private_key", SecretKey: "mtls_key"}}}, want: `certificate (secret key "mtls_cert"), private_key (secret key "mtls_key")`},
	}
	// Every fallback must remain actionable without inventing a name or credential value.
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) { // Isolate independent response compatibility cases.
			message := formatMissingCredentialDetails(nil, []MissingCredentialRequirement{test.requirement})
			if !strings.Contains(message, test.want) {
				t.Fatalf("diagnostic = %s", message)
			}
		})
	}
}

// TestCredentialErrorMetadataCannotEchoSecretsOrTerminalControls applies the
// same rejection gate to every newly displayed remote metadata field.
func TestCredentialErrorMetadataCannotEchoSecretsOrTerminalControls(t *testing.T) {
	unsafe := []string{"fsk_never_print", "https://user:password@example.test", "Authorization: Bearer abc", "password=hidden", "-----BEGIN PRIVATE KEY-----", "\x1b[31mJira", "Jira\nforged error", "Jira\u202eforged", strings.Repeat("a", 257)}
	// No metadata position is exempt from the display-only safety contract.
	for _, value := range unsafe {
		requirement := MissingCredentialRequirement{Service: value, ServiceID: value, AuthType: value, AuthName: value, RequiredFields: []MissingCredentialField{{Name: value, SecretKey: value}}}
		message := formatMissingCredentialDetails(&MissingCredentialBucket{ID: value, Name: value}, []MissingCredentialRequirement{requirement})
		// Unsafe material is omitted rather than transformed into misleading display text.
		if strings.Contains(message, value) {
			t.Fatalf("unsafe metadata echoed: %q", value)
		}
	}
}

// TestCredentialDetailsBoundHumanOutputOnly ensures large plans cannot flood the
// terminal while JSON and interactive remediation retain all required entries.
func TestCredentialDetailsBoundHumanOutputOnly(t *testing.T) {
	fields := make([]MissingCredentialField, 9)
	// Repeat a safe field to exercise the independent field-count bound.
	for i := range fields {
		fields[i] = MissingCredentialField{Name: "token", SecretKey: "apiToken"}
	}
	requirements := make([]MissingCredentialRequirement, 21)
	// Every input entry remains intact even when its rendered row is truncated.
	for i := range requirements {
		requirements[i] = MissingCredentialRequirement{Service: "Jira", RequiredFields: fields}
	}
	message := formatMissingCredentialDetails(nil, requirements)
	// Explicit truncation guidance prevents a partial display being mistaken for completeness.
	if strings.Count(message, "\n  - ") != 20 || !strings.Contains(message, "1 more requirements") || !strings.Contains(message, "additional fields (see --json)") {
		t.Fatalf("missing output bounds: %s", message)
	}
	// Formatting has no authority to change the requirements used for credential collection.
	if len(requirements) != 21 || len(requirements[20].RequiredFields) != 9 {
		t.Fatal("rendering mutated requirements")
	}
	// Errors without credential details must remain byte-for-byte unchanged.
	if got := formatMissingCredentialDetails(nil, nil); got != "" {
		t.Fatalf("empty details = %q", got)
	}
}
