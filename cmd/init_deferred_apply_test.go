package cmd

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnifiedInitDeferredApplyContinuation consumes init's saved receipts through the printed public commands.
func TestUnifiedInitDeferredApplyContinuation(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		enabled bool
		changed bool
	}{
		{name: "enabled_service", enabled: true},
		{name: "missing_service"},
		{name: "edited_config_rejects_receipt", enabled: true, changed: true},
	} {
		// Each case gets isolated desired state and an independent Engine generation counter.
		t.Run(scenario.name, func(t *testing.T) {
			directory := t.TempDir()
			withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
			fixture := &deferredApplyFixture{enabled: scenario.enabled, plans: map[string]map[string]any{}, archive: deferredSDKArchive(t)}
			// The fixture rejects app planning before workspace activation and validates receipt identity on apply.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fixture.serve(t, w, r)
			}))
			defer server.Close()
			oldURL, oldKey, oldConfig, oldNoInput, oldDownload := EngineURL, APIKey, ConfigFile, NoInput, sdkApplyDownload
			// Global Cobra options must not leak into neighboring command tests.
			t.Cleanup(func() {
				EngineURL, APIKey, ConfigFile, NoInput, sdkApplyDownload = oldURL, oldKey, oldConfig, oldNoInput, oldDownload
			})
			EngineURL, APIKey, ConfigFile, NoInput = server.URL, "fsk_test", "", true
			var output bytes.Buffer
			command := newUnifiedInitCommand()
			command.SetOut(&output)
			command.SetErr(&output)
			command.SetArgs([]string{"deferred-sdk", "--sdk", "--service", "linear", "--operation", "linear=issueUpdate", "--no-apply"})
			// Deferred initialization must return a real receipt without consuming it.
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			wantCalls := "workspace-plan"
			// An active workspace permits init to save the app receipt immediately.
			if scenario.enabled {
				wantCalls = "sdk-plan"
			}
			// No apply, generation, or download is allowed before the continuation commands run.
			if strings.Join(fixture.calls, ",") != wantCalls {
				t.Fatalf("init calls=%v, want %s", fixture.calls, wantCalls)
			}
			appPath := filepath.Join(directory, ".fused", "sdks", "deferred-sdk.yaml")
			// A meaningful YAML edit must invalidate the saved receipt instead of silently replanning.
			if scenario.changed {
				contents, err := os.ReadFile(appPath)
				// A missing generated config is itself a failed initialization.
				if err != nil {
					t.Fatal(err)
				}
				contents = bytes.ReplaceAll(contents, []byte("issueUpdate"), []byte("issueCreate"))
				// Only this test-owned config is changed to exercise source-hash admission.
				if err := os.WriteFile(appPath, contents, 0600); err != nil {
					t.Fatal(err)
				}
			}
			commandCount := 0
			for _, line := range strings.Split(output.String(), "\n") {
				line = strings.TrimSpace(line)
				// Execute only the continuation command lines, not status text or comments.
				if !strings.HasPrefix(line, "fused-cli ") {
					continue
				}
				args := strings.Fields(strings.TrimPrefix(line, "fused-cli "))
				for index := range args {
					// Fixture paths contain no whitespace; remove their emitted shell quotes before Cobra parsing.
					args[index] = strings.Trim(args[index], "'")
				}
				commandCount++
				// A changed source must fail locally without consuming the old Engine receipt.
				if scenario.changed {
					result := runCommandInDirExpectError(t, directory, server.URL, args)
					// The diagnostic must identify source drift instead of reporting a remote apply failure.
					if !strings.Contains(result, "config changed since plan") {
						t.Fatalf("unexpected changed-config error: %s", result)
					}
					continue
				}
				runCommandInDirOutput(t, directory, server.URL, args)
			}
			// Already-planned apps need one command; missing services require workspace apply then app plan/apply.
			if scenario.enabled && commandCount != 1 || !scenario.enabled && commandCount != 3 {
				t.Fatalf("unexpected continuation command count: %d", commandCount)
			}
			// Rejected receipts must never start remote work or create a package directory.
			if scenario.changed {
				// The local hash guard must stop before any additional lifecycle request.
				if strings.Join(fixture.calls, ",") != wantCalls {
					t.Fatalf("changed config reached lifecycle endpoints: %v", fixture.calls)
				}
				return
			}
			wantCalls = "sdk-plan,sdk-apply,generation,download"
			// Workspace activation is required exactly once, before the deferred app plan.
			if !scenario.enabled {
				wantCalls = "workspace-plan,workspace-apply," + wantCalls
			}
			// Exact ordering proves saved plans were applied rather than replaced by hidden replanning.
			if strings.Join(fixture.calls, ",") != wantCalls {
				t.Fatalf("continuation calls=%v, want %s", fixture.calls, wantCalls)
			}
			contents, err := os.ReadFile(filepath.Join(directory, "fused-sdks", "deferred-sdk", "README.md"))
			// Verify real extracted bytes, not merely the existence of an empty download directory.
			if err != nil || string(contents) != "deferred SDK package" {
				t.Fatalf("downloaded package=%q, err=%v", contents, err)
			}
		})
	}
}

