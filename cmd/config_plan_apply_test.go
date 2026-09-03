package cmd

import (
	"bytes"
	"encoding/json"
	"io"
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

func TestWorkspacePlanWritesReceiptAndPostsToEngine(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: [{version: "2026-07-01"}]
`)
	var sawPlan bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workspace/config/plan" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		sawPlan = true
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["config_key"] != "workspace" || body["source_hash"] == "" {
			t.Fatalf("unexpected plan body %#v", body)
		}
		_, _ = w.Write([]byte(`{"plan_id":"plan-workspace","config_key":"workspace","source_hash":"` + body["source_hash"].(string) + `","base_generation":0,"summary":{}}`))
	}))
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"workspace", "plan", "-f", path})

	if !sawPlan {
		t.Fatal("expected workspace plan request")
	}
	receipt := readReceipt(t, filepath.Join(dir, ".fused/.state/workspace.plan.json"))
	if receipt.PlanID != "plan-workspace" || receipt.ConfigKey != "workspace" {
		t.Fatalf("unexpected receipt %#v", receipt)
	}
}

// TestWorkspacePlanPreservesConnectionProfileAuthName pins the raw YAML-to-Engine selector transport used for named Registry streams.
func TestWorkspacePlanPreservesConnectionProfileAuthName(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  jira:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions:
      - version: "2026-08-01"
        connection_profiles:
          - auth_type: oauth
            auth_name: jiraOAuth
            profile_id: "00000000-0000-0000-0000-000000000002"
`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		// The captured Engine request is the public boundary this test protects.
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		config := body["config"].(map[string]any)
		services := config["services"].(map[string]any)
		versions := services["jira"].(map[string]any)["versions"].([]any)
		profiles := versions[0].(map[string]any)["connection_profiles"].([]any)
		// Named Registry identity must remain adjacent to auth_type and profile_id.
		if profiles[0].(map[string]any)["auth_name"] != "jiraOAuth" {
			t.Fatalf("connection profile auth_name changed: %#v", profiles)
		}
		_, _ = w.Write([]byte(`{"plan_id":"plan-workspace","config_key":"workspace","source_hash":"` + body["source_hash"].(string) + `","summary":{}}`))
	}))
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"workspace", "plan", "-f", path})
}

// TestMCPPlanAndApplyUseDedicatedEngineRoutes verifies hosted app routing and auth-ref plan transport.
func TestMCPPlanAndApplyUseDedicatedEngineRoutes(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "mcp.yaml", `
apiVersion: fused/v1
kind: mcp
name: github-agent
version: 1.0.0
description: Review repositories and help manage GitHub work.
bucket: default
services:
  github:
    version: "2026-07-01"
    operations: [reposList]
    auth:
      type: oauth
      name: githubOAuth
      ref: "${bucket.auth.github-app.sharedOAuth}"
    connect:
      scopes: [read:user]
`)
	var sourceHash string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok","plane":"engine","environment":"staging"}`))
		case "/mcp-config/plan":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			sourceHash = body["source_hash"].(string)
			config := body["config"].(map[string]any)
			auth := config["services"].(map[string]any)["github"].(map[string]any)["auth"].(map[string]any)
			// The plan request must preserve the reference once without resolving source credential material.
			if auth["ref"] != "${bucket.auth.github-app.sharedOAuth}" {
				t.Fatalf("MCP plan auth ref = %#v", auth)
			}
			if body["config_key"] != "mcp:github-agent:1.0.0" {
				t.Fatalf("unexpected config key: %#v", body)
			}
			_, _ = w.Write([]byte(`{"plan_id":"plan-mcp","config_key":"mcp:github-agent:1.0.0","source_hash":"` + sourceHash + `","summary":{}}`))
		case "/mcp-config/apply":
			_, _ = w.Write([]byte(`{"status":"applied","plan_id":"plan-mcp","config_key":"mcp:github-agent:1.0.0","app_family_id":"family-1","app_id":"runtime-1","default_transport":"streamable_http","stable":true,"stable_version_id":"runtime-1","transport_urls":{"streamable_http":"https://public.engine.test/mcp/family-1","sse":"https://public.engine.test/mcp/family-1/sse","versioned_streamable_http":"https://public.engine.test/mcp/runtime-1","versioned_sse":"https://public.engine.test/mcp/runtime-1/sse"},"execution_token":"shown-once"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"mcp", "plan", "-f", path})
	applyOutput := runCommandInDirOutput(t, dir, server.URL, []string{"mcp", "apply", "-f", path})
	if sourceHash == "" {
		t.Fatal("MCP plan did not include a source hash")
	}
	for _, expected := range []string{
		"Default transport: streamable_http",
		"Streamable HTTP (stable, recommended): https://public.engine.test/mcp/family-1",
		"Streamable HTTP (version-pinned): https://public.engine.test/mcp/runtime-1",
		"SSE (stable, legacy): https://public.engine.test/mcp/family-1/sse",
		"SSE (version-pinned, legacy): https://public.engine.test/mcp/runtime-1/sse",
	} {
		if !strings.Contains(applyOutput, expected) {
			t.Fatalf("apply output %q is missing %q", applyOutput, expected)
		}
	}
}

// TestSDKPlanPreservesAppAuthReference proves generated apps use the same exact JSON reference transport as MCP.
func TestSDKPlanPreservesAppAuthReference(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "sdk.yaml", `
apiVersion: fused/v1
kind: sdk
name: github-client
version: 1.0.0
language: typescript
services:
  github:
    version: "2026-07-01"
    operations: [reposList]
    auth:
      type: oidc
      name: githubOIDC
      ref: "${bucket.auth.github-app.sharedOIDC}"
`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sdk-config/plan" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		config := body["config"].(map[string]any)
		auth := config["services"].(map[string]any)["github"].(map[string]any)["auth"].(map[string]any)
		// Plan must carry only the opaque reference, never a locally resolved credential value.
		if auth["ref"] != "${bucket.auth.github-app.sharedOIDC}" {
			t.Fatalf("SDK plan auth ref = %#v", auth)
		}
		_, _ = w.Write([]byte(`{"plan_id":"plan-sdk","config_key":"sdk:github-client:1.0.0","source_hash":"` + body["source_hash"].(string) + `","summary":{}}`))
	}))
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"sdk", "plan", "-f", path})
}

// TestWorkspaceApplyUsesReceiptWithoutReplanning verifies receipts and generic secret material share one apply request.
func TestWorkspaceApplyUsesReceiptWithoutReplanning(t *testing.T) {
	t.Setenv("FUSED_TEST_WEBHOOK_SECRET", "resolved-webhook-secret")
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: [{version: "2026-07-01"}]
buckets:
  default:
    secrets:
      webhook_signing: $FUSED_TEST_WEBHOOK_SECRET
`)
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var sawApply bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			// workspace apply checks the Engine's environment label (Task 8)
			// before applying; "staging" here keeps this test's assertions
			// scoped to the receipt/apply behavior it's actually testing.
			w.Write([]byte(`{"status":"ok","plane":"engine","environment":"staging"}`))
			return
		}
		if r.URL.Path == "/workspace/config/plan" {
			t.Fatal("apply must not re-plan when a receipt exists")
		}
		if r.URL.Path != "/workspace/config/apply" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		sawApply = true
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["plan_id"] != "plan-workspace" || body["source_hash"] != parsed.SourceHash {
			t.Fatalf("unexpected apply body %#v", body)
		}
		// Retired provider-auth material must remain absent from the apply contract.
		if _, present := body["auth_materials"]; present {
			t.Fatalf("workspace apply retained auth_materials: %#v", body)
		}
		// Generic named secrets remain supported for webhook and similar consumers.
		bucketSecrets, ok := body["bucket_secret_materials"].(map[string]any)
		if !ok || bucketSecrets["default\x00webhook_signing"] != "resolved-webhook-secret" {
			t.Fatalf("workspace apply generic secrets = %#v", body)
		}
		_, _ = w.Write([]byte(`{"status":"applied","plan_id":"plan-workspace"}`))
	}))
	defer server.Close()
	writeReceipt(t, dir, planReceipt{ConfigKey: "workspace", PlanID: "plan-workspace", SourceHash: parsed.SourceHash, EngineURL: server.URL})

	runCommandInDir(t, dir, server.URL, []string{"workspace", "apply", "-f", path})
	if !sawApply {
		t.Fatal("expected workspace apply request")
	}
}

