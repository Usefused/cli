package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetSDKGenerationStatusReadsEngineOwnedVersionState verifies the CLI no longer addresses Registry jobs directly.
func TestGetSDKGenerationStatusReadsEngineOwnedVersionState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The immutable Version ID is the sole generation-status address.
		if r.Method != http.MethodGet || r.URL.Path != "/sdk-config/generation/version-1" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		// Status reads use the existing Engine control credential.
		if r.Header.Get("x-api-key") != "fsk_test" {
			t.Fatalf("x-api-key = %q", r.Header.Get("x-api-key"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"pending","app_id":"version-1","app_family_id":"sdk-1","job_id":"job-1"}`))
	}))
	defer server.Close()

	status, err := NewClient(server.URL, "fsk_test").GetSDKGenerationStatus(" version-1 ")
	// A valid Engine response must retain all identities needed by structured apply output.
	if err != nil {
		t.Fatalf("GetSDKGenerationStatus: %v", err)
	}
	// The client projection must not rename Engine's canonical terminal vocabulary.
	if status.Status != "pending" || status.AppID != "version-1" || status.AppFamilyID != "sdk-1" || status.JobID != "job-1" {
		t.Fatalf("status = %#v", status)
	}
}

// TestGetSDKGenerationStatusRejectsMissingVersionID keeps malformed local state away from Engine.
func TestGetSDKGenerationStatusRejectsMissingVersionID(t *testing.T) {
	_, err := NewClient("https://engine.invalid", "fsk_test").GetSDKGenerationStatus(" ")
	// Missing immutable identity must fail before a network request can be constructed.
	if err == nil {
		t.Fatal("expected missing Version ID error")
	}
}