type deferredApplyFixture struct {
	enabled bool
	calls   []string
	plans   map[string]map[string]any
	archive []byte
}

// serve models workspace activation and immutable app generation while enforcing exact plan/hash reuse.
func (fixture *deferredApplyFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	// Lifecycle endpoints are deliberately stateful so incorrect ordering cannot pass.
	switch r.URL.Path {
	case "/health":
		fmt.Fprint(w, `{"environment":"development"}`)
	case "/workspace/config/plan", "/sdk-config/plan", "/workspace/config/apply", "/sdk-config/apply":
		resource := "sdk"
		// Workspace and app receipts must remain separate audit authorities.
		if strings.HasPrefix(r.URL.Path, "/workspace/") {
			resource = "workspace"
		}
		action := filepath.Base(r.URL.Path)
		fixture.calls = append(fixture.calls, resource+"-"+action)
		var body map[string]any
		// Malformed requests cannot count as successful receipt consumption.
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		// App plans require the actual workspace snapshot, not merely local desired state.
		if resource == "sdk" && !fixture.enabled {
			t.Error("app lifecycle attempted before workspace activation")
			http.Error(w, "workspace inactive", http.StatusConflict)
			return
		}
		// Save the request hash so apply must consume exactly this plan, not any arbitrary plan identifier.
		if action == "plan" {
			fixture.plans[resource] = body
			fmt.Fprintf(w, `{"plan_id":%q,"config_key":%q,"source_hash":%q,"summary":{}}`, "plan-"+resource, body["config_key"], body["source_hash"])
			return
		}
		plan, exists := fixture.plans[resource]
		// Reject missing or mismatched receipts at the same authority boundary as Engine.
		if !exists || body["plan_id"] != "plan-"+resource || body["source_hash"] != plan["source_hash"] {
			t.Errorf("apply did not consume saved plan: body=%v plan=%v", body, plan)
			http.Error(w, "receipt mismatch", http.StatusConflict)
			return
		}
		// Only an accepted workspace apply activates services for later app planning.
		if resource == "workspace" {
			fixture.enabled = true
			fmt.Fprint(w, `{"status":"applied","plan_id":"plan-workspace"}`)
			return
		}
		fmt.Fprint(w, `{"status":"applied","plan_id":"plan-sdk","app_family_id":"family-1","app_id":"app-1","job_id":"job-1"}`)
	case "/sdk-config/generation/app-1":
		fixture.calls = append(fixture.calls, "generation")
		fmt.Fprint(w, `{"status":"complete","app_family_id":"family-1","app_id":"app-1","job_id":"job-1"}`)
	case "/sdks/app-1/download":
		fixture.calls = append(fixture.calls, "download")
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(fixture.archive)
	case "/engine/graphql", "/graphql":
		var body struct{ Query string }
		// GraphQL fixtures still decode the real CLI request envelope.
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		// Registry resolution stays independent of Engine activation state.
		if r.URL.Path == "/graphql" {
			writeSDKInitRegistryGraphQL(t, w, body.Query)
			return
		}
		// Reflect accepted workspace applies in subsequent discovery reads.
		if strings.Contains(body.Query, "WorkspaceServices") && fixture.enabled {
			fmt.Fprint(w, `{"data":{"workspaceServicePage":{"data":[{"service_id":"00000000-0000-4000-8000-000000000001","service_name":"Linear","service_slug":"linear","version":"v1","enabled_versions":[{"version":"v1"}]}],"total":1}}}`)
			return
		}
		writeSDKInitEngineGraphQL(t, w, body.Query)
	default:
		// Unknown routes reveal unintended work rather than receiving a permissive success response.
		t.Errorf("unexpected continuation endpoint: %s", r.URL.Path)
		http.NotFound(w, r)
	}
}

// deferredSDKArchive supplies a nonempty package to verify download extraction after receipt consumption.
func deferredSDKArchive(t *testing.T) []byte {
	t.Helper()
	var contents bytes.Buffer
	writer := zip.NewWriter(&contents)
	file, err := writer.Create("README.md")
	// An invalid fixture archive must not be mistaken for a lifecycle failure.
	if err != nil {
		t.Fatal(err)
	}
	// Real bytes distinguish package extraction from directory creation alone.
	if _, err := file.Write([]byte("deferred SDK package")); err != nil {
		t.Fatal(err)
	}
	// Closing writes the ZIP directory needed by the production extractor.
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return contents.Bytes()
}
