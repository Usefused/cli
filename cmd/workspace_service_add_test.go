package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// resetWorkspaceServiceAddState isolates mutable Cobra flag storage between tests.
func resetWorkspaceServiceAddState(t *testing.T) {
	t.Helper()
	oldVersion, oldID, oldInteractive, oldApply := workspaceServiceAddVersion, workspaceServiceAddID, workspaceServiceAddInteractive, workspaceServiceAddApply
	oldRequestID := RequestID
	workspaceServiceAddVersion, workspaceServiceAddID, workspaceServiceAddInteractive, workspaceServiceAddApply = "", "", false, false
	RequestID = ""
	t.Cleanup(func() {
		workspaceServiceAddVersion, workspaceServiceAddID, workspaceServiceAddInteractive, workspaceServiceAddApply = oldVersion, oldID, oldInteractive, oldApply
		RequestID = oldRequestID
	})
}

// TestWorkspaceAddMultipleServicesAndApplyUsesOnlyScopedActivations verifies the
// composite batches reads, writes config once, and never full-mirrors removals.
func TestWorkspaceAddMultipleServicesAndApplyUsesOnlyScopedActivations(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", `apiVersion: fused/v1
kind: workspace
services:
  existing:
    service_id: "00000000-0000-4000-8000-000000000099"
`)
	server, engineReads, registryReads, activations := newCompositeWorkspaceServiceServer(t)
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "add", "linear", "square", "--apply", "-f", path})
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Workspace.Services) != 3 || parsed.Workspace.Services["existing"].ServiceID == "" {
		t.Fatalf("composite did not preserve unrelated local intent: %#v", parsed.Workspace.Services)
	}
	if *engineReads != 1 || *registryReads != 1 || len(*activations) != 2 {
		t.Fatalf("unexpected request counts engine=%d registry=%d activations=%d", *engineReads, *registryReads, len(*activations))
	}
	if (*activations)[0].ServiceID != "00000000-0000-4000-8000-000000000001" || (*activations)[1].ServiceID != "00000000-0000-4000-8000-000000000002" {
		t.Fatalf("unexpected scoped activations: %#v", *activations)
	}
	assertCompositeWorkspaceServiceOutput(t, out)
}

// assertCompositeWorkspaceServiceOutput verifies each scoped success remains
// independently visible instead of collapsing mutations into vague prose.
func assertCompositeWorkspaceServiceOutput(t *testing.T, output string) {
	t.Helper()
	// Both canonical service identities are required because a single success
	// line could conceal a silently skipped activation.
	if !strings.Contains(output, "Activated service linear in workspace") || !strings.Contains(output, "Activated service square in workspace") {
		t.Fatalf("missing composite activation output: %q", output)
	}
}

// newCompositeWorkspaceServiceServer records bounded discovery and activation
// calls so the test can assert the composite reuses scoped infrastructure.
func newCompositeWorkspaceServiceServer(t *testing.T) (*httptest.Server, *int, *int, *[]api.AddWorkspaceServiceRequest) {
	t.Helper()
	engineReads, registryReads := 0, 0
	activationRequestID := ""
	activations := make([]api.AddWorkspaceServiceRequest, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// Each endpoint represents one existing production boundary; any other
		// route would signal duplicated or unexpectedly broadened orchestration.
		switch request.URL.Path {
		case "/health":
			_, _ = writer.Write([]byte(`{"environment":"development"}`))
		case "/engine/graphql":
			engineReads++
			_, _ = writer.Write([]byte(`{"data":{"workspaceServices":[]}}`))
		case "/graphql":
			registryReads++
			_, _ = writer.Write([]byte(`{"data":{"serviceCandidatesByRefs":[{"ref":"linear","candidates":[{"id":"00000000-0000-4000-8000-000000000001","name":"Linear","slug":"linear","is_owner":true,"is_public":true}]},{"ref":"square","candidates":[{"id":"00000000-0000-4000-8000-000000000002","name":"Square","slug":"square","is_owner":true,"is_public":true}]}]}}`))
		case "/workspace/services":
			requestID := request.Header.Get("X-Request-ID")
			// A generated UUID must be present before the first mutation and reused by
			// every sibling activation in the composite command.
			if _, err := uuid.Parse(requestID); err != nil {
				t.Errorf("activation request ID %q is not a generated UUID: %v", requestID, err)
			}
			if activationRequestID != "" && activationRequestID != requestID {
				t.Errorf("sibling activation request IDs differ: %q and %q", activationRequestID, requestID)
			}
			activationRequestID = requestID
			var activation api.AddWorkspaceServiceRequest
			// A decodable payload is necessary to prove exact resolved identities,
			// not merely the number of scoped HTTP requests.
			if err := json.NewDecoder(request.Body).Decode(&activation); err != nil {
				t.Fatalf("decode activation: %v", err)
			}
			activations = append(activations, activation)
			_, _ = writer.Write([]byte(`{"status":"ok"}`))
		default:
			t.Fatalf("unexpected composite request %s %s", request.Method, request.URL.Path)
		}
	}))
	return server, &engineReads, &registryReads, &activations
}

// TestWorkspaceAddMultipleServicesRejectsSingularServiceID ensures one escape-
// hatch identity cannot be accidentally applied to several service references.
func TestWorkspaceAddMultipleServicesRejectsSingularServiceID(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	errText := runCommandInDirExpectError(t, dir, "http://127.0.0.1:1", []string{
		"workspace", "service", "add", "linear", "square", "--service-id", "00000000-0000-4000-8000-000000000001", "-f", path,
	})
	if !strings.Contains(errText, "--service-id can only be used with one service reference") {
		t.Fatalf("unexpected multi-ID error: %q", errText)
	}
	assertWorkspaceConfigContainsNoServices(t, path)
}

// TestWorkspaceAddMultipleExactIDsWithoutApplyIsConfigOnly protects the default
// declarative workflow and avoids an unbounded lookup when every ref is exact.
func TestWorkspaceAddMultipleExactIDsWithoutApplyIsConfigOnly(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	first := "00000000-0000-4000-8000-000000000011"
	second := "00000000-0000-4000-8000-000000000012"
	runCommandInDir(t, dir, "http://127.0.0.1:1", []string{"workspace", "service", "add", first, second, "-f", path})

	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Workspace.Services) != 2 || parsed.Workspace.Services[first].ServiceID != first || parsed.Workspace.Services[second].ServiceID != second {
		t.Fatalf("config-only multi-add lost exact identities: %#v", parsed.Workspace.Services)
	}
}

// TestWorkspaceAddRejectsResolvedIdentityConflictBeforeWriteOrApply protects
// local declarative identity for both workspace and Registry discovery sources.
func TestWorkspaceAddRejectsResolvedIdentityConflictBeforeWriteOrApply(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		workspaceJSON string
		registryJSON  string
	}{
		{
			name: "workspace",
			workspaceJSON: `[{
				"service_id":"00000000-0000-4000-8000-000000000021",
				"service_name":"Billing","service_slug":"billing"
			}]`,
			registryJSON: `[]`,
		},
		{
			name:          "registry",
			workspaceJSON: `[]`,
			registryJSON: `[{
				"id":"00000000-0000-4000-8000-000000000022",
				"name":"Acme Billing","slug":"billing","provider":{"handle":"acme"},"is_owner":false,"is_public":true
			}]`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			resetWorkspaceServiceAddState(t)
			dir := t.TempDir()
			path := writeSprintConfig(t, dir, "workspace.yaml", `apiVersion: fused/v1
kind: workspace
services:
  billing:
    service_id: "00000000-0000-4000-8000-000000000099"
`)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			server, _ := newWorkspaceServiceDiscoveryServer(t, testCase.workspaceJSON, testCase.registryJSON)
			defer server.Close()

			errText := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "add", "billing", "--apply", "-f", path})
			if !strings.Contains(errText, "already has service_id") || !strings.Contains(errText, "refusing to resolve or activate") {
				t.Fatalf("missing identity conflict: %q", errText)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatalf("identity conflict changed config:\n%s", after)
			}
		})
	}
}

// TestWorkspaceAddApplyReportsPartialOutcomeAndExactRecovery verifies that a
// failed scoped suffix remains safely recoverable after an earlier commit.
func TestWorkspaceAddApplyReportsPartialOutcomeAndExactRecovery(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	server, activationCalls := newPartialWorkspaceServiceApplyServer(t)
	defer server.Close()

	errText := runCommandInDirExpectError(t, dir, server.URL, []string{
		"workspace", "service", "add", "linear", "square", "guard", "--apply", "-f", path,
		"--request-id", "composite-review-42",
	})
	for _, expected := range []string{
		"code=workspace_service_apply_partial", "phase=engine_scoped_activation", "request_id=",
		"committed=linear", "failed=square (not_committed, commit_possible=false)", "unattempted=guard",
		"workspace service add 'square' --service-id '00000000-0000-4000-8000-000000000002'",
		"workspace service add 'guard' --service-id '00000000-0000-4000-8000-000000000003'",
	} {
		if !strings.Contains(errText, expected) {
			t.Fatalf("partial outcome missing %q: %s", expected, errText)
		}
	}
	if *activationCalls != 2 {
		t.Fatalf("activation calls = %d, want 2", *activationCalls)
	}
	// The CLI-generated correlation identity stays bounded and explicit even
	// though Engine owns trusted request IDs for each scoped child mutation.
	requestIDText := strings.SplitN(strings.SplitN(errText, "request_id=", 2)[1], ";", 2)[0]
	if requestIDText != "composite-review-42" {
		t.Fatalf("partial outcome request ID %q did not preserve the audit identity: %s", requestIDText, errText)
	}
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Workspace.Services) != 3 {
		t.Fatalf("partial apply did not preserve full retry intent: %#v", parsed.Workspace.Services)
	}
}

// newPartialWorkspaceServiceApplyServer rejects the second scoped mutation to
// create one deterministic committed/failed/unattempted boundary.
func newPartialWorkspaceServiceApplyServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	activationCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// Discovery succeeds in bounded calls so only sequential scoped activation
		// determines the partial outcome exercised by this fixture.
		switch request.URL.Path {
		case "/health":
			_, _ = writer.Write([]byte(`{"environment":"development"}`))
		case "/engine/graphql":
			_, _ = writer.Write([]byte(`{"data":{"workspaceServices":[]}}`))
		case "/graphql":
			_, _ = writer.Write([]byte(`{"data":{"serviceCandidatesByRefs":[
				{"ref":"linear","candidates":[{"id":"00000000-0000-4000-8000-000000000001","name":"Linear","slug":"linear","is_owner":true}]},
				{"ref":"square","candidates":[{"id":"00000000-0000-4000-8000-000000000002","name":"Square","slug":"square","is_owner":true}]},
				{"ref":"guard","candidates":[{"id":"00000000-0000-4000-8000-000000000003","name":"Guard","slug":"guard","is_owner":true}]}
			]}}`))
		case "/workspace/services":
			activationCalls++
			// An explicit composite identity must reach each scoped child request,
			// matching the ID rendered in the partial outcome and OTEL attributes.
			if request.Header.Get("X-Request-ID") != "composite-review-42" {
				t.Errorf("activation request ID = %q", request.Header.Get("X-Request-ID"))
			}
			// The second known rejection leaves the first committed and the third
			// unattempted, which is the partial boundary the command must expose.
			if activationCalls == 2 {
				http.Error(writer, `{"error":"version not found"}`, http.StatusBadRequest)
				return
			}
			// A third request would prove the command ignored its first failure and
			// could no longer provide a trustworthy unattempted suffix.
			if activationCalls > 2 {
				t.Fatalf("unattempted target was mutated")
			}
			_, _ = writer.Write([]byte(`{"status":"ok"}`))
		default:
			t.Fatalf("unexpected request %s", request.URL.Path)
		}
	}))
	return server, &activationCalls
}

// TestWorkspaceServiceApplyOutcomeRedactsUnsafeRemoteText verifies human and
// structured partial results never reflect causes, URLs, controls, or bad refs.
func TestWorkspaceServiceApplyOutcomeRedactsUnsafeRemoteText(t *testing.T) {
	apiCause := &api.APIError{Code: "unsafe\ncode", Message: "POST https://private.invalid/path\nfsk_secret", HTTPStatus: http.StatusBadGateway}
	err := &workspaceServiceApplyOutcomeError{
		code: "bad\ncode", phase: "bad\rphase", requestID: "bad\nid",
		committed: []string{"https://private.invalid/service"}, failed: "evil\nslug",
		failedCommitState: "unknown\nstate", failedCommitPossible: true,
		unattempted: []string{"fsk_secret"}, recovery: "curl https://private.invalid\nfsk_secret",
		cause: fmt.Errorf("remote request failed: %w", apiCause),
	}
	human := err.Error()
	// Only bounded fallback fields and numeric status may survive presentation.
	for _, unsafe := range []string{"private.invalid", "fsk_secret", "evil\nslug", "unsafe\ncode", "remote request failed"} {
		if strings.Contains(human, unsafe) {
			t.Fatalf("human partial outcome leaked %q: %s", unsafe, human)
		}
	}
	if !strings.Contains(human, "failure_code=request_failed; http_status=502") || !strings.Contains(human, workspaceServiceSafeRecovery) {
		t.Fatalf("human partial outcome lost safe classifier metadata: %s", human)
	}
	var unwrapped *api.APIError
	// Unwrap remains available for internal status and telemetry classification.
	if !errors.As(err, &unwrapped) || unwrapped != apiCause {
		t.Fatalf("partial outcome did not retain typed cause: %v", err)
	}
	result := classifyCommandError(&cobra.Command{Use: "add"}, err)
	encoded, encodeErr := json.Marshal(result)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	// Agent output follows the same safe projection as human output.
	if strings.Contains(string(encoded), "private.invalid") || strings.Contains(string(encoded), "fsk_secret") || strings.Contains(string(encoded), "evil\\nslug") {
		t.Fatalf("structured partial outcome leaked unsafe text: %s", encoded)
	}
}

// TestPrepareWorkspaceServiceAddTargetsRejectsMalformedProviderPrefix keeps the
// identity-only batch resolver from treating incomplete @ references lexically.
func TestPrepareWorkspaceServiceAddTargetsRejectsMalformedProviderPrefix(t *testing.T) {
	for _, ref := range []string{"@acme", "@acme/", "@/billing", "@acme/billing/v2", "@ac me/billing", "@acme/\x1bbilling", "@acme/\nhttps://private.invalid"} {
		// Rejection occurs locally before Engine or Registry discovery can interpret
		// the malformed provider-prefixed text under a different lookup policy.
		if _, _, _, _, err := prepareWorkspaceServiceAddTargets([]string{ref}); err == nil || err.Error() != "provider-qualified service references must use @provider/service-slug" {
			t.Fatalf("malformed provider ref %q returned %v", ref, err)
		}
	}
}

// TestClassifyWorkspaceServiceApplyOutcomePreservesRecoveryMetadata keeps the
// slim composite state available to future structured command output.
func TestClassifyWorkspaceServiceApplyOutcomePreservesRecoveryMetadata(t *testing.T) {
	err := &workspaceServiceApplyOutcomeError{
		code: workspaceServiceApplyErrorCode, phase: workspaceServiceApplyPhase,
		requestID: "11111111-1111-4111-8111-111111111111",
		committed: []string{"linear"}, failed: "square", failedCommitState: "unknown", failedCommitPossible: true,
		unattempted: []string{"guard"}, recovery: "fused-cli workspace service add square --apply", cause: errors.New("lost response"),
	}
	result := classifyCommandError(&cobra.Command{Use: "add"}, err)
	// Stable top-level fields let agents recover without parsing human prose.
	if result.Code != workspaceServiceApplyErrorCode || result.Phase != workspaceServiceApplyPhase || result.RequestID != err.requestID || result.CommitState != "unknown" || result.Recovery != err.recovery {
		t.Fatalf("classified workspace apply outcome = %#v", result)
	}
	// Unknown delivery must preserve commit possibility in structured details.
	if possible, _ := result.Details["failed_commit_possible"].(bool); !possible {
		t.Fatalf("classified workspace apply lost commit possibility: %#v", result.Details)
	}
}

// TestWorkspaceServiceFailedCommitState keeps ambiguous server failures from
// being misreported as authoritative pre-commit rejections.
func TestWorkspaceServiceFailedCommitState(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		httpStatus int
		want       string
	}{
		{name: "client rejection", httpStatus: http.StatusConflict, want: "not_committed"},
		{name: "proxy timeout", httpStatus: http.StatusRequestTimeout, want: "unknown"},
		{name: "server failure", httpStatus: http.StatusInternalServerError, want: "unknown"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// The classification depends only on authoritative transport status;
			// remote prose must never influence whether a retry is declared safe.
			got := workspaceServiceFailedCommitState(&api.APIError{HTTPStatus: testCase.httpStatus})
			if got != testCase.want {
				t.Fatalf("commit state = %q, want %q", got, testCase.want)
			}
		})
	}
}

// newWorkspaceServiceDiscoveryServer exposes the shared workspace catalogue
// fixtures used by both service search and workspace-add command tests.
func newWorkspaceServiceDiscoveryServer(t *testing.T, workspaceJSON, registryJSON string) (*httptest.Server, *int) {
	t.Helper()
	registryCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode GraphQL request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/engine/graphql":
			if !strings.Contains(request.Query, "workspaceServices") {
				t.Errorf("expected workspaceServices query, got %q", request.Query)
			}
			_, _ = w.Write([]byte(`{"data":{"workspaceServices":` + workspaceJSON + `}}`))
		case "/graphql":
			registryCalls++
			writeWorkspaceServiceRegistryFixture(t, w, request.Query, request.Variables, registryJSON)
		default:
			t.Errorf("unexpected request path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	return server, &registryCalls
}

// writeWorkspaceServiceRegistryFixture serves each established GraphQL field
// while dedicated API tests enforce singular-add set-based resolver parity.
func writeWorkspaceServiceRegistryFixture(t *testing.T, w http.ResponseWriter, query string, variables map[string]any, registryJSON string) {
	t.Helper()
	// Workspace add uses the provider-aware set resolver and needs its positional
	// group envelope preserved even for a singular reference.
	if strings.Contains(query, "serviceCandidatesByRefs") {
		refs, _ := variables["refs"].([]any)
		ref := ""
		// The shared helper only models singular calls; the composite fixture
		// separately asserts the complete multi-reference variable list.
		if len(refs) == 1 {
			ref, _ = refs[0].(string)
		}
		encodedRef, _ := json.Marshal(ref)
		_, _ = w.Write([]byte(`{"data":{"serviceCandidatesByRefs":[{"ref":` + string(encodedRef) + `,"candidates":` + registryJSON + `}]}}`))
		return
	}
	// The read-only service search command intentionally retains its lexical
	// catalogue field, so shared display tests require the legacy response shape.
	if strings.Contains(query, "searchServices") {
		_, _ = w.Write([]byte(`{"data":{"searchServices":` + registryJSON + `}}`))
		return
	}
	// Any third Registry operation would make this broad fixture silently mask a
	// new boundary, so fail closed rather than inventing another response shape.
	t.Errorf("expected service catalogue query, got %q", query)
	http.Error(w, "unexpected Registry query", http.StatusBadRequest)
}

func TestWorkspaceAddServiceFallsBackToRegistryAndAutoAddsUniqueMatch(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	server, registryCalls := newWorkspaceServiceDiscoveryServer(t, `[]`, `[
		{"id":"00000000-0000-4000-8000-000000000002","name":"Acme Billing","slug":"billing","provider":{"handle":"acme"},"is_owner":false,"is_public":true}
	]`)
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "add", "billing", "-f", path})
	if *registryCalls != 1 {
		t.Fatalf("Registry search calls = %d, want 1", *registryCalls)
	}
	assertWorkspaceConfigContains(t, path, "@acme/billing:", false)
	if !strings.Contains(out, "planning will resolve its latest public version") {
		t.Fatalf("expected latest-version guidance, got %q", out)
	}
	wantView := server.URL + "/integrations/00000000-0000-4000-8000-000000000002"
	if !strings.Contains(out, "View @acme/billing: "+wantView) {
		t.Fatalf("expected canonical slug and Engine link, got %q", out)
	}
}

func TestWorkspaceAddServiceInteractiveConfirmsRegistryMatch(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	server, _ := newWorkspaceServiceDiscoveryServer(t, `[]`, `[
		{"id":"00000000-0000-4000-8000-000000000003","name":"Drive","slug":"drive","provider":{"handle":"google"},"is_owner":false,"is_public":true}
	]`)
	defer server.Close()

	oldConfirm := confirmWorkspaceRegistryService
	confirmWorkspaceRegistryService = func(service serviceSearchResult) (bool, error) {
		if service.Slug != "@google/drive" {
			t.Fatalf("confirmed service = %#v", service)
		}
		return true, nil
	}
	t.Cleanup(func() { confirmWorkspaceRegistryService = oldConfirm })

	runCommandInDir(t, dir, server.URL, []string{"workspace", "service", "add", "drive", "--interactive", "-f", path})
	assertWorkspaceConfigContains(t, path, "@google/drive:", false)
}

func TestWorkspaceAddServiceInteractiveCancellationDoesNotWrite(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	server, _ := newWorkspaceServiceDiscoveryServer(t, `[]`, `[
		{"id":"00000000-0000-4000-8000-000000000004","name":"Drive","slug":"drive","provider":{"handle":"google"},"is_owner":false,"is_public":true}
	]`)
	defer server.Close()

	oldConfirm := confirmWorkspaceRegistryService
	confirmWorkspaceRegistryService = func(service serviceSearchResult) (bool, error) { return false, nil }
	t.Cleanup(func() { confirmWorkspaceRegistryService = oldConfirm })
	errText := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "add", "drive", "--interactive", "-f", path})
	if !strings.Contains(errText, "service addition cancelled") {
		t.Fatalf("expected cancellation error, got %q", errText)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("cancelled add changed config:\n%s", after)
	}
}

func TestWorkspaceAddServiceNonInteractiveRejectsAmbiguousRegistryQuery(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	server, _ := newWorkspaceServiceDiscoveryServer(t, `[]`, `[
		{"id":"00000000-0000-4000-8000-000000000005","name":"Acme Billing","slug":"billing","provider":{"handle":"acme"},"is_owner":false,"is_public":true},
		{"id":"00000000-0000-4000-8000-000000000006","name":"Other Billing","slug":"billing","provider":{"handle":"other"},"is_owner":false,"is_public":true}
	]`)
	defer server.Close()

	errText := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "add", "bill", "-f", path})
	if !strings.Contains(errText, "matched 2 Registry services") || !strings.Contains(errText, "--interactive") {
		t.Fatalf("expected actionable ambiguity error, got %q", errText)
	}
}

func TestWorkspaceAddServiceStopsOnAmbiguousWorkspaceMatch(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	server, registryCalls := newWorkspaceServiceDiscoveryServer(t, `[
		{"service_id":"00000000-0000-4000-8000-000000000021","service_name":"Acme Billing","service_slug":"@acme/billing"},
		{"service_id":"00000000-0000-4000-8000-000000000022","service_name":"Other Billing","service_slug":"@other/billing"}
	]`, `[]`)
	defer server.Close()

	errText := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "add", "billing", "-f", path})
	if !strings.Contains(errText, "matches multiple workspace services") || !strings.Contains(errText, "--service-id") {
		t.Fatalf("expected actionable workspace ambiguity error, got %q", errText)
	}
	if *registryCalls != 0 {
		t.Fatalf("ambiguous workspace match must not fall through to Registry, got %d calls", *registryCalls)
	}
	assertWorkspaceConfigContainsNoServices(t, path)
}

func TestWorkspaceAddServiceQualifiedQueryIgnoresDifferentWorkspaceProvider(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	server, registryCalls := newWorkspaceServiceDiscoveryServer(t, `[
		{"service_id":"00000000-0000-4000-8000-000000000025","service_name":"Acme Billing","service_slug":"@acme/billing"}
	]`, `[
		{"id":"00000000-0000-4000-8000-000000000026","name":"Other Billing","slug":"billing","provider":{"handle":"other"},"is_owner":false,"is_public":true}
	]`)
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"workspace", "service", "add", "@other/billing", "-f", path})
	if *registryCalls != 1 {
		t.Fatalf("different provider workspace result must fall through to Registry, got %d calls", *registryCalls)
	}
	assertWorkspaceConfigContains(t, path, "@other/billing:", false)
}

func TestWorkspaceAddServiceInteractiveSelectsAmbiguousWorkspaceMatch(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	server, registryCalls := newWorkspaceServiceDiscoveryServer(t, `[
		{"service_id":"00000000-0000-4000-8000-000000000031","service_name":"Acme Billing","service_slug":"@acme/billing"},
		{"service_id":"00000000-0000-4000-8000-000000000032","service_name":"Other Billing","service_slug":"@other/billing"}
	]`, `[]`)
	defer server.Close()

	oldSelect := selectExistingWorkspaceService
	selectExistingWorkspaceService = func(services []api.WorkspaceService) (api.WorkspaceService, error) {
		return services[1], nil
	}
	t.Cleanup(func() { selectExistingWorkspaceService = oldSelect })
	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "add", "billing", "--interactive", "-f", path})
	if *registryCalls != 0 {
		t.Fatalf("selected workspace match must not search Registry, got %d calls", *registryCalls)
	}
	assertWorkspaceConfigContains(t, path, "@other/billing:", false)
	if !strings.Contains(out, "View @other/billing: "+server.URL+"/integrations/00000000-0000-4000-8000-000000000032") {
		t.Fatalf("expected selected workspace service link, got %q", out)
	}
}

func TestChooseRegistryServiceUsesExactQualifiedSlugWithoutPrompt(t *testing.T) {
	results := []serviceSearchResult{
		{Name: "Acme Billing", Slug: "@acme/billing", ServiceID: "service-acme"},
		{Name: "Other Billing", Slug: "@other/billing", ServiceID: "service-other"},
	}
	selected, err := chooseRegistryService("@other/billing", results, false)
	if err != nil {
		t.Fatal(err)
	}
	if selected.ServiceID != "service-other" {
		t.Fatalf("selected %#v, want exact qualified slug match", selected)
	}
}

func TestChooseRegistryServiceAcceptsEveryExactIdentity(t *testing.T) {
	results := []serviceSearchResult{
		{Name: "Acme Billing", Slug: "@acme/billing", ServiceID: "service-acme"},
		{Name: "Other Billing", Slug: "@other/billing", ServiceID: "service-other"},
	}
	for _, query := range []string{"OTHER BILLING", "SERVICE-OTHER"} {
		selected, err := chooseRegistryService(query, results, false)
		if err != nil {
			t.Fatalf("choose exact identity %q: %v", query, err)
		}
		if selected.ServiceID != "service-other" {
			t.Fatalf("query %q selected %#v, want exact identity", query, selected)
		}
	}
}

func TestChooseRegistryServiceInteractiveUsesSharedSelector(t *testing.T) {
	oldSelect := selectWorkspaceRegistryService
	selectWorkspaceRegistryService = func(results []serviceSearchResult) (serviceSearchResult, error) {
		return results[1], nil
	}
	t.Cleanup(func() { selectWorkspaceRegistryService = oldSelect })
	results := []serviceSearchResult{
		{Name: "Acme Billing", Slug: "@acme/billing", ServiceID: "service-acme"},
		{Name: "Other Billing", Slug: "@other/billing", ServiceID: "service-other"},
	}
	selected, err := chooseRegistryService("bill", results, true)
	if err != nil {
		t.Fatal(err)
	}
	if selected.ServiceID != "service-other" {
		t.Fatalf("selected %#v, want interactive choice", selected)
	}
}

func TestWorkspaceAddServiceStopsOnRegistryPermissionFailure(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/engine/graphql" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"workspaceServices":[]}}`))
			return
		}
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
	}))
	defer server.Close()

	errText := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "add", "billing", "-f", path})
	if !strings.Contains(strings.ToLower(errText), "forbidden") && !strings.Contains(errText, "403") {
		t.Fatalf("expected Registry permission failure, got %q", errText)
	}
	assertWorkspaceConfigContainsNoServices(t, path)
}

