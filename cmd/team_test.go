package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTeamListUsesPagedEngineContract(t *testing.T) {
	var request teamCommandGraphQLRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/engine/graphql" {
			t.Fatalf("path = %s, want /engine/graphql", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"teams":{"total":1,"items":[{"id":"team-1","name":"Payments","slug":"payments","description":"Money movement","status":"active","bindings":[],"created_at":"now","updated_at":"now"}]}}}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"team", "list", "--search", "pay", "--include-archived", "--limit", "5", "--offset", "2"})

	if !strings.Contains(request.Query, "teams(search: $search") || request.Variables["search"] != "pay" || request.Variables["includeArchived"] != true {
		t.Fatalf("unexpected team list request: %#v", request)
	}
	for _, want := range []string{"Payments", "payments", "active", "team-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}
}

func TestTeamAccessCommandsUseProductLanguageAndExactEnums(t *testing.T) {
	requests := make([]teamCommandGraphQLRequest, 0, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request teamCommandGraphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, request)
		field := teamMutationField(request.Query)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"` + field + `":{"binding":null,"authorization_revision":5,"changed":true}}}`))
	}))
	defer server.Close()

	runCommandInDir(t, t.TempDir(), server.URL, []string{"team", "access", "workspace", "set", "team-1", "builder"})
	runCommandInDir(t, t.TempDir(), server.URL, []string{"team", "access", "workspace", "clear", "team-1"})
	runCommandInDir(t, t.TempDir(), server.URL, []string{"team", "access", "service", "grant", "team-1", "service-1", "use"})
	runCommandInDir(t, t.TempDir(), server.URL, []string{"team", "access", "bucket", "revoke", "team-1", "bucket-1", "manage"})

	if len(requests) != 4 {
		t.Fatalf("request count = %d, want 4", len(requests))
	}
	if requests[0].Variables["role"] != "BUILDER" || !strings.Contains(requests[0].Query, "$role: TeamWorkspaceRole") || strings.Contains(requests[0].Query, "TeamWorkspaceRole!") {
		t.Fatalf("unexpected workspace role request: %#v", requests[0])
	}
	if requests[1].Variables["role"] != nil || !strings.Contains(requests[1].Query, "setTeamWorkspaceRole") {
		t.Fatalf("unexpected workspace role clear request: %#v", requests[1])
	}
	if requests[2].Variables["level"] != "USER" || !strings.Contains(requests[2].Query, "grantTeamServiceAccess") {
		t.Fatalf("unexpected service grant request: %#v", requests[2])
	}
	if requests[3].Variables["level"] != "MANAGER" || !strings.Contains(requests[3].Query, "revokeTeamBucketAccess") {
		t.Fatalf("unexpected bucket revoke request: %#v", requests[3])
	}
}

func TestTeamUpdateRequiresAChangedField(t *testing.T) {
	message := runCommandInDirExpectError(t, t.TempDir(), "http://unused.invalid", []string{"team", "update", "team-1"})
	if !strings.Contains(message, "provide at least one") {
		t.Fatalf("unexpected error: %q", message)
	}
}

func TestNormalizeTeamRoles(t *testing.T) {
	if got, err := normalizeWorkspaceRole("Admin"); err != nil || got != "ADMIN" {
		t.Fatalf("normalizeWorkspaceRole() = %q, %v", got, err)
	}
	if got, err := normalizeAccessLevel("use"); err != nil || got != "USER" {
		t.Fatalf("normalizeAccessLevel(use) = %q, %v", got, err)
	}
	if _, err := normalizeAccessLevel("read"); err == nil {
		t.Fatal("expected unsupported access level to fail")
	}
	if got, err := normalizeAppAccessLevel("read"); err != nil || got != "READER" {
		t.Fatalf("normalizeAppAccessLevel(read) = %q, %v", got, err)
	}
	if got, err := normalizeAppAccessLevel("use"); err != nil || got != "USER" {
		t.Fatalf("normalizeAppAccessLevel(use) = %q, %v", got, err)
	}
	for _, legacy := range []string{"user", "manager"} {
		if _, err := normalizeAccessLevel(legacy); err == nil {
			t.Errorf("legacy service access level %q was accepted", legacy)
		}
	}
	for _, legacy := range []string{"reader", "manager"} {
		if _, err := normalizeAppAccessLevel(legacy); err == nil {
			t.Errorf("legacy app access level %q was accepted", legacy)
		}
	}
	if _, err := normalizeSelectorResourceType("services"); err == nil {
		t.Error("legacy plural resource selector was accepted")
	}
}

func TestNoOpTeamAccessDoesNotEmitAppliedChangeAudit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"grantTeamServiceAccess":{"binding":null,"authorization_revision":5,"changed":false}}}`))
	}))
	defer server.Close()

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previousProvider) })

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"team", "access", "service", "grant", "team-1", "service-1", "use"})
	if !strings.Contains(out, "already up to date") {
		t.Fatalf("no-op output = %q", out)
	}
	if got := countAppliedChangeEvents(exporter.GetSpans()); got != 0 {
		t.Fatalf("applied change event count = %d, want 0", got)
	}
}

