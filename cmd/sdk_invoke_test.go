package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const sdkInvokeTestAppID = "7d563806-a193-4e57-88d6-ffac941bcd20"

type sdkInvokePhysicalTestHandler struct {
	t                 *testing.T
	controlCredential string
	executionToken    string
	resolutionCalls   int
	executionCalls    int
}

// ServeHTTP captures the distinct control-resolution and runtime-execution credential boundaries.
func (handler *sdkInvokePhysicalTestHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/engine/graphql":
		handler.resolutionCalls++
		if request.Header.Get("x-api-key") != handler.controlCredential {
			handler.t.Fatalf("resolution control header = %q", request.Header.Get("x-api-key"))
		}
		_, _ = writer.Write([]byte(`{"data":{"appReference":{"id":"` + sdkInvokeTestAppID + `","kind":"app"}}}`))
	case "/v1/apps/" + sdkInvokeTestAppID + "/executions":
		handler.executionCalls++
		assertSDKInvokeRuntimeHeaders(handler.t, request, handler.executionToken, "physical-idempotency", handler.controlCredential)
		body := decodeSDKInvokeTestRequest(handler.t, request)
		if body.Operation != "projects.list" || string(body.Input) != `{"limit":1}` || body.Selector == nil || body.Selector.Environment != "staging" {
			handler.t.Fatalf("physical body = %#v", body)
		}
		_, _ = writer.Write([]byte(`{"app_id":"` + sdkInvokeTestAppID + `","operation":"projects.list","kind":"physical","status_code":200,"results":[{"values":[]}]}`))
	default:
		handler.t.Fatalf("unexpected path %s", request.URL.Path)
	}
}

// resetSDKInvokeTestState isolates command globals so transport tests cannot affect the wider CLI suite.
func resetSDKInvokeTestState(t *testing.T) {
	t.Helper()
	oldEngineURL, oldAPIKey, oldRequestID := EngineURL, APIKey, RequestID
	oldTimeout := RequestTimeout
	oldParams, oldTokenEnv, oldTokenStdin := sdkInvokeParams, sdkInvokeTokenEnv, sdkInvokeTokenStdin
	oldEnvironment, oldIdempotency := sdkInvokeEnvironment, sdkInvokeIdempotencyKey
	oldTargets := append([]string(nil), sdkInvokeTargets...)
	oldSelector, oldSelectors := sdkInvokeSelector, sdkInvokeSelectors
	t.Cleanup(func() {
		EngineURL, APIKey, RequestID, RequestTimeout = oldEngineURL, oldAPIKey, oldRequestID, oldTimeout
		sdkInvokeParams, sdkInvokeTokenEnv, sdkInvokeTokenStdin = oldParams, oldTokenEnv, oldTokenStdin
		sdkInvokeEnvironment, sdkInvokeIdempotencyKey = oldEnvironment, oldIdempotency
		sdkInvokeTargets, sdkInvokeSelector, sdkInvokeSelectors = oldTargets, oldSelector, oldSelectors
	})
	sdkInvokeParams, sdkInvokeTokenEnv, sdkInvokeTokenStdin = "{}", defaultSDKTokenEnvironment, false
	sdkInvokeEnvironment, sdkInvokeIdempotencyKey = "", ""
	sdkInvokeTargets, sdkInvokeSelector, sdkInvokeSelectors = nil, "", ""
	RequestID, RequestTimeout = "", 5*time.Second
}

// TestReadSDKInvokeParamsAcceptsOneDuplicateFreeJSONValue verifies Unified input shapes remain reachable.
func TestReadSDKInvokeParamsAcceptsOneDuplicateFreeJSONValue(t *testing.T) {
	for _, valid := range []string{`{"count":1}`, `[1,"two",null]`, `"query"`, `42`, `true`, `null`} {
		data, err := readSDKInvokeParams(valid, false, strings.NewReader(""))
		if err != nil || string(data) != valid {
			t.Fatalf("params = %q, %v", data, err)
		}
	}
	for _, invalid := range []string{``, `{"same":1,"same":2}`, `{"nested":{"same":1,"same":2}}`, `{"ok":true} {"extra":true}`} {
		if _, err := readSDKInvokeParams(invalid, false, strings.NewReader("")); err == nil {
			t.Fatalf("expected %q to be rejected", invalid)
		}
	}
}

