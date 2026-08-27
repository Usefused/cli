package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

type discoveryRoundTripFunc func(*http.Request) (*http.Response, error)

// RoundTrip lets focused discovery tests observe HTTP decisions without a listener.
func (fn discoveryRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

// TestDiscoveryFailureIncludesTerminalRegistryDiagnostic verifies human and
// structured output retain the fixed failure explanation but not prior warnings.
func TestDiscoveryFailureIncludesTerminalRegistryDiagnostic(t *testing.T) {
	snapshot := &cliapi.DiscoverySnapshot{Payload: json.RawMessage(`{
		"failure_code":"operations_not_discovered",
		"diagnostics":[
			{"code":"crawl_partial_failures","message":"Some documentation pages could not be admitted."},
			{"code":"operations_not_discovered","message":"No selectable REST operations were found in the admitted documentation pages."}
		]
	}`)}
	err := discoveryFailure(snapshot)
	// The matching terminal classifier and explanation form the human contract.
	if !strings.Contains(err.Error(), "operations_not_discovered") || !strings.Contains(err.Error(), "No selectable REST operations") {
		t.Fatalf("human failure = %q", err)
	}
	// Prior review warnings must not be promoted into the terminal failure prose.
	if strings.Contains(err.Error(), "Some documentation pages") {
		t.Fatalf("human failure exposed non-terminal warning: %q", err)
	}
	result := classifyCommandError(&cobra.Command{Use: "discover"}, err)
	// JSON callers receive a stable local classifier rather than the remote code.
	if result.Code != "discovery_session_failed" || result.Category != "dependency" {
		t.Fatalf("structured failure metadata = %#v", result)
	}
	diagnostics, ok := result.Details["diagnostics"].([]cliapi.DiscoveryDiagnostic)
	// The structured detail must retain exactly the one safe terminal diagnostic.
	if !ok || len(diagnostics) != 1 || diagnostics[0].Code != "operations_not_discovered" || diagnostics[0].Message == "" {
		t.Fatalf("structured diagnostics = %#v", result.Details["diagnostics"])
	}
}

// TestDiscoveryFailureRejectsUnsafeOrUnboundedDiagnosticContent proves that a
// matching remote payload cannot expose credentials and that safe prose is capped.
func TestDiscoveryFailureRejectsUnsafeOrUnboundedDiagnosticContent(t *testing.T) {
	unsafe := &cliapi.DiscoverySnapshot{Payload: json.RawMessage(`{
		"failure_code":"operations_not_discovered",
		"diagnostics":[{"code":"operations_not_discovered","message":"inspect https://provider.example with fsk_never_return"}]
	}`)}
	unsafeErr := discoveryFailure(unsafe)
	// Credential- and URL-bearing messages must fall back to code-only output.
	if strings.Contains(unsafeErr.Error(), "https://") || strings.Contains(unsafeErr.Error(), "fsk_") {
		t.Fatalf("unsafe terminal diagnostic leaked: %q", unsafeErr)
	}

	longMessage := strings.Repeat("a", maxDiscoveryFailureDiagnosticRunes+20)
	bounded := &cliapi.DiscoverySnapshot{Payload: json.RawMessage(`{"failure_code":"operations_not_discovered","diagnostics":[{"code":"operations_not_discovered","message":` + strconv.Quote(longMessage) + `}]}`)}
	boundedErr := discoveryFailure(bounded)
	// Safe diagnostic prose is truncated at the CLI-owned character ceiling.
	if !strings.Contains(boundedErr.Error(), "…") || strings.Contains(boundedErr.Error(), longMessage) {
		t.Fatalf("bounded terminal diagnostic = %q", boundedErr)
	}
}

// TestPrintDiscoveryDiagnosticsSanitizesProgressMessages verifies non-terminal
// SSE and snapshot diagnostics cannot bypass the terminal-safe failure path.
func TestPrintDiscoveryDiagnosticsSanitizesProgressMessages(t *testing.T) {
	var output bytes.Buffer
	printDiscoveryDiagnostics(&output, []cliapi.DiscoveryDiagnostic{
		{Severity: "warning", Code: "crawl_warning", Message: "safe bounded explanation"},
		{Severity: "error\u001b[2J", Code: "provider\u202Espoof", Message: "inspect https://private.example with fsk_hidden"},
	})
	rendered := output.String()
	// Safe prose remains useful while controls, URLs, credentials, and malformed classifier values are suppressed.
	for _, want := range []string{"WARNING [crawl_warning] safe bounded explanation", "INFO [diagnostic] diagnostic detail omitted"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("diagnostic output %q does not contain %q", rendered, want)
		}
	}
	for _, forbidden := range []string{"\x1b", "\u202e", "private.example", "fsk_hidden"} {
		if strings.Contains(strings.ToLower(rendered), strings.ToLower(forbidden)) {
			t.Fatalf("diagnostic output leaked %q: %q", forbidden, rendered)
		}
	}
}