func TestWorkspaceAddServiceStopsOnWorkspacePermissionFailure(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	registryCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engine/graphql":
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		case "/graphql":
			registryCalls++
			http.Error(w, "unexpected Registry fallback", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	errText := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "add", "billing", "-f", path})
	if !strings.Contains(strings.ToLower(errText), "forbidden") && !strings.Contains(errText, "403") {
		t.Fatalf("expected workspace permission failure, got %q", errText)
	}
	if registryCalls != 0 {
		t.Fatalf("workspace permission failure must not fall through to Registry, got %d calls", registryCalls)
	}
	assertWorkspaceConfigContainsNoServices(t, path)
}

func TestWorkspaceAddServiceNoInputAutoAddsUniqueRegistryMatch(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	oldNoInput := NoInput
	NoInput = true
	t.Cleanup(func() { NoInput = oldNoInput })
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	server, _ := newWorkspaceServiceDiscoveryServer(t, `[]`, `[
		{"id":"00000000-0000-4000-8000-000000000023","name":"Drive","slug":"drive","provider":{"handle":"google"},"is_owner":false,"is_public":true}
	]`)
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"workspace", "service", "add", "drive", "-f", path})
	assertWorkspaceConfigContains(t, path, "@google/drive:", false)
}

func TestWorkspaceAddServiceExplicitServiceIDSkipsDiscoveryAndPreservesYAML(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	serviceID := "00000000-0000-4000-8000-000000000024"

	out := runCommandInDirOutput(t, dir, "http://127.0.0.1:1", []string{
		"workspace", "service", "add", "private-service", "--service-id", serviceID, "--version", "1.2.3", "-f", path,
	})
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service, ok := parsed.Workspace.Services["private-service"]
	if !ok || service.ServiceID != serviceID || len(service.Versions) != 1 || service.Versions[0].Version != "1.2.3" {
		t.Fatalf("explicit service identity was not preserved: %#v", parsed.Workspace.Services)
	}
	if !strings.Contains(out, "Added service private-service with version 1.2.3") {
		t.Fatalf("unexpected explicit add output %q", out)
	}
	if !strings.Contains(out, "View private-service: http://127.0.0.1:1/integrations/"+serviceID) {
		t.Fatalf("expected explicit service link, got %q", out)
	}
}

