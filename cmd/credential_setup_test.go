package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

// TestAPIInitCanSkipOAuthBeforeCollection reproduces two Google services with
// missing app credentials and proves skipping still publishes config and applies.
func TestAPIInitCanSkipOAuthBeforeCollection(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	t.Setenv("CI", "false")
	previousNoInput := NoInput
	NoInput = false
	// Interactive state is process-global and must not affect subsequent tests.
	t.Cleanup(func() { NoInput = previousNoInput })
	var calls []string
	client := sdkPlanTestClient(t, func(request *http.Request) (int, string) {
		// Credential endpoints are deliberately absent: skipping permits only app plan/apply.
		switch request.URL.Path {
		case "/sdk-config/plan":
			calls = append(calls, "plan")
			var payload map[string]any
			decodeSDKPlanRequest(t, request, &payload)
			readiness := api.CredentialReadiness{
				Bucket: &api.MissingCredentialBucket{ID: sdkPlanTestBucketID, Name: "default"},
				MissingCredentials: []api.MissingCredentialRequirement{
					{ServiceID: sdkPlanTestServiceID, Service: "Gmail", AuthType: "oauth", AuthName: "gmailOAuth", RequiredFields: []api.MissingCredentialField{
						{Name: "client_id", SecretKey: "gmailOAuth_client_id"}, {Name: "client_secret", SecretKey: "gmailOAuth_client_secret"},
					}},
					{ServiceID: "33333333-3333-4333-8333-333333333333", Service: "Google Sheets", AuthType: "oauth", AuthName: "sheetsOAuth", RequiredFields: []api.MissingCredentialField{
						{Name: "client_id", SecretKey: "sheetsOAuth_client_id"}, {Name: "client_secret", SecretKey: "sheetsOAuth_client_secret"},
					}},
				},
			}
			response, err := json.Marshal(map[string]any{
				"plan_id": "plan-without-credentials", "summary": map[string]any{},
				"config_key": payload["config_key"], "source_hash": payload["source_hash"], "credential_readiness": readiness,
			})
			// An invalid fixture must not masquerade as a failed application plan.
			if err != nil {
				t.Fatal(err)
			}
			return http.StatusOK, string(response)
		case "/sdk-config/apply":
			calls = append(calls, "apply")
			var payload map[string]any
			decodeSDKPlanRequest(t, request, &payload)
			// Skipping must consume the original successful plan, without replanning.
			if payload["plan_id"] != "plan-without-credentials" {
				t.Fatalf("unexpected apply plan: %v", payload["plan_id"])
			}
			return http.StatusOK, `{"status":"applied","plan_id":"plan-without-credentials","app_family_id":"family-1","app_id":"app-1"}`
		default:
			t.Errorf("unexpected request after skipping credentials: %s", request.URL.Path)
			return http.StatusInternalServerError, `{}`
		}
	})
	choices, collections := 0, 0
	withSDKPlanPromptFakes(t,
		// Entering the collector would reproduce the original blank-pair failure.
		func(*api.AuthConfig, string) (secretCredentialInput, error) {
			collections++
			_, _, err := validateOAuthApplicationSecretInput("", "", 2)
			return secretCredentialInput{}, err
		},
		// The user must know the exact target before any credential is requested.
		func(message string) (bool, error) {
			choices++
			// The choice names the service and the selected bucket, not an inferred fallback.
			if !strings.Contains(message, "Gmail") || !strings.Contains(message, `bucket "default"`) {
				t.Fatalf("setup choice lacks target: %s", message)
			}
			return false, nil
		},
	)
	path := filepath.Join(directory, "threadify.yaml")
	request := unifiedInitFailureTestRequest(path)
	request.name, request.generate = "threadify", false
	request.services = []scaffoldService{{name: "gmail", version: "v1"}, {name: "google-sheets", version: "v4"}}
	request.operations = []scaffoldOperation{{service: "gmail", operation: "listMessages"}, {service: "google-sheets", operation: "getSpreadsheet"}}
	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&output)
	err := createPlanApplyUnifiedInit(command, client, unifiedInitModeAPI, request, false, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	// A deliberate skip must complete the API lifecycle without entering any credential field.
	if err != nil || choices != 1 || collections != 0 || !reflect.DeepEqual(calls, []string{"plan", "apply"}) {
		t.Fatalf("init err=%v choices=%d collections=%d calls=%v", err, choices, collections, calls)
	}
	parsed, err := configfile.ParseFile(path)
	// Both exact service versions must survive credential-free publication.
	if err != nil || parsed.SDK.Name != "threadify" || parsed.SDK.Services["gmail"].Version != "v1" || parsed.SDK.Services["google-sheets"].Version != "v4" || sdkGeneratesPackage(parsed.SDK) {
		t.Fatalf("unexpected saved config: %v, err=%v", parsed, err)
	}
	// The operator still receives the outstanding readiness guidance after creation.
	if !strings.Contains(output.String(), "Credential setup skipped") || !strings.Contains(output.String(), "Credential readiness") || !strings.Contains(output.String(), "Direct API ready") {
		t.Fatalf("missing deferred credential guidance: %s", output.String())
	}
}

