package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

func TestWorkspaceSyncPreservesCanonicalRetryV3(t *testing.T) {
	payload := readWorkspaceSyncRetryFixture(t)
	var remoteRetry api.ServiceRetryConfig
	if err := json.Unmarshal(payload, &remoteRetry); err != nil {
		t.Fatalf("decode Registry retry policy: %v", err)
	}
	config := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{}}
	remote := []api.WorkspaceService{{ServiceName: "payments", ServiceID: "svc-owned", Version: "v1"}}
	visibility := map[string]api.ServiceVisibility{"svc-owned": {
		ServiceID: "svc-owned", Slug: "payments", IsOwner: true, IsPublic: true, RetryConfig: &remoteRetry,
	}}

	mustMergeWorkspaceServicesFromRemote(t, config, remote, visibility)
	policy := config.Services["payments"].ExecutionPolicy
	if policy == nil || policy.Retry == nil {
		t.Fatalf("synced retry policy is missing: %#v", policy)
	}
	encoded, err := json.Marshal(policy.Retry)
	if err != nil {
		t.Fatalf("encode synced retry policy: %v", err)
	}
	assertWorkspaceSyncRetryJSON(t, encoded, payload)
}

func readWorkspaceSyncRetryFixture(t *testing.T) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "contract-fixtures", "retry", "v3_idempotency_predicates.json"))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func assertWorkspaceSyncRetryJSON(t *testing.T, gotPayload, wantPayload []byte) {
	t.Helper()
	var got, want any
	if err := json.Unmarshal(gotPayload, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wantPayload, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workspace sync changed retry v3\ngot:  %s\nwant: %s", gotPayload, wantPayload)
	}
}
