package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type userGraphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func TestUserClientUsesExactReadContract(t *testing.T) {
	requests := make([]userGraphQLRequest, 0, 3)
	server := newUserClientServer(t, &requests)
	defer server.Close()
	client := NewClient(server.URL, "fsk_test")

	page, err := client.ListUsers("ada", true, PageOptions{Limit: 5, Offset: 2})
	requireUserClientNoError(t, err)
	if page.Total != 1 || page.Items[0].ID != "user-1" {
		t.Fatalf("users = %#v", page)
	}
	user, err := client.GetUser("user-1")
	requireUserClientNoError(t, err)
	if len(user.Memberships) != 1 || len(user.Credentials) != 1 {
		t.Fatalf("user detail = %#v", user)
	}
	members, err := client.ListTeamMembers("team-1", PageOptions{Limit: 10})
	requireUserClientNoError(t, err)
	if members.Total != 1 || members.Items[0].UserID != "user-1" {
		t.Fatalf("members = %#v", members)
	}

	assertUserQueryContains(t, requests[0].Query, "users(search: $search", "include_suspended: $includeSuspended", "id email display_name status")
	if requests[0].Variables["search"] != "ada" || requests[0].Variables["includeSuspended"] != true {
		t.Fatalf("list variables = %#v", requests[0].Variables)
	}
	assertUserQueryContains(t, requests[1].Query, "user(id: $id)", "memberships", "credentials", "key_prefix", "revoked_at")
	assertUserQueryContains(t, requests[2].Query, "teamMembers(team_id: $teamId", "user_id email display_name status membership_role")
}

func TestUserClientUsesExactMutationContract(t *testing.T) {
	requests := make([]userGraphQLRequest, 0, 10)
	server := newUserClientServer(t, &requests)
	defer server.Close()
	client := NewClient(server.URL, "fsk_test")
	email, name := "Ada@Example.test", "Ada Lovelace"

	_, err := client.CreateUser(CreateUserInput{Email: email, DisplayName: name})
	requireUserClientNoError(t, err)
	_, err = client.UpdateUser("user-1", UpdateUserInput{DisplayName: &name})
	requireUserClientNoError(t, err)
	_, err = client.SuspendUser("user-1")
	requireUserClientNoError(t, err)
	_, err = client.ReactivateUser("user-1")
	requireUserClientNoError(t, err)
	_, err = client.AddTeamMember("team-1", email, "MEMBER")
	requireUserClientNoError(t, err)
	_, err = client.RemoveTeamMember("team-1", "user-1")
	requireUserClientNoError(t, err)
	issued, err := client.IssueUserCredential("user-1", "laptop")
	requireUserClientNoError(t, err)
	if issued.Secret != "fsk_once_only" {
		t.Fatalf("issued secret = %q", issued.Secret)
	}
	_, err = client.RevokeUserCredential("user-1", "credential-1")
	requireUserClientNoError(t, err)

	wants := [][]string{
		{"$input: CreateUserInput!", "createUser(input: $input)"},
		{"$input: UpdateUserInput!", "updateUser(id: $id, input: $input)"},
		{"suspendUser(id: $id)"},
		{"reactivateUser(id: $id)"},
		{"$role: TeamMembershipRole!", "addTeamMember(team_id: $teamId, email: $email, membership_role: $role)"},
		{"removeTeamMember(team_id: $teamId, user_id: $userId)"},
		{"issueUserCredential(user_id: $userId, name: $name)", "credential {", "secret authorization_revision changed"},
		{"revokeUserCredential(user_id: $userId, credential_id: $credentialId)"},
	}
	if len(requests) != len(wants) {
		t.Fatalf("request count = %d, want %d", len(requests), len(wants))
	}
	for i, values := range wants {
		assertUserQueryContains(t, requests[i].Query, values...)
	}
	if requests[4].Variables["role"] != "MEMBER" || requests[6].Variables["name"] != "laptop" {
		t.Fatalf("mutation variables = %#v / %#v", requests[4].Variables, requests[6].Variables)
	}
	input := requests[1].Variables["input"].(map[string]any)
	if _, exists := input["email"]; exists || input["display_name"] != name {
		t.Fatalf("update input = %#v", input)
	}
}

func newUserClientServer(t *testing.T, requests *[]userGraphQLRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/engine/graphql" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		var request userGraphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		*requests = append(*requests, request)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(userClientResponse(request.Query)))
	}))
}

func userClientResponse(query string) string {
	user := `{"id":"user-1","email":"Ada@Example.test","display_name":"Ada","status":"ACTIVE","memberships":[{"team_id":"team-1","team_name":"Platform","team_slug":"platform","membership_role":"MEMBER","created_at":"now"}],"credentials":[{"id":"credential-1","name":"laptop","key_prefix":"fsk_abcd","expires_at":null,"last_used_at":null,"revoked_at":null,"created_at":"now"}],"created_at":"now","updated_at":"now"}`
	member := `{"user_id":"user-1","email":"Ada@Example.test","display_name":"Ada","status":"ACTIVE","membership_role":"MEMBER","created_at":"now"}`
	switch {
	case strings.Contains(query, "query Users"):
		return `{"data":{"users":{"total":1,"items":[` + user + `]}}}`
	case strings.Contains(query, "query User("):
		return `{"data":{"user":` + user + `}}`
	case strings.Contains(query, "query TeamMembers"):
		return `{"data":{"teamMembers":{"total":1,"items":[` + member + `]}}}`
	case strings.Contains(query, "addTeamMember("):
		return `{"data":{"addTeamMember":{"membership":` + member + `,"authorization_revision":3,"changed":true}}}`
	case strings.Contains(query, "removeTeamMember("):
		return `{"data":{"removeTeamMember":{"membership":null,"authorization_revision":3,"changed":true}}}`
	case strings.Contains(query, "issueUserCredential("):
		return `{"data":{"issueUserCredential":{"credential":{"id":"credential-1","name":"laptop","key_prefix":"fsk_abcd","expires_at":null,"last_used_at":null,"revoked_at":null,"created_at":"now"},"secret":"fsk_once_only","authorization_revision":3,"changed":true}}}`
	case strings.Contains(query, "revokeUserCredential("):
		return `{"data":{"revokeUserCredential":{"credential":null,"authorization_revision":3,"changed":true}}}`
	default:
		for _, field := range []string{"createUser", "updateUser", "suspendUser", "reactivateUser"} {
			if strings.Contains(query, field+"(") {
				return `{"data":{"` + field + `":{"user":` + user + `,"authorization_revision":3,"changed":true}}}`
			}
		}
		return `{"errors":[{"message":"unexpected query"}]}`
	}
}

func assertUserQueryContains(t *testing.T, query string, values ...string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(query, value) {
			t.Errorf("query does not contain %q: %s", value, query)
		}
	}
}

func requireUserClientNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("user client request failed: %v", err)
	}
}
