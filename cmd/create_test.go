package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

func TestIsMCPTarget(t *testing.T) {
	cases := []struct {
		name       string
		targetType string
		want       bool
	}{
		{"mcp target", "mcp", true},
		{"sdk target", "sdk", false},
		{"empty target", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMCPTarget(tc.targetType); got != tc.want {
				t.Errorf("isMCPTarget(%q) = %v, want %v", tc.targetType, got, tc.want)
			}
		})
	}
}

func TestValidateCreateFlags(t *testing.T) {
	cases := []struct {
		name           string
		targetType     string
		targetLanguage string
		wantErr        bool
	}{
		{"mcp + python is rejected", "mcp", "python", true},
		{"mcp + typescript is allowed", "mcp", "typescript", false},
		{"sdk + python is allowed", "sdk", "python", false},
		{"sdk + typescript is allowed", "sdk", "typescript", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCreateFlags(tc.targetType, tc.targetLanguage)
			if tc.wantErr && err == nil {
				t.Errorf("validateCreateFlags(%q, %q) = nil, want error", tc.targetType, tc.targetLanguage)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateCreateFlags(%q, %q) = %v, want nil", tc.targetType, tc.targetLanguage, err)
			}
		})
	}
}

// --- pure helpers extracted from runCreate ---

func TestGroupCartByService(t *testing.T) {
	cart := map[string]api.Integration{
		"ep1": {ID: "ep1", ServiceID: "svcA"},
		"ep2": {ID: "ep2", ServiceID: "svcA"},
		"ep3": {ID: "ep3", ServiceID: "svcB"},
	}

	got := groupCartByService(cart)

	if len(got["svcA"]) != 2 {
		t.Errorf("expected 2 endpoints for svcA, got %d", len(got["svcA"]))
	}
	if len(got["svcB"]) != 1 {
		t.Errorf("expected 1 endpoint for svcB, got %d", len(got["svcB"]))
	}
}

func TestRebuildCart(t *testing.T) {
	cart := map[string]api.Integration{
		"ep1": {ID: "ep1", Name: "one"},
		"ep2": {ID: "ep2", Name: "two"},
		"ep3": {ID: "ep3", Name: "three"},
	}

	got := rebuildCart(cart, []string{"ep1", "ep3"})

	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if _, ok := got["ep1"]; !ok {
		t.Error("expected ep1 to be kept")
	}
	if _, ok := got["ep3"]; !ok {
		t.Error("expected ep3 to be kept")
	}
	if _, ok := got["ep2"]; ok {
		t.Error("expected ep2 to be dropped")
	}
}

func TestBuildSelections(t *testing.T) {
	cart := map[string]api.Integration{
		"ep1": {ID: "ep1", ServiceID: "svcA"},
		"ep2": {ID: "ep2", ServiceID: "svcA"},
		"ep3": {ID: "ep3", ServiceID: "svcB"},
	}

	selections := buildSelections(cart)

	if len(selections) != 2 {
		t.Fatalf("expected 2 service groupings, got %d", len(selections))
	}

	byService := make(map[string][]string)
	for _, sel := range selections {
		ids := append([]string(nil), sel.EndpointIDs...)
		sort.Strings(ids)
		byService[sel.ServiceID] = ids
	}

	if got := byService["svcA"]; len(got) != 2 || got[0] != "ep1" || got[1] != "ep2" {
		t.Errorf("svcA selections = %v, want [ep1 ep2]", got)
	}
	if got := byService["svcB"]; len(got) != 1 || got[0] != "ep3" {
		t.Errorf("svcB selections = %v, want [ep3]", got)
	}
}

func TestMergeNewEndpoints(t *testing.T) {
	cart := map[string]api.Integration{
		"ep1": {ID: "ep1", Name: "existing"},
	}
	incoming := []api.Integration{
		{ID: "ep1", Name: "duplicate"},
		{ID: "ep2", Name: "new-one"},
		{ID: "ep3", Name: "new-two"},
	}

	added := mergeNewEndpoints(cart, incoming)

	if added != 2 {
		t.Errorf("added = %d, want 2", added)
	}
	if len(cart) != 3 {
		t.Errorf("cart size = %d, want 3", len(cart))
	}
	if cart["ep1"].Name != "existing" {
		t.Errorf("expected existing ep1 to be left untouched, got %q", cart["ep1"].Name)
	}
}

