package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Usefused/cli/internal/api"
)

// TestConnectAssignmentsStillOfferAuthSelection verifies supplied values do not suppress the terminal menu.
func TestConnectAssignmentsStillOfferAuthSelection(t *testing.T) {
	for _, test := range []struct {
		name              string
		noInput, json, ci bool
		wantSelections    int
	}{
		{name: "interactive assignments", wantSelections: 1},
		{name: "no input", noInput: true},
		{name: "JSON", json: true},
		{name: "CI", ci: true},
	} {
		// Each mode runs through the complete command, including metadata reads and credential persistence.
		t.Run(test.name, func(t *testing.T) {
			writes, selections := 0, 0
			auths := []api.AuthConfig{{Name: "providerKey", Type: "apiKey"}, {Name: "oauthScheme", Type: "oauth"}}
			server := httptest.NewServer(connectSetupTestHandler(t, false, &writes, auths...))
			defer server.Close()
			t.Cleanup(configureBucketSecretTestGlobals(server.URL))
			NoInput = test.noInput
			t.Setenv("CI", fmt.Sprint(test.ci))
			previous := selectConnectAuthInteractively
			// Restore the production selector so this fixture cannot change later terminal behavior.
			t.Cleanup(func() { selectConnectAuthInteractively = previous })
			// Capture the menu contents instead of requiring a real terminal during automated tests.
			selectConnectAuthInteractively = func(info []connectAuthOption) (*connectAuthOption, error) {
				selections++
				// Credential field names must not hide other families from the user's menu.
				if len(info) != 2 || info[1].auth.Name != "oauthScheme" {
					t.Fatal("available authentication methods were filtered by supplied fields")
				}
				return &info[0], nil
			}
			command := newConnectCommand()
			output := &bytes.Buffer{}
			command.SetOut(output)
			command.SetErr(output)
			args := []string{"crm", "providerKey=synthetic-token"}
			// JSON remains a non-prompting mode even without --no-input.
			if test.json {
				args = append(args, "--json")
			}
			command.SetArgs(args)
			err := command.Execute()
			// An ambiguous unattended run must fail without choosing a method or writing credentials.
			if (err == nil) != (test.wantSelections == 1) || selections != test.wantSelections || writes != test.wantSelections {
				t.Fatalf("err=%v selections=%d writes=%d", err, selections, writes)
			}
			// Neither successful receipts nor selection errors may disclose supplied values.
			if strings.Contains(output.String(), "synthetic-token") {
				t.Fatal("credential leaked into command output")
			}
		})
	}
}

// TestConnectCredentialSetup exercises status-only automation, explicit replacement, and interactive editing.
func TestConnectCredentialSetup(t *testing.T) {
	for _, test := range []struct {
		name                            string
		existing, interactive, supplied bool
		wantWrites                      int
	}{
		{"missing automation", false, false, true, 1},
		{"configured inspection", true, false, false, 0},
		{"explicit replacement", true, false, true, 1},
		{"interactive missing", false, true, false, 1},
		{"interactive edit", true, true, false, 1},
	} {
		// Each invocation gets isolated command flags and a new fake Engine.
		t.Run(test.name, func(t *testing.T) {
			writes, prompts := 0, 0
			// Metadata and persistence are checked together to prove bucket and service routing stay aligned.
			server := httptest.NewServer(connectSetupTestHandler(t, test.existing, &writes))
			defer server.Close()
			t.Cleanup(configureBucketSecretTestGlobals(server.URL))
			t.Setenv("CI", "false")
			NoInput = !test.interactive
			previousSelector := selectConnectAuthInteractively
			// Restore the terminal picker after inspecting fixture-only labels.
			t.Cleanup(func() { selectConnectAuthInteractively = previousSelector })
			// Both new and configured methods remain selectable, including a sole method.
			selectConnectAuthInteractively = func(options []connectAuthOption) (*connectAuthOption, error) {
				if (options[0].status == "Configured") != test.existing {
					t.Fatal("picker status did not match existing metadata")
				} // Labels reflect stored state before prompting for a replacement.
				return &options[0], nil
			}
			previous := collectSecretCredentialInput
			t.Cleanup(func() { collectSecretCredentialInput = previous }) // Prompt substitution must not escape the fixture.
			// Family collection is replaced only for terminal tests; assignments still use production validation.
			collectSecretCredentialInput = func(auth *api.AuthConfig, value string) (secretCredentialInput, error) {
				if test.interactive {
					prompts++
					return secretCredentialInput{token: "synthetic-token"}, nil
				} // Missing material opens one masked collector.
				return previous(auth, value)
			}
			command := newConnectCommand()
			output := &bytes.Buffer{}
			command.SetOut(output)
			command.SetErr(output)
			args := []string{"crm"}
			if !test.interactive {
				args = append(args, "--json")
			} // Automation never opens prompts.
			if test.supplied {
				args = append(args, "providerKey=synthetic-token")
			} // Explicit values request replacement without an extra flag.
			command.SetArgs(args)
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			} // A valid setup or no-op should complete successfully.
			if writes != test.wantWrites || strings.Contains(output.String(), "synthetic-token") {
				t.Fatalf("writes=%d unsafe output=%t", writes, strings.Contains(output.String(), "synthetic-token"))
			} // Receipts cannot expose credentials.
			if test.interactive && prompts != 1 {
				t.Fatalf("prompts=%d", prompts)
			} // Selecting either new or configured auth prompts for fresh material.
		})
	}
}

