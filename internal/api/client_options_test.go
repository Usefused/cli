package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewClientUsesFiniteDefaultTimeout(t *testing.T) {
	client := NewClient("https://engine.example.com", "test-key")
	if client.HTTP.Timeout != DefaultTimeout {
		t.Fatalf("expected default timeout %s, got %s", DefaultTimeout, client.HTTP.Timeout)
	}
}

func TestClientAddsRequestIDToEveryRequest(t *testing.T) {
	var requestID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID = r.Header.Get("X-Request-ID")
		_, _ = w.Write([]byte(`{"status":"ok","plane":"engine"}`))
	}))
	defer server.Close()

	client := NewClientWithOptions(server.URL, "test-key", ClientOptions{RequestID: "deploy-42"})
	if _, err := client.Health(); err != nil {
		t.Fatalf("health request: %v", err)
	}
	if requestID != "deploy-42" {
		t.Fatalf("expected request ID header, got %q", requestID)
	}
}

func TestClientTimeoutBoundsUnresponsiveRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(250 * time.Millisecond)
	}))
	defer server.Close()

	client := NewClientWithOptions(server.URL, "test-key", ClientOptions{Timeout: 20 * time.Millisecond})
	started := time.Now()
	_, err := client.Health()
	if err == nil {
		t.Fatal("expected request timeout")
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("request exceeded bounded timeout: %s", elapsed)
	}
}

func TestClientCancelsRequestWithExecutionContext(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := NewClientWithOptions(server.URL, "test-key", ClientOptions{Context: ctx, Timeout: time.Second})
	errCh := make(chan error, 1)
	go func() {
		_, err := client.Health()
		errCh <- err
	}()
	<-started
	cancel()

	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}
