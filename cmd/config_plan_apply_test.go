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

	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

func TestWorkspacePlanWritesReceiptAndPostsToEngine(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
kind: workspace
version: 1
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
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

func TestWorkspacePlanRejectsLegacyConnectEnvFields(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
kind: workspace
version: 1
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
    runtime_config:
      connect:
        bucket: prod
        auth_type: oauth
        client_id_env: FUSED_TEST_CLIENT_ID
        client_secret_env: FUSED_TEST_CLIENT_SECRET
        redirect_uri: https://engine.example.com/connect/callback
`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("legacy *_env config must fail before posting to Engine")
	}))
	defer server.Close()

	out := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "plan", "-f", path})
	if !strings.Contains(out, "not *_env fields") {
		t.Fatalf("expected legacy *_env rejection, got %s", out)
	}
}

func TestWorkspacePlanPostsDollarConnectRefsWithoutResolvedSecrets(t *testing.T) {
	t.Setenv("FUSED_TEST_CLIENT_ID", "resolved-client")
	t.Setenv("FUSED_TEST_CLIENT_SECRET", "resolved-secret")
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
kind: workspace
version: 1
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
    runtime_config:
      connect:
        bucket: prod
        auth_type: oauth
        client_id: $FUSED_TEST_CLIENT_ID
        client_secret: $FUSED_TEST_CLIENT_SECRET
        redirect_uri: https://engine.example.com/connect/callback
`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		config := body["config"].(map[string]any)
		service := config["services"].(map[string]any)["github"].(map[string]any)
		connect := service["runtime_config"].(map[string]any)["connect"].(map[string]any)
		if connect["client_id"] != "$FUSED_TEST_CLIENT_ID" || connect["client_secret"] != "$FUSED_TEST_CLIENT_SECRET" {
			t.Fatalf("expected dollar refs in plan payload, got %#v", connect)
		}
		encoded, _ := json.Marshal(connect)
		if strings.Contains(string(encoded), "resolved-secret") {
			t.Fatalf("resolved client_secret must not be posted during plan: %#v", connect)
		}
		_, _ = w.Write([]byte(`{"plan_id":"plan-workspace","config_key":"workspace","source_hash":"` + body["source_hash"].(string) + `","base_generation":0,"summary":{}}`))
	}))
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"workspace", "plan", "-f", path})
}

func TestWorkspaceApplyPostsConnectMaterialsFromDollarRefs(t *testing.T) {
	t.Setenv("FUSED_TEST_CLIENT_ID", "resolved-client")
	t.Setenv("FUSED_TEST_CLIENT_SECRET", "resolved-secret")
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
kind: workspace
version: 1
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
    runtime_config:
      connect:
        bucket: prod
        auth_type: oauth
        client_id: $FUSED_TEST_CLIENT_ID
        client_secret: ${FUSED_TEST_CLIENT_SECRET}
        redirect_uri: https://engine.example.com/connect/callback
`)
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, dir, planReceipt{ConfigKey: "workspace", PlanID: "plan-workspace", SourceHash: parsed.SourceHash})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Write([]byte(`{"status":"ok","plane":"engine","environment":"staging"}`))
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		materials := body["connect_materials"].(map[string]any)["github"].(map[string]any)
		if materials["client_id"] != "resolved-client" || materials["client_secret"] != "resolved-secret" {
			t.Fatalf("expected resolved connect materials during apply, got %#v", materials)
		}
		_, _ = w.Write([]byte(`{"status":"applied","plan_id":"plan-workspace"}`))
	}))
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"workspace", "apply", "-f", path})
}

func TestWorkspaceApplyPostsStaticAuthMaterialsFromDollarRefs(t *testing.T) {
	t.Setenv("FUSED_BASIC_USER", "alice")
	t.Setenv("FUSED_BASIC_PASS", "s3cr3t")
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
kind: workspace
version: 1
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
    runtime_config:
      auth:
        bucket: prod
        auth_type: basic
        username: $FUSED_BASIC_USER
        password: ${FUSED_BASIC_PASS}
`)
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, dir, planReceipt{ConfigKey: "workspace", PlanID: "plan-workspace", SourceHash: parsed.SourceHash})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Write([]byte(`{"status":"ok","plane":"engine","environment":"staging"}`))
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		materials := body["auth_materials"].(map[string]any)["github"].(map[string]any)
		if materials["username"] != "alice" || materials["password"] != "s3cr3t" {
			t.Fatalf("expected resolved auth materials during apply, got %#v", materials)
		}
		_, _ = w.Write([]byte(`{"status":"applied","plan_id":"plan-workspace"}`))
	}))
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"workspace", "apply", "-f", path})
}

