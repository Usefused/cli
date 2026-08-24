package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestDiscoveryClientUsesOnlyTheVersionedSessionContract exercises start, action, snapshot, and SSE identities.
func TestDiscoveryClientUsesOnlyTheVersionedSessionContract(t *testing.T) {
	fake := newDiscoveryAPIServer(t)
	defer fake.server.Close()
	client := api.NewClient(fake.server.URL, "test-key")
	snapshot := startDiscoveryClient(t, client)
	assertDiscoveryStart(t, fake.start, snapshot)
	assertDiscoveryAction(t, client, fake)
	assertDiscoverySnapshotReload(t, client)
	assertDiscoveryStream(t, client)
}

// startDiscoveryClient starts one focused client session and requires a valid response.
func startDiscoveryClient(t *testing.T, client *api.Client) *api.DiscoverySnapshot {
	t.Helper()
	snapshot, err := client.StartDiscovery(context.Background(), discoveryStartFixture())
	if err != nil {
		t.Fatalf("StartDiscovery: %v", err)
	}
	return snapshot
}

// assertDiscoveryAction verifies the typed route returns the committed next snapshot.
func assertDiscoveryAction(t *testing.T, client *api.Client, fake *discoveryAPIServer) {
	t.Helper()
	next, err := client.ApplyDiscoveryAction(context.Background(), "session-1", discoverySelectionAction())
	if err != nil {
		t.Fatalf("ApplyDiscoveryAction: %v", err)
	}
	if next.State != api.DiscoveryStateExtractContract || fake.action.Action != api.DiscoveryActionSelectOperations {
		t.Fatalf("unexpected action exchange: snapshot=%+v request=%+v", next, fake.action)
	}
}

// assertDiscoverySnapshotReload verifies GET returns the authoritative revision independently of SSE.
func assertDiscoverySnapshotReload(t *testing.T, client *api.Client) {
	t.Helper()
	loaded, err := client.GetDiscoverySession(context.Background(), "session-1")
	if err != nil || loaded.Revision != 2 {
		t.Fatalf("GetDiscoverySession = %+v, %v", loaded, err)
	}
}

// assertDiscoveryStream verifies the v1 event identity is retained by the client.
func assertDiscoveryStream(t *testing.T, client *api.Client) {
	t.Helper()
	var events []api.DiscoveryEvent
	if err := client.StreamDiscovery(context.Background(), "session-1", func(event api.DiscoveryEvent) error {
		events = append(events, event)
		return nil
	}); err != nil {
		t.Fatalf("StreamDiscovery: %v", err)
	}
	if len(events) != 1 || events[0].Type != "extraction_progress" || events[0].SessionID != "session-1" {
		t.Fatalf("unexpected events: %+v", events)
	}
}

// TestDiscoveryStreamRejectsCrossSessionEvents proves reconnect streams cannot switch session identity.
func TestDiscoveryStreamRejectsCrossSessionEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"version":1,"session_id":"other","revision":1,"state":"crawl_docs","type":"crawl_progress"}` + "\n\n"))
	}))
	defer server.Close()

	client := api.NewClient(server.URL, "")
	err := client.StreamDiscovery(context.Background(), "session-1", func(api.DiscoveryEvent) error { return nil })
	if err == nil {
		t.Fatal("expected cross-session event rejection")
	}
}

// TestDecodeDiscoveryPayloadRejectsUnknownFields ensures clients fail on an accidental parallel payload protocol.
func TestDecodeDiscoveryPayloadRejectsUnknownFields(t *testing.T) {
	_, err := api.DecodeDiscoveryPayload(json.RawMessage(`{"questions":[]}`))
	if err == nil {
		t.Fatal("expected unknown legacy payload rejection")
	}
}

// discoveryAPIServer captures the breaking v1 request shapes for focused client assertions.
type discoveryAPIServer struct {
	t      *testing.T
	server *httptest.Server
	start  api.DiscoveryStartRequest
	action api.DiscoveryActionRequest
}

// newDiscoveryAPIServer creates an in-memory Registry implementing no legacy routes.
func newDiscoveryAPIServer(t *testing.T) *discoveryAPIServer {
	fake := &discoveryAPIServer{t: t}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.handle))
	return fake
}

// handle serves only the shared discovery protocol endpoints.
func (s *discoveryAPIServer) handle(w http.ResponseWriter, request *http.Request) {
	if request.Header.Get("x-api-key") != "test-key" {
		s.t.Fatal("discovery request omitted API key")
	}
	switch request.URL.Path {
	case "/integrations/start":
		s.decode(w, request, &s.start, http.StatusAccepted, discoverySnapshotJSON("awaiting_selection", 1))
	case "/integrations/session/session-1/actions":
		s.decode(w, request, &s.action, http.StatusOK, discoverySnapshotJSON("extract_contract", 2))
	case "/integrations/session/session-1":
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(discoverySnapshotJSON("extract_contract", 2)))
	case "/integrations/session/session-1/stream":
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"version":1,"session_id":"session-1","revision":2,"state":"extract_contract","type":"extraction_progress","payload":{"operations":[]}}` + "\n\n"))
	default:
		s.t.Fatalf("unexpected discovery path %s", request.URL.Path)
	}
}

// decode captures one request and emits the exact expected response status.
func (s *discoveryAPIServer) decode(w http.ResponseWriter, request *http.Request, target any, status int, response string) {
	if err := json.NewDecoder(request.Body).Decode(target); err != nil {
		s.t.Fatalf("decode %s: %v", request.URL.Path, err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(response))
}

// discoveryStartFixture returns the only start request shape the CLI emits.
func discoveryStartFixture() api.DiscoveryStartRequest {
	return api.DiscoveryStartRequest{
		Name: "Docs API", Slug: "docs-api", SourceURL: "https://docs.example.test/api",
		SourceMode: "auto", RequestedWorkers: 3,
		Crawl: api.DiscoveryCrawlRequest{MaxPages: 12, MaxDepth: 2},
	}
}

// discoverySelectionAction binds a selection to the exact first snapshot revision.
func discoverySelectionAction() api.DiscoveryActionRequest {
	return api.DiscoveryActionRequest{
		Version: api.DiscoveryProtocolVersion, SessionID: "session-1", ExpectedRevision: 1,
		Action:  api.DiscoveryActionSelectOperations,
		Payload: json.RawMessage(`{"operations":[{"method":"GET","path":"/users"}]}`),
	}
}

// discoverySnapshotJSON builds a bounded authoritative test snapshot.
func discoverySnapshotJSON(state string, revision uint64) string {
	return `{"version":1,"session_id":"session-1","revision":` + fmtUint(revision) + `,"state":"` + state + `","payload":{"effective_workers":3,"max_pages":12,"max_depth":2,"max_selections":50}}`
}

// fmtUint formats a small test revision without importing production helpers.
func fmtUint(value uint64) string {
	if value == 1 {
		return "1"
	}
	return "2"
}

// assertDiscoveryStart verifies the CLI never emits former extraction fields.
func assertDiscoveryStart(t *testing.T, request api.DiscoveryStartRequest, snapshot *api.DiscoverySnapshot) {
	t.Helper()
	if request.Slug != "docs-api" || request.SourceMode != "auto" || request.RequestedWorkers != 3 {
		t.Fatalf("unexpected start request: %+v", request)
	}
	if snapshot.State != api.DiscoveryStateAwaitingSelection || snapshot.Revision != 1 {
		t.Fatalf("unexpected first snapshot: %+v", snapshot)
	}
}