// TestImportDiscoverProducesAReceiptWithoutApplying verifies the CLI stops at the ordinary plan boundary.
func TestImportDiscoverProducesAReceiptWithoutApplying(t *testing.T) {
	server := newImportDiscoveryServer(t)
	defer server.Close()
	directory := t.TempDir()
	// The fixed default is intentionally latest-wins across import plan and
	// discovery, so a new reviewed plan must atomically replace the stale receipt.
	if err := writeImportPlanReceiptFile(filepath.Join(directory, defaultImportReceiptPath), importPlanReceipt{
		Slug: "stale", PlanID: "stale-plan", ReviewHash: strings.Repeat("f", 64),
	}); err != nil {
		t.Fatalf("write stale receipt: %v", err)
	}
	output := runCommandInDirOutput(t, directory, server.URL, []string{
		"import", "discover", "--url", "https://docs.example.test/api",
		"--name", "Docs API", "--slug", "docs-api", "--all", "--reject-enrichment",
		"--workers", "3", "--max-pages", "12", "--max-depth", "2",
	})
	if !strings.Contains(output, "Import plan ready: plan-1") || !strings.Contains(output, "fused-cli import apply") {
		t.Fatalf("unexpected discovery output: %q", output)
	}
	receipt, err := readImportPlanReceiptFile(filepath.Join(directory, defaultImportReceiptPath))
	if err != nil {
		t.Fatalf("read discovery receipt: %v", err)
	}
	if receipt.PlanID != "plan-1" || receipt.ReviewHash != strings.Repeat("a", 64) || receipt.Slug != "docs-api" {
		t.Fatalf("unexpected discovery receipt: %+v", receipt)
	}
}

// TestImportApplyHelpIncludesDiscoveryReceipts keeps the shared apply handoff discoverable.
func TestImportApplyHelpIncludesDiscoveryReceipts(t *testing.T) {
	text := importApplyCmd.Short + "\n" + importApplyCmd.Long + "\n" + importApplyCmd.Flags().Lookup("receipt").Usage
	if !strings.Contains(text, "import discover") || !strings.Contains(text, "discovery receipt") {
		t.Fatalf("import apply help omitted discovery receipts: %q", text)
	}
}

// TestImportCommandDoesNotExposeTheRemovedDocsSubcommand enforces the intentional breaking cutover.
func TestImportCommandDoesNotExposeTheRemovedDocsSubcommand(t *testing.T) {
	for _, command := range importCmd.Commands() {
		if command.Name() == "docs" {
			t.Fatal("removed import docs compatibility command is still registered")
		}
	}
}

// TestValidateImportDiscoveryResumeOptions proves resumption has one session
// identity and never accepts ignored start or scheduling inputs.
func TestValidateImportDiscoveryResumeOptions(t *testing.T) {
	valid := importDiscoveryOptions{sessionID: "session-1", sourceMode: "auto", timeout: cliapi.DefaultTimeout}
	if err := validateImportDiscoveryOptions(valid); err != nil {
		t.Fatalf("valid resume options: %v", err)
	}
	for name, mutate := range map[string]func(*importDiscoveryOptions){
		"start identity": func(options *importDiscoveryOptions) { options.name = "Different API" },
		"crawl limit":    func(options *importDiscoveryOptions) { options.maxPages = 10 },
		"source mode":    func(options *importDiscoveryOptions) { options.sourceMode = "docs" },
		"invalid ID":     func(options *importDiscoveryOptions) { options.sessionID = "session/one" },
	} {
		t.Run(name, func(t *testing.T) {
			options := valid
			mutate(&options)
			if err := validateImportDiscoveryOptions(options); err == nil {
				t.Fatal("expected resume option rejection")
			}
		})
	}
}

