package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestUnifiedPromoteCommand exercises the actual command's discovery, mutation and structured receipt.
func TestUnifiedPromoteCommand(t *testing.T) {
	document := "apiVersion: fused/v1\nkind: unified_app\nname: Customer lookup\nversion: 2.0.0\nbucket: default\nservices:\n  crm:\n    version: v1\n    operations: [createCustomer]\nsource: 'export default buildUnifiedApp({});'\n"
	configPath := filepath.Join(t.TempDir(), "customer-app.yaml")
	// The local config version may differ from the explicit rollback destination.
	if err := os.WriteFile(configPath, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	const id = "11111111-1111-4111-8111-111111111111"
	calls := 0
	// The CLI resolves a named version, reads traffic state, then performs one explicit write.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// Human-readable selectors must use the exact Unified App namespace before any mutation.
		if r.URL.Path == "/engine/graphql" {
			var input struct {
				Variables map[string]string `json:"variables"`
			}
			_ = json.NewDecoder(r.Body).Decode(&input)
			if input.Variables["reference"] != "Customer lookup" || input.Variables["version"] != "1.0.0" || input.Variables["kind"] != "unified_app" {
				t.Errorf("wrong lookup: %#v", input.Variables)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"appReference": map[string]string{"id": id, "kind": "app"}}})
			return
		}
		// No implicit plan, apply, or unrelated request belongs in a retained-version switch.
		if r.URL.Path != "/apps/"+id+"/traffic" && r.URL.Path != "/apps/"+id+"/promote" {
			t.Errorf("unexpected route %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(api.UnifiedAppTraffic{AppFamilyID: id, ActiveAppID: id})
	}))
	defer server.Close()
	oldURL, oldKey, oldFile := EngineURL, APIKey, ConfigFile
	oldOut := unifiedPromoteCmd.OutOrStdout()
	oldVersion, _ := unifiedPromoteCmd.Flags().GetString("version")
	oldJSON, _ := unifiedPromoteCmd.Flags().GetBool(jsonOutputFlag)
	// Restore shared Cobra state so this test cannot affect other command tests.
	t.Cleanup(func() {
		EngineURL, APIKey, ConfigFile = oldURL, oldKey, oldFile
		_ = unifiedPromoteCmd.Flags().Set("version", oldVersion)
		unifiedPromoteCmd.SetOut(oldOut)
		_ = unifiedPromoteCmd.Flags().Set(jsonOutputFlag, "false")
		if oldJSON {
			_ = unifiedPromoteCmd.Flags().Set(jsonOutputFlag, "true")
		}
	})
	EngineURL, APIKey, ConfigFile = server.URL, "test-key", configPath
	var output bytes.Buffer
	unifiedPromoteCmd.SetOut(&output)
	_ = unifiedPromoteCmd.Flags().Set("version", "1.0.0")
	_ = unifiedPromoteCmd.Flags().Set(jsonOutputFlag, "true")
	// Execute the registered command callback rather than duplicating its API sequence in the test.
	if err := unifiedPromoteCmd.RunE(unifiedPromoteCmd, nil); err != nil {
		t.Fatal(err)
	}
	after, readErr := os.ReadFile(configPath)
	// Promotion must never rewrite the app declaration or its desired version.
	if readErr != nil || string(after) != document {
		t.Fatal("promotion changed the config")
	}
	var result unifiedPromotionOutput
	// Automation must receive valid JSON with the serving version rather than diagnostic prose.
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.ActiveAppID != id || result.Name != "Customer lookup" || result.Version != "1.0.0" || calls != 3 {
		t.Fatalf("output=%s calls=%d err=%v", output.String(), calls, err)
	}
	// Explicit names must work without a local config and use the same guarded promotion flow.
	calls = 0
	output.Reset()
	ConfigFile = filepath.Join(t.TempDir(), "missing.yaml")
	if err := unifiedPromoteCmd.Args(unifiedPromoteCmd, []string{"Customer lookup"}); err != nil {
		t.Fatal(err)
	}
	if err := unifiedPromoteCmd.RunE(unifiedPromoteCmd, []string{"Customer lookup"}); err != nil {
		t.Fatal(err)
	}
	// Named and config-based invocation must produce the same exact destination and readable labels.
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.ActiveAppID != id || result.Name != "Customer lookup" || result.Version != "1.0.0" || calls != 3 {
		t.Fatalf("named output=%s calls=%d err=%v", output.String(), calls, err)
	}
}

// TestUnifiedPromoteRequiresVersion prevents implicit latest-version traffic changes before client creation.
func TestUnifiedPromoteRequiresVersion(t *testing.T) {
	previous, _ := unifiedPromoteCmd.Flags().GetString("version")
	// Restore the shared flag after exercising the missing-selector boundary.
	t.Cleanup(func() { _ = unifiedPromoteCmd.Flags().Set("version", previous) })
	_ = unifiedPromoteCmd.Flags().Set("version", "")
	// A name alone must not choose whichever version happens to be newest.
	if err := unifiedPromoteCmd.RunE(unifiedPromoteCmd, nil); err == nil {
		t.Fatal("promotion accepted without explicit version")
	}
}

// TestUnifiedPromotionRejectsAmbiguousNames prevents blank or multiple names from selecting an unintended app.
func TestUnifiedPromotionRejectsAmbiguousNames(t *testing.T) {
	// Whitespace input is an error even when config discovery might otherwise find a target.
	if _, err := unifiedPromotionName([]string{"   "}, "unused.yaml"); err == nil {
		t.Fatal("blank name accepted")
	}
	// The command permits only one destination app per traffic switch.
	if err := unifiedPromoteCmd.Args(unifiedPromoteCmd, []string{"first", "second"}); err == nil {
		t.Fatal("multiple names accepted")
	}
}
