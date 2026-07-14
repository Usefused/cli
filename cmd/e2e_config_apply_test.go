package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestE2ESDKPlanAndApplyWithDownload(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "sdks/my-sdk.yaml", `
kind: sdk
version: 1
name: my-sdk
sdkVersion: 1.0.0
language: typescript
target: sdk
services:
  okta:
    version: "2026-07-01"
    operations:
      - "users.create"
`)

	var sawPlan, sawApply, sawStream bool
	downloadAttempts := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sdk-config/plan":
			sawPlan = true
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			_, _ = w.Write([]byte(`{"plan_id":"plan-sdk-123","config_key":"sdk:my-sdk","source_hash":"` + body["source_hash"].(string) + `","base_generation":0,"summary":{}}`))
		case "/sdk-config/apply":
			sawApply = true
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body["plan_id"] != "plan-sdk-123" {
				t.Fatalf("unexpected plan id in apply: %v", body["plan_id"])
			}
			_, _ = w.Write([]byte(`{"status":"applied","plan_id":"plan-sdk-123","sdk_id":"sdk-id-123","job_id":"job-sdk-123"}`))
		case "/sdks/job/job-sdk-123/stream":
			sawStream = true
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"complete\",\"message\":\"done\",\"integration_id\":\"sdk-id-123\"}\n\n"))
		case "/sdk-config/sdk:my-sdk/download":
			downloadAttempts++
			if downloadAttempts == 1 {
				http.Error(w, "not ready", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write([]byte("mock-zip-content"))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	oldAttempts, oldDelay := sdkDownloadRetryAttempts, sdkDownloadRetryDelay
	sdkDownloadRetryAttempts, sdkDownloadRetryDelay = 2, 0
	t.Cleanup(func() {
		sdkDownloadRetryAttempts, sdkDownloadRetryDelay = oldAttempts, oldDelay
	})

	// 1. Plan
	runCommandInDir(t, dir, server.URL, []string{"sdk", "plan", "-f", path})
	if !sawPlan {
		t.Fatal("expected sdk plan request")
	}

	// 2. Apply with --download
	runCommandInDir(t, dir, server.URL, []string{"sdk", "apply", "-f", path, "--download"})
	if !sawApply {
		t.Fatal("expected sdk apply request")
	}
	if !sawStream {
		t.Fatal("expected sdk generation stream request")
	}
	if downloadAttempts != 2 {
		t.Fatalf("expected sdk download to retry once, got %d attempts", downloadAttempts)
	}

	// 3. Verify Download Artifact
	content, err := os.ReadFile(filepath.Join(dir, "my-sdk.zip"))
	if err != nil {
		t.Fatalf("failed to read downloaded zip: %v", err)
	}
	if string(content) != "mock-zip-content" {
		t.Fatalf("unexpected zip content: %s", string(content))
	}
}