// TestLoadImportDiscoverySessionReloadsWithoutStart proves --session uses GET
// and cannot create a second discovery session as a compatibility fallback.
func TestLoadImportDiscoverySessionReloadsWithoutStart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/integrations/session/session-1" {
			t.Fatalf("unexpected resume request %s %s", request.Method, request.URL.Path)
		}
		writeImportDiscoverySnapshot(t, writer, 4, "awaiting_review", 1, `{"contract":{"draft_id":"draft-1","draft_revision":1,"review_hash":"`+strings.Repeat("a", 64)+`"}}`)
	}))
	defer server.Close()
	client := cliapi.NewClient(server.URL, "test-key")
	client.HTTP = server.Client()
	snapshot, started, err := loadImportDiscoverySession(t.Context(), client, importDiscoveryOptions{sessionID: "session-1"})
	if err != nil {
		t.Fatalf("resume session: %v", err)
	}
	if started || snapshot.Revision != 4 || snapshot.State != cliapi.DiscoveryStateAwaitingReview {
		t.Fatalf("unexpected resumed snapshot: started=%v snapshot=%+v", started, snapshot)
	}
}

// TestDiscoveryReviewURLPreservesBasePathAndEscapesSession verifies the exact
// browser handoff route without permitting the opaque identity to alter it.
func TestDiscoveryReviewURLPreservesBasePathAndEscapesSession(t *testing.T) {
	got, err := discoveryReviewURL(" https://engine.example.test/base/?old=1#old ", "session?&=#")
	if err != nil {
		t.Fatalf("review URL: %v", err)
	}
	want := "https://engine.example.test/base/integrations?handoff=cli&session=session%3F%26%3D%23&tab=pending"
	if got != want {
		t.Fatalf("review URL = %q, want %q", got, want)
	}
}

// TestDiscoveryReviewURLRejectsInvalidIdentities covers unsafe base URLs and
// route-breaking session aliases before a browser process can be invoked.
func TestDiscoveryReviewURLRejectsInvalidIdentities(t *testing.T) {
	tests := []struct {
		name      string
		engineURL string
		sessionID string
	}{
		{name: "relative base", engineURL: "/engine", sessionID: "session-1"},
		{name: "unsupported scheme", engineURL: "file:///tmp/engine", sessionID: "session-1"},
		{name: "embedded credentials", engineURL: "https://user:secret@engine.example", sessionID: "session-1"},
		{name: "missing hostname", engineURL: "https://:8443", sessionID: "session-1"},
		{name: "empty session", engineURL: "https://engine.example", sessionID: ""},
		{name: "path session", engineURL: "https://engine.example", sessionID: "session/one"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := discoveryReviewURL(test.engineURL, test.sessionID); err == nil {
				t.Fatal("expected review URL rejection")
			}
		})
	}
}

// TestPresentBrowserReviewUsesInjectedOpener proves one stable URL is printed
// and opened once even when the state loop presents the same review twice.
func TestPresentBrowserReviewUsesInjectedOpener(t *testing.T) {
	restoreDiscoveryBrowserTestGlobals(t)
	t.Setenv("CI", "")
	NoInput = false
	discoveryBrowserInteractive = func() bool { return true }
	var opened []string
	openDiscoveryBrowser = func(_ context.Context, target string) error {
		opened = append(opened, target)
		return nil
	}
	runner, output := discoveryBrowserTestRunner(t, importDiscoveryOptions{})
	snapshot := discoveryBrowserReviewSnapshot()
	if err := runner.presentBrowserReview(snapshot); err != nil {
		t.Fatalf("present review: %v", err)
	}
	if err := runner.presentBrowserReview(snapshot); err != nil {
		t.Fatalf("present repeated review: %v", err)
	}
	want, _ := discoveryReviewURL(runner.client.BaseURL, snapshot.SessionID)
	if len(opened) != 1 || opened[0] != want || strings.Count(output.String(), want) != 1 {
		t.Fatalf("opened=%#v output=%q want=%q", opened, output.String(), want)
	}
}

