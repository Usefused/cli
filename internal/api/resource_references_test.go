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

func TestResolveAppAndFamilyReferencesUseSeparateNamespaces(t *testing.T) {
	var requests []struct {
		query     string
		variables map[string]interface{}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, struct {
			query     string
			variables map[string]interface{}
		}{request.Query, request.Variables})
		if strings.Contains(request.Query, "appFamilyReference") {
			_, _ = w.Write([]byte(`{"data":{"appFamilyReference":{"id":"family-1","kind":"app"}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"appReference":{"id":"app-1","kind":"app"}}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	if id, err := client.ResolveSDKAppReference("support", "1.2.0"); err != nil || id != "app-1" {
		t.Fatalf("ResolveSDKAppReference = %q, %v", id, err)
	}
	if id, err := client.ResolveMCPFamilyReference("support"); err != nil || id != "family-1" {
		t.Fatalf("ResolveMCPFamilyReference = %q, %v", id, err)
	}
	if id, err := client.ResolveMCPAppReference("b531e354-126b-458f-920a-2d5aa987bbc3", ""); err != nil || id != "app-1" {
		t.Fatalf("ResolveMCPAppReference UUID = %q, %v", id, err)
	}
	if len(requests) != 3 || requests[0].variables["kind"] != "sdk" || requests[0].variables["version"] != "1.2.0" {
		t.Fatalf("exact app reference request = %#v", requests)
	}
	if requests[1].variables["kind"] != "mcp" || !strings.Contains(requests[1].query, "appFamilyReference") {
		t.Fatalf("family reference request = %#v", requests[1])
	}
	if requests[2].variables["kind"] != "mcp" || requests[2].variables["version"] != "" {
		t.Fatalf("UUID app reference request = %#v", requests[2])
	}
}

func TestListAppsUsesEngineGraphQL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query     string                 `json:"query"`
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if !strings.Contains(request.Query, "apps(kind: $kind") || request.Variables["kind"] != "mcp" {
			t.Fatalf("unexpected app request: %#v", request)
		}
		_, _ = w.Write([]byte(`{"data":{"apps":{"total":1,"items":[{"app_family_id":"family-1","app_id":"app-1","name":"support","version":"1.0.0","kind":"mcp","status":"active","created_at":"now","selections":[]}]}}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	page, err := client.ListApps("mcp", PageOptions{Limit: 10, Offset: 2})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Kind != "mcp" {
		t.Fatalf("ListApps = %#v, %v", page, err)
	}
}

func TestGetAppUsesExactAppID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Variables["appId"] != "app-1" {
			t.Fatalf("unexpected app ID: %#v", request.Variables)
		}
		_, _ = w.Write([]byte(`{"data":{"app":{"app_family_id":"family-1","app_id":"app-1","name":"support","version":"2.0.0","kind":"sdk","status":"active","created_at":"now","selections":[]}}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	app, err := client.GetApp("app-1")
	if err != nil || app.AppID != "app-1" || app.Kind != "sdk" {
		t.Fatalf("GetApp = %#v, %v", app, err)
	}
}

func TestListAppServicesUsesExactAppID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Variables map[string]interface{} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Variables["appId"] != "app-1" {
			t.Fatalf("unexpected SDK app ID: %#v", request.Variables)
		}
		_, _ = w.Write([]byte(`{"data":{"appServices":[{"service_id":"service-1","service_slug":"github","service_name":"GitHub","version":"v1","select_all":false,"endpoint_count":2,"webhook_count":1}]}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	services, err := client.ListAppServices("app-1")
	if err != nil || len(services) != 1 || services[0].ServiceSlug != "github" || services[0].EndpointCount != 2 {
		t.Fatalf("ListAppServices = %#v, %v", services, err)
	}
}