func TestAggregatePlanOrdersWorkspaceBeforeSDK(t *testing.T) {
	dir := t.TempDir()
	writeSprintConfig(t, dir, ".fused/workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: [{version: "2026-07-01"}]
`)
	writeSprintConfig(t, dir, ".fused/sdks/security.yaml", `
apiVersion: fused/v1
kind: sdk
name: security
version: "1.0.0"
language: typescript
services:
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`)
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/workspace/config/plan":
			_, _ = w.Write([]byte(`{"plan_id":"plan-workspace","config_key":"workspace","source_hash":"hash","base_generation":0,"summary":{}}`))
		case "/sdk-config/plan":
			_, _ = w.Write([]byte(`{"plan_id":"plan-sdk","config_key":"sdk:security:1.0.0","source_hash":"hash","base_generation":0,"summary":{}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"plan"})
	if got := strings.Join(paths, ","); got != "/workspace/config/plan,/sdk-config/plan" {
		t.Fatalf("expected workspace then sdk plan, got %s", got)
	}
}

func TestSDKPlanPrintsNotificationsFromPlanResponse(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "security.yaml", `
apiVersion: fused/v1
kind: sdk
name: security
version: "1.0.0"
language: typescript
services:
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sdk-config/plan" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"plan_id":"plan-sdk",
				"config_key":"sdk:security:1.0.0",
			"source_hash":"hash",
			"base_generation":0,
			"summary":{},
			"required_permissions":[{
				"permission":"service.manage",
				"resource_type":"service",
				"resource_id":"00000000-0000-0000-0000-000000000001",
				"display_name":"Okta"
			}],
			"notifications":{
				"items":[{
					"id":"registry:drift-1",
					"source":"registry",
					"type":"endpoint_drift",
					"severity":"breaking",
					"status":"pending",
					"service_id":"00000000-0000-0000-0000-000000000001",
					"message":"Endpoint drift detected for listLogEvents"
				}],
				"warnings":["registry_notifications_unavailable"]
			}
		}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"sdk", "plan", "-f", path})
	if !strings.Contains(out, "Workspace notifications for sdk:security:1.0.0") {
		t.Fatalf("expected notification heading, got %q", out)
	}
	if !strings.Contains(out, "Endpoint drift detected for listLogEvents") {
		t.Fatalf("expected drift notification, got %q", out)
	}
	if !strings.Contains(out, "registry_notifications_unavailable") {
		t.Fatalf("expected notification warning, got %q", out)
	}
}

// TestWorkspacePlanPrintsNotificationsFromPlanResponse is
// TestSDKPlanPrintsNotificationsFromPlanResponse's workspace counterpart --
// kind: workspace was previously the one plan response with no
// "notifications" key at all (see plans/plan-service-changelog.md's
// "## Phase 4"); ConfigPlanResponse now decodes it and planOneConfig's
// KindWorkspace case wires it through exactly like KindSDK/KindMCP already
// do, so printNotificationInbox needs no changes of its own to pick it up.
func TestWorkspacePlanPrintsNotificationsFromPlanResponse(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: [{version: "2026-07-01"}]
`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workspace/config/plan" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"plan_id":"plan-workspace",
			"config_key":"workspace",
			"source_hash":"hash",
			"base_generation":0,
			"summary":{},
			"required_permissions":[{
				"permission":"service.manage",
				"resource_type":"service",
				"resource_id":"00000000-0000-0000-0000-000000000001",
				"display_name":"Okta"
			}],
			"notifications":{
				"items":[{
					"id":"engine:note-1",
					"source":"engine",
					"type":"registry_version_deprecated",
					"severity":"non-breaking",
					"status":"pending",
					"service_id":"00000000-0000-0000-0000-000000000001",
					"message":"Version 2026-07-01 was deprecated"
				}],
				"warnings":[]
			}
		}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "plan", "-f", path})
	if !strings.Contains(out, "Workspace notifications for workspace") {
		t.Fatalf("expected notification heading, got %q", out)
	}
	if !strings.Contains(out, "Version 2026-07-01 was deprecated") {
		t.Fatalf("expected the registry_version_deprecated notification, got %q", out)
	}
	if !strings.Contains(out, `Ability to manage service "Okta"`) || strings.Contains(out, "service.manage") {
		t.Fatalf("expected required permission preview, got %q", out)
	}
}

