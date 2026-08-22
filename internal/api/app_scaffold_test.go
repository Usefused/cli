package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestAppScaffoldRequirementsUsesOneTypedEngineBatch proves the transport
// carries the complete selection surface without exposing generated values.
func TestAppScaffoldRequirementsUsesOneTypedEngineBatch(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		assertAppScaffoldRequest(t, request)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":{"appScaffoldRequirements":[{"service":"send bird","variable":"region"},{"service":"send bird","variable":"app_id"}]}}`))
	}))
	defer server.Close()

	client := api.NewClient(server.URL, "test-key")
	requirements, err := client.AppScaffoldRequirements([]api.AppScaffoldSelection{{
		Service: "send bird", Version: "v3", Operations: []string{"listUsers"}, SelectAll: false,
	}})
	// Transport assertions require a decoded successful response.
	if err != nil {
		t.Fatalf("AppScaffoldRequirements: %v", err)
	}
	// Transport preserves the Engine projection because scaffold canonicalization
	// belongs to the config-writing layer rather than this shared HTTP client.
	if requests != 1 || len(requirements) != 2 || requirements[0].Variable != "region" || requirements[1].Variable != "app_id" {
		t.Fatalf("requests=%d requirements=%#v", requests, requirements)
	}
}

// assertAppScaffoldRequest verifies the stable GraphQL path, selection, and
// intentionally metadata-only response projection.
func assertAppScaffoldRequest(t *testing.T, request *http.Request) {
	t.Helper()
	// Scaffold discovery is Engine-owned and must not fall back to Registry GraphQL.
	if request.URL.Path != "/engine/graphql" {
		t.Fatalf("path = %q", request.URL.Path)
	}
	var body struct {
		Query     string `json:"query"`
		Variables struct {
			Selections []api.AppScaffoldSelection `json:"selections"`
		} `json:"variables"`
	}
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	// Only requirement metadata is projected; bucket references and values are CLI-owned output.
	if !strings.Contains(body.Query, "appScaffoldRequirements") || !strings.Contains(body.Query, "service variable") || strings.Contains(body.Query, "value") {
		t.Fatalf("unexpected query: %s", body.Query)
	}
	// One typed selection proves operation scope and version were not dropped.
	if len(body.Variables.Selections) != 1 || body.Variables.Selections[0].Version != "v3" || body.Variables.Selections[0].Operations[0] != "listUsers" {
		t.Fatalf("selections = %#v", body.Variables.Selections)
	}
}

// TestAppScaffoldRequirementsSkipsEmptySelections keeps editable empty app
// skeletons offline and avoids a meaningless Engine request.
func TestAppScaffoldRequirementsSkipsEmptySelections(t *testing.T) {
	client := api.NewClient("http://127.0.0.1:1", "test-key")
	requirements, err := client.AppScaffoldRequirements(nil)
	// Empty input has no dependency failure because no request is required.
	if err != nil || len(requirements) != 0 {
		t.Fatalf("requirements=%#v err=%v", requirements, err)
	}
}
