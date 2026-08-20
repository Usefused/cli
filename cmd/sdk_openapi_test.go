package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const sdkOpenAPITestVersionID = "8c664473-3318-45fe-993b-81251034d625"

type sdkOpenAPICommandTestHandler struct {
	t *testing.T
}

// ServeHTTP returns exact app resolution and an app-bound OpenAPI fixture for the command test.
func (handler sdkOpenAPICommandTestHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/engine/graphql":
		if request.Header.Get("X-API-Key") != "fsk_control" {
			handler.t.Fatalf("resolution credential = %q", request.Header.Get("X-API-Key"))
		}
		_, _ = response.Write([]byte(`{"data":{"appReference":{"id":"` + sdkOpenAPITestVersionID + `","kind":"app"}}}`))
	case "/apps/" + sdkOpenAPITestVersionID + "/openapi":
		if request.Header.Get("X-API-Key") != "fsk_control" || request.URL.Query().Get("operation") != "issues.create/exact" {
			handler.t.Fatalf("export auth/query = %q/%q", request.Header.Get("X-API-Key"), request.URL.RawQuery)
		}
		_, _ = response.Write([]byte(sdkOpenAPICommandFixture(sdkOpenAPITestVersionID)))
	default:
		handler.t.Fatalf("unexpected path %s", request.URL.Path)
	}
}

// sdkOpenAPICommandFixture returns the exact-number, app-bound Engine document used by command tests.
func sdkOpenAPICommandFixture(appID string) string {
	return `{"openapi":"3.1.0","x-fused-app-id":"` + appID + `","x-fused-operation-count":1,"servers":[{"url":"/"}],"paths":{"/v1/apps/{app_id}/executions":{"post":{"parameters":[{"name":"app_id","in":"path","required":true,"schema":{"enum":["` + appID + `"]}}],"requestBody":{"content":{"application/json":{"schema":{"oneOf":[{"type":"object"}]}}}}}}},"components":{"schemas":{"Exact":{"minimum":9007199254740993,"multipleOf":1.234567890123456789e-20}}}}`
}

// resetSDKOpenAPITestState isolates command and global credential flags from the wider CLI suite.
func resetSDKOpenAPITestState(t *testing.T) {
	t.Helper()
	oldEngineURL, oldAPIKey, oldTimeout := EngineURL, APIKey, RequestTimeout
	oldOperation, oldOut, oldFormat := sdkOpenAPIOperation, sdkOpenAPIOut, sdkOpenAPIFormat
	operationFlag, outFlag, formatFlag, jsonFlag := sdkOpenAPICmd.Flags().Lookup("operation"), sdkOpenAPICmd.Flags().Lookup("out"), sdkOpenAPICmd.Flags().Lookup("format"), sdkOpenAPICmd.Flags().Lookup(jsonOutputFlag)
	t.Cleanup(func() {
		EngineURL, APIKey, RequestTimeout = oldEngineURL, oldAPIKey, oldTimeout
		sdkOpenAPIOperation, sdkOpenAPIOut, sdkOpenAPIFormat = oldOperation, oldOut, oldFormat
		operationFlag.Changed, outFlag.Changed, formatFlag.Changed, jsonFlag.Changed = false, false, false, false
		_ = sdkOpenAPICmd.Flags().Set(jsonOutputFlag, "false")
	})
	sdkOpenAPIOperation, sdkOpenAPIOut, sdkOpenAPIFormat = "", "", defaultSDKOpenAPIFormat
	operationFlag.Changed, outFlag.Changed, formatFlag.Changed, jsonFlag.Changed = false, false, false, false
	_ = sdkOpenAPICmd.Flags().Set(jsonOutputFlag, "false")
	RequestTimeout = 5 * time.Second
}

// TestSDKOpenAPIExportsDefaultYAMLAndStructuredMetadata covers resolution, filtering, server injection, and atomic output.
func TestSDKOpenAPIExportsDefaultYAMLAndStructuredMetadata(t *testing.T) {
	resetSDKOpenAPITestState(t)
	workspace := t.TempDir()
	t.Chdir(workspace)
	server := httptest.NewServer(sdkOpenAPICommandTestHandler{t: t})
	defer server.Close()
	stdout := configureSDKOpenAPICommandTest(t, server.URL)
	if err := runSDKOpenAPI(sdkOpenAPICmd, sdkDownloadTarget{Name: "jira sdk", Version: "1.2.0"}); err != nil {
		t.Fatalf("run SDK OpenAPI: %v", err)
	}
	wantPath := "jira-sdk-1.2.0.openapi.yaml"
	data, output := readSDKOpenAPICommandOutput(t, wantPath, stdout.Bytes())
	assertSDKOpenAPIYAMLExactNumbers(t, data, server.URL)
	assertSDKOpenAPIMetadata(t, output, wantPath, server.URL, data)
}