// TestOptionalCredentialSetupFailuresPreserveBoundaries distinguishes a skip
// from cancellation, invalid input, and denied writes after explicit opt-in.
func TestOptionalCredentialSetupFailuresPreserveBoundaries(t *testing.T) {
	aborted := errors.New("prompt aborted")
	for _, testCase := range []struct {
		name            string
		accepted        bool
		confirmationErr error
		invalidInput    bool
		wantEvents      []string
		wantError       string
	}{
		{"skip", false, nil, false, []string{"choice"}, errCredentialStorageDeclined.Error()},
		{"abort", false, aborted, false, []string{"choice"}, "prompt aborted"},
		{"invalid pair", true, nil, true, []string{"choice", "collect"}, "OAuth/OIDC application credentials require exactly"},
		{"denied write", true, nil, false, []string{"choice", "collect", "write"}, "403"},
	} {
		// Per-case prompt fakes assert the order of all credential boundaries.
		t.Run(testCase.name, func(t *testing.T) {
			var events []string
			client := sdkPlanTestClient(t, func(request *http.Request) (int, string) {
				events = append(events, "write")
				// OAuth registration must remain one atomic credential-family request.
				if request.URL.Path != "/workspace/secrets/bulk" {
					t.Errorf("unexpected mutation: %s", request.URL.Path)
				}
				return http.StatusForbidden, `{"error":"forbidden"}`
			})
			withSDKPlanPromptFakes(t,
				// Use the actual OAuth validator to retain strict pair requirements after opt-in.
				func(*api.AuthConfig, string) (secretCredentialInput, error) {
					events = append(events, "collect")
					// Missing input is an error only after the user elected to configure it.
					if testCase.invalidInput {
						_, _, err := validateOAuthApplicationSecretInput("", "", 2)
						return secretCredentialInput{}, err
					}
					return secretCredentialInput{clientID: "test-client", clientSecret: "test-secret"}, nil
				},
				// Confirmation always precedes collection, including cancellation and decline.
				func(string) (bool, error) {
					events = append(events, "choice")
					return testCase.accepted, testCase.confirmationErr
				},
			)
			err := setSecretForAuth(client, sdkPlanTestServiceID, sdkPlanTestBucketID,
				&api.AuthConfig{Name: "googleOAuth", Type: "oauth"}, "", nil, "Gmail", io.Discard,
				credentialMutationOptions{confirmation: "Set up now?"})
			// Neither collection errors nor denied writes may become successful skips.
			if err == nil || !strings.Contains(err.Error(), testCase.wantError) || !reflect.DeepEqual(events, testCase.wantEvents) {
				t.Fatalf("err=%v events=%v; want %q, %v", err, events, testCase.wantError, testCase.wantEvents)
			}
		})
	}
}
