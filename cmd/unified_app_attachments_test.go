package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestUnifiedAppConsumerPlanTransport proves CLI config parsing preserves the same attachment contract sent by the UI.
func TestUnifiedAppConsumerPlanTransport(t *testing.T) {
	for _, kind := range []string{"sdk", "mcp"} {
		// Each delivery adapter must retain exact names and versions through its own plan command.
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			// Package language and server description belong to different config kinds.
			kindFields := "language: typescript"
			if kind == "mcp" {
				kindFields = "description: Customer tools"
			}
			path := writeSprintConfig(t, dir, "consumer.yaml", fmt.Sprintf("apiVersion: fused/v1\nkind: %s\nname: portal\nversion: 1.0.0\n%s\nbucket: default\nunified_apps:\n  lookup:\n    name: Customer lookup\n    version: 1.0.0\n", kind, kindFields))
			seen := false
			// The stub receives the real CLI HTTP boundary without modifying a workspace.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Environment discovery is a normal CLI preflight before either plan route.
				if r.URL.Path == "/health" {
					_, _ = w.Write([]byte(`{"status":"ok","plane":"engine","environment":"staging"}`))
					return
				}
				if r.URL.Path != "/"+kind+"-config/plan" {
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				var body struct {
					Config struct {
						UnifiedApps map[string]struct {
							Name    string `json:"name"`
							Version string `json:"version"`
						} `json:"unified_apps"`
					} `json:"config"`
					SourceHash string `json:"source_hash"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				ref := body.Config.UnifiedApps["lookup"]
				if ref.Name != "Customer lookup" || ref.Version != "1.0.0" || body.SourceHash == "" {
					t.Errorf("lost attachment: %+v", body)
				}
				seen = true
				_ = json.NewEncoder(w).Encode(map[string]any{"plan_id": "attachment-plan", "config_key": kind + ":portal:1.0.0", "source_hash": body.SourceHash, "summary": map[string]any{}})
			}))
			defer server.Close()
			runCommandInDir(t, dir, server.URL, []string{kind, "plan", "-f", path})
			if !seen {
				t.Fatal("consumer plan never reached Engine")
			}
		})
	}
}
