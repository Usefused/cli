package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

const (
	sdkPlanTestBucketID  = "11111111-1111-4111-8111-111111111111"
	sdkPlanTestServiceID = "22222222-2222-4222-8222-222222222222"
)

type sdkPlanRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn sdkPlanRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

// TestSDKPlanSuccessfulReadinessWarningOptionallyStoresAndRetries proves the
// non-blocking Engine contract retains the terminal setup convenience.
func TestSDKPlanSuccessfulReadinessWarningOptionallyStoresAndRetries(t *testing.T) {
	const secret = "provider-token-never-print"
	planCalls, secretCalls := 0, 0
	client := sdkPlanTestClient(t, func(request *http.Request) (int, string) {
		// The successful first plan carries readiness; only the confirmed terminal path mutates and replans.
		switch request.URL.Path {
		case "/sdk-config/plan":
			planCalls++
			if planCalls == 1 {
				return http.StatusOK, sdkPlanReadinessResponse("plan-warning", "bearer", "jiraBearer", "token", "jiraBearer")
			}
			return http.StatusOK, `{"plan_id":"plan-ready","summary":{}}`
		case "/workspace/secrets":
			secretCalls++
			assertSDKPlanBearerSecretRequest(t, request, secret)
			return http.StatusOK, `{}`
		default:
			t.Fatalf("unexpected request path %s", request.URL.Path)
			return http.StatusNotFound, `{}`
		}
	})
	withSDKPlanPromptFakes(t,
		func(*api.AuthConfig, string) (secretCredentialInput, error) {
			return secretCredentialInput{token: secret}, nil
		},
		func(string) (bool, error) { return true, nil },
	)
	result, err := planConfigWithRemediation(client, sdkPlanTestConfig(), "http://engine.test", planOptions{interactive: true, output: io.Discard})
	if err != nil || result.receipt.PlanID != "plan-ready" || planCalls != 2 || secretCalls != 1 {
		t.Fatalf("successful readiness remediation = %#v / %v calls=%d/%d", result, err, planCalls, secretCalls)
	}
}

// TestSDKPlanReadinessWarningDoesNotBlockNonInteractivePublication locks the
// credential-free --no-input/CI behavior requested for automation.
func TestSDKPlanReadinessWarningDoesNotBlockNonInteractivePublication(t *testing.T) {
	planCalls, secretCalls := 0, 0
	client := sdkPlanTestClient(t, func(request *http.Request) (int, string) {
		// A non-plan request would prove automation unexpectedly crossed the credential mutation boundary.
		if request.URL.Path != "/sdk-config/plan" {
			secretCalls++
			return http.StatusInternalServerError, `{}`
		}
		planCalls++
		return http.StatusOK, sdkPlanReadinessResponse("plan-warning", "bearer", "jiraBearer", "token", "jiraBearer")
	})
	result, err := planConfigWithRemediation(client, sdkPlanTestConfig(), "http://engine.test", planOptions{interactive: false, output: io.Discard})
	if err != nil || result.receipt.PlanID != "plan-warning" || result.credentialReadiness == nil || planCalls != 1 || secretCalls != 0 {
		t.Fatalf("non-interactive readiness plan = %#v / %v calls=%d/%d", result, err, planCalls, secretCalls)
	}
}

// sdkPlanReadinessResponse builds one successful plan with credential-free readiness metadata.
func sdkPlanReadinessResponse(planID, authType, authName, fieldName, secretKey string) string {
	return fmt.Sprintf(`{"plan_id":%q,"summary":{},"credential_readiness":{"bucket":{"id":%q,"name":"production"},"missing_credentials":[{"service_id":%q,"service":"Jira","auth_type":%q,"auth_name":%q,"required_fields":[{"name":%q,"secret_key":%q}]}]}}`,
		planID, sdkPlanTestBucketID, sdkPlanTestServiceID, authType, authName, fieldName, secretKey)
}

// assertSDKPlanBearerSecretRequest validates identity and value without echoing the token on success.
func assertSDKPlanBearerSecretRequest(t *testing.T, request *http.Request, secret string) {
	t.Helper()
	var payload map[string]any
	decodeSDKPlanRequest(t, request, &payload)
	// Bucket and service identity must come from the typed readiness error.
	if payload["bucket_id"] != sdkPlanTestBucketID || payload["service_id"] != sdkPlanTestServiceID {
		t.Fatalf("secret payload = %#v", payload)
	}
	// The provider-declared key receives exactly the securely collected value.
	if payload["key_name"] != "jiraBearer" || payload["value"] != secret {
		t.Fatalf("stored secret metadata = %#v", payload)
	}
}