// TestWorkspaceApplyPostsMTLSAuthMaterialsFromDollarRefs proves cert/key env
// refs resolve only during apply, not during shareable plan creation.
func TestWorkspaceApplyPostsMTLSAuthMaterialsFromDollarRefs(t *testing.T) {
	t.Setenv("FUSED_CLIENT_CERT", "CERT-PEM")
	t.Setenv("FUSED_CLIENT_KEY", "KEY-PEM")
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
kind: workspace
version: 1
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
    runtime_config:
      auth:
        bucket: prod
        auth_type: mtls
        cert: $FUSED_CLIENT_CERT
        key: ${FUSED_CLIENT_KEY}
`)
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, dir, planReceipt{ConfigKey: "workspace", PlanID: "plan-workspace", SourceHash: parsed.SourceHash})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Write([]byte(`{"status":"ok","plane":"engine","environment":"staging"}`))
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		materials := body["auth_materials"].(map[string]any)["github"].(map[string]any)
		if materials["cert"] != "CERT-PEM" || materials["key"] != "KEY-PEM" {
			t.Fatalf("expected resolved mTLS auth materials during apply, got %#v", materials)
		}
		_, _ = w.Write([]byte(`{"status":"applied","plan_id":"plan-workspace"}`))
	}))
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"workspace", "apply", "-f", path})
}

func TestWorkspacePlanRejectsInlineStaticAuthMaterial(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
kind: workspace
version: 1
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
    runtime_config:
      auth:
        bucket: prod
        auth_type: basic
        username: alice
        password: s3cr3t
`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("inline static auth material must fail before posting to Engine")
	}))
	defer server.Close()

	out := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "plan", "-f", path})
	if !strings.Contains(out, "$ENV credential fields") {
		t.Fatalf("expected inline auth material rejection, got %s", out)
	}
}

func TestWorkspaceApplyUsesReceiptWithoutReplanning(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
kind: workspace
version: 1
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
`)
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, dir, planReceipt{ConfigKey: "workspace", PlanID: "plan-workspace", SourceHash: parsed.SourceHash})

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
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["plan_id"] != "plan-workspace" || body["source_hash"] != parsed.SourceHash {
			t.Fatalf("unexpected apply body %#v", body)
		}
		_, _ = w.Write([]byte(`{"status":"applied","plan_id":"plan-workspace"}`))
	}))
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"workspace", "apply", "-f", path})
	if !sawApply {
		t.Fatal("expected workspace apply request")
	}
}

func TestAggregatePlanOrdersWorkspaceBeforeSDK(t *testing.T) {
	dir := t.TempDir()
	writeSprintConfig(t, dir, ".fused/workspace.yaml", `
kind: workspace
version: 1
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
`)
	writeSprintConfig(t, dir, ".fused/sdks/security.yaml", `
kind: sdk
version: 1
name: security
sdkVersion: "1.0.0"
language: typescript
target: sdk
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
			_, _ = w.Write([]byte(`{"plan_id":"plan-sdk","config_key":"sdk:security","source_hash":"hash","base_generation":0,"summary":{}}`))
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
kind: sdk
version: 1
name: security
sdkVersion: "1.0.0"
language: typescript
target: sdk
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
			"config_key":"sdk:security",
			"source_hash":"hash",
			"base_generation":0,
			"summary":{},
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
	if !strings.Contains(out, "Workspace notifications for sdk:security") {
		t.Fatalf("expected notification heading, got %q", out)
	}
	if !strings.Contains(out, "Endpoint drift detected for listLogEvents") {
		t.Fatalf("expected drift notification, got %q", out)
	}
	if !strings.Contains(out, "registry_notifications_unavailable") {
		t.Fatalf("expected notification warning, got %q", out)
	}
}

