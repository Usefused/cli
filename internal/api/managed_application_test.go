package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestConnectSessionCarriesManagedApplication verifies the standalone CLI transport keeps application identity separate from the provider scheme.
func TestConnectSessionCarriesManagedApplication(t *testing.T) {
	const application = "729ea172-5512-4f37-b202-31084e2d2766"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		// Malformed transport input must fail the fixture without claiming a successful session.
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		// Application selection must survive independently of the unchanged provider scheme.
		if request.Variables["managedApplicationID"] != application || request.Variables["authName"] != "oauth2" || !strings.Contains(request.Query, "managed_application_id: $managedApplicationID") {
			t.Error("managed application selector lost in GraphQL transport")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"startConnectSession":{"authorize_url":"https://provider.example/authorize","expires_at":"2026-09-21T20:00:00Z"}}}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "fixture-key")
	_, err := client.StartConnectSession("bucket", "service", "user", "", "oauth", "oauth2", "${fused.bucket.auth.provider.oauth2}", nil, nil, application)
	// A rejected response must not masquerade as preserved client transport.
	if err != nil {
		t.Fatal(err)
	}
}
