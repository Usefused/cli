package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestExecutionBuildSelectionsUsesOneEngineBatch verifies exact IDs arrive through the authorized Engine surface.
func TestExecutionBuildSelectionsUsesOneEngineBatch(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		// The public client must keep all operation lookups in one Engine GraphQL request.
		if request.URL.Path != "/engine/graphql" {
			t.Errorf("path = %q", request.URL.Path)
		}
		var body struct {
			Query     string `json:"query"`
			Variables struct {
				Selections []api.AppScaffoldSelection `json:"selections"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if !strings.Contains(body.Query, "executionBuildSelections") || len(body.Variables.Selections) != 1 || len(body.Variables.Selections[0].Operations) != 2 {
			t.Errorf("query or selections = %#v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":{"executionBuildSelections":[{"service":"crm","operation":"create","serviceId":"service-id","serviceVersionId":"version-id","endpointId":"endpoint-id"},{"service":"crm","operation":"read","serviceId":"service-id","serviceVersionId":"version-id","endpointId":"endpoint-id-2"}]}}`))
	}))
	defer server.Close()
	client := api.NewClient(server.URL, "fsk_test")
	selections, err := client.ExecutionBuildSelections([]api.AppScaffoldSelection{{Service: "crm", Version: "v1", Operations: []string{"create", "read"}}})
	if err != nil || calls != 1 || len(selections) != 2 || selections[1].Operation != "read" {
		t.Fatalf("selections = %#v, calls=%d, err=%v", selections, calls, err)
	}
}

// TestDraftPromptExecutionAppUsesRegistryTransport verifies model grounding stays on Registry's authenticated path.
func TestDraftPromptExecutionAppUsesRegistryTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// Registry owns the model and exact contract reads; Engine receives only the resulting build scope.
		if request.URL.Path != "/graphql" {
			t.Errorf("path = %q", request.URL.Path)
		}
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if !strings.Contains(body.Query, "draftPromptExecutionApp") {
			t.Errorf("unexpected query %q", body.Query)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":{"draftPromptExecutionApp":"{\"clarification\":\"\",\"source\":\"export default buildExecutionApp({})\"}"}}`))
	}))
	defer server.Close()
	client := api.NewClient(server.URL, "fsk_test")
	draft, err := client.DraftPromptExecutionApp("create a customer", []api.PromptOperationSelection{{Service: "crm", ServiceID: "service-id", Version: "v1", Operation: "create"}})
	if err != nil || !strings.Contains(draft, "buildExecutionApp") {
		t.Fatalf("draft = %q, err=%v", draft, err)
	}
}