func TestSDKPlanResolvesFusedSDKShortcutAndPrintsConfigPath(t *testing.T) {
	dir := t.TempDir()
	writeSprintConfig(t, dir, ".fused/sdks/security.yaml", `
kind: sdk
version: 1
name: security
sdkVersion: "1.0.0"
language: typescript
target: sdk
services:
  okta:
    operations: ["listLogEvents"]
`)
	writeSprintConfig(t, dir, ".fused/sdks/other.yaml", `
kind: sdk
version: 1
name: other
sdkVersion: "1.0.0"
language: typescript
target: sdk
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
		if body["config_key"] != "sdk:security" {
			t.Fatalf("expected shortcut to select security only, got %#v", body["config_key"])
		}
		_, _ = w.Write([]byte(`{"plan_id":"plan-sdk","config_key":"sdk:security","source_hash":"` + body["source_hash"].(string) + `","base_generation":0,"summary":{}}`))
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
kind: sdk
version: 1
name: security
sdkVersion: "1.0.0"
language: typescript
target: sdk
services:
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`)
	runCommandInDir(t, dir, "", []string{"sdk", "service", "okta", "add", "getUser", "listGroups", "-f", path})
	afterAdd, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(afterAdd, []byte("getUser")) || !bytes.Contains(afterAdd, []byte("listGroups")) {
		t.Fatalf("expected getUser and listGroups in YAML:\n%s", string(afterAdd))
	}

	runCommandInDir(t, dir, "", []string{"sdk", "service", "okta", "remove", "listLogEvents", "getUser", "-f", path})
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
kind: sdk
version: 1
name: security
sdkVersion: "1.0.0"
language: typescript
target: sdk
services:
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`)
	runCommandInDir(t, dir, "", []string{"sdk", "service", "github", "add", "-f", path, "--version", "2026-06-15"})

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(after, []byte("github:")) || !bytes.Contains(after, []byte("2026-06-15")) {
		t.Fatalf("expected github service in YAML:\n%s", string(after))
	}

	runCommandInDir(t, dir, "", []string{"sdk", "service", "github", "remove", "-f", path})
	afterRemove, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(afterRemove, []byte("github:")) {
		t.Fatalf("expected github service removed from YAML:\n%s", string(afterRemove))
	}
}

func TestSDKServiceActionCompletionAfterSlug(t *testing.T) {
	got, directive := completeSDKServiceArgs(sdkServiceCmd, []string{"okta"}, "ad")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("expected no-file completion directive, got %v", directive)
	}
	if len(got) != 1 || got[0] != "add" {
		t.Fatalf("expected add completion after slug, got %#v", got)
	}
}

func TestSDKServiceSlugHelpShowsReadableActions(t *testing.T) {
	dir := t.TempDir()
	out := runCommandInDirOutput(t, dir, "", []string{"sdk", "service", "okta", "--help"})
	if !strings.Contains(out, "service <service-slug> [add|remove] [operationId...]") {
		t.Fatalf("expected readable sdk service use in help, got %q", out)
	}
	if !strings.Contains(out, "add") || !strings.Contains(out, "remove") {
		t.Fatalf("expected sdk service actions in help, got %q", out)
	}
}

func TestSDKValidateLoadsOnlySDKConfigs(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "fused.yaml", `
kind: sdk
version: 1
name: security
sdkVersion: "1.0.0"
language: typescript
target: sdk
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

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "@acme-inc/github", "versions"})
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
			assertProviderQualifiedSlugRequest(t, r)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"serviceVersions":[{"id":"ver-1","service_id":"svc-github","name":"2026-07-01","status":"public","created_at":"2026-07-16T00:00:00Z"}]}}`))
		case "/engine/graphql":
			state.sawWorkspaceList = true
			body := decodeTestGraphQLBody(t, r)
			if !strings.Contains(body.Query, "workspaceServices") {
				t.Fatalf("unexpected engine graphql query: %s", body.Query)
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

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "@acme-inc/github", "operations"})
	if state.sawVersion != "2026-07-16" {
		t.Fatalf("expected latest enabled version, got %q", state.sawVersion)
	}
	if !strings.Contains(out, "reposListForOrg\tGET\t/orgs/{org}/repos") || !strings.Contains(out, "issuesListForRepo") {
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
	if strings.Contains(body.Query, "serviceVersions") {
		assertProviderQualifiedSlugVariables(t, body.Variables)
		_, _ = w.Write([]byte(`{"data":{"serviceVersions":[{"id":"ver-latest","service_id":"svc-github","name":"2026-07-16","status":"public","created_at":"2026-07-16T00:00:00Z"}]}}`))
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

func assertProviderQualifiedSlugRequest(t *testing.T, r *http.Request) {
	t.Helper()
	body := decodeTestGraphQLBody(t, r)
	assertProviderQualifiedSlugVariables(t, body.Variables)
}

func decodeTestGraphQLBody(t *testing.T, r *http.Request) testGraphQLBody {
	t.Helper()
	var body testGraphQLBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode graphql body: %v", err)
	}
	return body
}

func assertProviderQualifiedSlugVariables(t *testing.T, variables map[string]any) {
	t.Helper()
	if variables["serviceId"] != "github" || variables["provider"] != "acme-inc" {
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
			if strings.Contains(body.Query, "serviceVersions") {
				_, _ = w.Write([]byte(`{"data":{"serviceVersions":[{"id":"ver-old","service_id":"svc-github","name":"2026-01-01","status":"public","created_at":"2026-01-01T00:00:00Z"}]}}`))
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

	runCommandInDir(t, dir, server.URL, []string{"workspace", "service", "github", "operations", "--version", "2026-01-01"})
	if sawVersion != "2026-01-01" {
		t.Fatalf("expected explicit version to be used, got %q", sawVersion)
	}
}

func TestWorkspaceServiceOperationsRejectsNonWorkspaceVersion(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/graphql":
			_, _ = w.Write([]byte(`{"data":{"serviceVersions":[{"id":"ver-new","service_id":"svc-github","name":"2026-08-01","status":"public","created_at":"2026-08-01T00:00:00Z"}]}}`))
		case "/engine/graphql":
			if !writeEngineWorkspaceServices(t, w, r, `[{"service_id":"svc-github","service_name":"GitHub REST API","version":"2026-07-16","enabled_versions":[{"version":"2026-07-16","service_version_id":"ver-latest"}]}]`) {
				t.Fatalf("unexpected engine graphql query")
			}
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	out := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "github", "operations", "--version", "2026-08-01"})
	if !strings.Contains(out, "not enabled in this workspace") {
		t.Fatalf("expected workspace-version rejection, got %q", out)
	}
}

func TestWorkspaceServiceActionCompletionAfterSlug(t *testing.T) {
	got, directive := completeWorkspaceServiceArgs(workspaceServiceCmd, []string{"github"}, "op")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("expected no-file completion directive, got %v", directive)
	}
	if len(got) != 1 || got[0] != "operations" {
		t.Fatalf("expected operations completion after slug, got %#v", got)
	}
}

func TestWorkspaceServiceSlugHelpShowsReadableActions(t *testing.T) {
	dir := t.TempDir()
	out := runCommandInDirOutput(t, dir, "", []string{"workspace", "service", "github", "--help"})
	if !strings.Contains(out, "service <service-slug> [versions|operations|webhooks|add|connect|remove|deprecate|version]") {
		t.Fatalf("expected readable service use in help, got %q", out)
	}
	if !strings.Contains(out, "operations") || !strings.Contains(out, "versions") || !strings.Contains(out, "webhooks") || !strings.Contains(out, "add") {
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

func TestWorkspaceVersionRemoveForceUpdatesPlanActionBeforeApply(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
kind: workspace
version: 1
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01", "2026-08-01"]
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
					"id":"disable_service_version:00000000-0000-0000-0000-000000000999:2026-07-01",
					"type":"disable_service_version",
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

	runCommandInDir(t, dir, server.URL, []string{"workspace", "service", "okta", "version", "remove", "2026-07-01", "-f", path, "--version-force"})

	if got := strings.Join(paths, ","); got != "/workspace/config/plan,/config/plans/plan-workspace/actions,/workspace/config/apply" {
		t.Fatalf("unexpected request order %s", got)
	}
	if len(patchedActions) != 2 || patchedActions[1]["decision"] != "force_remove" || patchedActions[0]["decision"] == "force_remove" {
		t.Fatalf("expected force_remove action patch, got %#v", patchedActions)
	}
}

func TestWorkspaceAddServiceWritesSlugOnlyYaml(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
kind: workspace
version: 1
services: {}
`)
	runCommandInDir(t, dir, "", []string{"workspace", "service", "okta", "add", "-f", path, "--add-version", "2026-07-01"})

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

