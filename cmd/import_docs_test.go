package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cliapi "github.com/Usefused/cli/internal/api"
)

func TestImportDocsSelectsAllDiscoveredEndpointsByDefault(t *testing.T) {
	dir := t.TempDir()
	fake := newDocsImportServer(t)
	defer fake.close()

	out := runCommandInDirOutput(t, dir, fake.url(), []string{
		"import", "docs", "--url", "https://docs.example.test/api",
		"--name", "Docs API", "--slug", "docs-api", "--version", "1.0",
	})

	assertDocsImportOutput(t, out)
	assertDefaultDocsSelection(t, fake.selected())
	assertDocsWorkspaceAdd(t, fake.workspaceAdd())
}

func TestSelectedDocsEndpointsByFlagRejectsMissingEndpoint(t *testing.T) {
	endpoints := []cliapi.Integration{{Method: "GET", Path: "/users", Name: "listUsers"}}
	_, err := selectedDocsEndpointsByFlag(endpoints, []string{"POST:/users"})
	if err == nil || !strings.Contains(err.Error(), "not discovered") {
		t.Fatalf("expected missing endpoint error, got %v", err)
	}
}

type docsImportServer struct {
	t                 *testing.T
	server            *httptest.Server
	responded         chan struct{}
	selectedEndpoints chan []map[string]string
	workspaceAdded    chan map[string]string
}

func newDocsImportServer(t *testing.T) *docsImportServer {
	fake := &docsImportServer{
		t:                 t,
		responded:         make(chan struct{}, 1),
		selectedEndpoints: make(chan []map[string]string, 1),
		workspaceAdded:    make(chan map[string]string, 1),
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.handle))
	return fake
}

func (s *docsImportServer) url() string {
	return s.server.URL
}

func (s *docsImportServer) close() {
	s.server.Close()
}

func (s *docsImportServer) selected() []map[string]string {
	select {
	case selected := <-s.selectedEndpoints:
		return selected
	default:
		s.t.Fatal("expected respond selection")
		return nil
	}
}

func (s *docsImportServer) workspaceAdd() map[string]string {
	select {
	case added := <-s.workspaceAdded:
		return added
	default:
		s.t.Fatal("expected workspace add")
		return nil
	}
}

func (s *docsImportServer) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/integrations/start":
		s.handleStart(w, r)
	case "/integrations/session/session-1/stream":
		s.handleStream(w)
	case "/integrations/respond":
		s.handleRespond(w, r)
	case "/workspace/services":
		s.handleWorkspaceAdd(w, r)
	default:
		s.t.Fatalf("unexpected path %s", r.URL.Path)
	}
}

func (s *docsImportServer) handleStart(w http.ResponseWriter, r *http.Request) {
	var decoded map[string]string
	if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
		s.t.Fatalf("decode start: %v", err)
	}
	if decoded["import_method"] != "docs" || decoded["source_url"] != "https://docs.example.test/api" {
		s.t.Fatalf("unexpected start body: %#v", decoded)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"session_id":"session-1"}`))
}

func (s *docsImportServer) handleStream(w http.ResponseWriter) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.t.Fatal("expected flusher")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	writeDocsSSETestEvent(s.t, w, flusher, `{"type":"awaiting_input","questions":[{"id":"preview","endpoints":[{"method":"GET","path":"/users","name":"listUsers"},{"method":"POST","path":"/users","name":"createUser"}]}]}`)
	select {
	case <-s.responded:
		s.writeCompletionEvents(w, flusher)
	case <-time.After(2 * time.Second):
		s.t.Fatal("timed out waiting for respond")
	}
}

func (s *docsImportServer) writeCompletionEvents(w http.ResponseWriter, flusher http.Flusher) {
	writeDocsSSETestEvent(s.t, w, flusher, `{"type":"extraction_started","message":"Extracting schemas for 2 selected endpoints..."}`)
	writeDocsSSETestEvent(s.t, w, flusher, `{"type":"extracted","payload":{"method":"GET","path":"/users"}}`)
	writeDocsSSETestEvent(s.t, w, flusher, `{"type":"extracted","payload":{"method":"POST","path":"/users"}}`)
	writeDocsSSETestEvent(s.t, w, flusher, `{"type":"complete","integration_id":"svc-1","version":"1.0"}`)
}

func (s *docsImportServer) handleRespond(w http.ResponseWriter, r *http.Request) {
	s.selectedEndpoints <- decodeDocsRespondSelection(s.t, r)
	s.responded <- struct{}{}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"resumed"}`))
}

func (s *docsImportServer) handleWorkspaceAdd(w http.ResponseWriter, r *http.Request) {
	var decoded map[string]string
	if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
		s.t.Fatalf("decode workspace add: %v", err)
	}
	s.workspaceAdded <- decoded
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}

func assertDocsImportOutput(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, "Imported service svc-1") || !strings.Contains(out, "2 endpoints") {
		t.Fatalf("expected successful docs import output, got %q", out)
	}
}

func assertDefaultDocsSelection(t *testing.T, selected []map[string]string) {
	t.Helper()
	if len(selected) != 2 || selected[0]["method"] != "GET" || selected[1]["method"] != "POST" {
		t.Fatalf("expected all discovered endpoints selected, got %#v", selected)
	}
}

func assertDocsWorkspaceAdd(t *testing.T, added map[string]string) {
	t.Helper()
	if added["service_id"] != "svc-1" || added["version_tag"] != "1.0" {
		t.Fatalf("unexpected workspace add body: %#v", added)
	}
}

func writeDocsSSETestEvent(t *testing.T, w http.ResponseWriter, flusher http.Flusher, raw string) {
	t.Helper()
	if _, err := w.Write([]byte("data: " + raw + "\n\n")); err != nil {
		t.Fatalf("write SSE event: %v", err)
	}
	flusher.Flush()
}

func decodeDocsRespondSelection(t *testing.T, r *http.Request) []map[string]string {
	t.Helper()
	var req struct {
		SessionID string `json:"session_id"`
		Answer    string `json:"answer"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		t.Fatalf("decode respond: %v", err)
	}
	return decodeDocsPreviewSelection(t, req.Answer)
}

func decodeDocsPreviewSelection(t *testing.T, answer string) []map[string]string {
	t.Helper()
	var wrapped map[string]string
	if err := json.Unmarshal([]byte(answer), &wrapped); err != nil {
		t.Fatalf("decode wrapped answer: %v", err)
	}
	var payload struct {
		SelectedItems []map[string]string `json:"selected_items"`
	}
	if err := json.Unmarshal([]byte(wrapped["preview"]), &payload); err != nil {
		t.Fatalf("decode preview answer: %v", err)
	}
	return payload.SelectedItems
}