func TestSDKPlanResolvesFusedSDKShortcutAndPrintsConfigPath(t *testing.T) {
	dir := t.TempDir()
	writeSprintConfig(t, dir, ".fused/sdks/security.yaml", `
apiVersion: fused/v1
kind: sdk
name: security
version: "1.0.0"
language: typescript
services:
  okta:
    operations: ["listLogEvents"]
`)
	writeSprintConfig(t, dir, ".fused/sdks/other.yaml", `
apiVersion: fused/v1
kind: sdk
name: other
version: "1.0.0"
language: typescript
services:
  github:
    operations: ["listRepos"]
`)
	var planCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		planCount++
		if r.URL.Path != "/sdk-config/plan" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["config_key"] != "sdk:security:1.0.0" {
			t.Fatalf("expected shortcut to select security only, got %#v", body["config_key"])
		}
		_, _ = w.Write([]byte(`{"plan_id":"plan-sdk","config_key":"sdk:security:1.0.0","source_hash":"` + body["source_hash"].(string) + `","base_generation":0,"summary":{}}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"sdk", "plan", "-f", "security.yaml"})

	if planCount != 1 {
		t.Fatalf("expected one SDK plan request, got %d", planCount)
	}
	if !strings.Contains(out, "Using config: .fused/sdks/security.yaml") {
		t.Fatalf("expected resolved config path in output, got %q", out)
	}
}

func TestSDKAddAndRemoveOperationWriteYaml(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "fused.yaml", `
apiVersion: fused/v1
kind: sdk
name: security
version: "1.0.0"
language: typescript
services:
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`)
	runCommandInDir(t, dir, "", []string{"sdk", "operation", "add", "okta", "getUser", "listGroups", "-f", path})
	afterAdd, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(afterAdd, []byte("getUser")) || !bytes.Contains(afterAdd, []byte("listGroups")) {
		t.Fatalf("expected getUser and listGroups in YAML:\n%s", string(afterAdd))
	}

	runCommandInDir(t, dir, "", []string{"sdk", "operation", "remove", "okta", "listLogEvents", "getUser", "-f", path})
	afterRemove, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(afterRemove, []byte("listLogEvents")) || bytes.Contains(afterRemove, []byte("getUser")) {
		t.Fatalf("expected listLogEvents and getUser removed from YAML:\n%s", string(afterRemove))
	}
	if !bytes.Contains(afterRemove, []byte("listGroups")) {
		t.Fatalf("expected listGroups to remain in YAML:\n%s", string(afterRemove))
	}
}

func TestSDKAddServiceWritesYaml(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "fused.yaml", `
apiVersion: fused/v1
kind: sdk
name: security
version: "1.0.0"
language: typescript
services:
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`)
	runCommandInDir(t, dir, "", []string{"sdk", "service", "add", "github", "-f", path, "--version", "2026-06-15"})

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(after, []byte("github:")) || !bytes.Contains(after, []byte("2026-06-15")) {
		t.Fatalf("expected github service in YAML:\n%s", string(after))
	}

	runCommandInDir(t, dir, "", []string{"sdk", "service", "remove", "github", "-f", path})
	afterRemove, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(afterRemove, []byte("github:")) {
		t.Fatalf("expected github service removed from YAML:\n%s", string(afterRemove))
	}
}

func TestSDKServiceHelpShowsExplicitActions(t *testing.T) {
	dir := t.TempDir()
	out := runCommandInDirOutput(t, dir, "", []string{"sdk", "service", "--help"})
	if !strings.Contains(out, "add") || !strings.Contains(out, "remove") {
		t.Fatalf("expected explicit sdk service subcommands in help, got %q", out)
	}
}

func TestSDKValidateLoadsOnlySDKConfigs(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "fused.yaml", `
apiVersion: fused/v1
kind: sdk
name: security
version: "1.0.0"
language: typescript
services:
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`)
	out := runCommandInDirOutput(t, dir, "", []string{"sdk", "validate", "-f", path})
	if !strings.Contains(out, "validated 1 sdk config") {
		t.Fatalf("expected sdk validation summary, got %q", out)
	}
}

func TestWorkspaceServicesListCallsEngine(t *testing.T) {
	dir := t.TempDir()
	var sawList bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writeEngineWorkspaceServices(t, w, r, `[{"service_id":"00000000-0000-0000-0000-000000000001","service_name":"okta","version":"2026-07-01","enabled_versions":[{"version":"2026-07-01"},{"version":"2026-07-02"}]}]`) {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		sawList = true
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "services", "list"})
	if !sawList {
		t.Fatal("expected workspace services request")
	}
	if !strings.Contains(out, "okta") || !strings.Contains(out, "2026-07-01") || !strings.Contains(out, "2026-07-01, 2026-07-02") {
		t.Fatalf("expected service output, got %q", out)
	}
}

func TestWorkspaceServicesListInteractiveCallsEngine(t *testing.T) {
	dir := t.TempDir()
	var sawList bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writeEngineWorkspaceServices(t, w, r, `[{"service_id":"00000000-0000-0000-0000-000000000001","service_name":"okta","version":"2026-07-01","enabled_versions":[{"version":"2026-07-01"},{"version":"2026-07-02"}]}]`) {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		sawList = true
	}))
	defer server.Close()

	oldInput := workspaceInput
	workspaceInput = strings.NewReader("1\n")
	t.Cleanup(func() { workspaceInput = oldInput })

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "services", "list", "--interactive"})
	if !sawList {
		t.Fatal("expected workspace services request")
	}
	if !strings.Contains(out, "1. okta") || !strings.Contains(out, "Enabled Versions for okta: 2026-07-01, 2026-07-02") {
		t.Fatalf("expected interactive service output, got %q", out)
	}
}

func TestWorkspaceServiceVersionsUsesSlugResolvedServiceID(t *testing.T) {
	dir := t.TempDir()
	state := &workspaceServiceVersionsSlugState{}
	server := httptest.NewServer(workspaceServiceVersionsSlugHandler(t, state))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "versions", "@acme-inc/github"})
	if !state.sawGraphQL || !state.sawWorkspaceList {
		t.Fatalf("expected graphql and workspace requests, saw graphql=%v workspace=%v", state.sawGraphQL, state.sawWorkspaceList)
	}
	if !strings.Contains(out, "2026-07-01") || !strings.Contains(out, "ver-1") {
		t.Fatalf("expected resolved workspace version output, got %q", out)
	}
	if strings.Contains(out, "ver-other") {
		t.Fatalf("expected service_id match to exclude other service, got %q", out)
	}
}

// TestWorkspaceServiceVersionsRejectsUnapprovedService keeps Registry
// visibility separate from Engine workspace approval.
func TestWorkspaceServiceVersionsRejectsUnapprovedService(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/graphql":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"service":{"id":"svc-github"}}}`))
		case "/engine/graphql":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"workspaceServices":[{"service_id":"svc-other","service_name":"Other","enabled_versions":[]}]}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	errText := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "versions", "@acme-inc/github"})
	if !strings.Contains(errText, "is not enabled in this workspace") {
		t.Fatalf("expected workspace approval rejection, got %q", errText)
	}
}

