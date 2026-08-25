package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestServiceLookupNameSharesQualifiedReferenceParsing prevents command helpers
// from recreating a second provider/service grammar.
func TestServiceLookupNameSharesQualifiedReferenceParsing(t *testing.T) {
	tests := map[string]string{
		"square":            "square",
		" @acme/square ":    "square",
		"@acme/square/v2":   "square/v2",
		"@malformed-handle": "@malformed-handle",
	}
	// Every input must retain the API client's established split semantics.
	for input, want := range tests {
		if got := ServiceLookupName(input); got != want {
			t.Fatalf("ServiceLookupName(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestReadBoundedHTTPErrorBody caps hostile response bodies before structured error parsing.
func TestReadBoundedHTTPErrorBody(t *testing.T) {
	payload := readBoundedHTTPErrorBody(strings.NewReader(strings.Repeat("x", int(maxCLIHTTPErrorBytes)+32)))
	if len(payload) != int(maxCLIHTTPErrorBytes) {
		t.Fatalf("bounded error body length = %d", len(payload))
	}
}

// TestAddWorkspaceServiceUsesScopedEngineBoundary verifies the CLI client sends
// exact additive identity/version fields and the configured control credential.
func TestAddWorkspaceServiceUsesScopedEngineBoundary(t *testing.T) {
	var received AddWorkspaceServiceRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/workspace/services" {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("x-api-key") != "test-key" {
			t.Fatalf("missing control credential header")
		}
		if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	err := client.AddWorkspaceService(AddWorkspaceServiceRequest{
		ServiceID: "00000000-0000-4000-8000-000000000001", ServiceName: "linear", VersionTag: "2026-08-07",
	})
	if err != nil {
		t.Fatal(err)
	}
	if received.ServiceID != "00000000-0000-4000-8000-000000000001" || received.ServiceName != "linear" || received.VersionTag != "2026-08-07" {
		t.Fatalf("unexpected scoped activation payload: %#v", received)
	}
}

// TestSearchServicesBatchUsesOneGraphQLRequest protects multi-add discovery
// from regressing into one Registry query per requested service.
func TestSearchServicesBatchUsesOneGraphQLRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		refs, _ := body.Variables["refs"].([]any)
		if len(refs) != 2 || refs[0] != "linear" || refs[1] != "square" {
			t.Fatalf("unexpected batched variables: %#v", body.Variables)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":{"serviceCandidatesByRefs":[{"ref":"linear","candidates":[{"id":"linear-id","name":"Linear","slug":"linear"}]},{"ref":"square","candidates":[{"id":"square-id","name":"Square","slug":"square"}]}]}}`))
	}))
	defer server.Close()

	results, err := NewClient(server.URL, "test-key").SearchServicesBatch([]string{"linear", "square"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(results["linear"]) != 1 || results["square"][0].ID != "square-id" {
		t.Fatalf("unexpected batched result calls=%d results=%#v", calls, results)
	}
}

// TestSearchServicesBatchUsesProviderAwareFieldForOneQualifiedRef prevents the
// singular CLI path from sending @provider/slug through lexical catalogue search.
func TestSearchServicesBatchUsesProviderAwareFieldForOneQualifiedRef(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount++
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		// Exact qualified identity resolution must use the set-based field even
		// when the composite contains only one reference.
		if !bytes.Contains(body, []byte("serviceCandidatesByRefs")) {
			t.Fatalf("qualified request did not use provider-aware field: %s", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":{"serviceCandidatesByRefs":[{"ref":"@acme/square","candidates":[{"id":"square-id","name":"Square","slug":"square","provider":{"handle":"acme"},"is_public":true}] }]}}`))
	}))
	defer server.Close()

	results, err := NewClient(server.URL, "test-key").SearchServicesBatch([]string{"@acme/square"})
	if err != nil {
		t.Fatalf("qualified batch search: %v", err)
	}
	if requestCount != 1 || len(results["@acme/square"]) != 1 {
		t.Fatalf("qualified result = %#v after %d requests", results, requestCount)
	}
}

// TestSearchServicesBatchUsesSetResolverForOneBareRef keeps singular and multi
// add semantics on the same Registry ranking and provider-identity policy.
func TestSearchServicesBatchUsesSetResolverForOneBareRef(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if !bytes.Contains(body, []byte("serviceCandidatesByRefs")) || bytes.Contains(body, []byte("query SearchServices(")) {
			t.Fatalf("bare singular request used divergent lookup policy: %s", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":{"serviceCandidatesByRefs":[{"ref":"square","candidates":[{"id":"square-id","name":"Square","slug":"square","is_owner":true}]}]}}`))
	}))
	defer server.Close()

	results, err := NewClient(server.URL, "test-key").SearchServicesBatch([]string{"square"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results["square"]) != 1 || results["square"][0].ID != "square-id" {
		t.Fatalf("unexpected singular result: %#v", results)
	}
}
