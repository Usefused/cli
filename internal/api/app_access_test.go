package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAppAccessClientUsesExactSelectorContract(t *testing.T) {
	requests := make([]teamGraphQLRequest, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := decodeAppAccessRequest(t, r)
		requests = append(requests, request)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(appAccessResponse(request.Query)))
	}))
	defer server.Close()
	client := NewClient(server.URL, "fsk_test")

	teams, err := client.ListAppOwningTeams("pay", PageOptions{Limit: 7, Offset: 2})
	if err != nil || teams.Total != 1 || teams.Items[0].ID != "team-1" {
		t.Fatalf("ListAppOwningTeams() = %#v, %v", teams, err)
	}
	selectors, err := client.ListAppBuildSelectors("team-1", "SERVICE", "str", PageOptions{Limit: 5, Offset: 1})
	if err != nil || selectors.Total != 1 || selectors.Items[0].ResourceID != "service-1" {
		t.Fatalf("ListAppBuildSelectors() = %#v, %v", selectors, err)
	}

	assertQueryContains(t, requests[0].Query, "appOwningTeams(search: $search", "items { id name slug }")
	assertQueryContains(t, requests[1].Query, "$resourceType: AppSelectorResourceType!", "owner_team_id: $ownerTeamId", "resource_type resource_id display_name")
	if requests[1].Variables["ownerTeamId"] != "team-1" || requests[1].Variables["resourceType"] != "SERVICE" || requests[1].Variables["search"] != "str" {
		t.Fatalf("selector variables = %#v", requests[1].Variables)
	}
}

func decodeAppAccessRequest(t *testing.T, r *http.Request) teamGraphQLRequest {
	t.Helper()
	var request teamGraphQLRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return request
}

func appAccessResponse(query string) string {
	if strings.Contains(query, "appOwningTeams") {
		return `{"data":{"appOwningTeams":{"total":1,"items":[{"id":"team-1","name":"Payments","slug":"payments"}]}}}`
	}
	return `{"data":{"appBuildSelectors":{"total":1,"items":[{"resource_type":"SERVICE","resource_id":"service-1","display_name":"Stripe"}]}}}`
}
