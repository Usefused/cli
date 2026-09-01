package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Usefused/cli/internal/api"
)

// TestWaitForSDKGenerationPollsEngineUntilComplete proves pending work is followed by Version ID without Registry SSE.
func TestWaitForSDKGenerationPollsEngineUntilComplete(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Every poll must remain on the Engine-owned immutable-version route.
		if r.URL.Path != "/sdk-config/generation/version-1" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		// The first read exposes durable queued work before the next read proves readiness.
		if requestCount == 1 {
			_, _ = w.Write([]byte(`{"status":"pending","app_id":"version-1","app_family_id":"sdk-1","job_id":"job-1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"complete","app_id":"version-1","app_family_id":"sdk-1","job_id":"job-1"}`))
	}))
	defer server.Close()

	status, err := waitForSDKGenerationWithTiming(api.NewClient(server.URL, "fsk_test"), "version-1", time.Millisecond, time.Second)
	// Completion must return the terminal Engine projection so JSON output can retain its job identity.
	if err != nil {
		t.Fatalf("waitForSDKGenerationWithTiming: %v", err)
	}
	// Exactly one pending and one complete read proves polling rather than stream consumption.
	if requestCount != 2 || status.Status != "complete" || status.JobID != "job-1" {
		t.Fatalf("requests=%d status=%#v", requestCount, status)
	}
}

// TestWaitForSDKGenerationReturnsTerminalFailureIdentity preserves post-commit context without exposing Registry error prose.
func TestWaitForSDKGenerationReturnsTerminalFailureIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"failed","app_id":"version-1","app_family_id":"sdk-1","job_id":"job-failed"}`))
	}))
	defer server.Close()

	status, err := waitForSDKGenerationWithTiming(api.NewClient(server.URL, "fsk_test"), "version-1", time.Millisecond, time.Second)
	// A failed generation is terminal and must not be mistaken for an apply failure or retried locally.
	if err == nil || !strings.Contains(err.Error(), "generation failed") {
		t.Fatalf("error = %v", err)
	}
	// The returned status retains the job identity for the structured stage error.
	if status == nil || status.JobID != "job-failed" {
		t.Fatalf("status = %#v", status)
	}
}

// TestWaitForSDKGenerationRejectsMismatchedVersion prevents one version's package readiness from authorizing another download.
func TestWaitForSDKGenerationRejectsMismatchedVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"complete","app_id":"version-2","app_family_id":"sdk-1","job_id":"job-2"}`))
	}))
	defer server.Close()

	_, err := waitForSDKGenerationWithTiming(api.NewClient(server.URL, "fsk_test"), "version-1", time.Millisecond, time.Second)
	// Identity mismatch is an invalid Engine response, even when the reported state is complete.
	if err == nil || !strings.Contains(err.Error(), "unexpected Version ID") {
		t.Fatalf("error = %v", err)
	}
}

// TestWaitForSDKGenerationRetriesTransientStatusRead proves a brief Engine failure does not strand an already-committed apply.
func TestWaitForSDKGenerationRetriesTransientStatusRead(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount++
		// A proxy or restart interruption is retryable because the CLI performs only local status reads.
		if requestCount == 1 {
			http.Error(w, `{"error":{"code":"engine_unavailable","message":"temporarily unavailable","retryable":true}}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"complete","app_id":"version-1","app_family_id":"sdk-1","job_id":"job-1"}`))
	}))
	defer server.Close()

	status, err := waitForSDKGenerationWithTiming(api.NewClient(server.URL, "fsk_test"), "version-1", time.Millisecond, time.Second)
	// One failed read followed by completion proves apply was not replayed and status polling recovered.
	if err != nil || requestCount != 2 || status == nil || status.Status != "complete" {
		t.Fatalf("requests=%d status=%#v error=%v", requestCount, status, err)
	}
}