type workspaceServiceVersionsSlugState struct {
	sawGraphQL       bool
	sawWorkspaceList bool
}

func workspaceServiceVersionsSlugHandler(t *testing.T, state *workspaceServiceVersionsSlugState) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/graphql":
			state.sawGraphQL = true
			assertProviderQualifiedServiceLookupRequest(t, r)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"service":{"id":"svc-github"}}}`))
		case "/engine/graphql":
			state.sawWorkspaceList = true
			body := decodeTestGraphQLBody(t, r)
			if !strings.Contains(body.Query, "workspaceServices") {
				t.Fatalf("unexpected engine graphql query: %s", body.Query)
			}
			names, _ := body.Variables["names"].([]interface{})
			if len(names) != 1 || names[0] != "github" {
				t.Fatalf("expected server-filtered workspace lookup, got %#v", body.Variables)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"workspaceServices":[
				{"service_id":"svc-other","service_name":"GitHub","version":"2026-01-01","enabled_versions":[{"version":"2026-01-01","service_version_id":"ver-other"}]},
				{"service_id":"svc-github","service_name":"GitHub REST API","version":"2026-07-01","enabled_versions":[{"version":"2026-07-01","service_version_id":"ver-1","status":"public","enabled_at":"2026-07-16T00:00:00Z"}]}
			]}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}
}

func TestWorkspaceServiceOperationsDefaultsToLatestEnabledVersion(t *testing.T) {
	dir := t.TempDir()
	state := &workspaceOperationsLatestState{}
	server := httptest.NewServer(workspaceOperationsLatestHandler(t, state))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "operations", "@acme-inc/github"})
	if state.sawVersion != "2026-07-16" {
		t.Fatalf("expected latest enabled version, got %q", state.sawVersion)
	}
	if !strings.Contains(out, "reposListForOrg") || !strings.Contains(out, "issuesListForRepo") || !strings.Contains(out, "NAME") {
		t.Fatalf("expected operation output, got %q", out)
	}
}

type workspaceOperationsLatestState struct {
	sawVersion string
}

func workspaceOperationsLatestHandler(t *testing.T, state *workspaceOperationsLatestState) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/graphql":
			handleWorkspaceOperationsGraphQL(t, w, r, state)
		case "/engine/graphql":
			if !writeEngineWorkspaceServices(t, w, r, `[
				{"service_id":"svc-github","service_name":"GitHub REST API","version":"2026-01-01","enabled_versions":[
					{"version":"2026-01-01","service_version_id":"ver-old","created_at":"2026-01-01T00:00:00Z","enabled_at":"2026-01-02T00:00:00Z"},
					{"version":"2026-07-16","service_version_id":"ver-latest","created_at":"2026-07-16T00:00:00Z","enabled_at":"2026-07-17T00:00:00Z"}
				]}
			]`) {
				t.Fatalf("unexpected engine graphql query")
			}
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}
}

func handleWorkspaceOperationsGraphQL(t *testing.T, w http.ResponseWriter, r *http.Request, state *workspaceOperationsLatestState) {
	t.Helper()
	body := decodeTestGraphQLBody(t, r)
	if strings.Contains(body.Query, "GetServiceInfo") {
		assertProviderQualifiedServiceLookupVariables(t, body.Variables)
		_, _ = w.Write([]byte(`{"data":{"service":{"id":"svc-github"}}}`))
		return
	}
	state.sawVersion, _ = body.Variables["version"].(string)
	if body.Variables["serviceId"] != "svc-github" {
		t.Fatalf("unexpected operation list variables %#v", body.Variables)
	}
	_, _ = w.Write([]byte(`{"data":{"serviceOperations":[{"id":"ep1","name":"reposListForOrg","method":"GET","path":"/orgs/{org}/repos","description":"","service_id":"svc-github"},{"id":"ep2","name":"issuesListForRepo","method":"GET","path":"/repos/{owner}/{repo}/issues","description":"","service_id":"svc-github"}]}}`))
}

type testGraphQLBody struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func writeEngineWorkspaceServices(t *testing.T, w http.ResponseWriter, r *http.Request, servicesJSON string) bool {
	t.Helper()
	if r.URL.Path != "/engine/graphql" {
		return false
	}
	body := decodeTestGraphQLBody(t, r)
	if !strings.Contains(body.Query, "workspaceServices") {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"data":{"workspaceServices":` + servicesJSON + `}}`))
	return true
}

func assertProviderQualifiedServiceLookupRequest(t *testing.T, r *http.Request) {
	t.Helper()
	body := decodeTestGraphQLBody(t, r)
	if !strings.Contains(body.Query, "GetServiceInfo") {
		t.Fatalf("expected exact service lookup, got %s", body.Query)
	}
	assertProviderQualifiedServiceLookupVariables(t, body.Variables)
}

func decodeTestGraphQLBody(t *testing.T, r *http.Request) testGraphQLBody {
	t.Helper()
	var body testGraphQLBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode graphql body: %v", err)
	}
	return body
}

func assertProviderQualifiedServiceLookupVariables(t *testing.T, variables map[string]any) {
	t.Helper()
	if variables["id"] != "github" || variables["provider"] != "acme-inc" {
		t.Fatalf("expected provider-qualified slug split, got %#v", variables)
	}
}

