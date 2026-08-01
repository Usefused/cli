package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSDKPlanOwnerTeamFlagUsesPlanMetadataContract(t *testing.T) {
	sdkPlanOwnerTeam = ""
	t.Cleanup(func() {
		sdkPlanOwnerTeam = ""
		_ = sdkPlanCmd.Flags().Set("owner-team", "")
	})
	const ownerTeamID = "11111111-1111-1111-1111-111111111111"
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "owner.yaml", `
apiVersion: fused/v1
kind: sdk
name: owner-test
version: "1.0.0"
language: typescript
services:
  github:
    version: "2026-08-01"
    operations: ["listRepositories"]
`)
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sdk-config/plan" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"plan_id":"plan-1","config_key":"sdk:owner-test:1.0.0","source_hash":"hash","summary":{}}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"sdk", "plan", "-f", path, "--owner-team", ownerTeamID})
	if request["owner_team_id"] != ownerTeamID || !strings.Contains(out, "Plan created") {
		t.Fatalf("request/output = %#v / %q", request, out)
	}
	if sdkApplyCmd.Flags().Lookup("owner-team") != nil {
		t.Fatal("apply must not accept an owner-team override")
	}
}