// TestReadSDKInvokeParamsSupportsFiles verifies @file uses the same strict JSON-value parser.
func TestReadSDKInvokeParamsSupportsFiles(t *testing.T) {
	file := filepath.Join(t.TempDir(), "params.json")
	if err := os.WriteFile(file, []byte(`{"query":"jira"}`), 0o600); err != nil {
		t.Fatalf("write params: %v", err)
	}
	data, err := readSDKInvokeParams("@"+file, false, strings.NewReader(""))
	if err != nil || string(data) != `{"query":"jira"}` {
		t.Fatalf("file params = %q, %v", data, err)
	}
}

// TestReadSDKInvokeParamsAndTokenCannotShareStdin verifies unambiguous stdin ownership.
func TestReadSDKInvokeParamsAndTokenCannotShareStdin(t *testing.T) {
	if _, err := readSDKInvokeParams("-", true, strings.NewReader(`{}`)); err == nil {
		t.Fatal("expected shared stdin to be rejected")
	}
}

// TestReadSDKInvokeTokenUsesOnlyExplicitRuntimeSources verifies credential boundary isolation.
func TestReadSDKInvokeTokenUsesOnlyExplicitRuntimeSources(t *testing.T) {
	resetSDKInvokeTestState(t)
	t.Setenv(defaultSDKTokenEnvironment, "runtime-token")
	t.Setenv("FUSED_API_KEY", "control-token")
	t.Setenv("FUSED_LICENSE_KEY", "license-token")
	token, err := readSDKInvokeToken(bytes.NewBuffer(nil))
	if err != nil || token != "runtime-token" {
		t.Fatalf("token = %q, %v", token, err)
	}
	t.Setenv(defaultSDKTokenEnvironment, "")
	if _, err := readSDKInvokeToken(bytes.NewBuffer(nil)); err == nil || strings.Contains(err.Error(), "FUSED_API_KEY") {
		t.Fatalf("runtime token failure = %v", err)
	}
}

// TestSDKInvokeSelectorParsingIsClosedAndFileBacked verifies only non-secret routing fields are accepted.
func TestSDKInvokeSelectorParsingIsClosedAndFileBacked(t *testing.T) {
	file := filepath.Join(t.TempDir(), "selector.json")
	if err := os.WriteFile(file, []byte(`{"end_user_ref":"jira-user","auth_type":"oauth","auth_name":"JiraOAuth"}`), 0o600); err != nil {
		t.Fatalf("write selector: %v", err)
	}
	selector, err := readSDKInvokeSelector("@" + file)
	if err != nil || selector.EndUserRef != "jira-user" || selector.AuthType != "oauth" {
		t.Fatalf("selector = %#v, %v", selector, err)
	}
	if _, err := readSDKInvokeSelector(`{"credentials":{"token":"secret"}}`); err == nil {
		t.Fatal("expected credential-bearing selector field to be rejected")
	}
	if _, err := readSDKInvokeSelectors(`{"jira":{"unknown":"value"}}`); err == nil {
		t.Fatal("expected unknown Unified selector field to be rejected")
	}
}

// TestBuildSDKInvokeRequestMergesEnvironmentAndRejectsNamespaceConflict verifies backward-compatible selector sugar.
func TestBuildSDKInvokeRequestMergesEnvironmentAndRejectsNamespaceConflict(t *testing.T) {
	resetSDKInvokeTestState(t)
	sdkInvokeSelector = `{"end_user_ref":"user-1","environment":"staging"}`
	sdkInvokeEnvironment = "staging"
	request, err := buildSDKInvokeRequest("issues.list", strings.NewReader(""))
	if err != nil || request.Selector == nil || request.Selector.Environment != "staging" {
		t.Fatalf("request = %#v, %v", request, err)
	}
	sdkInvokeEnvironment = "production"
	if _, err := buildSDKInvokeRequest("issues.list", strings.NewReader("")); err == nil {
		t.Fatal("expected conflicting environment selectors to fail")
	}
	sdkInvokeEnvironment, sdkInvokeSelector, sdkInvokeSelectors = "", `{}`, `{"jira":{}}`
	if _, err := buildSDKInvokeRequest("issues.list", strings.NewReader("")); err == nil {
		t.Fatal("expected physical and Unified selectors to conflict")
	}
}

