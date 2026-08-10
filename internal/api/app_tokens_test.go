package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAppTokenMutationsUseGenericFamilyScopedEndpoint(t *testing.T) {
	requests := make([]*http.Request, 0, 2)
	var generateInput AppTokenGenerateRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Clone(r.Context()))
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&generateInput); err != nil {
				t.Fatalf("decode generate input: %v", err)
			}
			_, _ = w.Write([]byte(`{"id":"token-1","app_family_id":"family-1","name":"agent","allow":["issues.list"],"expires_at":"2026-08-05T10:15:00Z","token":"shown-once","created_at":"2026-08-05T10:00:00Z"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	expiresIn := int64(900)
	token, err := client.GenerateAppToken("family-1", AppTokenGenerateRequest{
		Name:      "agent",
		Allow:     []string{"issues.list"},
		ExpiresIn: &expiresIn,
	})
	if err != nil || token.AppFamilyID != "family-1" {
		t.Fatalf("GenerateAppToken = %#v, %v", token, err)
	}
	if err := client.RevokeAppToken("family-1", "agent"); err != nil {
		t.Fatalf("RevokeAppToken: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	for _, request := range requests {
		if request.URL.Path != "/workspace/app-tokens" {
			t.Fatalf("token mutation path = %q", request.URL.Path)
		}
		query := request.URL.Query()
		if query.Get("app_family_id") != "family-1" || query.Has("artifact_id") || query.Has("app_id") {
			t.Fatalf("token mutation query = %q", request.URL.RawQuery)
		}
	}
	if generateInput.Name != "agent" || len(generateInput.Allow) != 1 || generateInput.Allow[0] != "issues.list" {
		t.Fatalf("generate input = %#v", generateInput)
	}
	if generateInput.ExpiresIn == nil || *generateInput.ExpiresIn != 900 {
		t.Fatalf("generate expiry = %#v", generateInput.ExpiresIn)
	}
}
