package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestAttachSDKBundleUsesExactControlRoute proves auth and immutable identities reach one bounded management POST.
func TestAttachSDKBundleUsesExactControlRoute(t *testing.T) {
	appID := uuid.NewString()
	sourceHash := "sha256:" + strings.Repeat("a", 64)
	client := &Client{BaseURL: "https://engine.test", APIKey: "saved-control-key"}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		assertSDKBundleAttachRequest(t, request, appID, sourceHash)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"status":"attached","app_id":"` + appID + `"}`))}, nil
	})}
	result, err := client.AttachSDKBundle(appID, sourceHash, []byte("compiled bytes"), json.RawMessage(`{"schemaVersion":1}`))
	// A metadata-only receipt proves the mutation was accepted without echoing authored code.
	if err != nil {
		t.Fatalf("attach bundle: %v", err)
	}
	// The successful response cannot substitute a different App ID.
	if result.Status != "attached" || result.AppID != appID {
		t.Fatalf("attachment receipt = %#v, error = %v", result, err)
	}
}

// assertSDKBundleAttachRequest checks the control credential and exact artifact bytes at the transport boundary.
func assertSDKBundleAttachRequest(t *testing.T, request *http.Request, appID, sourceHash string) {
	t.Helper()
	// The bundle is management input, so it must use the saved CLI key and exact version path.
	if request.Method != http.MethodPost || request.URL.Path != "/apps/"+appID+"/bundle" || request.Header.Get("x-api-key") != "saved-control-key" {
		t.Errorf("wrong request route or credential: %s %s", request.Method, request.URL.Path)
	}
	var payload struct {
		SourceHash string          `json:"source_hash"`
		BundleJS   string          `json:"bundle_js"`
		Manifest   json.RawMessage `json:"manifest"`
	}
	// The transport must preserve the exact compiled bytes and declaration without provider secrets.
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.SourceHash != sourceHash || payload.BundleJS != "compiled bytes" || string(payload.Manifest) != `{"schemaVersion":1}` {
		t.Errorf("unexpected attachment payload: %#v, error = %v", payload, err)
	}
}

// TestAttachSDKBundleRejectsInvalidLocalInput proves malformed material never reaches the Engine.
func TestAttachSDKBundleRejectsInvalidLocalInput(t *testing.T) {
	calls := 0
	client := &Client{BaseURL: "https://engine.test", APIKey: "saved-control-key"}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("failure"))}, nil
	})}
	appID := uuid.NewString()
	validHash := "sha256:" + strings.Repeat("a", 64)
	for _, testCase := range []struct {
		name, appID, hash string
		script, manifest  []byte
	}{
		{name: "family selector", appID: "latest", hash: validHash, script: []byte("code"), manifest: []byte(`{}`)},
		{name: "source label", appID: appID, hash: "sha256:source", script: []byte("code"), manifest: []byte(`{}`)},
		{name: "empty code", appID: appID, hash: validHash, manifest: []byte(`{}`)},
		{name: "oversized code", appID: appID, hash: validHash, script: make([]byte, maxSDKBundleBytes+1), manifest: []byte(`{}`)},
		{name: "manifest array", appID: appID, hash: validHash, script: []byte("code"), manifest: []byte(`[]`)},
	} {
		// Rejected input cannot consume management permission or create a partial artifact.
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := client.AttachSDKBundle(testCase.appID, testCase.hash, testCase.script, testCase.manifest); err == nil {
				t.Fatal("invalid bundle attachment was accepted")
			}
		})
	}
	// The local validator must stop all cases before a control request is emitted.
	if calls != 0 {
		t.Fatalf("invalid attachments made %d Engine calls", calls)
	}
}