// TestSDKInvokePhysicalUsesRESTBearerAndNeverLeaksControlCredential covers exact resolution plus physical execution.
func TestSDKInvokePhysicalUsesRESTBearerAndNeverLeaksControlCredential(t *testing.T) {
	resetSDKInvokeTestState(t)
	controlCredential, executionToken := "fsk_control_never_forward", "sdk_runtime_only"
	handler := &sdkInvokePhysicalTestHandler{t: t, controlCredential: controlCredential, executionToken: executionToken}
	server := httptest.NewServer(handler)
	defer server.Close()
	EngineURL, APIKey = server.URL, controlCredential
	t.Setenv(defaultSDKTokenEnvironment, executionToken)
	sdkInvokeParams, sdkInvokeEnvironment, sdkInvokeIdempotencyKey = `{"limit":1}`, "staging", "physical-idempotency"
	command := &cobra.Command{Use: "test"}
	var output bytes.Buffer
	command.SetOut(&output)
	if err := runSDKInvoke(command, sdkDownloadTarget{Name: "jira-sdk", Version: "1.0.0"}, "projects.list"); err != nil {
		t.Fatalf("run physical SDK invoke: %v", err)
	}
	if handler.resolutionCalls != 1 || handler.executionCalls != 1 || !strings.Contains(output.String(), "Kind: physical") {
		t.Fatalf("calls = resolution:%d execution:%d output:%q", handler.resolutionCalls, handler.executionCalls, output.String())
	}
}

// assertSDKInvokeRuntimeHeaders proves REST execution receives only its Bearer token and reviewed headers.
func assertSDKInvokeRuntimeHeaders(t *testing.T, request *http.Request, token, idempotencyKey, controlCredential string) {
	t.Helper()
	if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer "+token {
		t.Fatalf("runtime method/auth = %s/%q", request.Method, request.Header.Get("Authorization"))
	}
	if request.Header.Get("Idempotency-Key") != idempotencyKey || request.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("runtime headers = %#v", request.Header)
	}
	if request.Header.Get("x-api-key") != "" || request.Header.Get("x-api-key") == controlCredential {
		t.Fatalf("control credential leaked to runtime request")
	}
}

// decodeSDKInvokeTestRequest strictly decodes the request observed by an HTTP test server.
func decodeSDKInvokeTestRequest(t *testing.T, request *http.Request) sdkInvokeRequest {
	t.Helper()
	var body sdkInvokeRequest
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		t.Fatalf("decode execution request: %v", err)
	}
	return body
}

// TestSDKInvokeUnifiedSendsTargetsSelectorsAndDecodesRollbacks covers the Unified REST shape.
func TestSDKInvokeUnifiedSendsTargetsSelectorsAndDecodesRollbacks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assertSDKInvokeRuntimeHeaders(t, request, "runtime-token", "unified-idempotency", "control-token")
		body := decodeSDKInvokeTestRequest(t, request)
		if len(body.Targets) != 1 || body.Targets[0] != "jira_projects" || body.Selector != nil {
			t.Fatalf("Unified targets = %#v, selector = %#v", body.Targets, body.Selector)
		}
		if body.Selectors["jira"].EndUserRef != "jira-user" {
			t.Fatalf("Unified selectors = %#v", body.Selectors)
		}
		_, _ = w.Write([]byte(`{"app_id":"` + sdkInvokeTestAppID + `","operation":"research.run","kind":"unified","results":[{"target":"jira_projects","status":"success","data":{"values":[]},"error_code":null,"auth_action":null}],"rollbacks":[]}`))
	}))
	defer server.Close()
	prepared := preparedSDKInvocation{
		EngineURL: server.URL, AppID: sdkInvokeTestAppID, Token: "runtime-token", IdempotencyKey: "unified-idempotency",
		Request: sdkInvokeRequest{
			Operation: "research.run", Input: json.RawMessage(`{"query":"jira"}`), Targets: []string{"jira_projects"},
			Selectors: map[string]sdkInvokeSelectorValue{"jira": {EndUserRef: "jira-user", AuthType: "oauth", AuthName: "JiraOAuth"}},
		},
	}
	response, endpoint, err := executeSDKInvocation(context.Background(), prepared)
	if err != nil {
		t.Fatalf("execute Unified SDK invoke: %v", err)
	}
	if response.Kind != "unified" || len(response.Results) != 1 || len(response.Rollbacks) != 0 || !strings.HasSuffix(endpoint, "/executions") {
		t.Fatalf("Unified response = %#v, endpoint = %q", response, endpoint)
	}
}

