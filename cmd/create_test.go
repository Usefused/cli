package cmd

import (
	"encoding/json"
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

	added, svcAdded := processServiceIntent(client, api.IntentService{Name: "stripe", EndpointQuery: "refunds"}, wsCfg, cart, services, wsServices)

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
}

func TestProcessServiceIntent_ServiceNotFound(t *testing.T) {
	server := newGraphQLTestServer(t, map[string]string{
		"searchServices": `{"searchServices":[]}`,
	}, "[]")
	defer server.Close()

	client := api.NewClient(server.URL, "test-key")
	cart := make(map[string]api.Integration)
	services := make(map[string]api.Service)
	wsCfg := promptTestWorkspaceConfig()

	added, svcAdded := processServiceIntent(client, api.IntentService{Name: "nonexistent"}, wsCfg, cart, services, map[string]api.WorkspaceService{})

	if added != 0 || svcAdded {
		t.Errorf("processServiceIntent() = (%d, %v), want (0, false)", added, svcAdded)
	}
	if len(cart) != 0 {
		t.Errorf("cart should stay empty, got %d entries", len(cart))
	}
}

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

	added, svcAdded := processServiceIntent(client, api.IntentService{Name: "stripe"}, wsCfg, cart, services, map[string]api.WorkspaceService{})

	if added != 0 || svcAdded {
		t.Errorf("processServiceIntent() = (%d, %v), want (0, false)", added, svcAdded)
	}
}

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

	searchAndAddEndpoints(client, "stripe refunds and plunk emails", cart, services, wsServices)

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
	restore := usePromptTestWorkspaceFile(t)
	defer restore()

	searchAndAddEndpoints(client, "gibberish", cart, services, map[string]api.WorkspaceService{})

	if len(cart) != 0 {
		t.Errorf("cart size = %d, want 0 when intent parsing finds no services", len(cart))
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
