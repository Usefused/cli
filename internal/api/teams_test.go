package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type teamGraphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func TestTeamClientUsesEngineGraphQLReadContract(t *testing.T) {
	requests := make([]teamGraphQLRequest, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/engine/graphql" {
			t.Fatalf("path = %s, want /engine/graphql", r.URL.Path)
		}
		var request teamGraphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, request)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(request.Query, "query Teams") {
			_, _ = w.Write([]byte(`{"data":{"teams":{"total":1,"items":[{"id":"team-1","name":"Payments","slug":"payments","description":"","status":"active","bindings":[],"created_at":"now","updated_at":"now"}]}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"team":{"id":"team-1","name":"Payments","slug":"payments","description":"","status":"active","bindings":[],"created_at":"now","updated_at":"now"}}}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "fsk_test")

	page, err := client.ListTeams("pay", true, PageOptions{Limit: 7, Offset: 3})
	if err != nil || page.Total != 1 {
		t.Fatalf("ListTeams() = %#v, %v", page, err)
	}
	team, err := client.GetTeam("team-1")
	if err != nil || team.ID != "team-1" {
		t.Fatalf("GetTeam() = %#v, %v", team, err)
	}

	assertQueryContains(t, requests[0].Query, "teams(search: $search", "include_archived: $includeArchived", "bindings", "resource_display_name")
	if requests[0].Variables["search"] != "pay" || requests[0].Variables["includeArchived"] != true || requests[0].Variables["limit"] != float64(7) {
		t.Fatalf("unexpected list variables: %#v", requests[0].Variables)
	}
	assertQueryContains(t, requests[1].Query, "team(id: $id)", "$id: ID!")
}

func TestTeamClientUsesExactMutationContract(t *testing.T) {
	requests := make([]teamGraphQLRequest, 0, 11)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request teamGraphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, request)
		field := mutationField(request.Query)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(field, "TeamAccess") || strings.Contains(field, "WorkspaceRole") {
			_, _ = w.Write([]byte(`{"data":{"` + field + `":{"binding":null,"authorization_revision":4,"changed":true}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"` + field + `":{"team":{"id":"team-1","name":"Payments","slug":"payments","description":"","status":"active","bindings":[],"created_at":"now","updated_at":"now"},"authorization_revision":4,"changed":true}}}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "fsk_test")
	name, description, role := "Payments Platform", "", "BUILDER"

	_, err := client.CreateTeam(CreateTeamInput{Name: "Payments"})
	requireTeamClientNoError(t, err)
	_, err = client.UpdateTeam("team-1", UpdateTeamInput{Name: &name, Description: &description})
	requireTeamClientNoError(t, err)
	_, err = client.ArchiveTeam("team-1")
	requireTeamClientNoError(t, err)
	_, err = client.SetTeamWorkspaceRole("team-1", &role)
	requireTeamClientNoError(t, err)
	_, err = client.SetTeamWorkspaceRole("team-1", nil)
	requireTeamClientNoError(t, err)
	_, err = client.GrantTeamServiceAccess("team-1", "service-1", "USER")
	requireTeamClientNoError(t, err)
	_, err = client.RevokeTeamServiceAccess("team-1", "service-1", "USER")
	requireTeamClientNoError(t, err)
	_, err = client.GrantTeamBucketAccess("team-1", "bucket-1", "MANAGER")
	requireTeamClientNoError(t, err)
	_, err = client.RevokeTeamBucketAccess("team-1", "bucket-1", "MANAGER")
	requireTeamClientNoError(t, err)
	_, err = client.GrantTeamAppAccess("team-1", "family-1", "READER")
	requireTeamClientNoError(t, err)
	_, err = client.RevokeTeamAppAccess("team-1", "family-1", "MANAGER")
	requireTeamClientNoError(t, err)

	wants := []struct {
		field string
		args  []string
	}{
		{"createTeam", []string{"$input: CreateTeamInput!", "createTeam(input: $input)"}},
		{"updateTeam", []string{"$input: UpdateTeamInput!", "updateTeam(id: $id, input: $input)"}},
		{"archiveTeam", []string{"archiveTeam(id: $id)"}},
		{"setTeamWorkspaceRole", []string{"$role: TeamWorkspaceRole", "team_id: $teamId, role: $role"}},
		{"setTeamWorkspaceRole", []string{"$role: TeamWorkspaceRole", "team_id: $teamId, role: $role"}},
		{"grantTeamServiceAccess", []string{"$level: TeamAccessLevel!", "service_id: $resourceId", "level: $level"}},
		{"revokeTeamServiceAccess", []string{"$level: TeamAccessLevel!", "service_id: $resourceId", "level: $level"}},
		{"grantTeamBucketAccess", []string{"$level: TeamAccessLevel!", "bucket_id: $resourceId", "level: $level"}},
		{"revokeTeamBucketAccess", []string{"$level: TeamAccessLevel!", "bucket_id: $resourceId", "level: $level"}},
		{"grantTeamAppAccess", []string{"$level: TeamAppAccessLevel!", "app_family_id: $resourceId", "level: $level"}},
		{"revokeTeamAppAccess", []string{"$level: TeamAppAccessLevel!", "app_family_id: $resourceId", "level: $level"}},
	}
	if len(requests) != len(wants) {
		t.Fatalf("request count = %d, want %d", len(requests), len(wants))
	}
	for i, want := range wants {
		assertQueryContains(t, requests[i].Query, append([]string{want.field}, want.args...)...)
	}
	if strings.Contains(requests[3].Query, "TeamWorkspaceRole!") || requests[4].Variables["role"] != nil {
		t.Fatalf("workspace role clear contract = %#v", requests[4])
	}
	if got := requests[6].Variables["level"]; got != "USER" {
		t.Fatalf("revoke service level = %#v, want USER", got)
	}
	input := requests[1].Variables["input"].(map[string]any)
	if _, ok := input["slug"]; ok || input["description"] != "" {
		t.Fatalf("update input did not preserve omitted/clear semantics: %#v", input)
	}
}

func mutationField(query string) string {
	for _, field := range []string{
		"createTeam", "updateTeam", "archiveTeam", "setTeamWorkspaceRole",
		"grantTeamServiceAccess", "revokeTeamServiceAccess", "grantTeamBucketAccess", "revokeTeamBucketAccess",
		"grantTeamAppAccess", "revokeTeamAppAccess",
	} {
		if strings.Contains(query, field+"(") {
			return field
		}
	}
	return "unknown"
}

func assertQueryContains(t *testing.T, query string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(query, value) {
			t.Errorf("query does not contain %q: %s", value, query)
		}
	}
}

func requireTeamClientNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("team client request failed: %v", err)
	}
}
