package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAppTokenMutationsUseGenericFamilyScopedEndpoint(t *testing.T) {
	capture := &appTokenMutationCapture{}
	server := httptest.NewServer(appTokenMutationHandler(t, capture))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	expiresIn := int64(900)
	resourceID := "resource-1"
	token, err := client.GenerateAppToken("family-1", AppTokenGenerateRequest{
		Name: "agent", Allow: []string{"issues.list"}, ExpiresIn: &expiresIn,
		BindingMode: "fixed", Bindings: []AppTokenBindingRequest{{
			ServiceSlug: "google-drive", AuthName: "google", EndUserRef: "customer-1", ResourceID: &resourceID,
		}},
	})
	if err != nil || token.AppFamilyID != "family-1" || token.BindingMode != "fixed" || token.BindingCount != 1 {
		t.Fatalf("GenerateAppToken = %#v, %v", token, err)
	}
	if err := client.RevokeAppToken("family-1", "agent"); err != nil {
		t.Fatalf("RevokeAppToken: %v", err)
	}
	assertAppTokenMutationRequests(t, capture.requests)
	assertFixedTokenGenerateInput(t, capture.generateInput)
}

type appTokenMutationCapture struct {
	requests      []*http.Request
	generateInput AppTokenGenerateRequest
}

func appTokenMutationHandler(t *testing.T, capture *appTokenMutationCapture) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.requests = append(capture.requests, r.Clone(r.Context()))
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&capture.generateInput); err != nil {
				t.Fatalf("decode generate input: %v", err)
			}
			_, _ = w.Write([]byte(`{"id":"token-1","app_family_id":"family-1","name":"agent","allow":["issues.list"],"binding_mode":"fixed","binding_count":1,"expires_at":"2026-08-05T10:15:00Z","token":"shown-once","created_at":"2026-08-05T10:00:00Z"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func assertAppTokenMutationRequests(t *testing.T, requests []*http.Request) {
	t.Helper()
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
}

func assertFixedTokenGenerateInput(t *testing.T, input AppTokenGenerateRequest) {
	t.Helper()
	if input.Name != "agent" || len(input.Allow) != 1 || input.Allow[0] != "issues.list" || input.BindingMode != "fixed" {
		t.Fatalf("generate input = %#v", input)
	}
	if input.ExpiresIn == nil || *input.ExpiresIn != 900 || len(input.Bindings) != 1 {
		t.Fatalf("generate expiry/bindings = %#v/%#v", input.ExpiresIn, input.Bindings)
	}
	assertFixedTokenBinding(t, input.Bindings[0])
}

func assertFixedTokenBinding(t *testing.T, binding AppTokenBindingRequest) {
	t.Helper()
	if binding.ServiceSlug != "google-drive" || binding.AuthName != "google" || binding.EndUserRef != "customer-1" || binding.ResourceID == nil || *binding.ResourceID != "resource-1" {
		t.Fatalf("generate fixed binding = %#v", binding)
	}
}

func TestValidServiceSlugReference(t *testing.T) {
	for _, reference := range []string{"jira", "google-drive", "@google/gmail"} {
		if !ValidServiceSlugReference(reference) {
			t.Fatalf("rejected valid service slug reference %q", reference)
		}
	}
	for _, reference := range []string{"", "@google/", "@/gmail", "google/gmail", "de305d54-75b4-431b-adb2-eb6b9e546014"} {
		if ValidServiceSlugReference(reference) {
			t.Fatalf("accepted invalid service slug reference %q", reference)
		}
	}
}