// TestSDKPlanInteractiveRegistersOAuthAppWithoutProviderToken stores only the application pair through secret set.
func TestSDKPlanInteractiveRegistersOAuthAppWithoutProviderToken(t *testing.T) {
	const clientSecret = "oauth-client-secret-never-print"
	planCalls, secretCalls := 0, 0
	client := sdkPlanTestClient(t, func(request *http.Request) (int, string) {
		switch {
		case request.URL.Path == "/sdk-config/plan":
			planCalls++
			// The first response mirrors Engine's deterministic OAuth readiness fields.
			if planCalls == 1 {
				return http.StatusOK, sdkPlanOAuthReadinessResponse("plan-oauth-warning", "oauth", "googleOAuth")
			}
			return http.StatusOK, `{"plan_id":"plan-oauth","summary":{}}`
		case request.URL.Path == "/workspace/secrets/bulk":
			secretCalls++
			var payload struct {
				CredentialFamily api.CredentialFamilyUpsertRequest `json:"credential_family"`
			}
			decodeSDKPlanRequest(t, request, &payload)
			if payload.CredentialFamily.Values["client_secret"] != clientSecret || payload.CredentialFamily.CredentialType != "oauth" {
				t.Fatalf("secret family payload = %#v", payload)
			}
			return http.StatusNoContent, ``
		default:
			t.Fatalf("unexpected request path %s", request.URL.Path)
			return http.StatusNotFound, `{}`
		}
	})

	withSDKPlanPromptFakes(t,
		func(*api.AuthConfig, string) (secretCredentialInput, error) {
			return secretCredentialInput{clientID: "client-id", clientSecret: clientSecret}, nil
		},
		func(string) (bool, error) { return true, nil },
	)

	var output bytes.Buffer
	_, err := planConfigWithRemediation(client, sdkPlanTestConfig(), "http://engine.test", planOptions{interactive: true, output: &output})
	if err != nil {
		t.Fatalf("interactive OAuth plan: %v", err)
	}
	if planCalls != 2 || secretCalls != 1 || strings.Contains(output.String(), clientSecret) {
		t.Fatalf("plan_calls=%d secret_calls=%d output=%q", planCalls, secretCalls, output.String())
	}
}

// TestSDKPlanInteractiveDoesNotReinterpretPlanErrors proves only successful readiness can authorize credential setup.
func TestSDKPlanInteractiveDoesNotReinterpretPlanErrors(t *testing.T) {
	planCalls, mutationCalls, prompts := 0, 0, 0
	client := sdkPlanTestClient(t, func(request *http.Request) (int, string) {
		// Any route after the failed plan would revive the removed compatibility path.
		if request.URL.Path != "/sdk-config/plan" {
			mutationCalls++
			return http.StatusInternalServerError, `{}`
		}
		planCalls++
		return http.StatusBadRequest, `{"error":{"code":"bucket_credentials_missing","message":"obsolete blocking response","category":"validation"}}`
	})
	withSDKPlanPromptFakes(t, func(*api.AuthConfig, string) (secretCredentialInput, error) {
		prompts++
		return secretCredentialInput{token: "never-stored"}, nil
	}, func(string) (bool, error) { return true, nil })
	if _, err := planConfigWithRemediation(client, sdkPlanTestConfig(), "http://engine.test", planOptions{interactive: true}); err == nil {
		t.Fatal("obsolete blocking plan response was accepted")
	}
	if planCalls != 1 || mutationCalls != 0 || prompts != 0 {
		t.Fatalf("plan_calls=%d mutation_calls=%d prompts=%d", planCalls, mutationCalls, prompts)
	}
}

// TestSDKPlanInteractiveRetriesReadinessOnlyOnce proves a repeated warning cannot create a prompt loop.
func TestSDKPlanInteractiveRetriesReadinessOnlyOnce(t *testing.T) {
	planCalls, secretCalls, prompts := 0, 0, 0
	client := sdkPlanTestClient(t, func(request *http.Request) (int, string) {
		// Only two successful plans and one secret mutation belong to the bounded workflow.
		switch request.URL.Path {
		case "/sdk-config/plan":
			planCalls++
			return http.StatusOK, sdkPlanReadinessResponse("plan-warning", "bearer", "jiraBearer", "token", "jiraBearer")
		case "/workspace/secrets":
			secretCalls++
			return http.StatusOK, `{}`
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
			return http.StatusNotFound, `{}`
		}
	})
	withSDKPlanPromptFakes(t, func(*api.AuthConfig, string) (secretCredentialInput, error) {
		prompts++
		return secretCredentialInput{token: "value"}, nil
	}, func(string) (bool, error) { return true, nil })
	result, err := planConfigWithRemediation(client, sdkPlanTestConfig(), "http://engine.test", planOptions{interactive: true})
	if err != nil || result.credentialReadiness == nil || planCalls != 2 || secretCalls != 1 || prompts != 1 {
		t.Fatalf("result=%#v error=%v plans=%d secrets=%d prompts=%d", result, err, planCalls, secretCalls, prompts)
	}
}