// TestConnectPresenceRequiresCompleteUnexpiredFamily distinguishes partial pairs from configured auth.
func TestConnectPresenceRequiresCompleteUnexpiredFamily(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	for _, family := range []string{"basic", "mtls", "oauth", "oidc"} {
		auth := api.AuthConfig{Name: "provider", Type: family}
		row := api.SecretMetaResponse{ServiceID: "service", KeyName: "provider", CredentialType: family}
		for _, suffix := range connectAuthKeySuffixes(family) {
			row.KeyNames = append(row.KeyNames, "provider"+suffix)
		}
		if !connectAuthConfigured(row, "service", auth, now) {
			t.Fatalf("complete %s not recognized", family)
		} // Complete unexpired pairs are reusable.
		row.ExpiresAt = &past
		if connectAuthConfigured(row, "service", auth, now) {
			t.Fatalf("expired %s accepted", family)
		} // Expiry invalidates the entire family.
		row.ExpiresAt = nil
		row.KeyNames = row.KeyNames[:1]
		if connectAuthConfigured(row, "service", auth, now) {
			t.Fatalf("partial %s accepted", family)
		} // One half cannot stand in for a complete pair.
	}
}

// TestConnectStatusAndAutomationSelection preserves every declared method while requiring exact unattended choices.
func TestConnectStatusAndAutomationSelection(t *testing.T) {
	info := &api.ServiceInfo{AuthConfigs: []api.AuthConfig{{Name: "first", Type: "apiKey"}, {Name: "second", Type: "apiKey"}}}
	oldSelector := selectConnectAuthInteractively
	t.Cleanup(func() { selectConnectAuthInteractively = oldSelector }) // Restore the real terminal selector.
	// The menu must expose both schemes even when they share the same auth family.
	selectConnectAuthInteractively = func(selected []connectAuthOption) (*connectAuthOption, error) {
		if len(selected) != 2 {
			t.Fatal("auth menu omitted a method")
		} // Configured methods remain available for editing.
		return &selected[1], nil
	}
	options := []connectAuthOption{{auth: info.AuthConfigs[0], status: "Configured"}, {auth: info.AuthConfigs[1], status: "Not configured"}}
	auth, err := chooseConnectAuth(options, false)
	if err != nil || auth.auth.Name != "second" {
		t.Fatalf("selection=%v err=%v", auth, err)
	} // User selection owns the exact scheme.
	if _, err := chooseConnectAuth(options, true); err == nil {
		t.Fatal("ambiguous automation accepted")
	} // No arbitrary scheme choice in CI.
	choices, err := connectAuthChoices(info, connectOptions{authType: "api_key", authName: "second"})
	if err != nil || len(choices) != 1 || choices[0].Name != "second" {
		t.Fatal("exact auth selector failed")
	} // Same-type schemes remain separately addressable.
	command := newConnectCommand()
	NoInputBefore := NoInput
	NoInput = true
	t.Cleanup(func() { NoInput = NoInputBefore }) // Restore automation state for unrelated tests.
	if _, err := connectCredentialInput(command, &info.AuthConfigs[0], nil); err == nil {
		t.Fatal("missing automation value accepted")
	} // Missing values cannot open a prompt.
}