// TestWriteSDKInvocationJSONPreservesKindSpecificRollbacks verifies Unified emits [] while physical omits the field.
func TestWriteSDKInvocationJSONPreservesKindSpecificRollbacks(t *testing.T) {
	emptyRollbacks := []any{}
	for _, test := range []struct {
		name          string
		output        sdkInvokeOutput
		wantRollbacks bool
	}{
		{name: "physical", output: sdkInvokeOutput{Kind: "physical", Results: []any{map[string]any{}}, StatusCode: 200}},
		{name: "Unified", output: sdkInvokeOutput{Kind: "unified", Results: []any{}, Rollbacks: &emptyRollbacks}, wantRollbacks: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := &cobra.Command{Use: "test"}
			addJSONOutputFlag(command)
			if err := command.Flags().Set(jsonOutputFlag, "true"); err != nil {
				t.Fatalf("enable JSON output: %v", err)
			}
			var output bytes.Buffer
			command.SetOut(&output)
			if err := writeSDKInvocationOutput(command, test.output); err != nil {
				t.Fatalf("write SDK invocation: %v", err)
			}
			hasRollbacks := bytes.Contains(output.Bytes(), []byte(`"rollbacks"`))
			if hasRollbacks != test.wantRollbacks || (test.wantRollbacks && !bytes.Contains(output.Bytes(), []byte(`"rollbacks":[]`))) {
				t.Fatalf("JSON output = %s", output.Bytes())
			}
		})
	}
}

// TestSDKInvokeHTTPErrorResponsesAreStructuredAndAuthOpaque covers reviewed and untrusted failures.
func TestSDKInvokeHTTPErrorResponsesAreStructuredAndAuthOpaque(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantCode    string
		wantMessage string
		forbidden   string
	}{
		{name: "validation", status: http.StatusUnprocessableEntity, body: `{"error":{"code":"invalid_targets","message":"targets are invalid","details":{"field":"targets"}}}`, wantCode: "invalid_targets", wantMessage: "targets are invalid"},
		{name: "auth opaque", status: http.StatusUnauthorized, body: `{"error":{"code":"app_not_found","message":"app secret-app exists","details":{}}}`, wantCode: "sdk_authentication_failed", wantMessage: "SDK execution token was rejected", forbidden: "secret-app"},
		{name: "authorization structured", status: http.StatusForbidden, body: `{"error":{"code":"operation_not_allowed","message":"operation is outside this token policy","details":{"operation":"items.list"}}}`, wantCode: "operation_not_allowed", wantMessage: "outside this token policy"},
		{name: "authorization opaque", status: http.StatusForbidden, body: `not json`, wantCode: "sdk_authorization_failed", wantMessage: "SDK execution is not allowed"},
		{name: "proxy opaque", status: http.StatusBadGateway, body: `provider leaked token fsk_private`, wantCode: "sdk_engine_failed", wantMessage: "Engine could not complete", forbidden: "fsk_private"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			prepared := preparedSDKInvocation{
				EngineURL: server.URL, AppID: sdkInvokeTestAppID, Token: "runtime-token", IdempotencyKey: "request-key",
				Request: sdkInvokeRequest{Operation: "items.list", Input: json.RawMessage(`{}`)},
			}
			_, _, err := executeSDKInvocation(context.Background(), prepared)
			var invokeErr *sdkInvokeError
			if !errors.As(err, &invokeErr) || invokeErr.code != test.wantCode || !strings.Contains(invokeErr.message, test.wantMessage) {
				t.Fatalf("invoke error = %#v", err)
			}
			if test.forbidden != "" && strings.Contains(err.Error(), test.forbidden) {
				t.Fatalf("error leaked forbidden server text: %v", err)
			}
		})
	}
}

