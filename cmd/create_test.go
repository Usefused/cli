package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

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

// --- processServiceIntent / searchAndAddEndpoints: httptest-backed, following
// the pattern established in import_test.go for exercising api.Client against
// a fake server rather than mocking the client itself. ---

// graphQLResponder maps a substring found in the incoming GraphQL query text
// to the raw JSON to send back under the "data" key, so one handler can serve
// ParseSDKIntent/SearchServices/SearchEndpoints without extra plumbing.
func newGraphQLTestServer(t *testing.T, responders map[string]string, workspaceServicesJSON string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if writeEngineWorkspaceServices(t, w, r, workspaceServicesJSON) {
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

// TestProcessServiceIntent_AddsNewEndpoints verifies a complete remote lookup
// mutates both the cart and workspace selection without an error.
func TestProcessServiceIntent_AddsNewEndpoints(t *testing.T) {
	// Intent discovery consumes the lean summary field because it needs only the
	// version name before searching endpoints.
	server := newGraphQLTestServer(t, map[string]string{
		"searchServices":          `{"searchServices":[{"id":"svc-1","name":"Stripe"}]}`,
		"servicesByIds":           `{"servicesByIds":[{"id":"svc-1","slug":"stripe","provider":null,"is_owner":true,"is_public":false}]}`,
		"ServiceVersionSummaries": `{"serviceVersions":[{"id":"ver-1","service_id":"svc-1","name":"1.0","status":"active"}]}`,
		"searchEndpoints":         `{"searchEndpoints":[{"id":"ep-1","name":"Create Refund","path":"/refunds","method":"POST","service_id":"svc-1"}]}`,
	}, "[]")
	defer server.Close()

	client := api.NewClient(server.URL, "test-key")
	cart := make(map[string]api.Integration)
	services := make(map[string]api.Service)
	wsCfg := promptTestWorkspaceConfig()
	wsServices := make(map[string]api.WorkspaceService)

	added, svcAdded, err := processServiceIntent(client, api.IntentService{Name: "stripe", EndpointQuery: "refunds"}, wsCfg, cart, services, wsServices)

	// A successful resolution must not carry a latent operational failure.
	if err != nil {
		t.Fatalf("processServiceIntent() error = %v", err)
	}
	if added != 1 || !svcAdded {
		t.Errorf("processServiceIntent() = (%d, %v), want (1, true)", added, svcAdded)
	}
	if _, ok := cart["ep-1"]; !ok {
		t.Error("expected ep-1 to be added to the cart")
	}
	if _, ok := services["svc-1"]; !ok {
		t.Error("expected svc-1 to be recorded in the services map")
	}
	if _, ok := wsCfg.Services["stripe"]; !ok {
		t.Error("expected stripe to be added to the workspace config")
	}
	// SDK generation indexes its services by this canonical reference, so the prompt selection cannot leave it empty.
	if wsServices["svc-1"].ServiceSlug != "stripe" {
		t.Fatalf("workspace service slug = %q, want stripe", wsServices["svc-1"].ServiceSlug)
	}
}

// TestProcessServiceIntent_ServiceNotFound distinguishes a valid empty search
// from a successful no-op so prompt exits non-zero with the service name.
func TestProcessServiceIntent_ServiceNotFound(t *testing.T) {
	server := newGraphQLTestServer(t, map[string]string{
		"searchServices": `{"searchServices":[]}`,
	}, "[]")
	defer server.Close()

	client := api.NewClient(server.URL, "test-key")
	cart := make(map[string]api.Integration)
	services := make(map[string]api.Service)
	wsCfg := promptTestWorkspaceConfig()

	added, svcAdded, err := processServiceIntent(client, api.IntentService{Name: "nonexistent"}, wsCfg, cart, services, map[string]api.WorkspaceService{})

	// The empty Registry result must become an actionable validation error.
	if err == nil || !strings.Contains(err.Error(), `no service matched "nonexistent"`) {
		t.Fatalf("expected service-not-found error, got %v", err)
	}
	if added != 0 || svcAdded {
		t.Errorf("processServiceIntent() = (%d, %v), want (0, false)", added, svcAdded)
	}
	if len(cart) != 0 {
		t.Errorf("cart should stay empty, got %d entries", len(cart))
	}
}

// TestProcessServiceIntent_NoVersionsAvailable verifies an unversioned service
// fails before it can be written into the workspace config.
func TestProcessServiceIntent_NoVersionsAvailable(t *testing.T) {
	// An empty summary is the bounded signal that intent discovery cannot pin a
	// Registry version; no full policy read is needed for this branch.
	server := newGraphQLTestServer(t, map[string]string{
		"searchServices":          `{"searchServices":[{"id":"svc-1","name":"Stripe"}]}`,
		"servicesByIds":           `{"servicesByIds":[{"id":"svc-1","slug":"stripe","provider":null,"is_owner":true,"is_public":false}]}`,
		"ServiceVersionSummaries": `{"serviceVersions":[]}`,
	}, "[]")
	defer server.Close()

	client := api.NewClient(server.URL, "test-key")
	cart := make(map[string]api.Integration)
	services := make(map[string]api.Service)
	wsCfg := promptTestWorkspaceConfig()

	added, svcAdded, err := processServiceIntent(client, api.IntentService{Name: "stripe"}, wsCfg, cart, services, map[string]api.WorkspaceService{})

	// A service without versions cannot back any generated SDK endpoint.
	if err == nil || !strings.Contains(err.Error(), "no versions are available") {
		t.Fatalf("expected missing-version error, got %v", err)
	}
	if added != 0 || svcAdded {
		t.Errorf("processServiceIntent() = (%d, %v), want (0, false)", added, svcAdded)
	}
}

// TestProcessServiceIntentRejectsMissingVisibility distinguishes a malformed
// catalogue join from a legitimate service with no published versions.
func TestProcessServiceIntentRejectsMissingVisibility(t *testing.T) {
	server := newGraphQLTestServer(t, map[string]string{
		"searchServices": `{"searchServices":[{"id":"svc-1","name":"Stripe"}]}`,
		"servicesByIds":  `{"servicesByIds":[]}`,
	}, "[]")
	defer server.Close()

	_, _, err := processServiceIntent(api.NewClient(server.URL, "test-key"), api.IntentService{Name: "stripe"}, promptTestWorkspaceConfig(), map[string]api.Integration{}, map[string]api.Service{}, map[string]api.WorkspaceService{})
	// Missing visibility metadata must identify the failed join instead of falling through to a misleading version error.
	if err == nil || !strings.Contains(err.Error(), "visibility response omitted the canonical reference") {
		t.Fatalf("expected missing-visibility error, got %v", err)
	}
}

// TestSearchAndAddEndpoints_MergesAcrossMultipleIntents verifies successful
// intent batches still merge and de-duplicate endpoints after error propagation.
func TestSearchAndAddEndpoints_MergesAcrossMultipleIntents(t *testing.T) {
	// Repeated intents share the same summary response without expanding into
	// documentation and policy payloads for either lookup.
	server := newGraphQLTestServer(t, map[string]string{
		"parseSDKIntent":          `{"parseSDKIntent":{"services":[{"name":"stripe","endpoint_query":"refunds"},{"name":"plunk","endpoint_query":"emails"}]}}`,
		"searchServices":          `{"searchServices":[{"id":"svc-1","name":"Stripe"}]}`,
		"servicesByIds":           `{"servicesByIds":[{"id":"svc-1","slug":"stripe","provider":null,"is_owner":true,"is_public":false}]}`,
		"ServiceVersionSummaries": `{"serviceVersions":[{"id":"ver-1","service_id":"svc-1","name":"1.0","status":"active"}]}`,
		"searchEndpoints":         `{"searchEndpoints":[{"id":"ep-1","name":"Create Refund","path":"/refunds","method":"POST","service_id":"svc-1"}]}`,
	}, `[{"service_id":"svc-1","version":"1.0"}]`)
	defer server.Close()

	client := api.NewClient(server.URL, "test-key")
	cart := make(map[string]api.Integration)
	services := make(map[string]api.Service)
	wsServices := make(map[string]api.WorkspaceService)
	restore := usePromptTestWorkspaceFile(t)
	defer restore()

	err := searchAndAddEndpoints(client, "stripe refunds and plunk emails", cart, services, wsServices)

	// Both mocked intents resolve completely, so persistence and search succeed.
	if err != nil {
		t.Fatalf("searchAndAddEndpoints() error = %v", err)
	}
	// Both intents resolve to the same mocked service/endpoint here (the fake
	// server doesn't vary its response by query text), so the important
	// assertion is that the cart ends up populated and de-duplicated rather
	// than double-counted across the two intents.
	if len(cart) != 1 {
		t.Errorf("cart size = %d, want 1 (deduplicated across intents)", len(cart))
	}
}

// TestSearchAndAddEndpoints_NoServicesDetected verifies an unusable prompt is
// surfaced as validation failure rather than an empty successful cart.
func TestSearchAndAddEndpoints_NoServicesDetected(t *testing.T) {
	server := newGraphQLTestServer(t, map[string]string{
		"parseSDKIntent": `{"parseSDKIntent":{"services":[]}}`,
	}, "[]")
	defer server.Close()

	client := api.NewClient(server.URL, "test-key")
	cart := make(map[string]api.Integration)
	services := make(map[string]api.Service)
	restore := usePromptTestWorkspaceFile(t)
	defer restore()

	err := searchAndAddEndpoints(client, "gibberish", cart, services, map[string]api.WorkspaceService{})

	// No detected services means generation cannot proceed and must be non-zero.
	if err == nil || !strings.Contains(err.Error(), "no services detected") {
		t.Fatalf("expected no-services error, got %v", err)
	}
	if len(cart) != 0 {
		t.Errorf("cart size = %d, want 0 when intent parsing finds no services", len(cart))
	}
}

// TestProcessServiceIntentPreservesStructuredAPIError verifies prompt wrapping
// keeps the API client's reviewed diagnostic contract available to the CLI.
func TestProcessServiceIntentPreservesStructuredAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"code":"registry_request_failed","message":"Registry is unavailable.","category":"dependency","retryable":true,"remediation":"Retry after Registry recovers."}}`))
	}))
	defer server.Close()

	client := api.NewClient(server.URL, "test-key")
	_, _, err := processServiceIntent(client, api.IntentService{Name: "stripe"}, promptTestWorkspaceConfig(), map[string]api.Integration{}, map[string]api.Service{}, map[string]api.WorkspaceService{})

	var apiErr *api.APIError
	// errors.As proves the prompt wrapper did not flatten the stable contract.
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected wrapped APIError, got %T: %v", err, err)
	}
	// The current envelope keeps reviewed primary guidance in Message and reserves
	// ServerDetail for a separately admitted diagnostic detail field.
	if apiErr.Code == "" || !strings.Contains(apiErr.Message, "Registry is unavailable") || apiErr.Remediation == "" {
		t.Fatalf("structured API detail was not preserved: %+v", apiErr)
	}
}

// TestPersistPromptWorkspaceReturnsWriteFailure verifies local replacement
// errors reach the command instead of being downgraded to a warning.
func TestPersistPromptWorkspaceReturnsWriteFailure(t *testing.T) {
	blockingParent := filepath.Join(t.TempDir(), "not-a-directory")
	// A regular file in the parent position deterministically prevents the
	// atomic writer from creating its sibling temporary file.
	if err := os.WriteFile(blockingParent, []byte("blocked"), 0600); err != nil {
		t.Fatalf("write blocking parent: %v", err)
	}
	err := persistPromptWorkspace(filepath.Join(blockingParent, "workspace.yaml"), promptTestWorkspaceConfig(), []string{"stripe"})

	if err == nil || !strings.Contains(err.Error(), "writing updated workspace config") {
		t.Fatalf("expected workspace persistence error, got %v", err)
	}
}

// TestBuildCartTreatsExplicitCancelAsSuccess verifies user cancellation remains
// distinct from the operational failures now returned by the prompt flow.
func TestBuildCartTreatsExplicitCancelAsSuccess(t *testing.T) {
	server := newGraphQLTestServer(t, map[string]string{
		"parseSDKIntent":          `{"parseSDKIntent":{"services":[{"name":"stripe","endpoint_query":"refunds"}]}}`,
		"searchServices":          `{"searchServices":[{"id":"svc-1","name":"Stripe"}]}`,
		"servicesByIds":           `{"servicesByIds":[{"id":"svc-1","slug":"stripe","provider":null,"is_owner":true,"is_public":false}]}`,
		"ServiceVersionSummaries": `{"serviceVersions":[{"id":"ver-1","service_id":"svc-1","name":"1.0","status":"active"}]}`,
		"searchEndpoints":         `{"searchEndpoints":[{"id":"ep-1","name":"Create Refund","path":"/refunds","method":"POST","service_id":"svc-1"}]}`,
	}, "[]")
	defer server.Close()
	restoreWorkspace := usePromptTestWorkspaceFile(t)
	defer restoreWorkspace()
	oldRunner := promptCartActionRunner
	oldAutoYes := autoYes
	promptCartActionRunner = func() (string, error) { return "cancel", nil }
	autoYes = false
	defer func() {
		promptCartActionRunner = oldRunner
		autoYes = oldAutoYes
	}()

	_, _, proceed, err := buildCart(api.NewClient(server.URL, "test-key"), "stripe refunds")

	// Explicit cancel is a successful exit that simply declines generation.
	if err != nil || proceed {
		t.Fatalf("cancel returned proceed=%v err=%v", proceed, err)
	}
}

// promptTestWorkspaceConfig mirrors the minimum file state the prompt flow
// needs before it can safely auto-add a service version.
func promptTestWorkspaceConfig() *configfile.WorkspaceConfig {
	return &configfile.WorkspaceConfig{
		BaseConfig: configfile.BaseConfig{Kind: configfile.KindWorkspace, APIVersion: configfile.APIVersionV1},
		Services:   map[string]configfile.WorkspaceService{},
	}
}

// usePromptTestWorkspaceFile pins ConfigFile to an isolated workspace file so
// prompt tests never read or mutate a developer's real .fused directory.
func usePromptTestWorkspaceFile(t *testing.T) func() {
	t.Helper()
	oldConfigFile := ConfigFile
	path := filepath.Join(t.TempDir(), "workspace.yaml")
	if err := os.WriteFile(path, []byte("kind: workspace\napiVersion: fused/v1\nservices: {}\n"), 0644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	ConfigFile = path
	return func() { ConfigFile = oldConfigFile }
}
