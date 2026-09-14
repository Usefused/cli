package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

// TestPromptInitAlwaysRequiresInteractiveConfirmation verifies LLM-derived plans have no unattended approval path.
func TestPromptInitAlwaysRequiresInteractiveConfirmation(t *testing.T) {
	command := newPromptInitCommand()
	// Removing the approval bypass keeps every successful prompt execution behind the confirmation UI.
	if command.Flags().Lookup("yes") != nil {
		t.Fatal("prompt must not expose --yes")
	}

	previousNoInput := NoInput
	NoInput = true
	// Cleanup restores shared command state for the remaining package tests.
	t.Cleanup(func() {
		NoInput = previousNoInput
	})
	command.SetArgs([]string{"create an SDK for task management"})
	err := command.Execute()
	// Non-interactive execution must fail before intent parsing or any Engine mutation.
	if err == nil || !strings.Contains(err.Error(), "prompt requires a terminal confirmation") {
		t.Fatalf("expected interactive-only error, got %v", err)
	}
}

// TestBuildPromptInitPlanRejectsWebhookForREST verifies the receiver-free mode fails before service resolution or mutation.
func TestBuildPromptInitPlanRejectsWebhookForREST(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":{"parseSDKIntent":{"kind":"rest","name":"payments","language":"","description":"Receive payments","webhook_requested":true,"services":[{"name":"stripe","endpoint_query":"","event_queries":["payment succeeded"]}]}}}`))
	}))
	defer server.Close()
	_, err := buildPromptInitPlan(&cobra.Command{}, api.NewClient(server.URL, "fsk_test"), "receive Stripe payment events", &promptInitOptions{})
	// Direct REST execution cannot establish an SSE receiver session.
	if err == nil || !strings.Contains(err.Error(), "cannot receive webhook events") {
		t.Fatalf("expected incompatible webhook receiver error, got %v", err)
	}
}