// TestNoBrowserPrintsAndWaitsForBrowserPlan verifies the explicit manual-link
// mode never invokes an opener and reloads the browser-created plan snapshot.
func TestNoBrowserPrintsAndWaitsForBrowserPlan(t *testing.T) {
	restoreDiscoveryBrowserTestGlobals(t)
	t.Setenv("CI", "")
	NoInput = false
	discoveryBrowserInteractive = func() bool { return true }
	openDiscoveryBrowser = func(context.Context, string) error {
		t.Fatal("--no-browser invoked the browser opener")
		return nil
	}
	runner, output := discoveryBrowserTestRunner(t, importDiscoveryOptions{noBrowser: true})
	runner.client.HTTP = &http.Client{Transport: discoveryRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/base/integrations/session/session-1/stream":
			return discoveryTestHTTPResponse(http.StatusOK, "text/event-stream", `data: {"version":1,"session_id":"session-1","revision":5,"state":"plan_ready","type":"plan_ready"}`+"\n\n"), nil
		case "/base/integrations/session/session-1":
			return discoveryTestHTTPResponse(http.StatusOK, "application/json", discoveryPlanReadySnapshotJSON()), nil
		default:
			t.Fatalf("unexpected browser review request %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})}
	next, done, err := runner.advanceReview(discoveryBrowserReviewSnapshot())
	if err != nil || done || next.State != cliapi.DiscoveryStatePlanReady {
		t.Fatalf("browser review result = %+v, done=%v, err=%v", next, done, err)
	}
	if !strings.Contains(output.String(), "handoff=cli") {
		t.Fatalf("manual browser URL was not printed: %q", output.String())
	}
}

// TestBrowserReviewIgnoresCurrentDecisionReplay verifies an SSE reconnect can
// replay awaiting_review before delivering the browser-created plan revision.
func TestBrowserReviewIgnoresCurrentDecisionReplay(t *testing.T) {
	runner, _ := discoveryBrowserTestRunner(t, importDiscoveryOptions{})
	runner.client.HTTP = &http.Client{Transport: discoveryRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/base/integrations/session/session-1/stream":
			body := `data: {"version":1,"session_id":"session-1","revision":4,"state":"awaiting_review","type":"review_required"}` + "\n\n" +
				`data: {"version":1,"session_id":"session-1","revision":5,"state":"plan_ready","type":"plan_ready"}` + "\n\n"
			return discoveryTestHTTPResponse(http.StatusOK, "text/event-stream", body), nil
		case "/base/integrations/session/session-1":
			return discoveryTestHTTPResponse(http.StatusOK, "application/json", discoveryPlanReadySnapshotJSON()), nil
		default:
			t.Fatalf("unexpected browser replay request %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})}
	next, err := runner.waitForSnapshot(discoveryBrowserReviewSnapshot())
	if err != nil || next.State != cliapi.DiscoveryStatePlanReady || next.Revision != 5 {
		t.Fatalf("browser replay result = %+v, err=%v", next, err)
	}
}

// TestWaitForSnapshotReconnectsAfterUnexpectedEOF reproduces an Engine proxy
// timeout followed by a browser-created plan on the replacement stream.
func TestWaitForSnapshotReconnectsAfterUnexpectedEOF(t *testing.T) {
	runner, _ := discoveryBrowserTestRunner(t, importDiscoveryOptions{})
	streamAttempts, reloads := 0, 0
	runner.client.HTTP = &http.Client{Transport: discoveryRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/base/integrations/session/session-1/stream":
			streamAttempts++
			if streamAttempts == 1 {
				return nil, io.ErrUnexpectedEOF
			}
			body := `data: {"version":1,"session_id":"session-1","revision":4,"state":"awaiting_review","type":"review_required"}` + "\n\n" +
				`data: {"version":1,"session_id":"session-1","revision":5,"state":"plan_ready","type":"plan_ready"}` + "\n\n"
			return discoveryTestHTTPResponse(http.StatusOK, "text/event-stream", body), nil
		case "/base/integrations/session/session-1":
			reloads++
			if reloads == 1 {
				return discoveryTestHTTPResponse(http.StatusOK, "application/json", discoveryAwaitingReviewSnapshotJSON(4, false)), nil
			}
			return discoveryTestHTTPResponse(http.StatusOK, "application/json", discoveryPlanReadySnapshotJSON()), nil
		default:
			t.Fatalf("unexpected reconnect request %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})}
	next, err := runner.waitForSnapshot(discoveryBrowserReviewSnapshot())
	if err != nil || next == nil || next.State != cliapi.DiscoveryStatePlanReady || streamAttempts != 2 || reloads != 2 {
		t.Fatalf("reconnect result = %+v, streams=%d, reloads=%d, err=%v", next, streamAttempts, reloads, err)
	}
}

// TestWaitForSnapshotReconnectHonorsContext proves repeated proxy EOFs cannot
// outlive the discovery command's cancellation deadline.
func TestWaitForSnapshotReconnectHonorsContext(t *testing.T) {
	runner, _ := discoveryBrowserTestRunner(t, importDiscoveryOptions{})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	runner.ctx = ctx
	runner.client.HTTP = &http.Client{Transport: discoveryRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/stream") {
			return nil, io.ErrUnexpectedEOF
		}
		return discoveryTestHTTPResponse(http.StatusOK, "application/json", discoveryAwaitingReviewSnapshotJSON(4, false)), nil
	})}
	next, err := runner.waitForSnapshot(discoveryBrowserReviewSnapshot())
	if !errors.Is(err, context.DeadlineExceeded) || next != nil {
		t.Fatalf("cancelled reconnect = %+v, err=%v", next, err)
	}
}

// TestWaitForSnapshotReturnsPermanentStreamError ensures reconnect recovery is
// not broadened to arbitrary proxy or protocol failures.
func TestWaitForSnapshotReturnsPermanentStreamError(t *testing.T) {
	runner, _ := discoveryBrowserTestRunner(t, importDiscoveryOptions{})
	permanentErr := errors.New("permanent proxy failure")
	runner.client.HTTP = &http.Client{Transport: discoveryRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/stream") {
			return nil, permanentErr
		}
		return discoveryTestHTTPResponse(http.StatusOK, "application/json", discoveryAwaitingReviewSnapshotJSON(4, false)), nil
	})}
	next, err := runner.waitForSnapshot(discoveryBrowserReviewSnapshot())
	if !errors.Is(err, permanentErr) || next != nil {
		t.Fatalf("permanent stream failure = %+v, err=%v", next, err)
	}
}

