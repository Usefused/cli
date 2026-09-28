package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestExecutionConfigUsesDistinctPlanAndApplyRoutes proves SDK and hosted App requests cannot cross kinds.
func TestExecutionConfigUsesDistinctPlanAndApplyRoutes(t *testing.T) {
	paths := make([]string, 0, 2)
	client := &Client{BaseURL: "https://engine.test", APIKey: "saved-control-key"}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		// Every request keeps the saved management credential on its own route.
		if request.Header.Get("x-api-key") != "saved-control-key" {
			t.Errorf("missing management credential on %s", request.URL.Path)
		}
		body := `{"plan_id":"plan-1","source_hash":"sha256:source"}`
		if request.URL.Path == "/execution-config/apply" {
			body = `{"status":"applied","plan_id":"plan-1","app_family_id":"family-1","app_id":"app-1"}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	_, err := client.PlanExecutionConfig(DesiredConfigPlanIntent{ConfigKey: "execution:greeting:1.0.0", SourceHash: "sha256:source", Config: json.RawMessage(`{"kind":"execution"}`)})
	if err != nil {
		t.Fatalf("plan Execution App: %v", err)
	}
	_, err = client.ApplyExecutionConfig("plan-1", "sha256:source", false)
	if err != nil {
		t.Fatalf("apply Execution App: %v", err)
	}
	if len(paths) != 2 || paths[0] != "/execution-config/plan" || paths[1] != "/execution-config/apply" {
		t.Fatalf("Execution App routes = %#v", paths)
	}
}