// TestSDKPlanInteractiveCancellationKeepsValidPlan preserves explicit operator cancellation without mutation.
func TestSDKPlanInteractiveCancellationKeepsValidPlan(t *testing.T) {
	planCalls, mutationCalls := 0, 0
	client := sdkPlanTestClient(t, func(request *http.Request) (int, string) {
		switch request.URL.Path {
		case "/sdk-config/plan":
			planCalls++
			return http.StatusOK, sdkPlanReadinessResponse("plan-warning", "bearer", "jiraBearer", "token", "jiraBearer")
		case "/workspace/secrets":
			mutationCalls++
			return http.StatusOK, `{}`
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
			return http.StatusNotFound, `{}`
		}
	})
	withSDKPlanPromptFakes(t,
		func(*api.AuthConfig, string) (secretCredentialInput, error) {
			return secretCredentialInput{token: "never-stored"}, nil
		},
		func(string) (bool, error) { return false, nil },
	)

	result, err := planConfigWithRemediation(client, sdkPlanTestConfig(), "http://engine.test", planOptions{interactive: true, output: io.Discard})
	if err != nil || result.receipt.PlanID != "plan-warning" {
		t.Fatalf("cancelled setup did not preserve plan: %#v / %v", result, err)
	}
	if planCalls != 1 || mutationCalls != 0 {
		t.Fatalf("plan_calls=%d mutation_calls=%d", planCalls, mutationCalls)
	}
}

// TestValidateSDKPlanInteractionRejectsJSONAndCI preserves explicit-flag errors
// for structured and non-interactive invocations.
func TestValidateSDKPlanInteractionRejectsJSONAndCI(t *testing.T) {
	oldInteractive, oldJSON, oldNoInput := sdkPlanInteractive, sdkPlanJSON, NoInput
	t.Cleanup(func() { sdkPlanInteractive, sdkPlanJSON, NoInput = oldInteractive, oldJSON, oldNoInput })
	sdkPlanInteractive, sdkPlanJSON, NoInput = true, true, false
	if err := validateSDKPlanInteraction(); err == nil || !strings.Contains(err.Error(), "--json") {
		t.Fatalf("JSON validation error = %v", err)
	}
	sdkPlanJSON, NoInput = false, true
	if err := validateSDKPlanInteraction(); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("non-interactive validation error = %v", err)
	}
}

// TestSDKPlanCredentialRemediationDefaultsToInteractive verifies prompting is
// derived from execution context rather than requiring the compatibility flag.
func TestSDKPlanCredentialRemediationDefaultsToInteractive(t *testing.T) {
	oldInteractive, oldJSON, oldNoInput := sdkPlanInteractive, sdkPlanJSON, NoInput
	t.Cleanup(func() { sdkPlanInteractive, sdkPlanJSON, NoInput = oldInteractive, oldJSON, oldNoInput })
	t.Setenv("CI", "")
	sdkPlanInteractive, sdkPlanJSON, NoInput = false, false, false
	if !sdkPlanUsesInteractiveCredentialRemediation() {
		t.Fatal("terminal sdk plan should remediate missing credentials by default")
	}
	// JSON output is a machine-readable contract and must never contain prompts.
	sdkPlanJSON = true
	if sdkPlanUsesInteractiveCredentialRemediation() {
		t.Fatal("JSON sdk plan must remain non-interactive")
	}
	// --no-input is the explicit automation opt-out requested by the caller.
	sdkPlanJSON, NoInput = false, true
	if sdkPlanUsesInteractiveCredentialRemediation() {
		t.Fatal("--no-input sdk plan must remain non-interactive")
	}
}

