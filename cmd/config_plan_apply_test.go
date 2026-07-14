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
		if r.URL.Path == "/workspace/notifications" {
			t.Fatal("sdk plan must use notifications embedded in the plan response")
		}
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
	runCommandInDir(t, dir, "", []string{"sdk", "add-operation", "okta", "getUser", "listGroups", "-f", path})
	afterAdd, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(afterAdd, []byte("getUser")) || !bytes.Contains(afterAdd, []byte("listGroups")) {
		t.Fatalf("expected getUser and listGroups in YAML:\n%s", string(afterAdd))
	}

	runCommandInDir(t, dir, "", []string{"sdk", "remove-operation", "okta", "listLogEvents", "getUser", "-f", path})
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
	runCommandInDir(t, dir, "", []string{"sdk", "add-service", "github", "-f", path, "--version", "2026-06-15"})

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(after, []byte("github:")) || !bytes.Contains(after, []byte("2026-06-15")) {
		t.Fatalf("expected github service in YAML:\n%s", string(after))
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
		if r.URL.Path != "/workspace/services" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		sawList = true
		_, _ = w.Write([]byte(`[{"service_id":"00000000-0000-0000-0000-000000000001","service_name":"okta","version":"2026-07-01"}]`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "services", "list"})
	if !sawList {
		t.Fatal("expected workspace services request")
	}
	if !strings.Contains(out, "okta") || !strings.Contains(out, "2026-07-01") {
		t.Fatalf("expected service output, got %q", out)
	}
}

func TestWorkspaceAddServiceWritesSlugOnlyYaml(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `
kind: workspace
version: 1
services: {}
`)
	runCommandInDir(t, dir, "", []string{"workspace", "service", "add", "okta", "-f", path, "--version", "2026-07-01"})

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
		case "/workspace/services":
			_, _ = w.Write([]byte(`[{"service_id":"00000000-0000-0000-0000-000000000001","service_name":"okta","version":"2026-07-01"}]`))
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

	runCommandInDir(t, dir, server.URL, []string{"sdk", "add-operation", "--interactive", "-f", path})
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
		case "/workspace/services":
			_, _ = w.Write([]byte(`[{"service_id":"00000000-0000-0000-0000-000000000001","service_name":"okta","version":"2026-07-01"}]`))
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

	runCommandInDir(t, dir, server.URL, []string{"sdk", "add-operation", "--interactive", "-f", path})
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