// TestSDKInvokeRejectsRedirectWithoutForwardingBearer proves runtime tokens stay on the configured route.
func TestSDKInvokeRejectsRedirectWithoutForwardingBearer(t *testing.T) {
	var redirected bool
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		redirected = true
		if request.Header.Get("Authorization") != "" {
			t.Fatal("Bearer token reached redirect target")
		}
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL+"/capture")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	prepared := preparedSDKInvocation{
		EngineURL: origin.URL, AppID: sdkInvokeTestAppID, Token: "runtime-token", IdempotencyKey: "request-key",
		Request: sdkInvokeRequest{Operation: "items.list", Input: json.RawMessage(`{}`)},
	}
	if _, _, err := executeSDKInvocation(context.Background(), prepared); err == nil {
		t.Fatal("expected redirect response to fail")
	}
	if redirected {
		t.Fatal("execution client followed redirect")
	}
}

// TestDecodeSDKInvokeHTTPResponseEnforcesKindSpecificShape rejects ambiguous REST successes.
func TestDecodeSDKInvokeHTTPResponseEnforcesKindSpecificShape(t *testing.T) {
	for _, invalid := range []string{
		fmt.Sprintf(`{"app_id":%q,"operation":"op","kind":"physical","status_code":200,"results":[]}`, sdkInvokeTestAppID),
		fmt.Sprintf(`{"app_id":%q,"operation":"op","kind":"physical","status_code":200,"results":[{}],"rollbacks":[]}`, sdkInvokeTestAppID),
		fmt.Sprintf(`{"app_id":%q,"operation":"op","kind":"unified","status_code":200,"results":[],"rollbacks":[]}`, sdkInvokeTestAppID),
		fmt.Sprintf(`{"app_id":%q,"operation":"op","kind":"unified","results":[]}`, sdkInvokeTestAppID),
		`{"app_id":"not-a-uuid","operation":"op","kind":"physical","status_code":200,"results":[{}]}`,
	} {
		if _, err := decodeSDKInvokeHTTPResponse([]byte(invalid)); err == nil {
			t.Fatalf("expected invalid response %s to fail", invalid)
		}
	}
}

// TestDecodeSDKInvokeHTTPResponsePreservesLargeIntegers protects provider JSON precision above 2^53.
func TestDecodeSDKInvokeHTTPResponsePreservesLargeIntegers(t *testing.T) {
	body := fmt.Sprintf(`{"app_id":%q,"operation":"items.get","kind":"physical","status_code":200,"results":[{"id":9007199254740993}]}`, sdkInvokeTestAppID)
	response, err := decodeSDKInvokeHTTPResponse([]byte(body))
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	result, ok := response.Results[0].(map[string]any)
	if !ok {
		t.Fatalf("result type = %T", response.Results[0])
	}
	value, ok := result["id"].(json.Number)
	if !ok || value.String() != "9007199254740993" {
		t.Fatalf("large integer = %#v", result["id"])
	}
}

// TestDecodeSDKInvokeHTTPErrorPreservesProviderStatus verifies the CLI keeps
// Engine-reviewed status metadata without needing provider response text.
func TestDecodeSDKInvokeHTTPErrorPreservesProviderStatus(t *testing.T) {
	err := decodeSDKInvokeHTTPError(http.StatusBadGateway, []byte(`{"error":{"code":"provider_error","message":"provider returned an unsuccessful response","details":{"provider_http_status":429}}}`))
	var invokeErr *sdkInvokeError
	// Structured execution failures must remain typed for renderer and telemetry handling.
	if !errors.As(err, &invokeErr) {
		t.Fatalf("decode error type = %T", err)
	}
	// Both the Engine boundary and provider status remain distinguishable to callers.
	if invokeErr.details["provider_http_status"] != json.Number("429") || invokeErr.details["http_status"] != http.StatusBadGateway {
		t.Fatalf("decoded details = %#v", invokeErr.details)
	}
}