// TestNoInputReviewRemainsFlagDriven proves automation never opens, prints, or
// waits for a browser even when --no-browser and an interactive TTY coexist.
func TestNoInputReviewRemainsFlagDriven(t *testing.T) {
	restoreDiscoveryBrowserTestGlobals(t)
	t.Setenv("CI", "")
	NoInput = true
	discoveryBrowserInteractive = func() bool { return true }
	openDiscoveryBrowser = func(context.Context, string) error {
		t.Fatal("--no-input invoked the browser opener")
		return nil
	}
	var action cliapi.DiscoveryActionRequest
	runner, output := discoveryBrowserTestRunner(t, importDiscoveryOptions{noBrowser: true, rejectEnrichment: true})
	runner.client.HTTP = &http.Client{Transport: discoveryRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/base/integrations/session/session-1/actions" {
			t.Fatalf("unexpected flag-driven request %s %s", request.Method, request.URL.Path)
		}
		if err := json.NewDecoder(request.Body).Decode(&action); err != nil {
			t.Fatalf("decode action: %v", err)
		}
		return discoveryTestHTTPResponse(http.StatusOK, "application/json", discoveryAwaitingReviewSnapshotJSON(6, false)), nil
	})}
	next, done, err := runner.advanceReview(discoveryAwaitingReviewSnapshot(true))
	if err != nil || done || next.State != cliapi.DiscoveryStateAwaitingReview {
		t.Fatalf("flag review result = %+v, done=%v, err=%v", next, done, err)
	}
	if action.Action != cliapi.DiscoveryActionRejectEnrichment || strings.Contains(output.String(), "handoff=cli") {
		t.Fatalf("action=%+v output=%q", action, output.String())
	}
}

// TestJSONPlanReadyNeverPrintsOrOpensBrowser protects stdout as one exact JSON document.
func TestJSONPlanReadyNeverPrintsOrOpensBrowser(t *testing.T) {
	restoreDiscoveryBrowserTestGlobals(t)
	t.Setenv("CI", "")
	NoInput = false
	discoveryBrowserInteractive = func() bool { return true }
	openDiscoveryBrowser = func(context.Context, string) error {
		t.Fatal("--json invoked the browser opener")
		return nil
	}
	runner, output := discoveryBrowserTestRunner(t, importDiscoveryOptions{jsonOut: true})
	snapshot := &cliapi.DiscoverySnapshot{Version: 1, SessionID: "session-1", Revision: 5, State: cliapi.DiscoveryStatePlanReady}
	next, done, err := runner.completePlan(snapshot)
	if err != nil || !done || next != snapshot || output.Len() != 0 {
		t.Fatalf("JSON completion = %+v, done=%v, err=%v, output=%q", next, done, err, output.String())
	}
}