// TestResolvePromptInitModeRestrictsPrimaryOutputs verifies webhook remains a composed SDK capability rather than a prompt kind.
func TestResolvePromptInitModeRestrictsPrimaryOutputs(t *testing.T) {
	tests := []struct {
		name     string
		override string
		inferred string
		want     unifiedInitMode
		wantErr  string
	}{
		{name: "sdk", inferred: "sdk", want: unifiedInitModeSDK},
		{name: "rest", inferred: "rest", want: unifiedInitModeAPI},
		{name: "override", override: "mcp", inferred: "sdk", want: unifiedInitModeMCP},
		{name: "webhook rejected", inferred: "webhook", wantErr: "sdk, mcp, or rest"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolvePromptInitMode(test.override, test.inferred)
			// Expected validation failures must retain the public set of supported outputs.
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("resolvePromptInitMode error = %v", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("resolvePromptInitMode = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

// TestMatchPromptWebhookEventsSelectsExactProviderNames verifies comma-like multi-event intent becomes exact SDK event scope.
func TestMatchPromptWebhookEventsSelectsExactProviderNames(t *testing.T) {
	available := []api.Webhook{
		{Name: "payment.succeeded", Description: "A payment completed successfully."},
		{Name: "payment.failed", Description: "A payment attempt failed."},
		{Name: "customer.created", Description: "A customer was created."},
	}
	got, err := matchPromptWebhookEvents([]string{"successful payments", "payment failures"}, available)
	if err != nil {
		t.Fatalf("matchPromptWebhookEvents: %v", err)
	}
	want := []string{"payment.succeeded", "payment.failed"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

// TestMatchPromptWebhookEventsRejectsAmbiguity prevents a broad model phrase from silently selecting one of several event types.
func TestMatchPromptWebhookEventsRejectsAmbiguity(t *testing.T) {
	available := []api.Webhook{{Name: "payment.succeeded"}, {Name: "payment.failed"}}
	_, err := matchPromptWebhookEvents([]string{"payment"}, available)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous event error, got %v", err)
	}
}

// TestMatchPromptOperationRequiresOneGroundedResult verifies ranked semantic search cannot silently broaden app scope.
func TestMatchPromptOperationRequiresOneGroundedResult(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		endpoints []api.Integration
		want      string
		wantErr   string
	}{
		{name: "exact id wins", query: "listTasks", endpoints: []api.Integration{{Name: "createTask"}, {Name: "listTasks"}}, want: "listTasks"},
		{name: "one semantic result", query: "list tasks", endpoints: []api.Integration{{Name: "listTasks"}}, want: "listTasks"},
		{name: "ambiguous semantic results", query: "tasks", endpoints: []api.Integration{{Name: "listTasks"}, {Name: "createTask"}}, wantErr: "ambiguous"},
		{name: "empty results", query: "delete tasks", wantErr: "no operation matched"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := matchPromptOperation(test.query, test.endpoints)
			// Expected ambiguity or absence must fail closed with a stable explanation.
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("matchPromptOperation error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("matchPromptOperation = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

// TestPromptOperationQueriesPreservesDistinctRequests verifies plural intent remains separate and supports older Registry output.
func TestPromptOperationQueriesPreservesDistinctRequests(t *testing.T) {
	intent := api.IntentService{EndpointQuery: "legacyQuery", EndpointQueries: []string{"listTasks", "createTask", "listTasks", " "}}
	got := promptOperationQueries(intent)
	want := []string{"listTasks", "createTask"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("operation queries = %#v, want %#v", got, want)
	}
	legacy := promptOperationQueries(api.IntentService{EndpointQuery: "legacyQuery"})
	if len(legacy) != 1 || legacy[0] != "legacyQuery" {
		t.Fatalf("legacy operation query = %#v", legacy)
	}
}

// TestPromptIntentAliasesMergesDuplicateServiceMentions verifies case-only duplicates retain every explicit capability once.
func TestPromptIntentAliasesMergesDuplicateServiceMentions(t *testing.T) {
	aliases := promptIntentAliases([]api.IntentService{
		{Name: "Stripe", EndpointQueries: []string{"createPayment"}, EventQueries: []string{"payment.succeeded"}},
		{Name: "stripe", EndpointQueries: []string{"refundPayment"}, EventQueries: []string{"payment.failed"}},
	})
	intent, exists := aliases["stripe"]
	if !exists || strings.Join(intent.EndpointQueries, ",") != "createPayment,refundPayment" || strings.Join(intent.EventQueries, ",") != "payment.succeeded,payment.failed" {
		t.Fatalf("merged intent = %#v, exists = %v", intent, exists)
	}
}

// TestResolvePromptSelectionsResolvesEachOperationQuery verifies one model list entry becomes one exact app operation.
func TestResolvePromptSelectionsResolvesEachOperationQuery(t *testing.T) {
	// The fake Registry returns one grounded operation for each independent semantic query.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		query, _ := body.Variables["q"].(string)
		operation := map[string]string{"list tasks": "listTasks", "create tasks": "createTask"}[query]
		// An unexpected merged query recreates the hallucination-prone behavior this test prevents.
		if operation == "" {
			t.Fatalf("unexpected operation query %q", query)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":{"searchEndpoints":[{"name":"` + operation + `"}]}}`))
	}))
	defer server.Close()

	request := scaffoldRequest{kind: configfile.KindSDK}
	resolved := []sdkInitResolvedService{{target: workspaceServiceAddTarget{slug: "ledger", serviceID: "svc-1", requestedRefs: []string{"ledger"}}, version: "v1"}}
	intents := []api.IntentService{{Name: "ledger", EndpointQueries: []string{"list tasks", "create tasks"}}}
	got, _, err := resolvePromptSelections(api.NewClient(server.URL, "fsk_test"), request, resolved, intents, false)
	if err != nil {
		t.Fatalf("resolvePromptSelections: %v", err)
	}
	if len(got.operations) != 2 || got.operations[0].operation != "listTasks" || got.operations[1].operation != "createTask" || len(got.selectAll) != 0 {
		t.Fatalf("resolved selections = %#v", got)
	}
}

// TestResolvePromptSelectionsPreservesExplicitSelectAll verifies complete scope bypasses semantic operation search.
func TestResolvePromptSelectionsPreservesExplicitSelectAll(t *testing.T) {
	request := scaffoldRequest{kind: configfile.KindSDK}
	resolved := []sdkInitResolvedService{{target: workspaceServiceAddTarget{slug: "ledger", serviceID: "svc-1", requestedRefs: []string{"ledger"}}, version: "v1"}}
	intents := []api.IntentService{{Name: "ledger", SelectAllOperations: true}}
	got, _, err := resolvePromptSelections(nil, request, resolved, intents, false)
	if err != nil {
		t.Fatalf("resolvePromptSelections: %v", err)
	}
	if len(got.selectAll) != 1 || got.selectAll[0] != "ledger" || len(got.operations) != 0 {
		t.Fatalf("resolved selections = %#v", got)
	}
}

// TestResolvePromptSelectionsRejectsConflictingScope verifies malformed model output cannot combine narrow and complete access.
func TestResolvePromptSelectionsRejectsConflictingScope(t *testing.T) {
	resolved := []sdkInitResolvedService{{target: workspaceServiceAddTarget{slug: "ledger", serviceID: "svc-1", requestedRefs: []string{"ledger"}}, version: "v1"}}
	intents := []api.IntentService{{Name: "ledger", EndpointQueries: []string{"listTasks"}, SelectAllOperations: true}}
	_, _, err := resolvePromptSelections(nil, scaffoldRequest{kind: configfile.KindSDK}, resolved, intents, false)
	if err == nil || !strings.Contains(err.Error(), "both specific operations and all operations") {
		t.Fatalf("expected conflicting scope error, got %v", err)
	}
}

// TestCompleteSDKInitOperationSelectionsAllowsEventOnlyApps proves webhook receipt does not broaden into REST operations.
func TestCompleteSDKInitOperationSelectionsAllowsEventOnlyApps(t *testing.T) {
	for _, kind := range []configfile.ConfigKind{configfile.KindSDK, configfile.KindMCP} {
		t.Run(string(kind), func(t *testing.T) {
			request := scaffoldRequest{
				kind:   kind,
				events: []scaffoldEvent{{service: "stripe", event: "payment.succeeded"}},
			}
			services := []sdkInitResolvedService{{target: workspaceServiceAddTarget{slug: "stripe", serviceID: "svc-1"}, version: "2026-09-01"}}
			command := &cobra.Command{}
			command.SetOut(&bytes.Buffer{})
			got, err := completeSDKInitOperationSelections(command, nil, request, services)
			// Event-only SDK and MCP apps already have a finite capability boundary and need no operation prompt.
			if err != nil {
				t.Fatalf("completeSDKInitOperationSelections: %v", err)
			}
			if len(got.operations) != 0 || len(got.selectAll) != 0 {
				t.Fatalf("event-only app gained operation scope: %#v", got)
			}
		})
	}
}

// TestPrintPromptInitPlanNamesWebhookComposition keeps the final review explicit about create versus reuse behavior.
func TestPrintPromptInitPlanNamesWebhookComposition(t *testing.T) {
	output := &bytes.Buffer{}
	command := &cobra.Command{}
	command.SetOut(output)
	plan := promptInitPlan{
		mode: unifiedInitModeSDK,
		primary: scaffoldRequest{
			name: "payments-sdk", version: "1.0.0", webhookAttachment: "payments-sdk-webhooks",
			events: []scaffoldEvent{{service: "stripe", event: "payment.succeeded"}},
		},
		resolved: []sdkInitResolvedService{{target: workspaceServiceAddTarget{slug: "stripe"}, version: "2026-09-01"}},
	}
	if err := printPromptInitPlan(command, plan); err != nil {
		t.Fatalf("printPromptInitPlan: %v", err)
	}
	for _, fragment := range []string{"SDK payments-sdk version 1.0.0", "stripe@2026-09-01: no operations", "event: payment.succeeded", "payments-sdk-webhooks (create)"} {
		// Every composed capability must remain visible in the one prompt authorization.
		if !strings.Contains(output.String(), fragment) {
			t.Fatalf("proposal %q does not contain %q", output.String(), fragment)
		}
	}
}
