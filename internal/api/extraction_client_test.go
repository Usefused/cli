package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

func TestIntegrationExtractionClientDrivesSessionProtocol(t *testing.T) {
	fake := newExtractionAPIServer(t)
	defer fake.close()

	client := api.NewClient(fake.url(), "test-key")
	start, err := client.StartIntegrationExtraction(context.Background(), testExtractionStartRequest())
	if err != nil {
		t.Fatalf("StartIntegrationExtraction: %v", err)
	}
	assertExtractionStart(t, start, fake.startRequest)

	if err := client.RespondIntegrationExtraction(context.Background(), "session-1", `{"preview":"{}"}`); err != nil {
		t.Fatalf("RespondIntegrationExtraction: %v", err)
	}
	assertExtractionRespond(t, fake.respondRequest)

	events := streamExtractionEvents(t, client)
	assertExtractionEvents(t, events)
}

type extractionAPIServer struct {
	t              *testing.T
	server         *httptest.Server
	startRequest   api.IntegrationExtractionStartRequest
	respondRequest map[string]string
}

func newExtractionAPIServer(t *testing.T) *extractionAPIServer {
	fake := &extractionAPIServer{t: t}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.handle))
	return fake
}

func (s *extractionAPIServer) url() string {
	return s.server.URL
}

func (s *extractionAPIServer) close() {
	s.server.Close()
}

func (s *extractionAPIServer) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/integrations/start":
		s.handleStart(w, r)
	case "/integrations/respond":
		s.handleRespond(w, r)
	case "/integrations/session/session-1/stream":
		s.handleStream(w)
	default:
		s.t.Fatalf("unexpected path %s", r.URL.Path)
	}
}

func (s *extractionAPIServer) handleStart(w http.ResponseWriter, r *http.Request) {
	if err := json.NewDecoder(r.Body).Decode(&s.startRequest); err != nil {
		s.t.Fatalf("decode start: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"session_id":"session-1"}`))
}

func (s *extractionAPIServer) handleRespond(w http.ResponseWriter, r *http.Request) {
	if err := json.NewDecoder(r.Body).Decode(&s.respondRequest); err != nil {
		s.t.Fatalf("decode respond: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"resumed"}`))
}

func (s *extractionAPIServer) handleStream(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Write([]byte("data: {\"type\":\"thinking\",\"message\":\"Working\"}\n\n"))
	w.Write([]byte("data: {\"type\":\"complete\",\"integration_id\":\"svc-1\",\"version\":\"1.0\"}\n\n"))
}

func testExtractionStartRequest() api.IntegrationExtractionStartRequest {
	return api.IntegrationExtractionStartRequest{
		Name: "Docs API", ServiceSlug: "docs-api", Version: "1.0",
		SourceURL: "https://docs.example.test", ImportMethod: "docs", TargetType: "endpoints",
	}
}

func assertExtractionStart(t *testing.T, start *api.IntegrationExtractionStartResponse, req api.IntegrationExtractionStartRequest) {
	t.Helper()
	if start.SessionID != "session-1" || req.ImportMethod != "docs" || req.TargetType != "endpoints" {
		t.Fatalf("unexpected start exchange: response=%+v request=%+v", start, req)
	}
}

func assertExtractionRespond(t *testing.T, req map[string]string) {
	t.Helper()
	if req["session_id"] != "session-1" || req["answer"] == "" {
		t.Fatalf("unexpected respond request: %#v", req)
	}
}

func streamExtractionEvents(t *testing.T, client *api.Client) []api.IntegrationExtractionEvent {
	t.Helper()
	var events []api.IntegrationExtractionEvent
	err := client.StreamIntegrationExtraction(context.Background(), "session-1", func(event api.IntegrationExtractionEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamIntegrationExtraction: %v", err)
	}
	return events
}

func assertExtractionEvents(t *testing.T, events []api.IntegrationExtractionEvent) {
	t.Helper()
	if len(events) != 2 || events[0].Type != "thinking" || events[1].IntegrationID != "svc-1" {
		t.Fatalf("unexpected stream events: %+v", events)
	}
}