// configureSDKOpenAPICommandTest selects exact filtering and metadata output against the test Engine.
func configureSDKOpenAPICommandTest(t *testing.T, serverURL string) *bytes.Buffer {
	t.Helper()
	EngineURL, APIKey = serverURL, "fsk_control"
	if err := sdkOpenAPICmd.Flags().Set("operation", "issues.create/exact"); err != nil {
		t.Fatalf("set operation: %v", err)
	}
	if err := sdkOpenAPICmd.Flags().Set(jsonOutputFlag, "true"); err != nil {
		t.Fatalf("set JSON output: %v", err)
	}
	stdout := &bytes.Buffer{}
	sdkOpenAPICmd.SetOut(stdout)
	return stdout
}

// readSDKOpenAPICommandOutput loads the written document and decodes metadata from stdout.
func readSDKOpenAPICommandOutput(t *testing.T, path string, stdout []byte) ([]byte, sdkOpenAPIOutput) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read OpenAPI output: %v", err)
	}
	var output sdkOpenAPIOutput
	if err := json.Unmarshal(stdout, &output); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	return data, output
}

// assertSDKOpenAPIMetadata verifies identity, count, path, size, server, and final rendered-byte hash.
func assertSDKOpenAPIMetadata(t *testing.T, output sdkOpenAPIOutput, path, serverURL string, data []byte) {
	t.Helper()
	if output.VersionID != sdkOpenAPITestVersionID || output.Path != path || output.Format != "yaml" || output.Operation != "issues.create/exact" || output.Operations != 1 || output.Bytes != len(data) || output.ServerURL != serverURL {
		t.Fatalf("metadata = %#v", output)
	}
	assertSDKOpenAPIHash(t, output.SHA256, data)
}

// assertSDKOpenAPIHash verifies the canonical label matches the exact final file bytes.
func assertSDKOpenAPIHash(t *testing.T, actual string, data []byte) {
	t.Helper()
	if actual != sdkOpenAPIDocumentHash(data) || !strings.HasPrefix(actual, "sha256:") || len(actual) != 71 {
		t.Fatalf("metadata hash = %q", actual)
	}
}

// assertSDKOpenAPIYAMLExactNumbers verifies YAML retains exact integer/decimal spelling and the configured server.
func assertSDKOpenAPIYAMLExactNumbers(t *testing.T, data []byte, serverURL string) {
	t.Helper()
	text := string(data)
	for _, expected := range []string{"minimum: 9007199254740993", "multipleOf: 1.234567890123456789e-20", "url: " + serverURL} {
		if !strings.Contains(text, expected) {
			t.Fatalf("YAML missing %q:\n%s", expected, text)
		}
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode YAML: %v", err)
	}
}

// TestRenderSDKOpenAPIDocumentJSONPreservesNumber verifies JSON rendering also avoids float64 conversion.
func TestRenderSDKOpenAPIDocumentJSONPreservesNumber(t *testing.T) {
	payload := []byte(sdkOpenAPICommandFixture(sdkOpenAPITestVersionID))
	encoded, count, err := renderSDKOpenAPIDocument(payload, "https://engine.example", sdkOpenAPITestVersionID, "json")
	if err != nil || !bytes.Contains(encoded, []byte("9007199254740993")) || !bytes.Contains(encoded, []byte(`"url": "https://engine.example"`)) {
		t.Fatalf("JSON output = %s, %v", encoded, err)
	}
	if count != 1 {
		t.Fatalf("operation count = %d", count)
	}
}

// TestRenderSDKOpenAPIDocumentRejectsIdentityAndContractMismatch prevents writing another app's schema.
func TestRenderSDKOpenAPIDocumentRejectsIdentityAndContractMismatch(t *testing.T) {
	for _, payload := range []string{
		sdkOpenAPICommandFixture("7d563806-a193-4e57-88d6-ffac941bcd20"),
		strings.Replace(sdkOpenAPICommandFixture(sdkOpenAPITestVersionID), `"openapi":"3.1.0"`, `"openapi":"3.0.3"`, 1),
		strings.Replace(sdkOpenAPICommandFixture(sdkOpenAPITestVersionID), `"openapi":"3.1.0"`, `"openapi":"3.1.custom"`, 1),
		strings.Replace(sdkOpenAPICommandFixture(sdkOpenAPITestVersionID), "/v1/apps/{app_id}/executions", "/synthetic", 1),
		strings.Replace(sdkOpenAPICommandFixture(sdkOpenAPITestVersionID), `"x-fused-operation-count":1`, `"x-fused-operation-count":0`, 1),
		strings.Replace(sdkOpenAPICommandFixture(sdkOpenAPITestVersionID), `"x-fused-operation-count":1`, `"x-fused-operation-count":2`, 1),
		strings.Replace(sdkOpenAPICommandFixture(sdkOpenAPITestVersionID), `"enum":["`+sdkOpenAPITestVersionID+`"]`, `"enum":["7d563806-a193-4e57-88d6-ffac941bcd20"]`, 1),
	} {
		if _, _, err := renderSDKOpenAPIDocument([]byte(payload), "https://engine.example", sdkOpenAPITestVersionID, "yaml"); err == nil {
			t.Fatalf("expected mismatched document to fail: %s", payload)
		}
	}
}