func TestWorkspaceServiceViewURLNormalizesBaseAndEscapesID(t *testing.T) {
	got := workspaceServiceViewURL("HTTPS://ENGINE.EXAMPLE/base/?transport=api#fragment", "service/id")
	want := "https://engine.example/base/integrations/service%2Fid"
	if got != want {
		t.Fatalf("workspaceServiceViewURL() = %q, want %q", got, want)
	}
	if got := workspaceServiceViewURL("not-a-url", "service-id"); got != "" {
		t.Fatalf("invalid Engine URL produced link %q", got)
	}
}

func TestWorkspaceAddServiceUUIDReferenceSkipsDiscovery(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	serviceID := "00000000-0000-4000-8000-000000000043"

	runCommandInDir(t, dir, "http://127.0.0.1:1", []string{
		"workspace", "service", "add", serviceID, "-f", path,
	})
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service, ok := parsed.Workspace.Services[serviceID]
	if !ok || service.ServiceID != serviceID {
		t.Fatalf("UUID service reference was not preserved: %#v", parsed.Workspace.Services)
	}
}

func TestWorkspaceAddServicePreservesExistingLocalServiceMetadata(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	serviceID := "00000000-0000-4000-8000-000000000041"
	path := writeSprintConfig(t, dir, "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  billing:
    service_id: "00000000-0000-4000-8000-000000000041"
    public: true
    versions:
      - version: "1.0.0"
`)
	runCommandInDir(t, dir, "http://127.0.0.1:1", []string{
		"workspace", "service", "add", "billing", "--service-id", serviceID, "--version", "2.0.0", "-f", path,
	})
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service := parsed.Workspace.Services["billing"]
	if service.Public == nil || !*service.Public || service.ServiceID != serviceID {
		t.Fatalf("existing service metadata was not preserved: %#v", service)
	}
	if len(service.Versions) != 2 || service.Versions[0].Version != "1.0.0" || service.Versions[1].Version != "2.0.0" {
		t.Fatalf("versions were not merged additively: %#v", service.Versions)
	}
}

func TestWorkspaceAddServiceTrimsWhitespaceVersion(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	out := runCommandInDirOutput(t, dir, "http://127.0.0.1:1", []string{
		"workspace", "service", "add", "billing", "--service-id", "00000000-0000-4000-8000-000000000042", "--version", "   ", "-f", path,
	})
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if versions := parsed.Workspace.Services["billing"].Versions; len(versions) != 0 {
		t.Fatalf("whitespace version was persisted: %#v", versions)
	}
	if !strings.Contains(out, "planning will resolve its latest public version") {
		t.Fatalf("unexpected output %q", out)
	}
}

func TestWorkspaceAddServiceRejectsInvalidExplicitServiceID(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	errText := runCommandInDirExpectError(t, dir, "http://127.0.0.1:1", []string{
		"workspace", "service", "add", "billing", "--service-id", "not-a-uuid", "-f", path,
	})
	if !strings.Contains(errText, "--service-id must be a valid Registry service UUID") {
		t.Fatalf("expected UUID validation error, got %q", errText)
	}
	assertWorkspaceConfigContainsNoServices(t, path)
}

func TestWorkspaceAddServiceInteractiveIsRejectedInCI(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	t.Setenv("CI", "true")
	errText := runCommandInDirExpectError(t, t.TempDir(), "http://127.0.0.1:1", []string{"workspace", "service", "add", "drive", "--interactive", "-f", "workspace.yaml"})
	if !strings.Contains(errText, "interactive input is disabled") {
		t.Fatalf("expected CI gating error, got %q", errText)
	}
}

func TestWorkspaceAddServiceInteractiveIsRejectedWithNoInput(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	oldNoInput := NoInput
	NoInput = true
	t.Cleanup(func() { NoInput = oldNoInput })
	errText := runCommandInDirExpectError(t, t.TempDir(), "http://127.0.0.1:1", []string{"workspace", "service", "add", "drive", "--interactive", "-f", "workspace.yaml"})
	if !strings.Contains(errText, "interactive input is disabled") {
		t.Fatalf("expected --no-input gating error, got %q", errText)
	}
}

func assertWorkspaceConfigContains(t *testing.T, path, serviceKey string, expectServiceID bool) {
	t.Helper()
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service, ok := parsed.Workspace.Services[strings.TrimSuffix(serviceKey, ":")]
	if !ok {
		t.Fatalf("workspace config does not contain %q", serviceKey)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hasServiceID := service.ServiceID != "" || strings.Contains(string(body), "service_id:")
	if hasServiceID != expectServiceID {
		t.Fatalf("service_id presence = %t, want %t:\n%s", hasServiceID, expectServiceID, body)
	}
}

func assertWorkspaceConfigContainsNoServices(t *testing.T, path string) {
	t.Helper()
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Workspace.Services) != 0 {
		t.Fatalf("workspace config changed unexpectedly: %#v", parsed.Workspace.Services)
	}
}
