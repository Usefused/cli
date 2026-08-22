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

func TestSDKPlanInteractiveStoresBearerInTypedBucketAndRetriesOnce(t *testing.T) {
	const secret = "provider-token-never-print"
	planCalls, secretCalls := 0, 0
	client := sdkPlanTestClient(t, func(request *http.Request) (int, string) {
		switch request.URL.Path {
		case "/sdk-config/plan":
			planCalls++
			if planCalls == 1 {
				return http.StatusBadRequest, sdkPlanMissingCredentialError("bearer", "jiraBearer", "token", "jiraBearer")
			}
			return http.StatusOK, `{"plan_id":"plan-ok","summary":{}}`
		case "/workspace/secrets":
			secretCalls++
			var payload map[string]any
			decodeSDKPlanRequest(t, request, &payload)
			if payload["bucket_id"] != sdkPlanTestBucketID || payload["service_id"] != sdkPlanTestServiceID {
				t.Fatalf("secret payload = %#v", payload)
			}
			if payload["key_name"] != "jiraBearer" || payload["value"] != secret {
				t.Fatalf("stored secret metadata = %#v", payload)
			}
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
		nil,
		func(message string) (bool, error) {
			if !strings.Contains(message, `bucket "production"`) || !strings.Contains(message, "Jira") {
				t.Fatalf("confirmation = %q", message)
			}
			return true, nil
		},
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

func TestSDKPlanInteractiveRegistersOAuthAppWithoutProviderToken(t *testing.T) {
	const clientSecret = "oauth-client-secret-never-print"
	planCalls, connectCalls := 0, 0
	client := sdkPlanTestClient(t, func(request *http.Request) (int, string) {
		switch {
		case request.URL.Path == "/sdk-config/plan":
			planCalls++
			if planCalls == 1 {
				return http.StatusBadRequest, sdkPlanMissingCredentialError("oauth", "googleOAuth", "connection", "")
			}
			return http.StatusOK, `{"plan_id":"plan-oauth","summary":{}}`
		case strings.HasSuffix(request.URL.Path, "/connect-config"):
			connectCalls++
			var payload api.ConnectConfigUpsertRequest
			decodeSDKPlanRequest(t, request, &payload)
			if payload.ClientSecret == nil || *payload.ClientSecret != clientSecret || payload.AuthType == nil || *payload.AuthType != "oauth" {
				t.Fatalf("connect payload = %#v", payload)
			}
			return http.StatusOK, `{"id":"cfg-1","bucket_id":"` + sdkPlanTestBucketID + `","service_id":"` + sdkPlanTestServiceID + `","auth_type":"oauth","auth_name":"googleOAuth","enabled":true,"redirect_uri":"http://localhost:8081/workspace/connect/callback","has_client_id":true,"has_client_secret":true,"created_at":"2026-08-22T00:00:00Z","updated_at":"2026-08-22T00:00:00Z"}`
		default:
			t.Fatalf("unexpected request path %s", request.URL.Path)
			return http.StatusNotFound, `{}`
		}
	})

	withSDKPlanPromptFakes(t, nil,
		func(authType, authName, _ string) (api.ConnectConfigUpsertRequest, error) {
			clientID, redirectURI := "client-id", "http://localhost:8081/workspace/connect/callback"
			return api.ConnectConfigUpsertRequest{
				AuthType: &authType, AuthName: &authName, ClientID: &clientID,
				ClientSecret: stringPointer(clientSecret), RedirectURI: &redirectURI,
			}, nil
		},
		func(string) (bool, error) { return true, nil },
	)

	var output bytes.Buffer
	_, err := planConfigWithRemediation(client, sdkPlanTestConfig(), "http://engine.test", planOptions{interactive: true, output: &output})
	if err != nil {
		t.Fatalf("interactive OAuth plan: %v", err)
	}
	if planCalls != 2 || connectCalls != 1 || strings.Contains(output.String(), clientSecret) {
		t.Fatalf("plan_calls=%d connect_calls=%d output=%q", planCalls, connectCalls, output.String())
	}
}

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
			}, nil, func(string) (bool, error) { return true, nil })

			if _, err := planConfigWithRemediation(client, sdkPlanTestConfig(), "http://engine.test", planOptions{interactive: true}); err == nil {
				t.Fatal("expected plan error")
			}
			if planCalls != test.wantPlans || secretCalls != test.wantSecrets || prompts != test.wantSecrets {
				t.Fatalf("plans=%d secrets=%d prompts=%d", planCalls, secretCalls, prompts)
			}
		})
	}
}

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
		nil,
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

func TestMissingCredentialValidationAllowsUnnamedOAuthAndRejectsArbitraryStaticKey(t *testing.T) {
	oauth := api.MissingCredentialRequirement{
		ServiceID: sdkPlanTestServiceID, AuthType: "oauth", AuthName: "",
		RequiredFields: []api.MissingCredentialField{{Name: "connection"}},
	}
	if err := validateMissingCredentialRequirement(oauth); err != nil {
		t.Fatalf("unnamed OAuth requirement: %v", err)
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

func withSDKPlanPromptFakes(
	t *testing.T,
	secret func(*api.AuthConfig, string) (secretCredentialInput, error),
	connect func(string, string, string) (api.ConnectConfigUpsertRequest, error),
	confirm func(string) (bool, error),
) {
	t.Helper()
	oldSecret, oldConnect, oldConfirm := collectSecretCredentialInput, collectConnectSetFields, promptCredentialMutationConfirmation
	if secret != nil {
		collectSecretCredentialInput = secret
	}
	if connect != nil {
		collectConnectSetFields = connect
	}
	if confirm != nil {
		promptCredentialMutationConfirmation = confirm
	}
	t.Cleanup(func() {
		collectSecretCredentialInput, collectConnectSetFields, promptCredentialMutationConfirmation = oldSecret, oldConnect, oldConfirm
	})
}
