package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestWorkspaceAddServiceSuggestions exercises real command discovery without
// allowing a helpful typo hint to author config or activate a nearby service.
func TestWorkspaceAddServiceSuggestions(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		initialStatus    int
		suggestionStatus int
		candidates       []api.Service
		want             string
		wantReads        int
	}{
		{"close match", 200, 200, []api.Service{suggestionService("Google Sheets", "google-sheets"), suggestionService("Google Drive", "google-drive")}, `Did you mean "google-sheets"?`, 2},
		{"unrelated results", 200, 200, []api.Service{suggestionService("Google Drive", "google-drive")}, `service "google-sheet" was not found in the workspace or Registry`, 2},
		{"no candidates", 200, 200, nil, `service "google-sheet" was not found in the workspace or Registry`, 2},
		{"hint permission failure", 200, 403, nil, `service "google-sheet" was not found in the workspace or Registry`, 2},
		{"hint transport failure", 200, 502, nil, `service "google-sheet" was not found in the workspace or Registry`, 2},
		{"original permission failure", 403, 200, nil, "403", 1},
	} {
		// Each command owns isolated globals and a fixture that rejects mutations.
		t.Run(testCase.name, func(t *testing.T) {
			resetWorkspaceServiceAddState(t)
			useNonInteractiveWorkspaceAdd(t)
			dir := t.TempDir()
			before := "apiVersion: fused/v1\nkind: workspace\nservices: {}\n"
			path := writeSprintConfig(t, dir, "workspace.yaml", before)
			registryReads := 0
			// The server distinguishes the original exact miss from optional lexical hints.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Any activation or unexpected endpoint is a regression in the read-only fallback.
				switch r.URL.Path {
				case "/health":
					_, _ = w.Write([]byte(`{"environment":"development"}`))
				case "/engine/graphql":
					_, _ = w.Write([]byte(`{"data":{"workspaceServicePage":{"data":[],"total":0}}}`))
				case "/graphql":
					registryReads++
					var request struct {
						Variables struct {
							Refs        []string `json:"refs"`
							LimitPerRef int      `json:"limitPerRef"`
						} `json:"variables"`
					}
					// Decoding guards against accidentally exercising a different search API.
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Errorf("decode Registry query: %v", err)
						http.Error(w, "bad fixture request", 400)
						return
					}
					status := testCase.suggestionStatus
					var candidates []api.Service
					// Only the secondary query may return typo candidates.
					if registryReads == 1 {
						status = testCase.initialStatus
						// Exact discovery must still receive the user's unchanged reference.
						if !reflect.DeepEqual(request.Variables.Refs, []string{"google-sheet"}) {
							t.Errorf("initial refs = %v", request.Variables.Refs)
						}
					} else {
						candidates = testCase.candidates
						// Bounded lexical probes must include the phrase that matches Google Sheets.
						if !reflect.DeepEqual(request.Variables.Refs, []string{"google sheet", "goo", "she"}) || request.Variables.LimitPerRef != 20 {
							t.Errorf("unexpected hint request: %+v", request.Variables)
						}
					}
					// Optional lookup failures should not replace the original miss diagnostic.
					if status != http.StatusOK {
						http.Error(w, "lookup failed", status)
						return
					}
					results := make([]map[string]any, 0, len(request.Variables.Refs))
					for _, ref := range request.Variables.Refs {
						results = append(results, map[string]any{"ref": ref, "candidates": candidates})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"serviceCandidatesByRefs": results}})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			errText := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "add", "google-sheet", "--apply", "--no-input", "-f", path})
			// Error text and request counts prove hints are best effort and miss-only.
			if !strings.Contains(errText, testCase.want) || registryReads != testCase.wantReads {
				t.Fatalf("error = %q, Registry reads = %d; want %q, %d", errText, registryReads, testCase.want, testCase.wantReads)
			}
			// Failed or unrelated hint searches must not invent a plausible service.
			if !strings.Contains(testCase.want, "Did you mean") && strings.Contains(errText, "Did you mean") {
				t.Fatalf("unexpected suggestion: %s", errText)
			}
			after, err := os.ReadFile(path)
			// Even --apply must leave local intent intact when resolution fails.
			if err != nil || string(after) != before {
				t.Fatalf("config changed after typo: %q, %v", after, err)
			}
		})
	}
}

