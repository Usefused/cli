package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

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
