package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestWorkspaceServicePagesPreserveCompleteMembership verifies a catalogue above the server batch bound retains every version and credential scope.
func TestWorkspaceServicePagesPreserveCompleteMembership(t *testing.T) {
	offsets := []int{}
	// This server rejects transport or pagination changes that would bypass Engine authorization or lose the filter.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string
			Variables struct {
				Names         []string
				Limit, Offset int
			}
		}
		// Requests must be readable before their routing invariants can be checked.
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		// Every page must use the same authenticated Engine read and name filter.
		if r.URL.Path != "/engine/graphql" || r.Header.Get("x-api-key") != "test-key" || !strings.Contains(body.Query, "workspaceServicePage") || body.Variables.Limit != 100 || !reflect.DeepEqual(body.Variables.Names, []string{"Example Service"}) {
			t.Fatalf("unexpected request: %+v", body)
		}
		offset := body.Variables.Offset
		offsets = append(offsets, offset)
		services := []api.WorkspaceService{}
		// Return a partial final page to exercise exact total-based completion.
		for i := offset; i < min(offset+100, 205); i++ {
			services = append(services, api.WorkspaceService{ServiceID: fmt.Sprintf("service-%d", i), ServiceSlug: fmt.Sprintf("owner/service-%d", i), EnabledVersions: []api.WorkspaceServiceVersion{{ServiceVersionID: fmt.Sprintf("version-%d", i), Version: "1.0.0"}}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"workspaceServicePage": map[string]any{"data": services, "total": 205}}})
	}))
	defer server.Close()
	services, err := api.NewClient(server.URL, "test-key").ListWorkspaceServices("Example Service")
	// No service or pinned version may disappear when the catalogue crosses a page boundary.
	if err != nil || len(services) != 205 || !reflect.DeepEqual(offsets, []int{0, 100, 200}) {
		t.Fatalf("services=%d offsets=%v error=%v", len(services), offsets, err)
	}
	// Verify both the boundary and final result retain provider-qualified identity and immutable version metadata.
	for _, i := range []int{99, 100, 204} {
		// Membership identity and pinned version must stay paired across page boundaries.
		if services[i].ServiceSlug != fmt.Sprintf("owner/service-%d", i) || services[i].EnabledVersions[0].ServiceVersionID != fmt.Sprintf("version-%d", i) {
			t.Fatalf("lost membership metadata at %d: %+v", i, services[i])
		}
	}
}

// TestWorkspaceServicePagesRejectIncompleteSnapshots protects sync from partial results and pagination loops after a successful first page.
func TestWorkspaceServicePagesRejectIncompleteSnapshots(t *testing.T) {
	cases := map[string]string{
		"duplicate membership":  `{"data":{"workspaceServicePage":{"data":[{"service_id":"service-0"}],"total":101}}}`,
		"later GraphQL failure": `{"data":{"workspaceServicePage":{"data":[],"total":101}},"errors":[{"message":"denied"}]}`,
		"missing page":          `{"data":{}}`,
		"null page":             `{"data":{"workspaceServicePage":null}}`,
		"missing total":         `{"data":{"workspaceServicePage":{"data":[]}}}`,
		"missing data":          `{"data":{"workspaceServicePage":{"total":101}}}`,
		"empty later page":      `{"data":{"workspaceServicePage":{"data":[],"total":101}}}`,
		"changed total":         `{"data":{"workspaceServicePage":{"data":[],"total":100}}}`,
	}
	// Each malformed continuation must stop without returning the already fetched memberships.
	for name, response := range cases {
		// Isolate response state so each failure demonstrates bounded request count independently.
		t.Run(name, func(t *testing.T) {
			calls := 0
			// A full first page proves failures are handled atomically after useful data has arrived.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				// Fail the second request deterministically; no third request should be attempted.
				if calls > 1 {
					_, _ = w.Write([]byte(response))
					return
				}
				services := make([]api.WorkspaceService, 100)
				// Unique first-page identities make duplicate continuations distinguishable from valid progress.
				for i := range services {
					services[i].ServiceID = fmt.Sprintf("service-%d", i)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"workspaceServicePage": map[string]any{"data": services, "total": 101}}})
			}))
			defer server.Close()
			services, err := api.NewClient(server.URL, "test-key").ListWorkspaceServices()
			// Partial data is unsafe for workspace sync, which could otherwise infer removals.
			if err == nil || services != nil || calls != 2 {
				t.Fatalf("services=%v error=%v calls=%d", services, err, calls)
			}
		})
	}
}
