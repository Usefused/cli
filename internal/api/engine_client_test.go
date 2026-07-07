package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestActivateMCPServer_PostsToEngine verifies that calling ActivateMCPServer
// sends a POST to the Engine's activate endpoint and sets the correct headers.
func TestActivateMCPServer_PostsToEngine(t *testing.T) {
	const testSDKID = "sdk-123"
	const expectedMCPURL = "http://engine/mcp/stream"

	var reqMethod, reqPath, authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqMethod = r.Method
		reqPath = r.URL.Path
		authHeader = r.Header.Get("x-api-key")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(api.MCPActivateResult{
			MCPURL: expectedMCPURL,
		})
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	res, err := client.ActivateMCPServer(testSDKID)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reqMethod != "POST" {
		t.Errorf("expected POST, got %s", reqMethod)
	}
	if reqPath != "/engine/sdks/"+testSDKID+"/activate" {
		t.Errorf("expected path /engine/sdks/%s/activate, got %s", testSDKID, reqPath)
	}
	if authHeader != "test-key" {
		t.Errorf("expected auth header test-key, got %s", authHeader)
	}
	if res.MCPURL != expectedMCPURL {
		t.Errorf("expected MCP URL %q, got %q", expectedMCPURL, res.MCPURL)
	}
}

// TestActivateMCPServer_HandlesError verifies that non-200 responses return an error.
func TestActivateMCPServer_HandlesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "engine failed"}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	_, err := client.ActivateMCPServer("sdk-123")

	if err == nil {
		t.Fatal("expected error on 500 response, got nil")
	}
	if !strings.Contains(err.Error(), "engine failed") && !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to contain status or body, got: %v", err)
	}
}