// TestSDKOpenAPIUsesExplicitOutputAndUUIDFallback verifies format-specific paths and safe defaults.
func TestSDKOpenAPIUsesExplicitOutputAndUUIDFallback(t *testing.T) {
	if got := sdkOpenAPIOutputPath("custom/spec.yaml", sdkDownloadTarget{}, sdkOpenAPITestVersionID, "json"); got != "custom/spec.yaml" {
		t.Fatalf("explicit output = %q", got)
	}
	if got := sdkOpenAPIOutputPath("", sdkDownloadTarget{Name: sdkOpenAPITestVersionID}, sdkOpenAPITestVersionID, "json"); got != sdkOpenAPITestVersionID+".openapi.json" {
		t.Fatalf("UUID fallback = %q", got)
	}
	long := strings.Repeat("jira/unsafe ", 30)
	path := sdkOpenAPIOutputPath("", sdkDownloadTarget{Name: long, Version: "1/2"}, sdkOpenAPITestVersionID, "yaml")
	if strings.ContainsAny(filepath.Base(path), "/\\ ") || len(filepath.Base(path)) > 255 {
		t.Fatalf("unsafe default path = %q", path)
	}
}

// TestSDKOpenAPIRejectsInvalidLocalOptionsBeforeRequest verifies exact filtering and format admission.
func TestSDKOpenAPIRejectsInvalidLocalOptionsBeforeRequest(t *testing.T) {
	resetSDKOpenAPITestState(t)
	for _, test := range []struct {
		name      string
		operation string
		format    string
	}{
		{name: "trimmed operation", operation: " issues.list ", format: "yaml"},
		{name: "empty operation", operation: "", format: "yaml"},
		{name: "format", operation: "issues.list", format: "toml"},
	} {
		t.Run(test.name, func(t *testing.T) {
			sdkOpenAPIOperation, sdkOpenAPIFormat = test.operation, test.format
			sdkOpenAPICmd.Flags().Lookup("operation").Changed = true
			if _, err := readSDKOpenAPIOptions(sdkOpenAPICmd); err == nil {
				t.Fatal("expected local options to fail")
			}
		})
	}
}

// TestSDKOpenAPIFailedRenderLeavesExistingOutputUntouched verifies write preparation precedes atomic replacement.
func TestSDKOpenAPIFailedRenderLeavesExistingOutputUntouched(t *testing.T) {
	resetSDKOpenAPITestState(t)
	workspace := t.TempDir()
	path := filepath.Join(workspace, "existing.yaml")
	if err := os.WriteFile(path, []byte("original\n"), 0o640); err != nil {
		t.Fatalf("write existing output: %v", err)
	}
	sdkOpenAPIOut = path
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/engine/graphql" {
			_, _ = response.Write([]byte(`{"data":{"appReference":{"id":"` + sdkOpenAPITestVersionID + `","kind":"app"}}}`))
			return
		}
		_, _ = response.Write([]byte(`{"openapi":"3.1.0","paths":{}} trailing`))
	}))
	defer server.Close()
	EngineURL, APIKey = server.URL, "fsk_control"
	if err := runSDKOpenAPI(sdkOpenAPICmd, sdkDownloadTarget{Name: "jira", Version: "1.0.0"}); err == nil {
		t.Fatal("expected malformed export to fail")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original\n" {
		t.Fatalf("existing output = %q, %v", data, err)
	}
}

// TestSDKOpenAPICommandHelpDocumentsFileAndMetadataControls protects the public CLI surface.
func TestSDKOpenAPICommandHelpDocumentsFileAndMetadataControls(t *testing.T) {
	if sdkOpenAPICmd.Use != "openapi <sdk-name@version-or-version-id>" {
		t.Fatalf("Use = %q", sdkOpenAPICmd.Use)
	}
	for _, name := range []string{"operation", "out", "format", jsonOutputFlag} {
		if sdkOpenAPICmd.Flags().Lookup(name) == nil {
			t.Fatalf("missing --%s", name)
		}
	}
	if shorthand := sdkOpenAPICmd.Flags().ShorthandLookup("o"); shorthand == nil || shorthand.Name != "out" {
		t.Fatal("missing -o output shorthand")
	}
	if got := sdkOpenAPICmd.Flags().Lookup("format").DefValue; got != "yaml" {
		t.Fatalf("default format = %q", got)
	}
	if usage := sdkOpenAPICmd.Flags().Lookup(jsonOutputFlag).Usage; !strings.Contains(usage, "metadata") {
		t.Fatalf("--json usage = %q", usage)
	}
}

// Example output shape remains intentionally metadata-only when --json is selected.
func Example_sdkOpenAPIOutput() {
	encoded, _ := json.Marshal(sdkOpenAPIOutput{VersionID: sdkOpenAPITestVersionID, Operations: 1, Format: "yaml", Path: "jira-1.0.0.openapi.yaml", Bytes: 42, SHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ServerURL: "https://engine.example", Status: "completed"})
	fmt.Println(string(encoded))
	// Output: {"version_id":"8c664473-3318-45fe-993b-81251034d625","operation_count":1,"format":"yaml","path":"jira-1.0.0.openapi.yaml","bytes":42,"sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","server_url":"https://engine.example","status":"completed"}
}
