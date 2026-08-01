package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

func TestPrintPlanResultIncludesEngineSummary(t *testing.T) {
	planned := []plannedConfig{{
		receipt: planReceipt{ConfigKey: "workspace", PlanID: "plan-1", SourceHash: "sha256:abc"},
		summary: map[string]interface{}{
			"actions": []interface{}{map[string]interface{}{
				"type":       "add_service",
				"service_id": "service-1",
			}},
		},
	}}

	out := captureStdout(t, func() {
		if err := printPlanResult(planned, false); err != nil {
			t.Fatalf("printPlanResult: %v", err)
		}
	})

	for _, expected := range []string{"Plan summary:", `"type": "add_service"`, `"service_id": "service-1"`} {
		if !strings.Contains(out, expected) {
			t.Fatalf("expected %q in plan output:\n%s", expected, out)
		}
	}
}

func TestPrintPlanResultJSONIncludesSummaryAndNotifications(t *testing.T) {
	planned := []plannedConfig{{
		receipt: planReceipt{
			ConfigKey: "sdk:security:1.0.0", PlanID: "plan-1", SourceHash: "sha256:abc",
			EngineURL: "https://engine.example.com", CreatedAt: "2026-08-01T00:00:00Z",
		},
		summary: map[string]interface{}{"create_sdk": true},
		notifications: api.NotificationInbox{Items: []api.NotificationItem{{
			ID: "note-1", Type: "registry_version_changed", Severity: "breaking",
		}}},
	}}

	out := captureStdout(t, func() {
		if err := printPlanResult(planned, true); err != nil {
			t.Fatalf("printPlanResult: %v", err)
		}
	})
	var decoded []planResultOutput
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, out)
	}
	if len(decoded) != 1 || decoded[0].Summary["create_sdk"] != true {
		t.Fatalf("expected plan summary in JSON output, got %#v", decoded)
	}
	if len(decoded[0].Notifications.Items) != 1 || decoded[0].Notifications.Items[0].ID != "note-1" {
		t.Fatalf("expected notifications in JSON output, got %#v", decoded[0].Notifications)
	}
}

func TestValidateReceiptEngineURLRequiresBoundReceipt(t *testing.T) {
	err := validateReceiptEngineURL("", "https://engine.example.com")
	if err == nil || !strings.Contains(err.Error(), "no engine_url") {
		t.Fatalf("expected an unbound-receipt error, got %v", err)
	}
}

func TestValidateReceiptEngineURLRejectsDifferentTarget(t *testing.T) {
	err := validateReceiptEngineURL("https://staging.example.com", "https://production.example.com")
	if err == nil || !strings.Contains(err.Error(), "receipt targets") {
		t.Fatalf("expected a target mismatch, got %v", err)
	}
}

func TestValidateReceiptEngineURLNormalizesEquivalentTargets(t *testing.T) {
	if err := validateReceiptEngineURL("HTTPS://ENGINE.EXAMPLE.COM/", "https://engine.example.com"); err != nil {
		t.Fatalf("expected equivalent Engine URLs to match: %v", err)
	}
}

func TestPrepareConfigAppliesChecksEveryReceiptBeforeExecution(t *testing.T) {
	dir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	workspacePath := writeSprintConfig(t, dir, ".fused/workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services: {}
`)
	sdkPath := writeSprintConfig(t, dir, ".fused/sdks/security.yaml", `
apiVersion: fused/v1
kind: sdk
name: security
version: 1.0.0
language: typescript
services:
  okta:
    version: "2026-07-01"
    operations: [listLogEvents]
`)
	workspace, err := configfile.ParseFile(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := configfile.ParseFile(sdkPath)
	if err != nil {
		t.Fatal(err)
	}
	engineURL := "https://engine.example.com"
	writeTestPlanReceipt(t, dir, workspace, workspace.SourceHash, engineURL)
	writeTestPlanReceipt(t, dir, sdk, "sha256:stale", engineURL)

	prepared, err := prepareConfigApplies([]*configfile.ParsedConfig{workspace, sdk}, applyOptions{}, engineURL)
	if err == nil || !strings.Contains(err.Error(), "config changed since plan") {
		t.Fatalf("expected the later stale receipt to fail preflight, got prepared=%#v err=%v", prepared, err)
	}
	if prepared != nil {
		t.Fatalf("preflight must not return a partially prepared apply: %#v", prepared)
	}
}

func TestAggregateApplyPreflightsAllReceiptsBeforeRemoteMutation(t *testing.T) {
	dir := t.TempDir()
	workspacePath := writeSprintConfig(t, dir, ".fused/workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services: {}
`)
	sdkPath := writeSprintConfig(t, dir, ".fused/sdks/security.yaml", `
apiVersion: fused/v1
kind: sdk
name: security
version: 1.0.0
language: typescript
services:
  okta:
    version: "2026-07-01"
    operations: [listLogEvents]
`)
	workspace, err := configfile.ParseFile(workspacePath)
	if err != nil {
		t.Fatal(err)
	}
	sdk, err := configfile.ParseFile(sdkPath)
	if err != nil {
		t.Fatal(err)
	}
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requestCount++
	}))
	defer server.Close()
	writeTestPlanReceipt(t, dir, workspace, workspace.SourceHash, server.URL)
	writeTestPlanReceipt(t, dir, sdk, "sha256:stale", server.URL)

	out := runCommandInDirExpectError(t, dir, server.URL, []string{"apply"})
	if !strings.Contains(out, "config changed since plan") {
		t.Fatalf("expected stale receipt error, got %q", out)
	}
	if requestCount != 0 {
		t.Fatalf("preflight failure must happen before remote mutation; got %d request(s)", requestCount)
	}
}

func TestApplyRejectsReceiptForDifferentEngineBeforeRemoteMutation(t *testing.T) {
	dir := t.TempDir()
	sdkPath := writeSprintConfig(t, dir, "security.yaml", `
apiVersion: fused/v1
kind: sdk
name: security
version: 1.0.0
language: typescript
services:
  okta:
    version: "2026-07-01"
    operations: [listLogEvents]
`)
	sdk, err := configfile.ParseFile(sdkPath)
	if err != nil {
		t.Fatal(err)
	}
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requestCount++
	}))
	defer server.Close()
	receiptPath := filepath.Join(dir, defaultReceiptPath(sdk.ConfigKey))
	if err := writePlanReceiptFile(receiptPath, planReceipt{
		ConfigKey: sdk.ConfigKey, PlanID: "plan-sdk", SourceHash: sdk.SourceHash,
		EngineURL: "https://different-engine.example.com",
	}); err != nil {
		t.Fatal(err)
	}

	out := runCommandInDirExpectError(t, dir, server.URL, []string{"sdk", "apply", "-f", sdkPath})
	if !strings.Contains(out, "receipt targets") {
		t.Fatalf("expected target mismatch, got %q", out)
	}
	if requestCount != 0 {
		t.Fatalf("target mismatch must happen before remote mutation; got %d request(s)", requestCount)
	}
}

func writeTestPlanReceipt(t *testing.T, dir string, cfg *configfile.ParsedConfig, sourceHash, engineURL string) {
	t.Helper()
	receipt := planReceipt{
		ConfigKey: cfg.ConfigKey, PlanID: "plan-" + strings.ReplaceAll(cfg.ConfigKey, ":", "-"),
		SourceHash: sourceHash, EngineURL: engineURL,
	}
	path := filepath.Join(dir, defaultReceiptPath(cfg.ConfigKey))
	if err := writePlanReceiptFile(path, receipt); err != nil {
		t.Fatal(err)
	}
}