func TestWorkspaceServiceOperationsUsesExplicitEnabledVersion(t *testing.T) {
	dir := t.TempDir()
	var sawVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/graphql":
			var body struct {
				Query     string         `json:"query"`
				Variables map[string]any `json:"variables"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode graphql body: %v", err)
			}
			if strings.Contains(body.Query, "GetServiceInfo") {
				_, _ = w.Write([]byte(`{"data":{"service":{"id":"svc-github"}}}`))
				return
			}
			sawVersion, _ = body.Variables["version"].(string)
			_, _ = w.Write([]byte(`{"data":{"serviceOperations":[{"id":"ep1","name":"reposListForOrg","method":"GET","path":"/orgs/{org}/repos","description":"","service_id":"svc-github"}]}}`))
		case "/engine/graphql":
			if !writeEngineWorkspaceServices(t, w, r, `[{"service_id":"svc-github","service_name":"GitHub REST API","version":"2026-07-16","enabled_versions":[{"version":"2026-01-01","service_version_id":"ver-old"},{"version":"2026-07-16","service_version_id":"ver-latest"}]}]`) {
				t.Fatalf("unexpected engine graphql query")
			}
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"workspace", "service", "operations", "github", "--version", "2026-01-01"})
	if sawVersion != "2026-01-01" {
		t.Fatalf("expected explicit version to be used, got %q", sawVersion)
	}
}

func TestWorkspaceServiceOperationsRejectsNonWorkspaceVersion(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/graphql":
			_, _ = w.Write([]byte(`{"data":{"service":{"id":"svc-github"}}}`))
		case "/engine/graphql":
			if !writeEngineWorkspaceServices(t, w, r, `[{"service_id":"svc-github","service_name":"GitHub REST API","version":"2026-07-16","enabled_versions":[{"version":"2026-07-16","service_version_id":"ver-latest"}]}]`) {
				t.Fatalf("unexpected engine graphql query")
			}
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	out := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "operations", "github", "--version", "2026-08-01"})
	if !strings.Contains(out, "not enabled in this workspace") {
		t.Fatalf("expected workspace-version rejection, got %q", out)
	}
}

func TestWorkspaceServiceUsesDiscoverableSubcommands(t *testing.T) {
	for _, name := range []string{"add", "connect", "delete", "deprecate", "operations", "versions", "webhooks", "version"} {
		if child, _, err := workspaceServiceCmd.Find([]string{name}); err != nil || child.Name() != name {
			t.Fatalf("expected %q subcommand, child=%v err=%v", name, child, err)
		}
	}
}

func TestWorkspaceServiceSlugHelpShowsReadableActions(t *testing.T) {
	dir := t.TempDir()
	out := runCommandInDirOutput(t, dir, "", []string{"workspace", "service", "--help"})
	if !strings.Contains(out, "Available Commands:") || !strings.Contains(out, "operations") || !strings.Contains(out, "versions") || !strings.Contains(out, "webhooks") || !strings.Contains(out, "add") {
		t.Fatalf("expected service actions in help, got %q", out)
	}
}

func TestWorkspaceHasCallsEngine(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/engine/graphql" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		body := decodeTestGraphQLBody(t, r)
		names, _ := body.Variables["names"].([]interface{})
		name := ""
		if len(names) > 0 {
			name, _ = names[0].(string)
		}
		if name == "okta" {
			_, _ = w.Write([]byte(`{"data":{"workspaceServices":[{"service_id":"00000000-0000-0000-0000-000000000001","service_name":"okta","version":"2026-07-01","enabled_versions":[{"version":"2026-07-01"},{"version":"2026-07-02"}]}]}}`))
		} else {
			_, _ = w.Write([]byte(`{"data":{"workspaceServices":[]}}`))
		}
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "has", "okta"})
	if !strings.Contains(out, "Found service okta (Enabled Versions: 2026-07-01, 2026-07-02)") {
		t.Fatalf("expected has command output for okta, got %q", out)
	}
}

// writeWorkspaceServiceLookupResponse returns a bounded live membership snapshot for force-removal tests.
func writeWorkspaceServiceLookupResponse(t *testing.T, w http.ResponseWriter, serviceID, serviceName string, versions ...string) {
	t.Helper()
	// Tests using this helper must describe at least one active version because the CLI is proving a removal target.
	if len(versions) == 0 {
		t.Fatal("workspace service lookup fixture requires a version")
	}
	enabled := make([]map[string]string, 0, len(versions))
	for _, version := range versions {
		enabled = append(enabled, map[string]string{"version": version, "service_version_id": "version-" + version})
	}
	payload := map[string]any{"data": map[string]any{"workspaceServices": []map[string]any{{
		"service_id": serviceID, "service_name": serviceName, "version": versions[0], "enabled_versions": enabled,
	}}}}
	// Encoding failures make the HTTP fixture unusable and should fail at their source.
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("encode workspace service lookup: %v", err)
	}
}

// TestWorkspaceVersionRemoveForceUpdatesPlanActionBeforeApply verifies one exact removal decision precedes apply.
func TestWorkspaceVersionRemoveForceUpdatesPlanActionBeforeApply(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: [{version: "2026-07-01"}, {version: "2026-08-01"}]
`)
	var paths []string
	var patchedActions []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/workspace/config/plan":
			_, _ = w.Write([]byte(`{
				"plan_id":"plan-workspace",
				"config_key":"workspace",
				"source_hash":"hash",
				"base_generation":1,
				"summary":{"actions":[{
					"id":"update_policy:00000000-0000-0000-0000-000000000999",
					"type":"update_policy",
					"service_id":"00000000-0000-0000-0000-000000000999",
					"version":"2026-07-01",
					"requires_decision":true
				},{
					"id":"disable_service_version:00000000-0000-0000-0000-000000000001:2026-07-01",
					"type":"disable_service_version",
					"service_id":"00000000-0000-0000-0000-000000000001",
					"version":"2026-07-01",
					"requires_decision":true
				}]}
			}`))
		case "/config/plans/plan-workspace/actions":
			if r.Method != http.MethodPatch {
				t.Fatalf("expected PATCH action update, got %s", r.Method)
			}
			var body struct {
				Actions []map[string]any `json:"actions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode actions body: %v", err)
			}
			patchedActions = body.Actions
			_, _ = w.Write([]byte(`{"status":"updated","plan_id":"plan-workspace","revision":2}`))
		case "/workspace/config/apply":
			_, _ = w.Write([]byte(`{"status":"applied","plan_id":"plan-workspace"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"workspace", "service", "version", "delete", "okta", "2026-07-01", "-f", path, "--force"})

	if got := strings.Join(paths, ","); got != "/workspace/config/plan,/config/plans/plan-workspace/actions,/workspace/config/apply" {
		t.Fatalf("unexpected request order %s", got)
	}
	if len(patchedActions) != 2 || patchedActions[1]["decision"] != "force_remove" || patchedActions[0]["decision"] == "force_remove" {
		t.Fatalf("expected force_remove action patch, got %#v", patchedActions)
	}
}