// TestSelectedDiscoveryOperationsRejectsUnseenCoordinates preserves exact non-interactive authorization.
func TestSelectedDiscoveryOperationsRejectsUnseenCoordinates(t *testing.T) {
	operations := []cliapi.DiscoveryOperation{{Method: "GET", Path: "/users"}}
	_, err := selectedDiscoveryOperations(operations, []string{"POST:/users"}, 10)
	if err == nil || !strings.Contains(err.Error(), "was not discovered") {
		t.Fatalf("expected exact selection rejection, got %v", err)
	}
}

// TestReadDiscoveryOverlayRequiresJSONObject rejects YAML and scalar aliases at the typed action boundary.
func TestReadDiscoveryOverlayRequiresJSONObject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overlay.yaml")
	if err := os.WriteFile(path, []byte("x-fused-connect: {}\n"), 0o600); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
	if _, err := readDiscoveryOverlay(path); err == nil {
		t.Fatal("expected non-JSON discovery overlay rejection")
	}
}

// restoreDiscoveryBrowserTestGlobals isolates process-wide CLI presentation hooks.
func restoreDiscoveryBrowserTestGlobals(t *testing.T) {
	t.Helper()
	previousOpen, previousInteractive, previousNoInput := openDiscoveryBrowser, discoveryBrowserInteractive, NoInput
	t.Cleanup(func() {
		openDiscoveryBrowser, discoveryBrowserInteractive, NoInput = previousOpen, previousInteractive, previousNoInput
	})
}

// discoveryBrowserTestRunner creates a focused runner with captured output and no network by default.
func discoveryBrowserTestRunner(t *testing.T, options importDiscoveryOptions) (*discoverySessionRunner, *bytes.Buffer) {
	t.Helper()
	output := &bytes.Buffer{}
	command := &cobra.Command{Use: "discover"}
	command.SetOut(output)
	command.SetErr(&bytes.Buffer{})
	command.SetContext(t.Context())
	client := cliapi.NewClient("https://engine.example.test/base", "test-key")
	return &discoverySessionRunner{ctx: t.Context(), cmd: command, client: client, options: options}, output
}

// discoveryBrowserReviewSnapshot returns the exact decision-state receipt used by browser tests.
func discoveryBrowserReviewSnapshot() *cliapi.DiscoverySnapshot {
	return discoveryAwaitingReviewSnapshot(false)
}

// discoveryAwaitingReviewSnapshot builds one valid authoritative review snapshot.
func discoveryAwaitingReviewSnapshot(withProposal bool) *cliapi.DiscoverySnapshot {
	return &cliapi.DiscoverySnapshot{
		Version: 1, SessionID: "session-1", Revision: 4, DraftRevision: 1,
		State: cliapi.DiscoveryStateAwaitingReview, Payload: json.RawMessage(discoveryAwaitingReviewPayload(withProposal)),
	}
}

// discoveryAwaitingReviewPayload includes an optional complete public proposal for flag-path tests.
func discoveryAwaitingReviewPayload(withProposal bool) string {
	proposal := ""
	if withProposal {
		proposal = `,"proposals":[{"id":"proposal-1","extension":"x-fused-connect","pointer":"/x-fused-connect","scope":"document","value":{},"dependencies":["/"],"rationale":"routing","evidence":[],"confidence":"high","requires_confirmation":true}]`
	}
	return `{"contract":{"draft_id":"draft-1","draft_revision":1,"review_hash":"` + strings.Repeat("a", 64) + `"}` + proposal + `}`
}

// discoveryAwaitingReviewSnapshotJSON serializes a server response for an action-created review revision.
func discoveryAwaitingReviewSnapshotJSON(revision uint64, withProposal bool) string {
	return fmt.Sprintf(`{"version":1,"session_id":"session-1","revision":%d,"draft_revision":1,"state":"awaiting_review","payload":%s}`, revision, discoveryAwaitingReviewPayload(withProposal))
}

// discoveryPlanReadySnapshotJSON returns the browser-created terminal discovery handoff.
func discoveryPlanReadySnapshotJSON() string {
	return `{"version":1,"session_id":"session-1","revision":5,"draft_revision":1,"state":"plan_ready","payload":{"plan":{"plan_id":"plan-1","review_hash":"` + strings.Repeat("b", 64) + `"}}}`
}

