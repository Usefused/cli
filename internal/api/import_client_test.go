package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestPlanSpecImport_PostsToImportPlanEndpoint verifies PlanSpecImport sends
// a POST with the expected body/headers to /integrations/import/plan and
// decodes the plan response, including a populated usage block.
func TestPlanSpecImport_PostsToImportPlanEndpoint(t *testing.T) {
	var reqMethod, reqPath, authHeader string
	var decoded api.SpecImportPlanRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqMethod = r.Method
		reqPath = r.URL.Path
		authHeader = r.Header.Get("x-api-key")
		json.NewDecoder(r.Body).Decode(&decoded)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(api.SpecImportPlanResponse{
			PlanID:           "plan-1",
			SourceHash:       "hash-1",
			ServiceID:        "svc-1",
			Slug:             "widgets",
			Name:             "Widgets",
			IsNewService:     false,
			ResolvedStrategy: "modify_existing",
			Diff: api.SpecImportDiff{
				Added: 1, Changed: 2, Removed: 0,
				ChangedNames: []string{"listWidgets"},
			},
			Usage: &api.SpecImportUsage{
				SDKs:       []api.SpecImportSDKUsage{{ID: "sdk-1", Name: "widgets-sdk", UsesChangedEndpoint: true}},
				Workspaces: []api.SpecImportWorkspaceUsage{{ID: "ws-1", Name: "prod"}},
			},
		})
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	resp, err := client.PlanSpecImport(api.SpecImportPlanRequest{
		Name:          "Widgets",
		Slug:          "widgets",
		SourceContent: `{"openapi":"3.0.0"}`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reqMethod != "POST" {
		t.Errorf("expected POST, got %s", reqMethod)
	}
	if reqPath != "/integrations/import/plan" {
		t.Errorf("expected /integrations/import/plan, got %s", reqPath)
	}
	if authHeader != "test-key" {
		t.Errorf("expected auth header test-key, got %s", authHeader)
	}
	if decoded.Name != "Widgets" || decoded.Slug != "widgets" {
		t.Errorf("expected name/slug to reach the server, got %+v", decoded)
	}
	if resp.PlanID != "plan-1" || resp.ResolvedStrategy != "modify_existing" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if resp.Usage == nil || len(resp.Usage.SDKs) != 1 || !resp.Usage.SDKs[0].UsesChangedEndpoint {
		t.Errorf("expected usage block to decode, got %+v", resp.Usage)
	}
}

// TestPlanSpecImport_HandlesError verifies non-2xx responses surface as an
// error carrying the server's message.
func TestPlanSpecImport_HandlesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "invalid spec"}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	_, err := client.PlanSpecImport(api.SpecImportPlanRequest{Name: "Widgets", SourceContent: "not a spec"})
	if err == nil {
		t.Fatal("expected an error on 400 response")
	}
	if !strings.Contains(err.Error(), "invalid spec") {
		t.Errorf("expected error to carry server message, got: %v", err)
	}
}

// TestApplySpecImport_PostsToImportApplyEndpoint verifies ApplySpecImport
// sends plan_id/source_hash to /integrations/import/apply.
func TestApplySpecImport_PostsToImportApplyEndpoint(t *testing.T) {
	var reqPath string
	var decoded api.SpecImportApplyRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&decoded)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(api.SpecImportApplyResponse{
			Status: "applied", PlanID: "plan-1", ServiceID: "svc-1",
			IsNewService: false, ResolvedStrategy: "modify_existing", Version: "2026-07-14",
		})
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	resp, err := client.ApplySpecImport("plan-1", "hash-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reqPath != "/integrations/import/apply" {
		t.Errorf("expected /integrations/import/apply, got %s", reqPath)
	}
	if decoded.PlanID != "plan-1" || decoded.SourceHash != "hash-1" {
		t.Errorf("expected plan_id/source_hash to reach the server, got %+v", decoded)
	}
	if resp.Status != "applied" || resp.Version != "2026-07-14" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

// TestApplySpecImport_HandlesError verifies a source_hash mismatch (or any
// non-2xx) surfaces as an error rather than a silently empty response.
func TestApplySpecImport_HandlesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error": "source_hash_mismatch"}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	_, err := client.ApplySpecImport("plan-1", "stale-hash")
	if err == nil {
		t.Fatal("expected an error on 409 response")
	}
	if !strings.Contains(err.Error(), "source_hash_mismatch") {
		t.Errorf("expected error to carry server message, got: %v", err)
	}
}
