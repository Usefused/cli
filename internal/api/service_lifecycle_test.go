package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestServiceLifecyclePreservesRejections verifies every route retains typed errors and never retries a mutation.
func TestServiceLifecyclePreservesRejections(t *testing.T) {
	for _, status := range []int{400, 403, 404, 410, 503} {
		// A status table covers validation, ownership, missing definitions, elapsed deadlines and old deployments.
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			requests := 0
			// Return the exact HTTP rejection without including private server diagnostics.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":"lifecycle rejected"}`)
			}))
			defer server.Close()
			client := NewClient(server.URL, "test-key")
			_, readErr := client.GetServiceLifecycle("service-id")
			for _, err := range []error{readErr, client.ScheduleServiceDeletion("service-id", time.Now().Add(time.Hour)), client.CancelServiceDeletion("service-id"), client.DeleteRegistryService("service-id")} {
				var apiErr *APIError
				// Wrapping must retain HTTP status for the CLI's machine error envelope.
				if !errors.As(err, &apiErr) || apiErr.HTTPStatus != status {
					t.Fatalf("status=%d err=%v", status, err)
				}
			}
			// One attempt per operation avoids unsafe blind replay after server errors.
			if requests != 4 {
				t.Fatalf("unexpected retry count: %d", requests)
			}
		})
	}
}

// TestServiceLifecycleRejectsMalformedSuccess prevents stale or partial responses from being reported as authoritative state.
func TestServiceLifecycleRejectsMalformedSuccess(t *testing.T) {
	for _, body := range []string{"", "not JSON", "{}", `{"service_id":"another-service"}`} {
		// Each fixture supplies a syntactically or semantically invalid successful read.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, body)
		}))
		_, err := NewClient(server.URL, "test-key").GetServiceLifecycle("service-id")
		server.Close()
		// Missing identity must not become a default active-service state.
		if err == nil {
			t.Fatalf("accepted malformed lifecycle response %q", body)
		}
	}
}
