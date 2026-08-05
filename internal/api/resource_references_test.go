package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveResourceReferencesUsesExactEngineFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Variables["reference"] != "company" {
			t.Fatalf("reference = %#v", request.Variables["reference"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"bucketReference":{"id":"bucket-1","kind":"bucket"}}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	id, err := client.ResolveBucketReference("company")
	if err != nil || id != "bucket-1" {
		t.Fatalf("ResolveBucketReference = %q, %v", id, err)
	}
}

func TestResolveSDKAndMCPReferencesUseSeparateNamespaces(t *testing.T) {
	var kinds []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		kinds = append(kinds, request.Variables["kind"].(string))
		_, _ = w.Write([]byte(`{"data":{"artifactReference":{"id":"resource-1","kind":"artifact"}}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	if id, err := client.ResolveSDKReference("support"); err != nil || id != "resource-1" {
		t.Fatalf("ResolveSDKReference = %q, %v", id, err)
	}
	if id, err := client.ResolveMCPReference("support"); err != nil || id != "resource-1" {
		t.Fatalf("ResolveMCPReference = %q, %v", id, err)
	}
	if len(kinds) != 2 || kinds[0] != "sdk" || kinds[1] != "mcp" {
		t.Fatalf("reference namespaces = %#v", kinds)
	}
}

func TestListArtifactsUsesEngineGraphQL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !strings.Contains(request.Query, "artifactSnapshots") || request.Variables["kind"] != "mcp" {
			t.Fatalf("unexpected artifact request: %#v", request)
		}
		_, _ = w.Write([]byte(`{"data":{"artifactSnapshots":{"total":1,"items":[{"id":"artifact-1","name":"support","version":"1.0.0","kind":"mcp","active":true,"created_at":"now"}]}}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	page, err := client.ListArtifacts("mcp", PageOptions{Limit: 10, Offset: 2})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Kind != "mcp" {
		t.Fatalf("ListArtifacts = %#v, %v", page, err)
	}
}

func TestGetArtifactSummaryUsesHumanReference(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Variables["reference"] != "support@2.0.0" || request.Variables["kind"] != "sdk" {
			t.Fatalf("unexpected reference: %#v", request.Variables)
		}
		_, _ = w.Write([]byte(`{"data":{"artifact":{"id":"artifact-1","name":"support","version":"2.0.0","kind":"sdk","active":true,"created_at":"now"}}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	artifact, err := client.GetArtifactSummary("support@2.0.0", "sdk")
	if err != nil || artifact.ID != "artifact-1" || artifact.Kind != "sdk" {
		t.Fatalf("GetArtifactSummary = %#v, %v", artifact, err)
	}
}

func TestListArtifactServicesUsesKindScopedEngineGraphQL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Variables["reference"] != "support" || request.Variables["kind"] != "sdk" {
			t.Fatalf("unexpected SDK service reference: %#v", request.Variables)
		}
		_, _ = w.Write([]byte(`{"data":{"artifactServices":[{"service_id":"service-1","service_slug":"github","service_name":"GitHub","version":"v1","select_all":false,"endpoint_count":2,"webhook_count":1}]}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	services, err := client.ListArtifactServices("support", "sdk")
	if err != nil || len(services) != 1 || services[0].ServiceSlug != "github" || services[0].EndpointCount != 2 {
		t.Fatalf("ListArtifactServices = %#v, %v", services, err)
	}
}