// TestConnectMetadataPaginationAndDenial prevents a first-page miss or permission failure from becoming a write.
func TestConnectMetadataPaginationAndDenial(t *testing.T) {
	for _, denied := range []bool{false, true} {
		// An existing method on a later page must be found before deciding that setup is needed.
		t.Run(fmt.Sprint(denied), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(connectMetadataTestHandler(t, denied, &calls))
			defer server.Close()
			options, err := loadConnectAuthOptions(api.NewClient(server.URL, "test"), entryTestBucketID, "service", []api.AuthConfig{{Name: "providerKey", Type: "apiKey"}})
			if denied && (err == nil || calls != 1) {
				t.Fatalf("denial err=%v calls=%d", err, calls)
			} // A denial must stop immediately.
			if !denied && (err != nil || len(options) != 1 || options[0].status != "Configured" || calls != 2) {
				t.Fatalf("pagination err=%v calls=%d", err, calls)
			} // Do not ignore later configured credentials.
		})
	}
}

// connectSetupTestHandler asserts the metadata and write protocol without mixing it into terminal setup.
func connectSetupTestHandler(t *testing.T, existing bool, writes *int, auths ...api.AuthConfig) http.HandlerFunc {
	// Most fixtures use one scheme; selection tests supply the full declared service catalogue.
	if len(auths) == 0 {
		auths = []api.AuthConfig{{Type: "apiKey", Name: "providerKey"}}
	}
	// This handler returns only synthetic fixture data and validates exact write identities.
	return func(w http.ResponseWriter, r *http.Request) {

		if r.Method == http.MethodPut { // Credential writes must use the existing provider-secret contract.
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			} // A malformed mutation is always a test failure.
			for key, expected := range map[string]any{"bucket_id": entryTestBucketID, "service_id": "service-id", "key_name": "providerKey", "value": "synthetic-token"} {
				// Every identity and value must agree with the metadata target selected earlier.
				if body[key] != expected {
					t.Errorf("wrong credential field %s", key)
				}
			} // Never change the target chosen during inspection.
			(*writes)++
			w.WriteHeader(http.StatusNoContent)
			return
		}
		request := decodeGraphQLTestRequest(t, r)
		switch { // Reads resolve the bucket, service, and then credential metadata without plaintext.
		case strings.Contains(request.Query, "bucketReference"):
			if request.Variables["reference"] != "default" {
				t.Error("missing default bucket")
			} // Omission chooses default explicitly.
			fmtJSON(t, w, map[string]any{"data": map[string]any{"bucketReference": map[string]string{"id": entryTestBucketID}}})
		case strings.Contains(request.Query, "GetServiceInfo"):
			fmtJSON(t, w, map[string]any{"data": map[string]any{"service": map[string]any{"id": "service-id", "auth_configs": auths}}})
		case strings.Contains(request.Query, "SecretMetaPage"):
			items := []map[string]any{}
			if existing {
				items = append(items, map[string]any{"service_id": "service-id", "key_name": "providerKey", "credential_type": "api_key"})
			} // Existing metadata alone prevents a write.
			fmtJSON(t, w, map[string]any{"data": map[string]any{"secretMetaPage": map[string]any{"total": len(items), "items": items}}})
		default:
			t.Error("unexpected request")
			http.Error(w, "unexpected", 400)
		}
	}
}

// connectMetadataTestHandler provides either a denied read or a configured method on the second page.
func connectMetadataTestHandler(t *testing.T, denied bool, calls *int) http.HandlerFunc {
	// Serve only synthetic fixture data while checking exact request scope.
	return func(w http.ResponseWriter, r *http.Request) {

		(*calls)++
		if denied {
			http.Error(w, "permission denied", http.StatusForbidden)
			return
		} // Denials propagate without another strategy.
		request := decodeGraphQLTestRequest(t, r)
		item := map[string]any{"service_id": "other", "key_name": "providerKey", "credential_type": "api_key"}
		if request.Variables["offset"] == float64(1) {
			item["service_id"] = "service"
		} // Only page two has the requested service.
		fmtJSON(t, w, map[string]any{"data": map[string]any{"secretMetaPage": map[string]any{"total": 2, "items": []map[string]any{item}}}})
	}
}