// TestValidateSDKPlanCredentialTargetAcceptsEngineDefaultWhenYAMLOmitsBucket preserves default resolution for warning-based prompts.
func TestValidateSDKPlanCredentialTargetAcceptsEngineDefaultWhenYAMLOmitsBucket(t *testing.T) {
	cfg := sdkPlanTestConfig()
	cfg.SDK.Bucket = ""
	// Successful readiness owns the bucket target now that API errors cannot trigger remediation.
	readinessBucket := &api.MissingCredentialBucket{ID: sdkPlanTestBucketID, Name: "default"}
	bucket, err := validateSDKPlanCredentialTarget(cfg, readinessBucket)
	if err != nil || bucket.ID != sdkPlanTestBucketID || bucket.Name != "default" {
		t.Fatalf("default bucket = %#v, %v", bucket, err)
	}
}

// TestMissingCredentialValidationRequiresExactOAuthKeys rejects both the
// retired connection sentinel and arbitrary static-secret destinations.
func TestMissingCredentialValidationRequiresExactOAuthKeys(t *testing.T) {
	oauth := api.MissingCredentialRequirement{
		ServiceID: sdkPlanTestServiceID, AuthType: "oauth", AuthName: "googleOAuth",
		RequiredFields: []api.MissingCredentialField{
			{Name: "client_id", SecretKey: "googleOAuth_client_id"},
			{Name: "client_secret", SecretKey: "googleOAuth_client_secret"},
		},
	}
	if err := validateMissingCredentialRequirement(oauth); err != nil {
		t.Fatalf("OAuth requirement: %v", err)
	}
	// The removed workspace-connect shape must fail instead of reviving a second credential path.
	oauth.RequiredFields = []api.MissingCredentialField{{Name: "connection"}}
	if err := validateMissingCredentialRequirement(oauth); err == nil {
		t.Fatal("expected retired connection field to be rejected")
	}
	bearer := api.MissingCredentialRequirement{
		ServiceID: sdkPlanTestServiceID, AuthType: "bearer", AuthName: "jiraBearer",
		RequiredFields: []api.MissingCredentialField{{Name: "token", SecretKey: "<credential-name>"}},
	}
	if err := validateMissingCredentialRequirement(bearer); err == nil {
		t.Fatal("expected arbitrary/sentinel secret key to be rejected")
	}
}

func sdkPlanTestConfig() *configfile.ParsedConfig {
	return &configfile.ParsedConfig{
		Kind: configfile.KindSDK, ConfigKey: "sdk:google:1.0.0", SourceHash: "sha256:test",
		SDK: &configfile.SDKConfig{Name: "google", Version: "1.0.0", Language: "typescript", Bucket: "production"},
	}
}

// sdkPlanOAuthReadinessResponse mirrors the two-field successful readiness contract for one atomic application pair.
func sdkPlanOAuthReadinessResponse(planID, authType, authName string) string {
	readiness := map[string]any{
		"bucket": map[string]string{"id": sdkPlanTestBucketID, "name": "production"},
		"missing_credentials": []any{map[string]any{
			"service_id": sdkPlanTestServiceID, "service": "Jira", "auth_type": authType,
			"auth_name": authName, "required_fields": []any{
				map[string]string{"name": "client_id", "secret_key": authName + "_client_id"},
				map[string]string{"name": "client_secret", "secret_key": authName + "_client_secret"},
			},
		}},
	}
	payload := map[string]any{"plan_id": planID, "summary": map[string]any{}, "credential_readiness": readiness}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}

func sdkPlanTestClient(t *testing.T, handler func(*http.Request) (int, string)) *api.Client {
	t.Helper()
	client := api.NewClient("http://engine.test", "control-key")
	client.HTTP.Transport = sdkPlanRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		status, body := handler(request)
		return &http.Response{
			StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})
	return client
}

func decodeSDKPlanRequest(t *testing.T, request *http.Request, target any) {
	t.Helper()
	if err := json.NewDecoder(request.Body).Decode(target); err != nil {
		t.Fatalf("decode %s request: %v", request.URL.Path, err)
	}
}

// withSDKPlanPromptFakes scopes secure input and confirmation doubles to one test.
func withSDKPlanPromptFakes(
	t *testing.T,
	secret func(*api.AuthConfig, string) (secretCredentialInput, error),
	confirm func(string) (bool, error),
) {
	t.Helper()
	oldSecret, oldConfirm := collectSecretCredentialInput, promptCredentialMutationConfirmation
	if secret != nil {
		collectSecretCredentialInput = secret
	}
	if confirm != nil {
		promptCredentialMutationConfirmation = confirm
	}
	t.Cleanup(func() {
		collectSecretCredentialInput, promptCredentialMutationConfirmation = oldSecret, oldConfirm
	})
}
