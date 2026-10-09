package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const mcpTestService = "00000000-0000-4000-8000-000000000001"
const mcpTestVersion = "00000000-0000-4000-8000-000000000002"
const mcpTestDraft = "00000000-0000-4000-8000-000000000003"

// TestMCPCatalogReviewTransport proves discovery and approval use distinct exact Engine routes without forwarding credentials to a provider.
func TestMCPCatalogReviewTransport(t *testing.T) {
	calls := []string{}
	// The fixture validates public HTTP payloads instead of mirroring client implementation details.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		// Both path UUIDs and the Engine control credential are required for every action.
		if !strings.HasPrefix(r.URL.Path, "/workspace/services/"+mcpTestService+"/versions/"+mcpTestVersion+"/mcp-catalog") || r.Header.Get("x-api-key") != "engine-test-key" {
			t.Error("wrong scope or credential")
		}
		w.Header().Set("Content-Type", "application/json")
		// Only draft identity may cross the promotion boundary; provider URL belongs to discovery alone.
		switch {
		case strings.HasSuffix(r.URL.Path, "/apply"):
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body) != 1 || body["draft_id"] != mcpTestDraft {
				t.Errorf("unexpected approval: %v", body)
			}
			fmt.Fprintf(w, `{"id":%q,"catalog":{"tools":[]}}`, mcpTestDraft)
		case strings.HasSuffix(r.URL.Path, "/discover"):
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["secret_name"] != "provider-key" || body["bucket_name"] != "default" || len(body) != 3 {
				t.Errorf("unexpected discovery: %v", body)
			}
			fmt.Fprintf(w, `{"id":%q,"expires_at":"later","catalog":{"tools":[{"name":"echo","inputSchema":{"type":"object","$defs":{"x":{"type":"string"}}}}]}}`, mcpTestDraft)
		default:
			fmt.Fprint(w, `{"catalog":null}`)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "engine-test-key")
	saved, err := client.GetMCPCatalog(mcpTestService, mcpTestVersion)
	// An absent saved catalog remains distinguishable from an unavailable Engine.
	if err != nil || saved != nil {
		t.Fatalf("empty catalog=%v error=%v", saved, err)
	}
	draft, err := client.DiscoverMCPCatalog(mcpTestService, mcpTestVersion, MCPDiscoveryInput{URL: "https://example.com/mcp", BucketName: "default", SecretName: "provider-key"})
	// Exact JSON Schema definitions survive discovery review.
	if err != nil || !strings.Contains(string(draft.Catalog.Tools[0]), "$defs") {
		t.Fatalf("preview=%v error=%v", draft, err)
	}
	_, err = client.ApplyMCPCatalog(mcpTestService, mcpTestVersion, draft.ID)
	// No hidden discovery or automatic promotion may occur beyond the three explicit calls.
	if err != nil || len(calls) != 3 {
		t.Fatalf("calls=%v err=%v", calls, err)
	}
	_, err = client.ApplyMCPCatalog(mcpTestService, mcpTestVersion, "../other")
	// Invalid identities never reach the authenticated transport.
	if err == nil || len(calls) != 3 {
		t.Fatal("invalid draft reached Engine")
	}
}

// TestMCPCatalogPreservesConflict keeps stale reviews actionable without retrying their mutation.
func TestMCPCatalogPreservesConflict(t *testing.T) {
	calls := 0
	// The fixture simulates Engine's actor/revision conflict response.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(409)
		fmt.Fprint(w, `{"error":{"code":"mcp_catalog_preview_stale","message":"Discover again"}}`)
	}))
	defer server.Close()
	_, err := NewClient(server.URL, "test").ApplyMCPCatalog(mcpTestService, mcpTestVersion, mcpTestDraft)
	var failure *APIError
	// Callers need the stable conflict code; a retry could approve stale intent.
	if !errors.As(err, &failure) || failure.Code != "mcp_catalog_preview_stale" || calls != 1 {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
}