func TestTeamAppAccessCanShareOneFamilyWithTwoTeams(t *testing.T) {
	requests := make([]teamCommandGraphQLRequest, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request teamCommandGraphQLRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, request)
		_, _ = w.Write([]byte(`{"data":{"grantTeamAppAccess":{"binding":{"id":"binding-1","team_id":"team-1","role_slug":"app-reader","role_display_name":"App reader","resource_type":"app","resource_id":"family-1","resource_display_name":"Support SDK","created_at":"now"},"authorization_revision":8,"changed":true}}}`))
	}))
	defer server.Close()

	runCommandInDir(t, t.TempDir(), server.URL, []string{"team", "access", "app", "grant", "platform", "support", "read"})
	runCommandInDir(t, t.TempDir(), server.URL, []string{"team", "access", "app", "grant", "support", "support", "manage"})

	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	if requests[0].Variables["teamId"] != "platform" || requests[0].Variables["resourceId"] != "support" || requests[0].Variables["level"] != "READER" {
		t.Fatalf("first team sharing request = %#v", requests[0])
	}
	if requests[1].Variables["teamId"] != "support" || requests[1].Variables["resourceId"] != "support" || requests[1].Variables["level"] != "MANAGER" {
		t.Fatalf("second team sharing request = %#v", requests[1])
	}
	for _, request := range requests {
		if !strings.Contains(request.Query, "$level: TeamAppAccessLevel!") || !strings.Contains(request.Query, "grantTeamAppAccess") || !strings.Contains(request.Query, "app_family_id") {
			t.Fatalf("app sharing contract = %#v", request)
		}
	}
}

func TestTeamAppSharingHelpClaimsOnlyScopeBackedBuilds(t *testing.T) {
	for _, helpText := range []string{teamAppAccessCmd.Short, teamAppGrantCmd.Short, teamAppRevokeCmd.Short} {
		help := strings.ToLower(helpText)
		if strings.Contains(help, "webhook") {
			t.Fatalf("app sharing help advertises unsupported webhook sharing: %q", helpText)
		}
	}
	if !strings.Contains(teamAppAccessCmd.Short, "SDK") || !strings.Contains(teamAppAccessCmd.Short, "MCP") {
		t.Fatalf("app sharing help does not name supported build types: %q", teamAppAccessCmd.Short)
	}
}

func TestTeamBuildAccessShowsOnlySelectorResults(t *testing.T) {
	var request teamCommandGraphQLRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"data":{"appBuildSelectors":{"total":1,"items":[{"resource_type":"BUCKET","resource_id":"bucket-1","display_name":"Production"}]}}}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"team", "build-access", "team-1", "--resource", "bucket", "--search", "prod", "--limit", "5"})
	for _, want := range []string{"Production", "bucket-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}
	if request.Variables["ownerTeamId"] != "team-1" || request.Variables["resourceType"] != "BUCKET" || request.Variables["search"] != "prod" {
		t.Fatalf("selector request = %#v", request)
	}
}

func TestTeamEligibleOwnersUsesNarrowBuilderContract(t *testing.T) {
	var request teamCommandGraphQLRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"data":{"appOwningTeams":{"total":1,"items":[{"id":"team-1","name":"Support","slug":"support"}]}}}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"team", "eligible-owners", "--search", "sup", "--limit", "5"})
	for _, want := range []string{"Support", "support", "team-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}
	if request.Variables["search"] != "sup" || !strings.Contains(request.Query, "appOwningTeams") {
		t.Fatalf("eligible-owner request = %#v", request)
	}
}

type teamCommandGraphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func teamMutationField(query string) string {
	for _, field := range []string{"setTeamWorkspaceRole", "grantTeamServiceAccess", "revokeTeamBucketAccess", "grantTeamAppAccess", "revokeTeamAppAccess"} {
		if strings.Contains(query, field+"(") {
			return field
		}
	}
	return "unknown"
}
