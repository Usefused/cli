package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

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
