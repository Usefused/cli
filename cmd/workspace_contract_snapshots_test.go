package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunWorkspaceRefreshMissingContractsPrintsSummary(t *testing.T) {
	origEngineURL, origAPIKey, origLimit := EngineURL, APIKey, workspaceRefreshMissingContractsLimit
	defer func() {
		EngineURL, APIKey, workspaceRefreshMissingContractsLimit = origEngineURL, origAPIKey, origLimit
	}()

	var sawPath string
	var sawAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawAPIKey = r.Header.Get("x-api-key")
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		if !strings.Contains(body.Query, "refreshMissingServiceContracts") {
			t.Fatalf("expected refreshMissingServiceContracts mutation, got %s", body.Query)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"refreshMissingServiceContracts":{"status":"ok","missing":3,"refreshed":2,"failed":1,"results":[]}}}`))
	}))
	defer srv.Close()

	EngineURL = srv.URL
	APIKey = "fsk_test"
	workspaceRefreshMissingContractsLimit = 3
	cmd := &cobra.Command{}
	var out strings.Builder
	cmd.SetOut(&out)

	if err := runWorkspaceRefreshMissingContracts(cmd); err != nil {
		t.Fatalf("runWorkspaceRefreshMissingContracts() error = %v", err)
	}
	if sawPath != "/engine/graphql" {
		t.Fatalf("expected /engine/graphql, got %s", sawPath)
	}
	if sawAPIKey != "fsk_test" {
		t.Fatalf("expected API key header, got %q", sawAPIKey)
	}
	if !strings.Contains(out.String(), "Refreshed 2 of 3 missing runtime contracts (1 failed).") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}
