package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestHealth_ParsesEnvironmentLabel is Task 8's CLI-side AC
// (engine_workspace_registration_plan.md): the Engine's /health echo of its
// --environment label must be parseable so callers can warn before a
// destructive workspace apply.
func TestHealth_ParsesEnvironmentLabel(t *testing.T) {
	var reqPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","plane":"engine","environment":"production"}`))
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	health, err := client.Health()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reqPath != "/health" {
		t.Errorf("expected GET /health, got %s", reqPath)
	}
	if health.Environment != "production" || health.Status != "ok" || health.Plane != "engine" {
		t.Errorf("unexpected health status: %+v", health)
	}
}

// TestHealth_HandlesError verifies non-2xx responses return an error rather
// than a zero-value HealthStatus, so callers can tell "couldn't reach
// Engine" apart from "Engine has no environment label".
func TestHealth_HandlesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "test-key")
	_, err := client.Health()

	if err == nil {
		t.Fatal("expected error on 404 response, got nil")
	}
}
