package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// TestMissingContractsRefreshTimeoutUsesLongDefaultAndExplicitOverride protects live bulk repair from the root one-minute budget.
func TestMissingContractsRefreshTimeoutUsesLongDefaultAndExplicitOverride(t *testing.T) {
	previousTimeout := RequestTimeout
	t.Cleanup(func() { RequestTimeout = previousTimeout })
	command := &cobra.Command{Use: "refresh-missing-contracts"}
	command.Flags().DurationVar(&RequestTimeout, "timeout", time.Minute, "test timeout")
	// The maintenance default must cover bounded Registry fallback for a full one-hundred-version pass.
	if got := missingContractsRefreshTimeout(command); got != defaultMissingContractsRefreshTimeout {
		t.Fatalf("default timeout = %s, want %s", got, defaultMissingContractsRefreshTimeout)
	}
	// A caller-supplied value must override the maintenance default exactly.
	if err := command.Flags().Set("timeout", "45s"); err != nil {
		t.Fatal(err)
	}
	if got := missingContractsRefreshTimeout(command); got != 45*time.Second {
		t.Fatalf("explicit timeout = %s, want 45s", got)
	}
}

// TestRunWorkspaceRefreshMissingContractsPrintsSummary verifies the maintenance summary names both repairable snapshot states.
func TestRunWorkspaceRefreshMissingContractsPrintsSummary(t *testing.T) {
	origEngineURL, origAPIKey, origLimit := EngineURL, APIKey, workspaceRefreshMissingContractsLimit
	defer func() {
		EngineURL, APIKey, workspaceRefreshMissingContractsLimit = origEngineURL, origAPIKey, origLimit
	}()

	var sawPath string
	var sawAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawAPIKey = r.Header.Get("x-api-key")
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		if !strings.Contains(body.Query, "refreshMissingServiceContracts") {
			t.Fatalf("expected refreshMissingServiceContracts mutation, got %s", body.Query)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"refreshMissingServiceContracts":{"status":"ok","missing":3,"refreshed":2,"failed":1,"results":[]}}}`))
	}))
	defer srv.Close()

	EngineURL = srv.URL
	APIKey = "fsk_test"
	workspaceRefreshMissingContractsLimit = 3
	cmd := &cobra.Command{}
	var out strings.Builder
	cmd.SetOut(&out)

	if err := runWorkspaceRefreshMissingContracts(cmd); err != nil {
		t.Fatalf("runWorkspaceRefreshMissingContracts() error = %v", err)
	}
	if sawPath != "/engine/graphql" {
		t.Fatalf("expected /engine/graphql, got %s", sawPath)
	}
	if sawAPIKey != "fsk_test" {
		t.Fatalf("expected API key header, got %q", sawAPIKey)
	}
	// The Engine's historical `missing` count includes both absent snapshots and snapshots missing a generation pin.
	if !strings.Contains(out.String(), "Refreshed 2 of 3 missing or unpinned runtime contract snapshots (1 failed).") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

// TestRunWorkspaceRefreshMissingContractsWritesJSON verifies the suggested repair command is safe for structured automation.
func TestRunWorkspaceRefreshMissingContractsWritesJSON(t *testing.T) {
	origEngineURL, origAPIKey, origLimit := EngineURL, APIKey, workspaceRefreshMissingContractsLimit
	defer func() {
		EngineURL, APIKey, workspaceRefreshMissingContractsLimit = origEngineURL, origAPIKey, origLimit
	}()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the Engine GraphQL maintenance mutation is valid for this focused command fixture.
		if r.URL.Path != "/engine/graphql" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":{"refreshMissingServiceContracts":{"status":"ok","missing":1,"refreshed":1,"failed":0,"results":[{"service_id":"svc-1","service_version_id":"ver-1","version":"1.0.0","contract_hash":"sha256:abc"}]}}}`))
	}))
	defer server.Close()
	EngineURL, APIKey, workspaceRefreshMissingContractsLimit = server.URL, "fsk_test", 100
	command := &cobra.Command{}
	addJSONOutputFlag(command)
	// The recovery command's advertised flag must select structured output.
	if err := command.Flags().Set(jsonOutputFlag, "true"); err != nil {
		t.Fatalf("set JSON output: %v", err)
	}
	var output strings.Builder
	command.SetOut(&output)
	if err := runWorkspaceRefreshMissingContracts(command); err != nil {
		t.Fatalf("run structured refresh: %v", err)
	}
	var result map[string]any
	// Successful JSON must expose exact progress so callers know whether another pass is required.
	if err := json.Unmarshal([]byte(output.String()), &result); err != nil {
		t.Fatalf("decode refresh output: %v", err)
	}
	if result["status"] != "ok" || result["missing"] != float64(1) || result["refreshed"] != float64(1) || result["failed"] != float64(0) {
		t.Fatalf("structured refresh = %#v", result)
	}
}

// TestWorkspaceRefreshMissingContractsHelpNamesUnpinnedSnapshots keeps discovery and limit guidance aligned with Engine classification.
func TestWorkspaceRefreshMissingContractsHelpNamesUnpinnedSnapshots(t *testing.T) {
	// The compatibility command name remains stable while its summary describes the full repair scope.
	if !strings.Contains(workspaceRefreshMissingContractsCmd.Short, "missing or unpinned runtime contract snapshots") {
		t.Fatalf("unexpected command summary %q", workspaceRefreshMissingContractsCmd.Short)
	}
	limitFlag := workspaceRefreshMissingContractsCmd.Flags().Lookup("limit")
	// A registered limit flag must describe the same bounded missing-or-unpinned batch.
	if limitFlag == nil || !strings.Contains(limitFlag.Usage, "missing or unpinned activated service versions") {
		t.Fatalf("unexpected limit flag %#v", limitFlag)
	}
	// Recovery metadata may advertise JSON only when the repair command registers the corresponding flag.
	if workspaceRefreshMissingContractsCmd.Flags().Lookup(jsonOutputFlag) == nil {
		t.Fatal("refresh-missing-contracts must support --json")
	}
}
