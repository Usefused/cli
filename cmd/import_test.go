package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImportPlanWritesReceiptAndPostsSpecContent covers Task 6's CLI test
// requirement: a local spec file is read and sent as source_content, the
// request reaches /integrations/import/plan with the right headers, and the
// plan receipt is written for a later "import apply" to pick up.
func TestImportPlanWritesReceiptAndPostsSpecContent(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "widgets.json")
	if err := os.WriteFile(specPath, []byte(`{"openapi":"3.0.0","info":{"title":"Widgets","version":"1.0"},"paths":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	var sawPlan bool
	var authHeader string
	var decoded map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/integrations/import/plan" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		sawPlan = true
		authHeader = r.Header.Get("x-api-key")
		if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"plan_id": "plan-1",
			"source_hash": "hash-1",
			"service_id": "",
			"slug": "widgets",
			"name": "Widgets",
			"is_new_service": true,
			"diff": {"added": 1, "changed": 0, "removed": 0}
		}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"import", "plan", specPath, "--name", "Widgets", "--slug", "widgets"})

	if !sawPlan {
		t.Fatal("expected a request to /integrations/import/plan")
	}
	if authHeader != "fsk_test" {
		t.Errorf("expected api key header, got %q", authHeader)
	}
	if decoded["name"] != "Widgets" || decoded["slug"] != "widgets" {
		t.Errorf("unexpected request body %#v", decoded)
	}
	if decoded["source_content"] == "" || decoded["source_content"] == nil {
		t.Error("expected the local spec file's content to be sent as source_content")
	}
	if !strings.Contains(out, "New service") || !strings.Contains(out, "plan-1") {
		t.Errorf("expected a new-service summary naming the plan ID, got %q", out)
	}

	receiptPath := filepath.Join(dir, ".fused/.state/import.plan.json")
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("expected a plan receipt to be written: %v", err)
	}
	var receipt importPlanReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.PlanID != "plan-1" || receipt.SourceHash != "hash-1" {
		t.Errorf("unexpected receipt %+v", receipt)
	}
}

func TestBuildSpecImportRequestRequiresSlug(t *testing.T) {
	specPath := filepath.Join(t.TempDir(), "widgets.json")
	if err := os.WriteFile(specPath, []byte(`{"openapi":"3.0.0","info":{"title":"Widgets","version":"1.0"},"paths":{}}`), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := buildSpecImportRequest(specPath, importSpecPlanOptions{name: "Widgets"})
	if err == nil || !strings.Contains(err.Error(), "slug") {
		t.Fatalf("expected a missing-slug error, got %v", err)
	}
}

// TestImportPlanPrintsUsageWarningWhenNonEmpty is Task 6's other explicit
// CLI test requirement: when the mocked plan response's usage block is
// non-empty, the printed summary must include the "N SDKs / M workspaces"
// line naming which ones touch the changed endpoint.
func TestImportPlanPrintsUsageWarningWhenNonEmpty(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "widgets.json")
	os.WriteFile(specPath, []byte(`{"openapi":"3.0.0","info":{"title":"Widgets","version":"1.0"},"paths":{}}`), 0644)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"plan_id": "plan-2",
			"source_hash": "hash-2",
			"service_id": "svc-1",
			"slug": "widgets",
			"name": "Widgets",
			"is_new_service": false,
			"resolved_strategy": "modify_existing",
			"diff": {"added": 0, "changed": 1, "removed": 0, "changed_names": ["listWidgets"]},
			"usage": {
				"sdks": [{"id": "sdk-1", "name": "widgets-sdk", "uses_changed_endpoint": true}],
				"workspaces": [{"id": "ws-1", "name": "prod"}]
			}
		}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"import", "plan", specPath, "--name", "Widgets", "--slug", "widgets"})

	if !strings.Contains(out, "1 SDKs / 1 workspaces use this version") {
		t.Fatalf("expected the usage-warning line, got %q", out)
	}
	if !strings.Contains(out, "widgets-sdk") || !strings.Contains(out, "uses a changed/removed endpoint") {
		t.Fatalf("expected the SDK usage line to name the SDK and flag the changed endpoint, got %q", out)
	}
	if !strings.Contains(out, "prod") {
		t.Fatalf("expected the workspace usage line, got %q", out)
	}
}

// TestImportApplyUsesReceiptWithoutReplanning covers "apply reads the most
// recent local receipt" -- it must post plan_id/source_hash straight from
// the receipt file, never re-plan.
func TestImportApplyUsesReceiptWithoutReplanning(t *testing.T) {
	dir := t.TempDir()
	receiptPath := filepath.Join(dir, ".fused/.state/import.plan.json")
	if err := os.MkdirAll(filepath.Dir(receiptPath), 0755); err != nil {
		t.Fatal(err)
	}
	receipt := importPlanReceipt{Slug: "widgets", PlanID: "plan-3", SourceHash: "hash-3"}
	data, _ := json.Marshal(receipt)
	if err := os.WriteFile(receiptPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	var sawApply bool
	var decoded map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/integrations/import/plan" {
			t.Fatal("apply must not re-plan when a receipt exists")
		}
		if r.URL.Path != "/integrations/import/apply" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		sawApply = true
		if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"applied","plan_id":"plan-3","service_id":"svc-1","is_new_service":false,"resolved_strategy":"modify_existing","version":"2026-07-14"}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"import", "apply"})

	if !sawApply {
		t.Fatal("expected a request to /integrations/import/apply")
	}
	if decoded["plan_id"] != "plan-3" || decoded["source_hash"] != "hash-3" {
		t.Errorf("expected plan_id/source_hash from the receipt, got %#v", decoded)
	}
	if !strings.Contains(out, "svc-1") || !strings.Contains(out, "2026-07-14") {
		t.Errorf("expected apply result naming service/version, got %q", out)
	}
}

// TestImportApplyPlanIDRequiresSourceHash guards the one deliberate CLI
// deviation from the config plan/apply flags: --plan-id alone isn't enough
// here, since there's no locally-loaded config to recompute a hash from.
func TestImportApplyPlanIDRequiresSourceHash(t *testing.T) {
	dir := t.TempDir()
	oldwd, _ := os.Getwd()
	defer os.Chdir(oldwd)
	os.Chdir(dir)

	oldEngineURL, oldAPIKey := EngineURL, APIKey
	defer func() { EngineURL, APIKey = oldEngineURL, oldAPIKey }()
	EngineURL, APIKey = "http://example.invalid", "fsk_test"

	err := runImportApply(RootCmd, importSpecApplyOptions{planID: "plan-4"})
	if err == nil || !strings.Contains(err.Error(), "--source-hash") {
		t.Fatalf("expected a --source-hash-required error, got %v", err)
	}
}
