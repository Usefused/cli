package cmd

import (
	"bytes"
	"context"
	"encoding/json"
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

// TestSDKPlanInteractiveStoresBearerInTypedBucketAndRetriesOnce covers the shared single-secret remediation path.
func TestSDKPlanInteractiveStoresBearerInTypedBucketAndRetriesOnce(t *testing.T) {
	const secret = "provider-token-never-print"
	planCalls, secretCalls := 0, 0
	client := sdkPlanTestClient(t, func(request *http.Request) (int, string) {
		return sdkPlanBearerResponse(t, request, secret, &planCalls, &secretCalls)
	})

	withSDKPlanPromptFakes(t,
		func(*api.AuthConfig, string) (secretCredentialInput, error) {
			return secretCredentialInput{token: secret}, nil
		},
		confirmSDKPlanBearerMutation(t),
	)

	var output bytes.Buffer
	result, err := planConfigWithRemediation(client, sdkPlanTestConfig(), "http://engine.test", planOptions{
		interactive: true, output: &output, auditCtx: context.Background(), auditAction: "fused-cli sdk plan",
	})
	if err != nil {
		t.Fatalf("interactive plan: %v", err)
	}
	if result.receipt.PlanID != "plan-ok" || planCalls != 2 || secretCalls != 1 {
		t.Fatalf("result=%#v plan_calls=%d secret_calls=%d", result, planCalls, secretCalls)
	}
	if strings.Contains(output.String(), secret) || !strings.Contains(output.String(), "Jira") {
		t.Fatalf("unsafe or incomplete output %q", output.String())
	}
}

// sdkPlanBearerResponse serves the two plan attempts and one exact secret mutation.
func sdkPlanBearerResponse(t *testing.T, request *http.Request, secret string, planCalls, secretCalls *int) (int, string) {
	t.Helper()
	// Route ownership distinguishes planning from the one authorized credential mutation.
	switch request.URL.Path {
	case "/sdk-config/plan":
		*planCalls++
		// The first attempt reports the typed remediation target; the retry succeeds.
		if *planCalls == 1 {
			return http.StatusBadRequest, sdkPlanMissingCredentialError("bearer", "jiraBearer", "token", "jiraBearer")
		}
		return http.StatusOK, `{"plan_id":"plan-ok","summary":{}}`
	case "/workspace/secrets":
		*secretCalls++
		assertSDKPlanBearerSecretRequest(t, request, secret)
		return http.StatusOK, `{}`
	default:
		// Any additional route would indicate remediation escaped the shared secret path.
		t.Fatalf("unexpected request path %s", request.URL.Path)
		return http.StatusNotFound, `{}`
	}
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

// confirmSDKPlanBearerMutation verifies the prompt names the authoritative remediation target.
func confirmSDKPlanBearerMutation(t *testing.T) func(string) (bool, error) {
	t.Helper()
	return func(message string) (bool, error) {
		// The operator must see both the exact bucket and service before approving storage.
		if !strings.Contains(message, `bucket "production"`) || !strings.Contains(message, "Jira") {
			t.Fatalf("confirmation = %q", message)
		}
		return true, nil
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
				return http.StatusBadRequest, sdkPlanOAuthMissingCredentialError("oauth", "googleOAuth")
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

// TestSDKPlanInteractiveDoesNotHandleOtherErrorsOrLoop bounds remediation to one typed retry.
func TestSDKPlanInteractiveDoesNotHandleOtherErrorsOrLoop(t *testing.T) {
	tests := []struct {
		name          string
		firstResponse string
		wantPlans     int
		wantSecrets   int
	}{
		{name: "unrelated error", firstResponse: `{"error":{"code":"request_rejected","message":"invalid SDK","category":"validation"}}`, wantPlans: 1},
		{name: "second readiness failure", firstResponse: sdkPlanMissingCredentialError("bearer", "jiraBearer", "token", "jiraBearer"), wantPlans: 2, wantSecrets: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			planCalls, secretCalls, prompts := 0, 0, 0
			client := sdkPlanTestClient(t, func(request *http.Request) (int, string) {
				switch request.URL.Path {
				case "/sdk-config/plan":
					planCalls++
					return http.StatusBadRequest, test.firstResponse
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

			if _, err := planConfigWithRemediation(client, sdkPlanTestConfig(), "http://engine.test", planOptions{interactive: true}); err == nil {
				t.Fatal("expected plan error")
			}
			if planCalls != test.wantPlans || secretCalls != test.wantSecrets || prompts != test.wantSecrets {
				t.Fatalf("plans=%d secrets=%d prompts=%d", planCalls, secretCalls, prompts)
			}
		})
	}
}

// TestSDKPlanInteractiveCancellationDoesNotMutateOrRetry preserves explicit operator cancellation.
func TestSDKPlanInteractiveCancellationDoesNotMutateOrRetry(t *testing.T) {
	planCalls, mutationCalls := 0, 0
	client := sdkPlanTestClient(t, func(request *http.Request) (int, string) {
		switch request.URL.Path {
		case "/sdk-config/plan":
			planCalls++
			return http.StatusBadRequest, sdkPlanMissingCredentialError("bearer", "jiraBearer", "token", "jiraBearer")
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

	if _, err := planConfigWithRemediation(client, sdkPlanTestConfig(), "http://engine.test", planOptions{interactive: true}); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("cancellation error = %v", err)
	}
	if planCalls != 1 || mutationCalls != 0 {
		t.Fatalf("plan_calls=%d mutation_calls=%d", planCalls, mutationCalls)
	}
}

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

func TestValidateSDKPlanCredentialTargetAcceptsEngineDefaultWhenYAMLOmitsBucket(t *testing.T) {
	cfg := sdkPlanTestConfig()
	cfg.SDK.Bucket = ""
	apiErr := &api.APIError{}
	apiErr.Details.Bucket = &api.MissingCredentialBucket{ID: sdkPlanTestBucketID, Name: "default"}
	bucket, err := validateSDKPlanCredentialTarget(cfg, apiErr)
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

func sdkPlanMissingCredentialError(authType, authName, fieldName, secretKey string) string {
	field := map[string]string{"name": fieldName}
	if secretKey != "" {
		field["secret_key"] = secretKey
	}
	details := map[string]any{
		"bucket": map[string]string{"id": sdkPlanTestBucketID, "name": "production"},
		"missing_credentials": []any{map[string]any{
			"service_id": sdkPlanTestServiceID, "service": "Jira", "auth_type": authType,
			"auth_name": authName, "required_fields": []any{field},
		}},
	}
	payload := map[string]any{"error": map[string]any{
		"code": "bucket_credentials_missing", "message": "credentials missing", "category": "validation", "details": details,
	}}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}

// sdkPlanOAuthMissingCredentialError mirrors the two-field readiness contract
// emitted by Engine for one atomic OAuth/OIDC application pair.
func sdkPlanOAuthMissingCredentialError(authType, authName string) string {
	details := map[string]any{
		"bucket": map[string]string{"id": sdkPlanTestBucketID, "name": "production"},
		"missing_credentials": []any{map[string]any{
			"service_id": sdkPlanTestServiceID, "service": "Jira", "auth_type": authType,
			"auth_name": authName, "required_fields": []any{
				map[string]string{"name": "client_id", "secret_key": authName + "_client_id"},
				map[string]string{"name": "client_secret", "secret_key": authName + "_client_secret"},
			},
		}},
	}
	payload := map[string]any{"error": map[string]any{
		"code": "bucket_credentials_missing", "message": "missing", "category": "validation", "details": details,
	}}
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
