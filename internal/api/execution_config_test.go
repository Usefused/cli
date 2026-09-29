package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestUnifiedAppConfigUsesDistinctPlanAndApplyRoutes proves SDK and hosted App requests cannot cross kinds.
func TestUnifiedAppConfigUsesDistinctPlanAndApplyRoutes(t *testing.T) {
	paths := make([]string, 0, 2)
	client := &Client{BaseURL: "https://engine.test", APIKey: "saved-control-key"}
	client.HTTP = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		// Every request keeps the saved management credential on its own route.
		if request.Header.Get("x-api-key") != "saved-control-key" {
			t.Errorf("missing management credential on %s", request.URL.Path)
		}
		body := `{"plan_id":"plan-1","source_hash":"sha256:source"}`
		if request.URL.Path == "/unified-app-config/apply" {
			body = `{"status":"applied","plan_id":"plan-1","app_family_id":"family-1","app_id":"app-1"}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	_, err := client.PlanUnifiedAppConfig(DesiredConfigPlanIntent{ConfigKey: "unified_app:greeting:1.0.0", SourceHash: "sha256:source", Config: json.RawMessage(`{"kind":"unified_app"}`)})
	if err != nil {
		t.Fatalf("pla Unified App: %v", err)
	}
	_, err = client.ApplyUnifiedAppConfig("plan-1", "sha256:source", false)
	if err != nil {
		t.Fatalf("apply Unified App: %v", err)
	}
	if len(paths) != 2 || paths[0] != "/unified-app-config/plan" || paths[1] != "/unified-app-config/apply" {
		t.Fatalf("Unified App routes = %#v", paths)
	}
}
