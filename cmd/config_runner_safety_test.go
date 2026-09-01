package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

// TestPrintPlanResultIncludesEngineSummary verifies human plan output retains change, permission, and credential-readiness guidance.
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
		credentialReadiness: &api.CredentialReadiness{
			Bucket: &api.MissingCredentialBucket{ID: "22222222-2222-4222-8222-222222222222", Name: "production"},
			MissingCredentials: []api.MissingCredentialRequirement{{
				ServiceID: "33333333-3333-4333-8333-333333333333", Service: "Okta", AuthType: "api_key",
			}},
		},
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
		`Credential readiness for workspace: 1 authentication requirement(s) are missing from bucket "production".`,
		"fused-cli secret set '33333333-3333-4333-8333-333333333333' --bucket '22222222-2222-4222-8222-222222222222' --interactive",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("expected %q in plan output:\n%s", expected, out)
		}
	}
	if strings.Contains(out, "service.manage") || strings.Contains(out, "11111111-1111-1111-1111-111111111111") {
		t.Fatalf("normal plan output leaked advanced permission diagnostics:\n%s", out)
	}
}

// TestSDKApplyAmbiguousFailuresUseReadOnlyRecovery pins timeout, proxy, malformed-success, and explicit-unknown classification.
func TestSDKApplyAmbiguousFailuresUseReadOnlyRecovery(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "proxy 502", status: http.StatusBadGateway, body: `bad gateway`},
		{name: "explicit unknown", status: http.StatusConflict, body: `{"error":{"code":"sdk_apply_interrupted","message":"outcome unavailable","category":"indeterminate","retryable":false,"phase":"apply","commit_state":"unknown"}}`},
		{name: "malformed success", status: http.StatusOK, body: `{"status":"applied"`},
	}
	// Each response variant must converge on the same non-replay recovery contract.
	for _, test := range tests {
		// Each response crosses the SDK apply write boundary without positive or negative commit proof.
		t.Run(test.name, func(t *testing.T) {
			assertSDKApplyAmbiguousResponse(t, test.status, test.body)
		})
	}
}

// TestSDKApplyTimeoutUsesReadOnlyRecovery proves a transport deadline suppresses the nested retryable API classification.
func TestSDKApplyTimeoutUsesReadOnlyRecovery(t *testing.T) {
	cfg, receipt := sdkApplyAmbiguityFixture()
	timeout := &api.APIError{Code: "request_timed_out", Message: "Engine request timed out", Category: "timeout", Retryable: true}
	var unknown *sdkApplyOutcomeUnknownError
	// A transport deadline receives the same unknown/non-replay contract even without an HTTP status.
	timeoutErr := classifySDKApplyFailure(timeout, cfg, receipt)
	if !errors.As(timeoutErr, &unknown) {
		t.Fatalf("timeout classification = %T %v", timeoutErr, timeoutErr)
	}
	timeoutResult := classifyCommandError(&cobra.Command{Use: "apply"}, timeoutErr)
	// Structured timeout output must explicitly suppress the wrapped API error's retryable flag.
	if timeoutResult.Code != "sdk_apply_outcome_unknown" || timeoutResult.Retryable || timeoutResult.CommitState != "unknown" {
		t.Fatalf("timeout result = %#v", timeoutResult)
	}
}

// assertSDKApplyAmbiguousResponse verifies one HTTP outcome against the stable non-replay command contract.
func assertSDKApplyAmbiguousResponse(t *testing.T, status int, body string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The fixture accepts only the one mutation request under test.
		if r.URL.Path != "/sdk-config/apply" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	cfg, receipt := sdkApplyAmbiguityFixture()
	_, err := applyPreparedSDKJSON(api.NewClient(server.URL, "fsk_test"), cfg, receipt, false)
	var unknown *sdkApplyOutcomeUnknownError
	// Ambiguous responses are non-retryable and carry the exact immutable inspection target plus plan identity.
	if !errors.As(err, &unknown) {
		t.Fatalf("apply error = %T %v", err, err)
	}
	result := classifyCommandError(&cobra.Command{Use: "apply"}, err)
	// JSON exposes the plan, immutable target, and read-only command without inheriting retryability.
	if result.Code != "sdk_apply_outcome_unknown" || result.Retryable || result.CommitState != "unknown" || result.OperationID != "plan-1" || result.Recovery != "fused-cli sdk show 'security@1.2.0'" || result.Details["config_key"] != cfg.ConfigKey {
		t.Fatalf("classification = %#v", result)
	}
}

// sdkApplyAmbiguityFixture returns one immutable config and receipt shared by response-loss tests.
func sdkApplyAmbiguityFixture() (*configfile.ParsedConfig, planReceipt) {
	cfg := &configfile.ParsedConfig{
		Kind: configfile.KindSDK, ConfigKey: "sdk:security:1.2.0",
		SDK: &configfile.SDKConfig{Name: "security", Version: "1.2.0"},
	}
	return cfg, planReceipt{PlanID: "plan-1", SourceHash: "hash"}
}

