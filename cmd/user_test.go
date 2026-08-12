package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestUserListAndTeamMemberCommandsUseEngineContract(t *testing.T) {
	requests := make([]userCommandRequest, 0, 3)
	server := userCommandServer(t, &requests)
	defer server.Close()

	listOutput := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"user", "list", "--search", "ada", "--include-suspended"})
	memberOutput := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"team", "member", "list", "platform"})
	addOutput := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"team", "member", "add", "platform", "Ada@Example.test", "--role", "manager"})

	for _, want := range []string{"Ada", "Ada@Example.test", "active", "user-1"} {
		if !strings.Contains(listOutput, want) {
			t.Errorf("user list %q lacks %q", listOutput, want)
		}
	}
	if !strings.Contains(memberOutput, "member") || !strings.Contains(addOutput, "Added Ada") {
		t.Fatalf("member outputs = %q / %q", memberOutput, addOutput)
	}
	if requests[2].Variables["role"] != "MANAGER" || requests[2].Variables["email"] != "Ada@Example.test" {
		t.Fatalf("add member variables = %#v", requests[2].Variables)
	}
}

func TestIssueUserCredentialShowsSecretOnceWithWarning(t *testing.T) {
	requests := make([]userCommandRequest, 0, 1)
	server := userCommandServer(t, &requests)
	defer server.Close()

	output := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"user", "credential", "issue", "ada@example.test", "--name", "laptop"})

	if strings.Count(output, "fsk_once_only") != 1 || !strings.Contains(output, "will not be shown again") || !strings.Contains(output, "Keep it secret") {
		t.Fatalf("credential output = %q", output)
	}
	if requests[0].Variables["name"] != "laptop" || strings.Contains(requests[0].Query, "fsk_once_only") {
		t.Fatalf("credential request = %#v", requests[0])
	}
}

func TestMalformedIssueCredentialDoesNotLeakToErrorOrTelemetry(t *testing.T) {
	const secret = "fsk_malformed_response_must_never_leak"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"issueUserCredential":{"secret":"` + secret))
	}))
	defer server.Close()

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previousProvider) })

	message := runCommandInDirExpectError(t, t.TempDir(), server.URL, []string{"user", "credential", "issue", "user-1", "--name", "laptop"})
	if !strings.Contains(message, "graphql_response_malformed") {
		t.Fatalf("error lacks actionable category: %q", message)
	}
	if strings.Contains(message, secret) {
		t.Fatalf("command error leaked credential: %q", message)
	}

	telemetry := fmt.Sprintf("%#v", exporter.GetSpans())
	if !strings.Contains(telemetry, "graphql_response_malformed") || !strings.Contains(telemetry, "error.code") {
		t.Fatalf("expected safe error to be recorded in telemetry: %s", telemetry)
	}
	if strings.Contains(telemetry, "exception.message") {
		t.Fatalf("telemetry should not record remote error messages: %s", telemetry)
	}
	if strings.Contains(telemetry, secret) {
		t.Fatalf("telemetry leaked credential: %s", telemetry)
	}
}

func TestUserAndMembershipMutationCommandsUseSharedFields(t *testing.T) {
	requests := make([]userCommandRequest, 0, 6)
	server := userCommandServer(t, &requests)
	defer server.Close()

	runCommandInDir(t, t.TempDir(), server.URL, []string{"user", "create", "Ada@Example.test", "--name", "Ada"})
	runCommandInDir(t, t.TempDir(), server.URL, []string{"user", "update", "ada@example.test", "--name", "Ada Lovelace"})
	runCommandInDir(t, t.TempDir(), server.URL, []string{"user", "suspend", "ada@example.test"})
	runCommandInDir(t, t.TempDir(), server.URL, []string{"user", "reactivate", "ada@example.test"})
	runCommandInDir(t, t.TempDir(), server.URL, []string{"user", "credential", "revoke", "ada@example.test", "laptop"})
	runCommandInDir(t, t.TempDir(), server.URL, []string{"team", "member", "remove", "platform", "ada@example.test"})

	fields := []string{"createUser", "updateUser", "suspendUser", "reactivateUser", "revokeUserCredential", "removeTeamMember"}
	if len(requests) != len(fields) {
		t.Fatalf("request count = %d, want %d", len(requests), len(fields))
	}
	for i, field := range fields {
		if !strings.Contains(requests[i].Query, field+"(") {
			t.Errorf("request %d does not use %s: %s", i, field, requests[i].Query)
		}
	}
}