// TestWorkspaceFinalVersionForceDisclosesOwnedRegistryArchive verifies both
// final-version blockers are approved and their Registry consequence is shown unattended.
func TestWorkspaceFinalVersionForceDisclosesOwnedRegistryArchive(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, ".fused/workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  gmail:
    service_id: "00000000-0000-4000-8000-000000000001"
    versions: [{version: "v1"}]
`)
	var paths []string
	var plannedTargets []api.WorkspaceRemovalTarget
	var patchedActions []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/engine/graphql":
			// The final-version expansion is safe only after the CLI proves there is no live sibling.
			writeWorkspaceServiceLookupResponse(t, w, "00000000-0000-4000-8000-000000000001", "gmail", "v1")
		case "/workspace/config/plan":
			var body struct {
				RemoveTargets []api.WorkspaceRemovalTarget `json:"remove_targets"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode plan body: %v", err)
			}
			plannedTargets = body.RemoveTargets
			_, _ = w.Write([]byte(`{"plan_id":"plan-workspace","config_key":"workspace","source_hash":"hash","base_generation":0,"summary":{"actions":[{"id":"disable_service_version:00000000-0000-4000-8000-000000000001:v1","type":"disable_service_version","service_id":"00000000-0000-4000-8000-000000000001","version":"v1","explicit_removal":true,"requires_decision":true},{"id":"remove_service:00000000-0000-4000-8000-000000000001","type":"remove_service","service_id":"00000000-0000-4000-8000-000000000001","will_archive":true,"requires_decision":true}]}}`))
		case "/config/plans/plan-workspace/actions":
			// One replacement must carry every approved blocker so apply never observes a partial review.
			var body struct {
				Actions []map[string]any `json:"actions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode actions body: %v", err)
			}
			patchedActions = body.Actions
			_, _ = w.Write([]byte(`{"status":"updated","plan_id":"plan-workspace","revision":2}`))
		case "/workspace/config/apply":
			_, _ = w.Write([]byte(`{"status":"applied","plan_id":"plan-workspace"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldNoInput := NoInput
	// The unattended flag must not suppress disclosure of the Registry-side consequence.
	t.Cleanup(func() { NoInput = oldNoInput })
	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "version", "delete", "gmail", "v1", "--force", "--no-input"})
	// The complete governed sequence includes one read, one plan, one full decision replacement, and one apply.
	if got := strings.Join(paths, ","); got != "POST /engine/graphql,POST /workspace/config/plan,PATCH /config/plans/plan-workspace/actions,POST /workspace/config/apply" {
		t.Fatalf("unexpected final-version removal sequence %s", got)
	}
	// The explicit request remains exact even though the reviewed plan includes its equivalent whole-service consequence.
	if len(plannedTargets) != 1 || plannedTargets[0].ServiceID != "00000000-0000-4000-8000-000000000001" || plannedTargets[0].Version != "v1" {
		t.Fatalf("unexpected planned targets %#v", plannedTargets)
	}
	// Both blockers must be decided in one PATCH so no partially-authorized plan exists remotely.
	if len(patchedActions) != 2 || patchedActions[0]["decision"] != "force_remove" || patchedActions[1]["decision"] != "force_remove" {
		t.Fatalf("expected both final-removal blockers to be approved, got %#v", patchedActions)
	}
	// The exact version effect and owned-service Registry archive must both precede the completion message.
	versionEffect := "Plan effect: disable version v1 of service gmail in this workspace."
	archiveEffect := "Plan effect: archive owned service gmail from the Registry and remove it from this workspace."
	completion := "Deleted version v1 from service gmail"
	if !strings.Contains(out, versionEffect) || !strings.Contains(out, archiveEffect) || strings.Index(out, archiveEffect) > strings.Index(out, completion) {
		t.Fatalf("force removal output did not disclose archival before completion: %q", out)
	}
	after, err := os.ReadFile(path)
	// The successful remote apply must leave the corresponding local desired state readable.
	if err != nil {
		t.Fatal(err)
	}
	// Removing the final version must leave a valid empty services map on disk.
	if bytes.Contains(after, []byte("gmail:")) {
		t.Fatalf("owned final-version removal left gmail in config:\n%s", after)
	}
}

// TestWorkspaceFinalVersionForceRejectsLiveSibling fails before editing when local state would hide a live version.
func TestWorkspaceFinalVersionForceRejectsLiveSibling(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, ".fused/workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  gmail:
    service_id: "00000000-0000-4000-8000-000000000001"
    versions: [{version: "v1"}]
`)
	before, err := os.ReadFile(path)
	// Exact rollback comparison needs the accepted bytes, not a reparsed approximation.
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		// The live snapshot deliberately contains a sibling absent from the stale local document.
		if r.URL.Path == "/engine/graphql" {
			writeWorkspaceServiceLookupResponse(t, w, "00000000-0000-4000-8000-000000000001", "gmail", "v1", "v2")
			return
		}
		t.Fatalf("stale-sibling rejection should not reach %s", r.URL.Path)
	}))
	defer server.Close()

	errText := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "version", "delete", "gmail", "v1", "--force"})
	// The diagnostic should tell the operator to reconcile stale local state.
	if !strings.Contains(errText, "live versions outside requested version v1") {
		t.Fatalf("unexpected sibling error %q", errText)
	}
	// No plan or mutation request may follow a failed live-scope proof.
	if got := strings.Join(paths, ","); got != "POST /engine/graphql" {
		t.Fatalf("sibling rejection performed remote mutations: %s", got)
	}
	after, err := os.ReadFile(path)
	// A pre-edit rejection must preserve the original local document byte-for-byte.
	if err != nil {
		t.Fatal(err)
	}
	// Stale remote state cannot silently rewrite local intent.
	if !bytes.Equal(after, before) {
		t.Fatalf("sibling rejection changed local config:\n%s", after)
	}
}

// TestWorkspaceForceRemovalRejectsUnrelatedPlanWithoutPatch prevents partial approval of a broadened plan.
func TestWorkspaceForceRemovalRejectsUnrelatedPlanWithoutPatch(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  okta:
    service_id: "00000000-0000-4000-8000-000000000001"
    versions: [{version: "v1"}, {version: "v2"}]
`)
	before, err := os.ReadFile(path)
	// Exact rollback comparison needs the accepted bytes, not a reparsed approximation.
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		// A malformed or stale plan broadens the exact request with a sibling removal.
		if r.URL.Path == "/workspace/config/plan" {
			_, _ = w.Write([]byte(`{"plan_id":"plan-workspace","source_hash":"hash","summary":{"actions":[{"id":"disable_service_version:00000000-0000-4000-8000-000000000001:v1","type":"disable_service_version","service_id":"00000000-0000-4000-8000-000000000001","version":"v1","requires_decision":true},{"id":"disable_service_version:00000000-0000-4000-8000-000000000001:v2","type":"disable_service_version","service_id":"00000000-0000-4000-8000-000000000001","version":"v2","requires_decision":true}]}}`))
			return
		}
		t.Fatalf("broadened plan must be rejected before %s", r.URL.Path)
	}))
	defer server.Close()

	errText := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "version", "delete", "okta", "v1", "-f", path, "--force"})
	// Broadened receipt scope must produce an actionable review error.
	if !strings.Contains(errText, "unrelated removal") {
		t.Fatalf("unexpected broadened-plan error %q", errText)
	}
	// Full validation must finish before the CLI sends any decision replacement or apply.
	if got := strings.Join(paths, ","); got != "POST /workspace/config/plan" {
		t.Fatalf("broadened plan received a partial decision or apply: %s", got)
	}
	after, err := os.ReadFile(path)
	// Authoritative pre-apply rejection permits restoring the accepted bytes.
	if err != nil {
		t.Fatal(err)
	}
	// Rejected expanded scope must not survive as speculative local state.
	if !bytes.Equal(after, before) {
		t.Fatalf("broadened-plan rejection left speculative config:\n%s", after)
	}
}

