package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	cliapi "github.com/Usefused/cli/internal/api"
)

// TestWorkspaceServicesListQueryFiltersVisibleServices verifies workspace behavior using the bounded Engine service page contract.
func TestWorkspaceServicesListQueryFiltersVisibleServices(t *testing.T) {
	previousQuery := workspaceServicesListQuery
	previousInteractive := workspaceServicesListInteractive
	t.Cleanup(func() {
		workspaceServicesListQuery = previousQuery
		workspaceServicesListInteractive = previousInteractive
	})
	workspaceServicesListQuery = ""
	workspaceServicesListInteractive = false

	// Serve the bounded membership response while preserving this fixture's command-specific checks.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/engine/graphql" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		body := decodeTestGraphQLBody(t, r)
		// Match the bounded membership read used by catalogue and sync commands.
		if !strings.Contains(body.Query, "workspaceServicePage") {
			t.Fatalf("unexpected query: %s", body.Query)
		}
		_, _ = w.Write([]byte(`{"data":{"workspaceServicePage":{"data":[{"service_id":"svc-github","service_name":"GitHub REST API","service_slug":"github","version":"v1"},{"service_id":"svc-stripe","service_name":"Stripe Billing","service_slug":"@stripe/payments","version":"v2"}],"total":2}}}`))
	}))
	defer server.Close()

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"workspace", "services", "list", "--q", "stripe payments"})
	if !strings.Contains(out, "Stripe Billing") || !strings.Contains(out, "@stripe/payments") {
		t.Fatalf("expected matching workspace service, got %q", out)
	}
	if strings.Contains(out, "GitHub REST API") {
		t.Fatalf("expected non-matching service to be filtered, got %q", out)
	}

	noMatch := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"workspace", "services", "list", "--q", "missing"})
	if !strings.Contains(noMatch, `No visible workspace services found matching "missing".`) {
		t.Fatalf("expected access-aware no-match output, got %q", noMatch)
	}
}

func TestFilterWorkspaceServicesIsCaseInsensitiveAndRequiresEveryTerm(t *testing.T) {
	services := []cliapi.WorkspaceService{
		{ServiceName: "GitHub REST API", ServiceSlug: "@github/cloud"},
		{ServiceName: "GitLab API", ServiceSlug: "gitlab"},
	}

	got := filterWorkspaceServices(services, "GITHUB cloud")
	if len(got) != 1 || got[0].ServiceSlug != "@github/cloud" {
		t.Fatalf("unexpected filtered services: %#v", got)
	}
	if got := filterWorkspaceServices(services, "github billing"); len(got) != 0 {
		t.Fatalf("expected every query term to match, got %#v", got)
	}
}