// TestDecodeSDKInvokeMissingCredentialBuildsSafeCommand proves human invocation
// errors expose the exact setup action only from validated structured fields.
func TestDecodeSDKInvokeMissingCredentialBuildsSafeCommand(t *testing.T) {
	bucketID, serviceID := uuid.NewString(), uuid.NewString()
	body := fmt.Sprintf(`{"error":{"code":"bucket_credentials_missing","message":"provider credentials are not configured","details":{"bucket_id":%q,"service_id":%q,"auth_type":"api_key","auth_name":"providerKey","command":"ignored-server-command"}}}`, bucketID, serviceID)
	err := decodeSDKInvokeHTTPError(http.StatusConflict, []byte(body))
	want := "fused-cli secret set " + serviceID + " --bucket " + bucketID + " --type 'api_key' --auth-name 'providerKey' --interactive"
	if !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "ignored-server-command") {
		t.Fatalf("credential invocation error = %q", err.Error())
	}
	// Malformed identity metadata must fall back to the safe message with no command.
	malformed := decodeSDKInvokeHTTPError(http.StatusConflict, []byte(`{"error":{"code":"bucket_credentials_missing","message":"provider credentials are not configured","details":{"bucket_id":"bad;id","service_id":"bad","auth_type":"api_key","auth_name":"x"}}}`))
	if strings.Contains(malformed.Error(), "fused-cli") {
		t.Fatalf("malformed metadata produced command: %q", malformed.Error())
	}
	providerAppBody := fmt.Sprintf(`{"error":{"code":"bucket_credentials_missing","message":"provider credentials are not configured","details":{"bucket_id":%q,"service_id":%q,"auth_type":"oauth","auth_name":"oauth2"}}}`, bucketID, serviceID)
	providerAppErr := decodeSDKInvokeHTTPError(http.StatusConflict, []byte(providerAppBody))
	providerAppWant := "fused-cli secret set " + serviceID + " --bucket " + bucketID + " --type 'oauth' --auth-name 'oauth2' --interactive"
	// OAuth remediation configures only the application client pair through the secure family-aware prompt.
	if !strings.Contains(providerAppErr.Error(), providerAppWant) {
		t.Fatalf("OAuth application credential error = %q", providerAppErr.Error())
	}
}

// TestDecodeSDKInvokeConnectionRequiredBuildsSafeCommandForHumanAndJSON proves
// call-time OAuth failures expose one local UUID-pinned consent action.
func TestDecodeSDKInvokeConnectionRequiredBuildsSafeCommandForHumanAndJSON(t *testing.T) {
	bucketID, serviceID := uuid.NewString(), uuid.NewString()
	body := fmt.Sprintf(`{"error":{"code":"connection_required","message":"a provider connection is required","details":{"bucket_id":%q,"service_id":%q,"end_user_ref":"","command":"ignored-server-command"}}}`, bucketID, serviceID)
	err := decodeSDKInvokeHTTPError(http.StatusConflict, []byte(body))
	want := "fused-cli workspace service connect " + serviceID + " --bucket " + bucketID + " --user-ref YOUR_USER_REFERENCE"
	// Human output appends only the command reconstructed from validated local fields.
	if !strings.Contains(err.Error(), "Run: "+want) || strings.Contains(err.Error(), "ignored-server-command") {
		t.Fatalf("connection invocation error=%q", err.Error())
	}
	jsonCommand := &cobra.Command{Use: "invoke"}
	addJSONOutputFlag(jsonCommand)
	// Enabling the command's structured mode exercises the emitted envelope rather than only its classifier.
	if flagErr := jsonCommand.Flags().Set(jsonOutputFlag, "true"); flagErr != nil {
		t.Fatalf("enable JSON output: %v", flagErr)
	}
	var jsonOutput bytes.Buffer
	// Rendering must succeed before the stable recovery field can be inspected.
	if writeErr := writeCommandError(&jsonOutput, jsonCommand, err); writeErr != nil {
		t.Fatalf("write JSON error: %v", writeErr)
	}
	var envelope jsonErrorEnvelope
	// A valid envelope proves the command is available to automation without parsing prose.
	if decodeErr := json.Unmarshal(jsonOutput.Bytes(), &envelope); decodeErr != nil {
		t.Fatalf("decode JSON error: %v", decodeErr)
	}
	result := envelope.Error
	// JSON keeps the Engine message separate and publishes the command in the stable recovery field.
	if result.Code != "connection_required" || result.Message != "a provider connection is required" || result.Recovery != want || result.Remediation == "" || strings.Contains(result.Recovery, "ignored-server-command") || result.Details["command"] != nil {
		t.Fatalf("connection JSON result=%#v", result)
	}

	malformedBodies := []string{
		`{"bucket_id":"not-a-uuid","service_id":"` + serviceID + `","end_user_ref":""}`,
		`{"bucket_id":"` + bucketID + `","service_id":"not-a-uuid","end_user_ref":""}`,
		`{"bucket_id":"` + bucketID + `","service_id":"` + strings.ReplaceAll(serviceID, "-", "") + `","end_user_ref":""}`,
		`{"bucket_id":"` + bucketID + `","service_id":"` + serviceID + `"}`,
		`{"bucket_id":"` + bucketID + `","service_id":"` + serviceID + `","end_user_ref":"bad\nref"}`,
	}
	// Malformed or incomplete detail maps remain inert in both output formats.
	for _, details := range malformedBodies {
		malformed := decodeSDKInvokeHTTPError(http.StatusConflict, []byte(`{"error":{"code":"connection_required","message":"connection required","details":`+details+`}}`))
		malformedResult := classifyCommandError(&cobra.Command{Use: "invoke"}, malformed)
		// No unvalidated field may cross into a copyable command.
		if strings.Contains(malformed.Error(), "fused-cli") || malformedResult.Recovery != "" {
			t.Fatalf("malformed details=%s human=%q JSON=%#v", details, malformed.Error(), malformedResult)
		}
	}
}

