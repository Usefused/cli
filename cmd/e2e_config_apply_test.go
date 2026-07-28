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
apiVersion: fused/v1
kind: sdk
name: my-sdk
version: "1.0.0"
language: typescript
services:
  okta:
    version: "2026-07-01"
    operations:
      - "users.create"
`)

	seen := &e2eSDKSeen{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleE2ESDKRequest(t, seen, w, r)
	}))
	defer server.Close()
	// 1. Plan
	runCommandInDir(t, dir, server.URL, []string{"sdk", "plan", "-f", path})
	if !seen.plan {
		t.Fatal("expected sdk plan request")
	}

	// 2. Apply with --download
	runCommandInDir(t, dir, server.URL, []string{"sdk", "apply", "-f", path, "--download"})
	if !seen.apply {
		t.Fatal("expected sdk apply request")
	}
	if !seen.stream {
		t.Fatal("expected sdk generation stream request")
	}
	if !seen.download {
		t.Fatal("expected generated sdk id download request")
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

type e2eSDKSeen struct {
	plan     bool
	apply    bool
	stream   bool
	download bool
}

func handleE2ESDKRequest(t *testing.T, seen *e2eSDKSeen, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	switch r.URL.Path {
	case "/sdk-config/plan":
		seen.plan = true
		writeE2ESDKPlanResponse(t, w, r)
	case "/sdk-config/apply":
		seen.apply = true
		writeE2ESDKApplyResponse(t, w, r)
	case "/sdks/job/job-sdk-123/stream":
		seen.stream = true
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"complete\",\"message\":\"done\",\"integration_id\":\"sdk-id-123\"}\n\n"))
	case "/sdks/sdk-id-123/download":
		seen.download = true
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write([]byte("mock-zip-content"))
	default:
		t.Fatalf("unexpected path %s", r.URL.Path)
	}
}

func writeE2ESDKPlanResponse(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	_, _ = w.Write([]byte(`{"plan_id":"plan-sdk-123","config_key":"sdk:my-sdk:1.0.0","source_hash":"` + body["source_hash"].(string) + `","base_generation":0,"summary":{}}`))
}

func writeE2ESDKApplyResponse(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["plan_id"] != "plan-sdk-123" {
		t.Fatalf("unexpected plan id in apply: %v", body["plan_id"])
	}
	_, _ = w.Write([]byte(`{"status":"applied","plan_id":"plan-sdk-123","artifact_id":"sdk-id-123","job_id":"job-sdk-123"}`))
}