// suggestionService supplies real-shaped Registry identities for spelling tests.
func suggestionService(name, slug string) api.Service {
	return api.Service{ID: "id-" + slug, Name: name, Slug: slug, IsOwner: true}
}

// TestRankServiceSuggestions covers spelling, identity scope, deterministic
// limits, and unrelated or malformed results independently of network discovery.
func TestRankServiceSuggestions(t *testing.T) {
	sheets := suggestionService("Google Sheets", "google-sheets")
	stripe := suggestionService("Stripe", "stripe")
	qualified := sheets
	qualified.Provider = &api.ServiceProviderIdentity{Handle: "google"}
	for _, testCase := range []struct {
		name       string
		query      string
		candidates []api.Service
		want       []string
	}{
		{"singular", "google-sheet", []api.Service{sheets}, []string{"google-sheets"}},
		{"case and separators", "GOOGLE_SHEETS", []api.Service{sheets}, []string{"google-sheets"}},
		{"transposition", "stirpe", []api.Service{stripe}, []string{"stripe"}},
		{"display name", "google-sheet", []api.Service{suggestionService("Google Sheets", "sheets-v4")}, []string{"sheets-v4"}},
		{"unicode", "cafe", []api.Service{suggestionService("Café", "café")}, []string{"café"}},
		{"unrelated", "google-sheet", []api.Service{suggestionService("Google Drive", "google-drive"), stripe}, nil},
		{"deduplicated", "google-sheet", []api.Service{sheets, sheets}, []string{"google-sheets"}},
		{"same provider", "@google/google-sheet", []api.Service{qualified, sheets}, []string{"@google/google-sheets"}},
		{"wrong provider", "@other/google-sheet", []api.Service{qualified}, nil},
		{"missing identity", "google-sheet", []api.Service{{Name: "Google Sheets", Slug: "google-sheets"}}, nil},
		{"unsafe identity", "google-sheet", []api.Service{suggestionService("Google Sheets", "google-sheets\n")}, nil},
		{"short input", "go", []api.Service{sheets}, nil},
		{"oversized input", strings.Repeat("x", 129), []api.Service{stripe}, nil},
		{"deterministic limit", "stripe", []api.Service{suggestionService("stripec", "stripec"), suggestionService("stripeb", "stripeb"), suggestionService("stripea", "stripea"), stripe}, []string{"stripe", "stripea", "stripeb"}},
	} {
		// Table cases assert observable hints rather than the scoring implementation.
		t.Run(testCase.name, func(t *testing.T) {
			got := rankServiceSuggestions(testCase.query, testCase.candidates)
			// Exact reference comparisons catch lost qualifiers and unstable ordering.
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("suggestions = %v, want %v", got, testCase.want)
			}
		})
	}
}

// TestServiceSuggestionsPreserveOtherErrors proves optional discovery never
// handles permission, ambiguity, or cancellation errors as catalogue misses.
func TestServiceSuggestionsPreserveOtherErrors(t *testing.T) {
	original := errors.New("permission denied")
	// A nil client would panic if an unrelated error triggered any lookup.
	if got := withServiceSuggestions(nil, original); got != original {
		t.Fatalf("error replaced: %v", got)
	}
}

// TestServiceSuggestionQueriesBounds prevents tiny or oversized inputs from
// broadening discovery and checks spelling probes for a single-word service.
func TestServiceSuggestionQueriesBounds(t *testing.T) {
	for _, query := range []string{"", "a", "--", strings.Repeat("a", 129)} {
		// Ambiguous or oversized input must not issue a fallback request.
		if got := serviceSuggestionQueries(query); len(got) != 0 {
			t.Fatalf("queries for %q = %v", query, got)
		}
	}
	// A single-word typo can be found through either its prefix or suffix.
	if got := serviceSuggestionQueries("strpie"); !reflect.DeepEqual(got, []string{"strpie", "str", "pie"}) {
		t.Fatalf("single-word queries = %v", got)
	}
}