// TestWorkspaceForceRemovalRestoresConfigWhenPlanOmitsTarget verifies old Engines fail closed without speculative local drift.
func TestWorkspaceForceRemovalRestoresConfigWhenPlanOmitsTarget(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, ".fused/workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  gmail:
    service_id: "00000000-0000-4000-8000-000000000001"
    versions: [{version: "v1"}]
`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Final-version scope verification precedes creation of the immutable plan.
		if r.URL.Path == "/engine/graphql" {
			writeWorkspaceServiceLookupResponse(t, w, "00000000-0000-4000-8000-000000000001", "gmail", "v1")
			return
		}
		// An older Engine ignores remove_targets and therefore returns no exact action.
		_, _ = w.Write([]byte(`{"plan_id":"plan-workspace","config_key":"workspace","source_hash":"hash","summary":{"actions":[]}}`))
	}))
	defer server.Close()

	errText := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "version", "delete", "gmail", "v1", "--force"})
	if !strings.Contains(errText, "upgrade the Engine") {
		t.Fatalf("unexpected old-Engine error %q", errText)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("old-Engine rejection left speculative config:\n%s", after)
	}
}

// TestWorkspaceForceRemovalRejectsMissingPlanHash verifies removal never infers receipt identity from local state.
func TestWorkspaceForceRemovalRejectsMissingPlanHash(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, ".fused/workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  gmail:
    service_id: "00000000-0000-4000-8000-000000000001"
    versions: [{version: "v1"}]
`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Final-version scope verification precedes creation of the immutable plan.
		if r.URL.Path == "/engine/graphql" {
			writeWorkspaceServiceLookupResponse(t, w, "00000000-0000-4000-8000-000000000001", "gmail", "v1")
			return
		}
		_, _ = w.Write([]byte(`{"plan_id":"plan-workspace","config_key":"workspace","summary":{"actions":[{"id":"disable_service_version:00000000-0000-4000-8000-000000000001:v1","type":"disable_service_version","service_id":"00000000-0000-4000-8000-000000000001","version":"v1","explicit_removal":true}]}}`))
	}))
	defer server.Close()

	errText := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "version", "delete", "gmail", "v1", "--force"})
	if !strings.Contains(errText, "without source_hash") {
		t.Fatalf("unexpected missing-hash error %q", errText)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("missing-hash rejection left speculative config:\n%s", after)
	}
}

// TestWorkspaceForceRemovalRestoresConfigOnAuthoritativeApplyRejection verifies negative commit proof permits rollback.
func TestWorkspaceForceRemovalRestoresConfigOnAuthoritativeApplyRejection(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, ".fused/workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  gmail:
    service_id: "00000000-0000-4000-8000-000000000001"
    versions: [{version: "v1"}]
`)
	before, _ := os.ReadFile(path)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Final-version scope verification precedes creation of the immutable plan.
		if r.URL.Path == "/engine/graphql" {
			writeWorkspaceServiceLookupResponse(t, w, "00000000-0000-4000-8000-000000000001", "gmail", "v1")
			return
		}
		if r.URL.Path == "/workspace/config/plan" {
			_, _ = w.Write([]byte(`{"plan_id":"plan-workspace","source_hash":"hash","summary":{"actions":[{"id":"disable_service_version:00000000-0000-4000-8000-000000000001:v1","type":"disable_service_version","service_id":"00000000-0000-4000-8000-000000000001","version":"v1","explicit_removal":true}]}}`))
			return
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"plan_stale","message":"plan stale","phase":"apply_admission","commit_state":"not_committed"}}`))
	}))
	defer server.Close()

	runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "version", "delete", "gmail", "v1", "--force"})
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, before) {
		t.Fatalf("authoritative apply rejection left speculative config:\n%s", after)
	}
}