func TestTerminalEventResult(t *testing.T) {
	cases := []struct {
		name         string
		ev           api.SDKEvent
		wantResult   string
		wantTerminal bool
	}{
		{"complete carries the SDK id", api.SDKEvent{Type: "complete", IntegrationID: "sdk-123"}, "sdk-123", true},
		{"error is terminal with no id", api.SDKEvent{Type: "error", Message: "boom"}, "", true},
		{"progress is not terminal", api.SDKEvent{Type: "progress", Message: "working"}, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, terminal := terminalEventResult(tc.ev)
			if result != tc.wantResult || terminal != tc.wantTerminal {
				t.Errorf("terminalEventResult(%+v) = (%q, %v), want (%q, %v)", tc.ev, result, terminal, tc.wantResult, tc.wantTerminal)
			}
		})
	}
}

// --- collectGenerationResult: fed fake channels, no network involved ---

func TestCollectGenerationResult_CompleteEvent(t *testing.T) {
	eventChan := make(chan api.SDKEvent, 2)
	errChan := make(chan error, 1)
	eventChan <- api.SDKEvent{Type: "progress", Message: "working"}
	eventChan <- api.SDKEvent{Type: "complete", IntegrationID: "sdk-abc"}
	close(eventChan)
	close(errChan)

	got := collectGenerationResult(eventChan, errChan)

	if got != "sdk-abc" {
		t.Errorf("collectGenerationResult() = %q, want %q", got, "sdk-abc")
	}
}

func TestCollectGenerationResult_ErrorEvent(t *testing.T) {
	eventChan := make(chan api.SDKEvent, 1)
	errChan := make(chan error, 1)
	eventChan <- api.SDKEvent{Type: "error", Message: "vendor rejected request"}
	close(eventChan)
	close(errChan)

	got := collectGenerationResult(eventChan, errChan)

	if got != "" {
		t.Errorf("collectGenerationResult() = %q, want empty string on a stream-reported error", got)
	}
}

func TestCollectGenerationResult_StreamTransportError(t *testing.T) {
	eventChan := make(chan api.SDKEvent, 1)
	errChan := make(chan error, 1)
	errChan <- fmt.Errorf("connection reset")
	close(eventChan)
	close(errChan)

	got := collectGenerationResult(eventChan, errChan)

	if got != "" {
		t.Errorf("collectGenerationResult() = %q, want empty string on a transport error", got)
	}
}

func TestCollectGenerationResult_BothChannelsCloseWithoutTerminalEvent(t *testing.T) {
	eventChan := make(chan api.SDKEvent)
	errChan := make(chan error)
	close(eventChan)
	close(errChan)

	got := collectGenerationResult(eventChan, errChan)

	if got != "" {
		t.Errorf("collectGenerationResult() = %q, want empty string when streams end without a terminal event", got)
	}
}

// --- processServiceIntent / searchAndAddEndpoints: httptest-backed, following
// the pattern established in import_test.go for exercising api.Client against
// a fake server rather than mocking the client itself. ---

