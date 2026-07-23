package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBucketListUsesPagedEngineGraphQL(t *testing.T) {
	var sawPath string
	var sawVariables map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		if !strings.Contains(body.Query, "bucketSummaryPage") {
			t.Fatalf("expected bucketSummaryPage query, got %s", body.Query)
		}
		sawVariables = body.Variables
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"bucketSummaryPage":{"total":2,"items":[{"id":"bucket-1","workspace_id":"ws-1","name":"prod","is_default":true,"secret_count":3,"value_count":1,"created_at":"2026-07-21T00:00:00Z","updated_at":"2026-07-21T00:00:00Z"}]}}}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"bucket", "list", "--limit", "1", "--offset", "1"})
	if sawPath != "/engine/graphql" {
		t.Fatalf("expected /engine/graphql, got %s", sawPath)
	}
	if sawVariables["limit"] != float64(1) || sawVariables["offset"] != float64(1) {
		t.Fatalf("unexpected variables: %#v", sawVariables)
	}
	if !strings.Contains(out, "prod (default)") || !strings.Contains(out, "bucket-1") || !strings.Contains(out, "NAME") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestServiceOperationsSearchUsesServerPagination(t *testing.T) {
	var sawSearchVars map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(body.Query, "GetServiceInfo"):
			w.Write([]byte(`{"data":{"service":{"id":"svc-1","name":"GitHub","slug":"github","base_url":"https://api.github.test","provider":null,"is_owner":true,"servers":[],"auth_configs":[]}}}`))
		case strings.Contains(body.Query, "searchEndpoints"):
			sawSearchVars = body.Variables
			w.Write([]byte(`{"data":{"searchEndpoints":[{"id":"ep-1","name":"issuesList","path":"/issues","method":"GET","description":"","service_id":"svc-1"}]}}`))
		default:
			t.Fatalf("unexpected query: %s", body.Query)
		}
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"service", "github", "operations", "--q", "issue", "--limit", "5", "--offset", "10"})
	if sawSearchVars["limit"] != float64(5) || sawSearchVars["offset"] != float64(10) || sawSearchVars["q"] != "issue" {
		t.Fatalf("unexpected search variables: %#v", sawSearchVars)
	}
	if !strings.Contains(out, "issuesList") || !strings.Contains(out, "/issues") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestSDKListUsesRegistryPagination(t *testing.T) {
	var sawVariables map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		if !strings.Contains(body.Query, "sdks") {
			t.Fatalf("expected sdks query, got %s", body.Query)
		}
		sawVariables = body.Variables
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"sdks":{"total":1,"items":[{"id":"sdk-1","name":"security","description":"","version":"1.0.0","target_type":"sdk","target_language":"typescript","sandbox_url":"","created_at":"2026-07-21T00:00:00Z","killed_at":""}]}}}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"sdk", "list", "--limit", "10", "--offset", "20", "--target", "sdk", "--language", "typescript"})
	if sawVariables["limit"] != float64(10) || sawVariables["offset"] != float64(20) || sawVariables["targetLanguage"] != "typescript" {
		t.Fatalf("unexpected variables: %#v", sawVariables)
	}
	if !strings.Contains(out, "security") || !strings.Contains(out, "typescript") {
		t.Fatalf("unexpected output: %q", out)
	}
}