func TestSDKInteractiveAddOperationUsesWorkspaceAndVersion(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "fused.yaml", `
kind: sdk
version: 1
name: security
sdkVersion: "1.0.0"
language: typescript
target: sdk
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
			_, _ = w.Write([]byte(`{"data":{"searchEndpoints":[{"id":"ep1","name":"getUser","method":"GET","path":"/users/{id}","description":"","service_id":"00000000-0000-0000-0000-000000000001"},{"id":"ep2","name":"listGroups","method":"GET","path":"/groups","description":"","service_id":"00000000-0000-0000-0000-000000000001"}]}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldInput := sdkInput
	sdkInput = strings.NewReader("all\n")
	t.Cleanup(func() { sdkInput = oldInput })

	runCommandInDir(t, dir, server.URL, []string{"sdk", "service", "", "add", "--interactive", "-f", path})
	if sawVersion != "2026-07-01" {
		t.Fatalf("expected versioned endpoint search, got %q", sawVersion)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(after, []byte("getUser")) || !bytes.Contains(after, []byte("listGroups")) {
		t.Fatalf("expected selected operations in YAML:\n%s", string(after))
	}
}

func TestSDKInteractiveAddOperationPromptsForService(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "fused.yaml", `
kind: sdk
version: 1
name: security
sdkVersion: "1.0.0"
language: typescript
target: sdk
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
			_, _ = w.Write([]byte(`{"data":{"searchEndpoints":[{"id":"ep1","name":"getUser","method":"GET","path":"/users/{id}","description":"","service_id":"00000000-0000-0000-0000-000000000001"},{"id":"ep2","name":"listGroups","method":"GET","path":"/groups","description":"","service_id":"00000000-0000-0000-0000-000000000001"}]}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	oldInput := sdkInput
	sdkInput = strings.NewReader("2\n1-2\n")
	t.Cleanup(func() { sdkInput = oldInput })

	runCommandInDir(t, dir, server.URL, []string{"sdk", "service", "", "add", "--interactive", "-f", path})
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

// resetHelpFlags clears the "help" flag across the entire command tree
// before each test invocation. Cobra's *cobra.Command values are
// package-level singletons shared by every test in this binary, and pflag
// does not reset a bool flag's value between Parse() calls -- it only sets
// flags that are actually present in the new args. So a test that invokes
// `<cmd> --help` leaves that command's help flag permanently "true" for
// every later test that reaches the same command without passing --help
// again, causing cobra to silently print help instead of running RunE. This
// makes every command execution start from the correct default (help not
// requested) regardless of what an earlier test in the same process did.
func resetHelpFlags(cmd *cobra.Command) {
	if help := cmd.Flags().Lookup("help"); help != nil {
		_ = help.Value.Set("false")
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