// graphQLResponder maps a substring found in the incoming GraphQL query text
// to the raw JSON to send back under the "data" key, so one handler can serve
// ParseSDKIntent/SearchServices/SearchEndpoints without extra plumbing.
func newGraphQLTestServer(t *testing.T, responders map[string]string, workspaceServicesJSON string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/workspace/services" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(workspaceServicesJSON))
			return
		}
		if r.URL.Path != "/graphql" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode graphql body: %v", err)
		}
		for substr, data := range responders {
			if strings.Contains(body.Query, substr) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"data":` + data + `}`))
				return
			}
		}
		t.Fatalf("no responder configured for query: %s", body.Query)
	}))
}

func TestProcessServiceIntent_AddsNewEndpoints(t *testing.T) {
	server := newGraphQLTestServer(t, map[string]string{
		"searchServices":  `{"searchServices":[{"id":"svc-1","name":"Stripe"}]}`,
		"searchEndpoints": `{"searchEndpoints":[{"id":"ep-1","name":"Create Refund","path":"/refunds","method":"POST","service_id":"svc-1"}]}`,
	}, "[]")
	defer server.Close()

	client := api.NewClient(server.URL, "test-key")
	cart := make(map[string]api.Integration)
	services := make(map[string]api.Service)
	workspaceServices := []api.WorkspaceService{{ServiceID: "svc-1", Version: "1.0"}}

	added := processServiceIntent(client, api.IntentService{Name: "stripe", EndpointQuery: "refunds"}, workspaceServices, cart, services)

	if added != 1 {
		t.Errorf("added = %d, want 1", added)
	}
	if _, ok := cart["ep-1"]; !ok {
		t.Error("expected ep-1 to be added to the cart")
	}
	if _, ok := services["svc-1"]; !ok {
		t.Error("expected svc-1 to be recorded in the services map")
	}
}

func TestProcessServiceIntent_ServiceNotFound(t *testing.T) {
	server := newGraphQLTestServer(t, map[string]string{
		"searchServices": `{"searchServices":[]}`,
	}, "[]")
	defer server.Close()

	client := api.NewClient(server.URL, "test-key")
	cart := make(map[string]api.Integration)
	services := make(map[string]api.Service)

	added := processServiceIntent(client, api.IntentService{Name: "nonexistent"}, nil, cart, services)

	if added != 0 {
		t.Errorf("added = %d, want 0 when no service matches", added)
	}
	if len(cart) != 0 {
		t.Errorf("cart should stay empty, got %d entries", len(cart))
	}
}

func TestProcessServiceIntent_NotEnabledInWorkspace(t *testing.T) {
	server := newGraphQLTestServer(t, map[string]string{
		"searchServices": `{"searchServices":[{"id":"svc-1","name":"Stripe"}]}`,
	}, "[]")
	defer server.Close()

	client := api.NewClient(server.URL, "test-key")
	cart := make(map[string]api.Integration)
	services := make(map[string]api.Service)
	// workspaceServices deliberately doesn't contain svc-1, so
	// resolveServiceVersion should fail and no endpoints get fetched.
	workspaceServices := []api.WorkspaceService{{ServiceID: "svc-other", Version: "1.0"}}

	added := processServiceIntent(client, api.IntentService{Name: "stripe"}, workspaceServices, cart, services)

	if added != 0 {
		t.Errorf("added = %d, want 0 when the service isn't enabled in the workspace", added)
	}
}

func TestSearchAndAddEndpoints_MergesAcrossMultipleIntents(t *testing.T) {
	server := newGraphQLTestServer(t, map[string]string{
		"parseSDKIntent":  `{"parseSDKIntent":{"services":[{"name":"stripe","endpoint_query":"refunds"},{"name":"plunk","endpoint_query":"emails"}]}}`,
		"searchServices":  `{"searchServices":[{"id":"svc-1","name":"Stripe"}]}`,
		"searchEndpoints": `{"searchEndpoints":[{"id":"ep-1","name":"Create Refund","path":"/refunds","method":"POST","service_id":"svc-1"}]}`,
	}, `[{"service_id":"svc-1","version":"1.0"}]`)
	defer server.Close()

	client := api.NewClient(server.URL, "test-key")
	cart := make(map[string]api.Integration)
	services := make(map[string]api.Service)

	searchAndAddEndpoints(client, "stripe refunds and plunk emails", cart, services)

	// Both intents resolve to the same mocked service/endpoint here (the fake
	// server doesn't vary its response by query text), so the important
	// assertion is that the cart ends up populated and de-duplicated rather
	// than double-counted across the two intents.
	if len(cart) != 1 {
		t.Errorf("cart size = %d, want 1 (deduplicated across intents)", len(cart))
	}
}

func TestSearchAndAddEndpoints_NoServicesDetected(t *testing.T) {
	server := newGraphQLTestServer(t, map[string]string{
		"parseSDKIntent": `{"parseSDKIntent":{"services":[]}}`,
	}, "[]")
	defer server.Close()

	client := api.NewClient(server.URL, "test-key")
	cart := make(map[string]api.Integration)
	services := make(map[string]api.Service)

	searchAndAddEndpoints(client, "gibberish", cart, services)

	if len(cart) != 0 {
		t.Errorf("cart size = %d, want 0 when intent parsing finds no services", len(cart))
	}
}
