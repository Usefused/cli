package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSDKTokenMutationsUseAppFamilyID(t *testing.T) {
	requests := make([]*http.Request, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Clone(r.Context()))
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"token-1","app_family_id":"family-1","name":"default","token":"shown-once","created_at":"2026-08-05T10:00:00Z"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	token, err := client.GenerateSDKToken("family-1", "default")
	if err != nil || token.AppFamilyID != "family-1" {
		t.Fatalf("GenerateSDKToken = %#v, %v", token, err)
	}
	if err := client.RevokeSDKToken("family-1", "default"); err != nil {
		t.Fatalf("RevokeSDKToken: %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	for _, request := range requests {
		query := request.URL.Query()
		if query.Get("app_family_id") != "family-1" || query.Has("artifact_id") || query.Has("app_id") {
			t.Fatalf("token mutation query = %q", request.URL.RawQuery)
		}
	}
}