func TestUserUpdateRequiresAField(t *testing.T) {
	userUpdateEmail, userUpdateName = "", ""
	userUpdateCmd.Flags().Lookup("email").Changed = false
	userUpdateCmd.Flags().Lookup("name").Changed = false
	message := runCommandInDirExpectError(t, t.TempDir(), "http://unused.invalid", []string{"user", "update", "user-1"})
	if !strings.Contains(message, "provide --email or --name") {
		t.Fatalf("unexpected error = %q", message)
	}
}

func TestMembershipRoleValidation(t *testing.T) {
	if got, err := normalizeMembershipRole("Manager"); err != nil || got != "MANAGER" {
		t.Fatalf("normalizeMembershipRole = %q, %v", got, err)
	}
	if _, err := normalizeMembershipRole("owner"); err == nil {
		t.Fatal("expected invalid membership role")
	}
}

type userCommandRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

func userCommandServer(t *testing.T, requests *[]userCommandRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request userCommandRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		*requests = append(*requests, request)
		w.Header().Set("Content-Type", "application/json")
		member := `{"user_id":"user-1","email":"Ada@Example.test","display_name":"Ada","status":"ACTIVE","membership_role":"MEMBER","created_at":"now"}`
		switch {
		case strings.Contains(request.Query, "query Users"):
			_, _ = w.Write([]byte(`{"data":{"users":{"total":1,"items":[{"id":"user-1","email":"Ada@Example.test","display_name":"Ada","status":"ACTIVE","created_at":"now","updated_at":"now"}]}}}`))
		case strings.Contains(request.Query, "query TeamMembers"):
			_, _ = w.Write([]byte(`{"data":{"teamMembers":{"total":1,"items":[` + member + `]}}}`))
		case strings.Contains(request.Query, "addTeamMember("):
			_, _ = w.Write([]byte(`{"data":{"addTeamMember":{"membership":` + member + `,"authorization_revision":3,"changed":true}}}`))
		case strings.Contains(request.Query, "issueUserCredential("):
			_, _ = w.Write([]byte(`{"data":{"issueUserCredential":{"credential":{"id":"credential-1","name":"laptop","key_prefix":"fsk_abcd","expires_at":null,"last_used_at":null,"revoked_at":null,"created_at":"now"},"secret":"fsk_once_only","authorization_revision":3,"changed":true}}}`))
		case strings.Contains(request.Query, "revokeUserCredential("):
			_, _ = w.Write([]byte(`{"data":{"revokeUserCredential":{"credential":null,"authorization_revision":3,"changed":true}}}`))
		case strings.Contains(request.Query, "removeTeamMember("):
			_, _ = w.Write([]byte(`{"data":{"removeTeamMember":{"membership":null,"authorization_revision":3,"changed":true}}}`))
		default:
			for _, field := range []string{"createUser", "updateUser", "suspendUser", "reactivateUser"} {
				if strings.Contains(request.Query, field+"(") {
					_, _ = w.Write([]byte(`{"data":{"` + field + `":{"user":{"id":"user-1","email":"Ada@Example.test","display_name":"Ada","status":"ACTIVE","memberships":[],"credentials":[],"created_at":"now","updated_at":"now"},"authorization_revision":3,"changed":true}}}`))
					return
				}
			}
			t.Fatalf("unexpected query: %s", request.Query)
		}
	}))
}
