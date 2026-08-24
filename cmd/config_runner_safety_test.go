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
		requiredPermissions: []api.PermissionRequirement{{
			Permission: "service.manage", ResourceType: "service",
			ResourceID: "11111111-1111-1111-1111-111111111111", DisplayName: "GitHub",
		}, {
			Permission: "bucket.use", ResourceType: "bucket",
			ResourceID: "22222222-2222-2222-2222-222222222222",
		}},
	}}

	out := captureStdout(t, func() {
		if err := printPlanResult(planned, false); err != nil {
			t.Fatalf("printPlanResult: %v", err)
		}
	})

	for _, expected := range []string{
		"Plan summary:", `"type": "add_service"`, `"service_id": "service-1"`,
		"Required permissions:", `Ability to manage service "GitHub"`,
		"Ability to use the selected bucket",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("expected %q in plan output:\n%s", expected, out)
		}
	}
	if strings.Contains(out, "service.manage") || strings.Contains(out, "11111111-1111-1111-1111-111111111111") {
		t.Fatalf("normal plan output leaked advanced permission diagnostics:\n%s", out)
	}
}

func TestApplyPreparedSDKPrintsOneTimeExecutionToken(t *testing.T) {
	tests := []struct {
		name          string
		responseToken string
		wantTokenLine bool
	}{
		{name: "initial apply", responseToken: "shown-once", wantTokenLine: true},
		{name: "idempotent apply", wantTokenLine: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/sdk-config/apply" {
					t.Fatalf("unexpected path %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"status":"applied","app_family_id":"family-1","app_id":"app-1","execution_token":"` + test.responseToken + `"}`))
			}))
			defer server.Close()

			cfg := &configfile.ParsedConfig{SDK: &configfile.SDKConfig{Name: "security"}}
			out := captureStdout(t, func() {
				err := applyPreparedSDK(api.NewClient(server.URL, "fsk_test"), cfg, planReceipt{PlanID: "plan-1", SourceHash: "hash"}, false)
				if err != nil {
					t.Fatalf("applyPreparedSDK: %v", err)
				}
			})

			gotCount := strings.Count(out, "SDK token (shown once):")
			if test.wantTokenLine && (gotCount != 1 || !strings.Contains(out, "SDK token (shown once): shown-once")) {
				t.Fatalf("expected one SDK token line, got:\n%s", out)
			}
			if !test.wantTokenLine && gotCount != 0 {
				t.Fatalf("expected no SDK token line, got:\n%s", out)
			}
		})
	}
}

// TestApplyPreparedSDKJSONPreservesIDsTokenAndStageOutcomes verifies the automation contract.
func TestApplyPreparedSDKJSONPreservesIDsTokenAndStageOutcomes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sdk-config/apply" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"applied","plan_id":"plan-1","app_family_id":"sdk-1","app_id":"version-1","job_id":"job-1","execution_token":"shown-once"}`))
	}))
	defer server.Close()

	cfg := &configfile.ParsedConfig{ConfigKey: "sdk:security:1.0.0", SDK: &configfile.SDKConfig{Name: "security"}}
	result, err := applyPreparedSDKJSON(api.NewClient(server.URL, "fsk_test"), cfg, planReceipt{PlanID: "plan-1", SourceHash: "hash"}, false)
	if err != nil {
		t.Fatalf("applyPreparedSDKJSON: %v", err)
	}
	if result.SDKID != "sdk-1" || result.VersionID != "version-1" || result.ExecutionToken != "shown-once" {
		t.Fatalf("identity output = %#v", result)
	}
	if result.Generation.Status != "queued" || result.Generation.JobID != "job-1" || result.Download.Status != "not_requested" {
		t.Fatalf("stage output = %#v", result)
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
		requiredPermissions: []api.PermissionRequirement{{
			Permission: "app.create", ResourceType: "workspace",
			ResourceID: "33333333-3333-3333-3333-333333333333", DisplayName: "workspace",
		}},
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
	if len(decoded[0].RequiredPermissions) != 1 || decoded[0].RequiredPermissions[0].Permission != "app.create" {
		t.Fatalf("expected required permissions in JSON output, got %#v", decoded[0].RequiredPermissions)
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