func TestWorkspaceAddServiceWritesSlugOnlyYaml(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services: {}
`)
	server, registryCalls := newWorkspaceServiceDiscoveryServer(t,
		`[{"service_id":"00000000-0000-4000-8000-000000000001","service_name":"Okta","service_slug":"okta","version":"2026-07-01","enabled_versions":[]}]`,
		`[]`,
	)
	defer server.Close()
	runCommandInDir(t, dir, server.URL, []string{"workspace", "service", "add", "okta", "-f", path, "--version", "2026-07-01"})
	if *registryCalls != 0 {
		t.Fatalf("workspace hit should not search Registry, got %d calls", *registryCalls)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(after, []byte("okta:")) || !bytes.Contains(after, []byte("2026-07-01")) {
		t.Fatalf("expected okta service in YAML:\n%s", string(after))
	}
	if bytes.Contains(after, []byte("service_id:")) {
		t.Fatalf("expected slug-only YAML without service_id:\n%s", string(after))
	}
}

// TestSDKInteractiveAddOperationUsesWorkspaceAndVersion keeps complete operation discovery pinned to the configured service version.
func TestSDKInteractiveAddOperationUsesWorkspaceAndVersion(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "fused.yaml", `
apiVersion: fused/v1
kind: sdk
name: security
version: "1.0.0"
language: typescript
services:
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`)
	var sawVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engine/graphql":
			if !writeEngineWorkspaceServices(t, w, r, `[{"service_id":"00000000-0000-0000-0000-000000000001","service_name":"okta","version":"2026-07-01"}]`) {
				t.Fatalf("unexpected engine graphql query")
			}
		case "/graphql":
			var body struct {
				Variables map[string]any `json:"variables"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode graphql body: %v", err)
			}
			if body.Variables["serviceId"] != "00000000-0000-0000-0000-000000000001" {
				t.Fatalf("unexpected serviceId variable %#v", body.Variables)
			}
			sawVersion, _ = body.Variables["version"].(string)
			_, _ = w.Write([]byte(`{"data":{"serviceOperations":[{"id":"ep1","name":"getUser","method":"GET","path":"/users/{id}","description":"","service_id":"00000000-0000-0000-0000-000000000001"},{"id":"ep2","name":"listGroups","method":"GET","path":"/groups","description":"","service_id":"00000000-0000-0000-0000-000000000001"}]}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldInput := sdkInput
	sdkInput = strings.NewReader("")
	t.Cleanup(func() { sdkInput = oldInput })
	restoreSelector := stubSDKOperationSelectionRunner(t, func(_ io.Reader, _ io.Writer, _ string, _ string, endpoints []api.Integration) (sdkOperationSelection, error) {
		// Operation add expands the visible All operations choice into today's concrete Registry IDs.
		return sdkOperationSelection{operations: operationNames(endpoints), selectAll: true}, nil
	})
	t.Cleanup(restoreSelector)

	runCommandInDir(t, dir, server.URL, []string{"sdk", "operation", "add", "", "--interactive", "-f", path})
	if sawVersion != "2026-07-01" {
		t.Fatalf("expected versioned operation lookup, got %q", sawVersion)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(after, []byte("getUser")) || !bytes.Contains(after, []byte("listGroups")) {
		t.Fatalf("expected selected operations in YAML:\n%s", string(after))
	}
}

// TestSDKInteractiveAddOperationPromptsForService proves service choice precedes complete operation discovery for multi-service configs.
func TestSDKInteractiveAddOperationPromptsForService(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "fused.yaml", `
apiVersion: fused/v1
kind: sdk
name: security
version: "1.0.0"
language: typescript
services:
  github:
    version: "2026-06-15"
    operations: ["listRepositories"]
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engine/graphql":
			if !writeEngineWorkspaceServices(t, w, r, `[{"service_id":"00000000-0000-0000-0000-000000000001","service_name":"okta","version":"2026-07-01"}]`) {
				t.Fatalf("unexpected engine graphql query")
			}
		case "/graphql":
			_, _ = w.Write([]byte(`{"data":{"serviceOperations":[{"id":"ep1","name":"getUser","method":"GET","path":"/users/{id}","description":"","service_id":"00000000-0000-0000-0000-000000000001"},{"id":"ep2","name":"listGroups","method":"GET","path":"/groups","description":"","service_id":"00000000-0000-0000-0000-000000000001"}]}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldInput := sdkInput
	sdkInput = strings.NewReader("2\n")
	t.Cleanup(func() { sdkInput = oldInput })
	restoreSelector := stubSDKOperationSelectionRunner(t, func(_ io.Reader, _ io.Writer, _ string, _ string, endpoints []api.Integration) (sdkOperationSelection, error) {
		// The explicit Choose operations path returns only the selected operation IDs.
		return sdkOperationSelection{operations: operationNames(endpoints)}, nil
	})
	t.Cleanup(restoreSelector)

	runCommandInDir(t, dir, server.URL, []string{"sdk", "operation", "add", "", "--interactive", "-f", path})
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(after, []byte("getUser")) || !bytes.Contains(after, []byte("listGroups")) {
		t.Fatalf("expected selected okta operations in YAML:\n%s", string(after))
	}
}

func runCommandInDir(t *testing.T, dir, engineURL string, args []string) {
	t.Helper()
	_ = runCommandInDirOutput(t, dir, engineURL, args)
}

func runCommandInDirOutput(t *testing.T, dir, engineURL string, args []string) string {
	t.Helper()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	oldEngineURL, oldAPIKey, oldConfigFile := EngineURL, APIKey, ConfigFile
	t.Cleanup(func() {
		EngineURL, APIKey, ConfigFile = oldEngineURL, oldAPIKey, oldConfigFile
	})
	EngineURL = engineURL
	APIKey = "fsk_test"
	ConfigFile = ""

	out := &bytes.Buffer{}
	oldStdout := os.Stdout
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = stdoutWriter
	t.Cleanup(func() { os.Stdout = oldStdout })

	resetHelpFlags(RootCmd)
	RootCmd.SetOut(out)
	RootCmd.SetErr(&bytes.Buffer{})
	RootCmd.SetArgs(args)
	if err := RootCmd.Execute(); err != nil {
		_ = stdoutWriter.Close()
		t.Fatalf("execute %v: %v", args, err)
	}
	_ = stdoutWriter.Close()
	stdoutBytes, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatal(err)
	}
	return out.String() + string(stdoutBytes)
}

func runCommandInDirExpectError(t *testing.T, dir, engineURL string, args []string) string {
	t.Helper()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	oldEngineURL, oldAPIKey, oldConfigFile := EngineURL, APIKey, ConfigFile
	t.Cleanup(func() {
		EngineURL, APIKey, ConfigFile = oldEngineURL, oldAPIKey, oldConfigFile
	})
	EngineURL = engineURL
	APIKey = "fsk_test"
	ConfigFile = ""

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	resetHelpFlags(RootCmd)
	RootCmd.SetOut(out)
	RootCmd.SetErr(errOut)
	RootCmd.SetArgs(args)
	err = RootCmd.Execute()
	if err == nil {
		t.Fatalf("expected command %v to fail", args)
	}
	return out.String() + errOut.String() + err.Error()
}

// resetHelpFlags clears reusable boolean flags across the entire command tree
// before each test invocation. Cobra's *cobra.Command values are
// package-level singletons shared by every test in this binary, and pflag
// does not reset a bool flag's value between Parse() calls -- it only sets
// flags that are actually present in the new args. So a test that invokes
// `<cmd> --help` leaves that command's help flag permanently "true" for
// every later test that reaches the same command without passing --help
// again, causing cobra to silently print help instead of running RunE. This
// makes every command execution start from the correct defaults regardless of
// what an earlier test in the same process did.
func resetHelpFlags(cmd *cobra.Command) {
	for _, name := range []string{"help", jsonOutputFlag} {
		if flag := cmd.Flags().Lookup(name); flag != nil {
			_ = flag.Value.Set("false")
			flag.Changed = false
		}
	}
	for _, child := range cmd.Commands() {
		resetHelpFlags(child)
	}
}

func writeSprintConfig(t *testing.T, dir, rel, body string) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readReceipt(t *testing.T, path string) planReceipt {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var receipt planReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	return receipt
}

func writeReceipt(t *testing.T, dir string, receipt planReceipt) {
	t.Helper()
	path := filepath.Join(dir, ".fused/.state/workspace.plan.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(receipt)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}
