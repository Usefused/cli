package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sdkOpenAPITestAppID = "7d563806-a193-4e57-88d6-ffac941bcd20"

// TestExportSDKOpenAPIUsesProtectedExactBoundedGET verifies the route, filter, credential, and media contract.
func TestExportSDKOpenAPIUsesProtectedExactBoundedGET(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/apps/"+sdkOpenAPITestAppID+"/openapi" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.URL.Query().Get("operation") != "issues.create/exact value" {
			t.Fatalf("operation query = %q", request.URL.RawQuery)
		}
		if request.Header.Get("X-API-Key") != "fsk_control" || request.Header.Get("Accept") != "application/vnd.oai.openapi+json;version=3.1.0" {
			t.Fatalf("headers = %#v", request.Header)
		}
		_, _ = response.Write([]byte(`{"openapi":"3.1.0","paths":{}}`))
	}))
	defer server.Close()
	document, err := NewClient(server.URL, "fsk_control").ExportSDKOpenAPI(sdkOpenAPITestAppID, "issues.create/exact value")
	if err != nil || string(document) != `{"openapi":"3.1.0","paths":{}}` {
		t.Fatalf("ExportSDKOpenAPI = %q, %v", document, err)
	}
}

// TestExportSDKOpenAPIRejectsOversizedResponse verifies the 16 MiB client memory boundary.
func TestExportSDKOpenAPIRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(strings.Repeat("x", maxSDKOpenAPIDocumentBytes+1)))
	}))
	defer server.Close()
	if _, err := NewClient(server.URL, "fsk_control").ExportSDKOpenAPI(sdkOpenAPITestAppID, ""); err == nil {
		t.Fatal("expected oversized OpenAPI response to fail")
	}
}

// TestExportSDKOpenAPIRejectsRedirectWithoutForwardingCredential verifies the control key stays on the configured Engine.
func TestExportSDKOpenAPIRejectsRedirectWithoutForwardingCredential(t *testing.T) {
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		redirected = true
		if request.Header.Get("X-API-Key") != "" {
			t.Fatal("control credential reached redirect target")
		}
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Location", target.URL+"/capture")
		response.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	if _, err := NewClient(origin.URL, "fsk_control").ExportSDKOpenAPI(sdkOpenAPITestAppID, ""); err == nil {
		t.Fatal("expected redirect to fail")
	}
	if redirected {
		t.Fatal("OpenAPI client followed redirect")
	}
}
