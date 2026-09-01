package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestRefreshServiceContractUsesExactEngineRoute verifies stable UUIDs and the control credential select one snapshot.
func TestRefreshServiceContractUsesExactEngineRoute(t *testing.T) {
	const serviceID = "00000000-0000-4000-8000-000000000001"
	const versionID = "00000000-0000-4000-8000-000000000002"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// The client must use the exact Engine route and never translate the UUIDs through Registry.
		if request.Method != http.MethodPost || request.URL.Path != "/workspace/services/"+serviceID+"/versions/"+versionID+"/refresh" {
			t.Fatalf("unexpected refresh request %s %s", request.Method, request.URL.Path)
		}
		// The same control credential used for planning authorizes the workspace snapshot refresh.
		if request.Header.Get("x-api-key") != "test-key" {
			t.Fatal("refresh omitted the Engine control credential")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"refreshed","service_id":"` + serviceID + `","service_version_id":"` + versionID + `","version":"v1","contract_hash":"sha256:contract"}`))
	}))
	defer server.Close()

	result, err := api.NewClient(server.URL, "test-key").RefreshServiceContract(serviceID, versionID)
	if err != nil {
		t.Fatalf("RefreshServiceContract: %v", err)
	}
	// Successful decoding must retain the immutable version and contract proof returned by Engine.
	if result.Version != "v1" || result.ContractHash != "sha256:contract" {
		t.Fatalf("unexpected refresh result %#v", result)
	}
}

// TestRefreshServiceContractPreservesTypedEngineError proves callers can branch safely on the reviewed Engine code.
func TestRefreshServiceContractPreservesTypedEngineError(t *testing.T) {
	const serviceID = "00000000-0000-4000-8000-000000000001"
	const versionID = "00000000-0000-4000-8000-000000000002"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = writer.Write([]byte(`{"error":{"code":"runtime_contract_dependency_unavailable","message":"The Engine could not fetch the runtime contract.","category":"dependency","retryable":true}}`))
	}))
	defer server.Close()

	_, err := api.NewClient(server.URL, "test-key").RefreshServiceContract(serviceID, versionID)
	var apiErr *api.APIError
	// Error wrapping must preserve the stable code instead of exposing or flattening remote response text.
	if !errors.As(err, &apiErr) || apiErr.Code != "runtime_contract_dependency_unavailable" {
		t.Fatalf("typed refresh error=%v APIError=%#v", err, apiErr)
	}
}

func TestRefreshMissingServiceContractsUsesEngineGraphQL(t *testing.T) {
	var sawPath string
	var sawLimit any
	var sawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		sawQuery = body.Query
		sawLimit = body.Variables["limit"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"refreshMissingServiceContracts":{"status":"ok","missing":2,"refreshed":2,"failed":0,"results":[]}}}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	result, err := client.RefreshMissingServiceContracts(2)

	if err != nil {
		t.Fatalf("RefreshMissingServiceContracts: %v", err)
	}
	if sawPath != "/engine/graphql" {
		t.Fatalf("expected /engine/graphql, got %s", sawPath)
	}
	if !strings.Contains(sawQuery, "refreshMissingServiceContracts") {
		t.Fatalf("expected refreshMissingServiceContracts mutation, got %s", sawQuery)
	}
	if sawLimit != float64(2) {
		t.Fatalf("expected limit variable 2, got %#v", sawLimit)
	}
	if result.Missing != 2 || result.Refreshed != 2 || result.Failed != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestRefreshMissingServiceContractsClampsLimit(t *testing.T) {
	var sawLimit any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		sawLimit = body.Variables["limit"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"refreshMissingServiceContracts":{"status":"ok","missing":0,"refreshed":0,"failed":0,"results":[]}}}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	if _, err := client.RefreshMissingServiceContracts(1000); err != nil {
		t.Fatalf("RefreshMissingServiceContracts: %v", err)
	}
	if sawLimit != float64(100) {
		t.Fatalf("expected clamped limit 100, got %#v", sawLimit)
	}
}
