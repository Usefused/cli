package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestE2ESDKPlanAndApplyWithDownload covers plan, prompt apply, Engine-owned generation polling, and package extraction.
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
	// Plan must complete before the receipt can authorize apply.
	if !seen.plan {
		t.Fatal("expected sdk plan request")
	}

	// 2. Apply with --download
	runCommandInDir(t, dir, server.URL, []string{"sdk", "apply", "-f", path, "--download"})
	// Apply now acknowledges durable generation instead of holding the request open.
	if !seen.apply {
		t.Fatal("expected sdk apply request")
	}
	// Download waits on the Engine-owned immutable-version status route.
	if !seen.generation {
		t.Fatal("expected sdk generation status request")
	}
	// Package bytes are requested only after Engine reports generation complete.
	if !seen.download {
		t.Fatal("expected generated sdk id download request")
	}

	// 3. Verify Download Artifact Extracted
	// Successful extraction is the final observable --download outcome.
	if info, err := os.Stat(filepath.Join(dir, "fused-sdks", "my-sdk")); err != nil || !info.IsDir() {
		t.Fatalf("failed to find extracted sdk directory: %v", err)
	}
}

type e2eSDKSeen struct {
	plan       bool
	apply      bool
	generation bool
	download   bool
}

// handleE2ESDKRequest provides one deterministic Engine surface for the CLI lifecycle test.
func handleE2ESDKRequest(t *testing.T, seen *e2eSDKSeen, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	switch r.URL.Path {
	// Planning returns the hash-bound receipt consumed by apply.
	case "/sdk-config/plan":
		seen.plan = true
		writeE2ESDKPlanResponse(t, w, r)
	// Apply returns promptly with immutable identity and queued generation metadata.
	case "/sdk-config/apply":
		seen.apply = true
		writeE2ESDKApplyResponse(t, w, r)
	// Generation readiness is read from Engine by exact Version ID.
	case "/sdk-config/generation/sdk-id-123":
		seen.generation = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"complete","app_id":"sdk-id-123","app_family_id":"family-sdk-123","job_id":"job-sdk-123"}`))
	// Download receives package bytes only after readiness is proven.
	case "/sdks/sdk-id-123/download":
		seen.download = true
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write([]byte{0x50, 0x4b, 0x05, 0x06, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	// Any extra route would reintroduce an unreviewed lifecycle dependency.
	default:
		t.Fatalf("unexpected path %s", r.URL.Path)
	}
}

// writeE2ESDKPlanResponse echoes the submitted source hash into a valid plan receipt.
func writeE2ESDKPlanResponse(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var body map[string]any
	// Invalid test requests must fail at the same boundary as the production decoder.
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	_, _ = w.Write([]byte(`{"plan_id":"plan-sdk-123","config_key":"sdk:my-sdk:1.0.0","source_hash":"` + body["source_hash"].(string) + `","base_generation":0,"summary":{}}`))
}

// writeE2ESDKApplyResponse validates plan identity before acknowledging queued generation.
func writeE2ESDKApplyResponse(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var body map[string]any
	// Malformed apply input invalidates the lifecycle fixture.
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	// Apply must consume the exact plan created earlier in this scenario.
	if body["plan_id"] != "plan-sdk-123" {
		t.Fatalf("unexpected plan id in apply: %v", body["plan_id"])
	}
	_, _ = w.Write([]byte(`{"status":"applied","plan_id":"plan-sdk-123","app_family_id":"family-sdk-123","app_id":"sdk-id-123","job_id":"job-sdk-123"}`))
}