// TestSDKApplyNotCommittedFailurePreservesEngineProof keeps quota rejection safe to correct and retry through a new plan.
func TestSDKApplyNotCommittedFailurePreservesEngineProof(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The quota fixture rejects before commit and supplies the authoritative negative proof.
		if r.URL.Path != "/sdk-config/apply" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"sdk_family_limit_exceeded","message":"SDK family limit exceeded","category":"quota","retryable":false,"phase":"apply_admission","commit_state":"not_committed"}}`))
	}))
	defer server.Close()
	cfg := &configfile.ParsedConfig{
		Kind: configfile.KindSDK, ConfigKey: "sdk:security:1.2.0",
		SDK: &configfile.SDKConfig{Name: "security", Version: "1.2.0"},
	}
	_, err := applyPreparedSDKJSON(api.NewClient(server.URL, "fsk_test"), cfg, planReceipt{PlanID: "plan-1", SourceHash: "hash"}, false)
	var unknown *sdkApplyOutcomeUnknownError
	var apiErr *api.APIError
	// Proven non-commit remains the Engine quota code instead of being rewritten as indeterminate.
	if errors.As(err, &unknown) || !errors.As(err, &apiErr) || apiErr.Code != "sdk_family_limit_exceeded" || apiErr.CommitState != "not_committed" {
		t.Fatalf("quota apply error = %T %v API=%#v", err, err, apiErr)
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
		_, _ = w.Write([]byte(`{"status":"pending","generation_status":"pending","plan_id":"plan-1","app_family_id":"sdk-1","app_id":"version-1","job_id":"job-1","execution_token":"shown-once"}`))
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

// TestSDKApplyGenerationStageStatusDistinguishesQueueAndCacheHit verifies low-latency apply does not mislabel immediate completion.
func TestSDKApplyGenerationStageStatusDistinguishesQueueAndCacheHit(t *testing.T) {
	tests := []struct {
		name             string
		response         api.SDKConfigApplyResponse
		generatesPackage bool
		want             string
	}{
		{name: "queued", response: api.SDKConfigApplyResponse{Status: "pending", GenerationStatus: "pending"}, generatesPackage: true, want: "queued"},
		{name: "cache hit", response: api.SDKConfigApplyResponse{Status: "applied", GenerationStatus: "complete"}, generatesPackage: true, want: "completed"},
		{name: "legacy complete", response: api.SDKConfigApplyResponse{Status: "applied"}, generatesPackage: true, want: "completed"},
		{name: "package skipped", response: api.SDKConfigApplyResponse{Status: "applied"}, want: "skipped"},
	}
	// Response compatibility must remain deterministic across current and pre-generation-status Engines.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := sdkApplyGenerationStageStatus(&test.response, test.generatesPackage)
			// Incorrect stage output would tell automation to wait for work that is already terminal or skip pending work.
			if got != test.want {
				t.Fatalf("generation stage = %q, want %q", got, test.want)
			}
		})
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

// TestValidateDownloadableConfigsRejectsDownloadWhenNoPackageIsBuilt proves the
// contradiction is caught before anything is applied.
func TestValidateDownloadableConfigsRejectsDownloadWhenNoPackageIsBuilt(t *testing.T) {
	no := false
	yes := true
	generating := &configfile.ParsedConfig{
		Kind: configfile.KindSDK, ConfigKey: "sdk:billing:1.0.0",
		SDK: &configfile.SDKConfig{Name: "billing", Generate: &yes},
	}
	notGenerating := &configfile.ParsedConfig{
		Kind: configfile.KindSDK, ConfigKey: "sdk:ledger:1.0.0",
		SDK: &configfile.SDKConfig{Name: "ledger", Generate: &no},
	}
	unset := &configfile.ParsedConfig{
		Kind: configfile.KindSDK, ConfigKey: "sdk:legacy:1.0.0",
		SDK: &configfile.SDKConfig{Name: "legacy"},
	}

	if err := validateDownloadableConfigs([]*configfile.ParsedConfig{notGenerating}, false); err != nil {
		t.Fatalf("without --download the config is applyable: %v", err)
	}
	if err := validateDownloadableConfigs([]*configfile.ParsedConfig{generating, unset}, true); err != nil {
		t.Fatalf("generate true and generate absent both build a package: %v", err)
	}
	err := validateDownloadableConfigs([]*configfile.ParsedConfig{generating, notGenerating}, true)
	if err == nil {
		t.Fatal("expected --download to be rejected for a config that builds no package")
	}
	if !strings.Contains(err.Error(), "ledger") || !strings.Contains(err.Error(), "generate: false") {
		t.Fatalf("error must name the config and the field, got %q", err)
	}
}

// TestApplyPreparedSDKJSONReportsSkippedGenerationWhenNoPackageIsBuilt keeps the
// automation contract honest: nothing was queued, so nothing may claim to be.
func TestApplyPreparedSDKJSONReportsSkippedGenerationWhenNoPackageIsBuilt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"applied","plan_id":"plan-1","app_family_id":"sdk-1","app_id":"version-1","job_id":"job-1"}`))
	}))
	defer server.Close()

	no := false
	cfg := &configfile.ParsedConfig{
		ConfigKey: "sdk:ledger:1.0.0",
		SDK:       &configfile.SDKConfig{Name: "ledger", Generate: &no},
	}
	result, err := applyPreparedSDKJSON(api.NewClient(server.URL, "fsk_test"), cfg, planReceipt{PlanID: "plan-1", SourceHash: "hash"}, false)
	if err != nil {
		t.Fatalf("applyPreparedSDKJSON: %v", err)
	}
	if result.Generation.Status != "skipped" || result.Generation.JobID != "job-1" {
		t.Fatalf("generation stage = %#v", result.Generation)
	}
	if result.Download.Status != "not_requested" {
		t.Fatalf("download stage = %#v", result.Download)
	}
}