// TestReadBoundedSDKInvokeResponseAllowsUnifiedAggregate verifies multiple bounded results may exceed the input cap.
func TestReadBoundedSDKInvokeResponseAllowsUnifiedAggregate(t *testing.T) {
	resultPayload := strings.Repeat("x", 600<<10)
	encoded, err := json.Marshal(map[string]any{
		"app_id": sdkInvokeTestAppID, "operation": "aggregate.run", "kind": "unified",
		"results": []any{
			map[string]any{"target": "first", "data": resultPayload},
			map[string]any{"target": "second", "data": resultPayload},
		},
		"rollbacks": []any{},
	})
	if err != nil || len(encoded) <= maxSDKInvokeInputBytes {
		t.Fatalf("aggregate fixture bytes = %d, marshal error = %v", len(encoded), err)
	}
	bounded, err := readBoundedSDKInvokeResponse(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("read aggregate response: %v", err)
	}
	if _, err := decodeSDKInvokeHTTPResponse(bounded); err != nil {
		t.Fatalf("decode aggregate response: %v", err)
	}
}

// TestReadBoundedSDKInvokeResponseRejectsAggregateOverCap verifies the distinct response memory bound.
func TestReadBoundedSDKInvokeResponseRejectsAggregateOverCap(t *testing.T) {
	oversized := make([]byte, maxSDKInvokeResponseBytes+1)
	if _, err := readBoundedSDKInvokeResponse(bytes.NewReader(oversized)); err == nil {
		t.Fatal("expected oversized aggregate response to fail")
	}
}

// TestSDKInvokeUsesGlobalEngineRESTFlags verifies the private gRPC override is gone.
func TestSDKInvokeUsesGlobalEngineRESTFlags(t *testing.T) {
	if sdkInvokeCmd.Flags().Lookup("grpc-url") != nil {
		t.Fatal("sdk invoke must not expose --grpc-url")
	}
	for _, name := range []string{"target", "selector", "selectors", "environment", "idempotency-key"} {
		if sdkInvokeCmd.Flags().Lookup(name) == nil {
			t.Fatalf("sdk invoke flag --%s is missing", name)
		}
	}
	targetUsage := sdkInvokeCmd.Flags().Lookup("target").Usage
	if !strings.Contains(targetUsage, "required 1-16") || !strings.Contains(targetUsage, "unique") {
		t.Fatalf("--target help does not state the Unified contract: %q", targetUsage)
	}
	if got, err := validateSDKInvokeEngineURL("https://engine.example.com/base/"); err != nil || got != "https://engine.example.com/base" {
		t.Fatalf("Engine REST URL = %q, %v", got, err)
	}
}
