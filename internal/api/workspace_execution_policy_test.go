package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPlanWorkspaceConfigPreservesServerVariables(t *testing.T) {
	var request struct {
		Config json.RawMessage `json:"config"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"plan_id":"plan-1"}`))
	}))
	defer server.Close()

	config := json.RawMessage(`{"services":{"confluence":{"execution_policy":{"server_variables":{"your-domain":"acme"}}}}}`)
	if _, err := NewClient(server.URL, "test-key").PlanWorkspaceConfig("sha256:test", "workspace", config); err != nil {
		t.Fatalf("PlanWorkspaceConfig: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(request.Config, &decoded); err != nil {
		t.Fatal(err)
	}
	services := decoded["services"].(map[string]any)
	policy := services["confluence"].(map[string]any)["execution_policy"].(map[string]any)
	variables := policy["server_variables"].(map[string]any)
	if variables["your-domain"] != "acme" {
		t.Fatalf("server_variables were changed in API payload: %#v", variables)
	}
}
