package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkspaceAccessCommandsExposeBoundedUseWithoutTeamIdentity(t *testing.T) {
	requests := make([]teamCommandGraphQLRequest, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request teamCommandGraphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, request)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(request.Query, "workspaceShares("):
			_, _ = w.Write([]byte(`{"data":{"workspaceShares":{"total":1,"items":[{"id":"share-1","role_slug":"bucket-user","role_display_name":"Bucket user","resource_type":"bucket","resource_id":"bucket-1","resource_display_name":"company","created_at":"now"}]}}}`))
		case strings.Contains(request.Query, "grantWorkspaceBucketAccess("):
			_, _ = w.Write([]byte(`{"data":{"grantWorkspaceBucketAccess":{"share":null,"authorization_revision":8,"changed":true}}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{"revokeWorkspaceAppAccess":{"share":null,"authorization_revision":9,"changed":true}}}`))
		}
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"workspace", "access", "list", "--resource", "bucket"})
	runCommandInDir(t, t.TempDir(), server.URL, []string{"workspace", "access", "bucket", "grant", "company"})
	runCommandInDir(t, t.TempDir(), server.URL, []string{"workspace", "access", "app", "revoke", "support-sdk"})

	if !strings.Contains(out, "company") || !strings.Contains(out, "use") {
		t.Fatalf("workspace access output = %q", out)
	}
	if len(requests) != 3 || requests[0].Variables["resourceType"] != "BUCKET" {
		t.Fatalf("workspace access requests = %#v", requests)
	}
	for _, request := range requests[1:] {
		if _, exists := request.Variables["teamId"]; exists {
			t.Fatalf("workspace access must not ask callers to act as a team: %#v", request)
		}
	}
	if requests[1].Variables["resourceId"] != "company" || requests[2].Variables["resourceId"] != "support-sdk" {
		t.Fatalf("human resource references were not forwarded: %#v", requests)
	}
}

func TestNormalizeWorkspaceShareResourceRejectsBroadOrLegacyValues(t *testing.T) {
	if got, err := normalizeWorkspaceShareResource("app"); err != nil || got != "APP" {
		t.Fatalf("app resource = %q, %v", got, err)
	}
	for _, invalid := range []string{"service", "workspace", "buckets", "artifact"} {
		if _, err := normalizeWorkspaceShareResource(invalid); err == nil {
			t.Fatalf("resource %q unexpectedly accepted", invalid)
		}
	}
}
