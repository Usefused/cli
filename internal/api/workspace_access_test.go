package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkspaceAccessClientUsesExactGraphQLContract(t *testing.T) {
	requests := make([]teamGraphQLRequest, 0, 5)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request teamGraphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, request)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(request.Query, "query WorkspaceShares") {
			_, _ = w.Write([]byte(`{"data":{"workspaceShares":{"total":0,"items":[]}}}`))
			return
		}
		field := "grantWorkspaceBucketAccess"
		for _, candidate := range []string{"revokeWorkspaceBucketAccess", "grantWorkspaceArtifactAccess", "revokeWorkspaceArtifactAccess"} {
			if strings.Contains(request.Query, candidate+"(") {
				field = candidate
			}
		}
		_, _ = w.Write([]byte(`{"data":{"` + field + `":{"share":null,"authorization_revision":8,"changed":true}}}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "fsk_test")

	if _, err := client.ListWorkspaceShares("BUCKET", PageOptions{Limit: 5, Offset: 2}); err != nil {
		t.Fatalf("ListWorkspaceShares: %v", err)
	}
	mutations := []func() error{
		func() error { _, err := client.GrantWorkspaceBucketAccess("bucket-1"); return err },
		func() error { _, err := client.RevokeWorkspaceBucketAccess("bucket-1"); return err },
		func() error { _, err := client.GrantWorkspaceArtifactAccess("artifact-1"); return err },
		func() error { _, err := client.RevokeWorkspaceArtifactAccess("artifact-1"); return err },
	}
	for _, mutate := range mutations {
		if err := mutate(); err != nil {
			t.Fatalf("workspace mutation: %v", err)
		}
	}
	if len(requests) != 5 || requests[0].Variables["resourceType"] != "BUCKET" || requests[0].Variables["limit"] != float64(5) {
		t.Fatalf("workspace requests = %#v", requests)
	}
	for index, expected := range []string{"grantWorkspaceBucketAccess", "revokeWorkspaceBucketAccess", "grantWorkspaceArtifactAccess", "revokeWorkspaceArtifactAccess"} {
		request := requests[index+1]
		if !strings.Contains(request.Query, expected+"(") || request.Variables["resourceId"] == "" {
			t.Fatalf("request %d = %#v", index+1, request)
		}
	}
}