// discoveryTestHTTPResponse constructs one bounded in-memory API response.
func discoveryTestHTTPResponse(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Header: http.Header{"Content-Type": {contentType}},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}

// newImportDiscoveryServer exposes only start and typed actions; any apply request fails the test.
func newImportDiscoveryServer(t *testing.T) *httptest.Server {
	revision := uint64(0)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/integrations/start":
			assertImportDiscoveryStart(t, request)
			revision = 1
			writeImportDiscoverySnapshot(t, w, revision, "awaiting_selection", 0, `{"effective_workers":3,"max_pages":12,"max_depth":2,"max_selections":10,"operations":[{"method":"GET","path":"/users","summary":"List users","occurrences":2}]}`)
		case "/integrations/session/session-1/actions":
			var action cliapi.DiscoveryActionRequest
			if err := json.NewDecoder(request.Body).Decode(&action); err != nil {
				t.Fatalf("decode discovery action: %v", err)
			}
			revision++
			if action.Action == cliapi.DiscoveryActionSelectOperations {
				assertImportDiscoverySelectionPayload(t, action.Payload)
				writeImportDiscoverySnapshot(t, w, revision, "awaiting_review", 1, `{"contract":{"draft_id":"draft-1","draft_revision":1,"review_hash":"`+strings.Repeat("b", 64)+`"}}`)
				return
			}
			if action.Action != cliapi.DiscoveryActionRequestPlan || action.DraftRevision != 1 {
				t.Fatalf("unexpected discovery action: %+v", action)
			}
			writeImportDiscoverySnapshot(t, w, revision, "plan_ready", 1, `{"plan":{"plan_id":"plan-1","review_hash":"`+strings.Repeat("a", 64)+`"}}`)
		case "/integrations/session/session-1/stream":
			revision++
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: {\"version\":1,\"session_id\":\"session-1\",\"revision\":%d,\"state\":\"plan_ready\",\"type\":\"plan_ready\"}\n\n", revision)
		case "/integrations/session/session-1":
			writeImportDiscoverySnapshot(t, w, revision, "plan_ready", 1, `{"plan":{"plan_id":"plan-1","review_hash":"`+strings.Repeat("a", 64)+`"}}`)
		case "/integrations/import/apply", "/workspace/services":
			t.Fatalf("discovery command attempted mutation through %s", request.URL.Path)
		default:
			t.Fatalf("unexpected discovery path %s", request.URL.Path)
		}
	}))
}

// assertImportDiscoverySelectionPayload protects the strict action boundary
// from leaking operation-preview fields into Registry authorization.
func assertImportDiscoverySelectionPayload(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var payload struct {
		Operations []map[string]any `json:"operations"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode operation selection payload: %v", err)
	}
	if len(payload.Operations) != 1 || len(payload.Operations[0]) != 2 || payload.Operations[0]["method"] != "GET" || payload.Operations[0]["path"] != "/users" {
		t.Fatalf("selection action was not the exact method/path shape: %#v", payload.Operations)
	}
}

// assertImportDiscoveryStart verifies advisory limits and the new slug field reach Registry unchanged.
func assertImportDiscoveryStart(t *testing.T, request *http.Request) {
	t.Helper()
	var start cliapi.DiscoveryStartRequest
	if err := json.NewDecoder(request.Body).Decode(&start); err != nil {
		t.Fatalf("decode discovery start: %v", err)
	}
	if start.Slug != "docs-api" || start.SourceMode != "auto" || start.RequestedWorkers != 3 || start.Crawl.MaxPages != 12 || start.Crawl.MaxDepth != 2 {
		t.Fatalf("unexpected discovery start: %+v", start)
	}
}

// writeImportDiscoverySnapshot emits one authoritative state for the command loop.
func writeImportDiscoverySnapshot(t *testing.T, writer http.ResponseWriter, revision uint64, state string, draftRevision uint64, payload string) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	status := http.StatusOK
	if revision == 1 {
		status = http.StatusAccepted
	}
	writer.WriteHeader(status)
	snapshot := map[string]any{
		"version": 1, "session_id": "session-1", "revision": revision,
		"state": state, "payload": json.RawMessage(payload),
	}
	if draftRevision > 0 {
		snapshot["draft_revision"] = draftRevision
	}
	if err := json.NewEncoder(writer).Encode(snapshot); err != nil {
		t.Fatalf("encode discovery snapshot: %v", err)
	}
}
