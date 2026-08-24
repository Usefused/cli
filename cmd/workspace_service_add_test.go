package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

func resetWorkspaceServiceAddState(t *testing.T) {
	t.Helper()
	oldVersion, oldID, oldInteractive := workspaceServiceAddVersion, workspaceServiceAddID, workspaceServiceAddInteractive
	workspaceServiceAddVersion, workspaceServiceAddID, workspaceServiceAddInteractive = "", "", false
	t.Cleanup(func() {
		workspaceServiceAddVersion, workspaceServiceAddID, workspaceServiceAddInteractive = oldVersion, oldID, oldInteractive
	})
}

func newWorkspaceServiceDiscoveryServer(t *testing.T, workspaceJSON, registryJSON string) (*httptest.Server, *int) {
	t.Helper()
	registryCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode GraphQL request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/engine/graphql":
			if !strings.Contains(request.Query, "workspaceServices") {
				t.Errorf("expected workspaceServices query, got %q", request.Query)
			}
			_, _ = w.Write([]byte(`{"data":{"workspaceServices":` + workspaceJSON + `}}`))
		case "/graphql":
			registryCalls++
			if !strings.Contains(request.Query, "searchServices") {
				t.Errorf("expected searchServices query, got %q", request.Query)
			}
			_, _ = w.Write([]byte(`{"data":{"searchServices":` + registryJSON + `}}`))
		default:
			t.Errorf("unexpected request path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	return server, &registryCalls
}

func TestWorkspaceAddServiceFallsBackToRegistryAndAutoAddsUniqueMatch(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	server, registryCalls := newWorkspaceServiceDiscoveryServer(t, `[]`, `[
		{"id":"00000000-0000-4000-8000-000000000002","name":"Acme Billing","slug":"billing","provider":{"handle":"acme"},"is_owner":false,"is_public":true}
	]`)
	defer server.Close()

	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "add", "billing", "-f", path})
	if *registryCalls != 1 {
		t.Fatalf("Registry search calls = %d, want 1", *registryCalls)
	}
	assertWorkspaceConfigContains(t, path, "@acme/billing:", false)
	if !strings.Contains(out, "planning will resolve its latest public version") {
		t.Fatalf("expected latest-version guidance, got %q", out)
	}
	wantView := server.URL + "/integrations/00000000-0000-4000-8000-000000000002"
	if !strings.Contains(out, "View @acme/billing: "+wantView) {
		t.Fatalf("expected canonical slug and Engine link, got %q", out)
	}
}

func TestWorkspaceAddServiceInteractiveConfirmsRegistryMatch(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	server, _ := newWorkspaceServiceDiscoveryServer(t, `[]`, `[
		{"id":"00000000-0000-4000-8000-000000000003","name":"Drive","slug":"drive","provider":{"handle":"google"},"is_owner":false,"is_public":true}
	]`)
	defer server.Close()

	oldConfirm := confirmWorkspaceRegistryService
	confirmWorkspaceRegistryService = func(service serviceSearchResult) (bool, error) {
		if service.Slug != "@google/drive" {
			t.Fatalf("confirmed service = %#v", service)
		}
		return true, nil
	}
	t.Cleanup(func() { confirmWorkspaceRegistryService = oldConfirm })

	runCommandInDir(t, dir, server.URL, []string{"workspace", "service", "add", "drive", "--interactive", "-f", path})
	assertWorkspaceConfigContains(t, path, "@google/drive:", false)
}

func TestWorkspaceAddServiceInteractiveCancellationDoesNotWrite(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	server, _ := newWorkspaceServiceDiscoveryServer(t, `[]`, `[
		{"id":"00000000-0000-4000-8000-000000000004","name":"Drive","slug":"drive","provider":{"handle":"google"},"is_owner":false,"is_public":true}
	]`)
	defer server.Close()

	oldConfirm := confirmWorkspaceRegistryService
	confirmWorkspaceRegistryService = func(service serviceSearchResult) (bool, error) { return false, nil }
	t.Cleanup(func() { confirmWorkspaceRegistryService = oldConfirm })
	errText := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "add", "drive", "--interactive", "-f", path})
	if !strings.Contains(errText, "service addition cancelled") {
		t.Fatalf("expected cancellation error, got %q", errText)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("cancelled add changed config:\n%s", after)
	}
}

func TestWorkspaceAddServiceNonInteractiveRejectsAmbiguousRegistryQuery(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	server, _ := newWorkspaceServiceDiscoveryServer(t, `[]`, `[
		{"id":"00000000-0000-4000-8000-000000000005","name":"Acme Billing","slug":"billing","provider":{"handle":"acme"},"is_owner":false,"is_public":true},
		{"id":"00000000-0000-4000-8000-000000000006","name":"Other Billing","slug":"billing","provider":{"handle":"other"},"is_owner":false,"is_public":true}
	]`)
	defer server.Close()

	errText := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "add", "bill", "-f", path})
	if !strings.Contains(errText, "matched 2 Registry services") || !strings.Contains(errText, "--interactive") {
		t.Fatalf("expected actionable ambiguity error, got %q", errText)
	}
}

func TestWorkspaceAddServiceStopsOnAmbiguousWorkspaceMatch(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	server, registryCalls := newWorkspaceServiceDiscoveryServer(t, `[
		{"service_id":"00000000-0000-4000-8000-000000000021","service_name":"Acme Billing","service_slug":"@acme/billing"},
		{"service_id":"00000000-0000-4000-8000-000000000022","service_name":"Other Billing","service_slug":"@other/billing"}
	]`, `[]`)
	defer server.Close()

	errText := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "add", "billing", "-f", path})
	if !strings.Contains(errText, "matches multiple workspace services") || !strings.Contains(errText, "--service-id") {
		t.Fatalf("expected actionable workspace ambiguity error, got %q", errText)
	}
	if *registryCalls != 0 {
		t.Fatalf("ambiguous workspace match must not fall through to Registry, got %d calls", *registryCalls)
	}
	assertWorkspaceConfigContainsNoServices(t, path)
}

func TestWorkspaceAddServiceQualifiedQueryIgnoresDifferentWorkspaceProvider(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	server, registryCalls := newWorkspaceServiceDiscoveryServer(t, `[
		{"service_id":"00000000-0000-4000-8000-000000000025","service_name":"Acme Billing","service_slug":"@acme/billing"}
	]`, `[
		{"id":"00000000-0000-4000-8000-000000000026","name":"Other Billing","slug":"billing","provider":{"handle":"other"},"is_owner":false,"is_public":true}
	]`)
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"workspace", "service", "add", "@other/billing", "-f", path})
	if *registryCalls != 1 {
		t.Fatalf("different provider workspace result must fall through to Registry, got %d calls", *registryCalls)
	}
	assertWorkspaceConfigContains(t, path, "@other/billing:", false)
}

func TestWorkspaceAddServiceInteractiveSelectsAmbiguousWorkspaceMatch(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	server, registryCalls := newWorkspaceServiceDiscoveryServer(t, `[
		{"service_id":"00000000-0000-4000-8000-000000000031","service_name":"Acme Billing","service_slug":"@acme/billing"},
		{"service_id":"00000000-0000-4000-8000-000000000032","service_name":"Other Billing","service_slug":"@other/billing"}
	]`, `[]`)
	defer server.Close()

	oldSelect := selectExistingWorkspaceService
	selectExistingWorkspaceService = func(services []api.WorkspaceService) (api.WorkspaceService, error) {
		return services[1], nil
	}
	t.Cleanup(func() { selectExistingWorkspaceService = oldSelect })
	out := runCommandInDirOutput(t, dir, server.URL, []string{"workspace", "service", "add", "billing", "--interactive", "-f", path})
	if *registryCalls != 0 {
		t.Fatalf("selected workspace match must not search Registry, got %d calls", *registryCalls)
	}
	assertWorkspaceConfigContains(t, path, "@other/billing:", false)
	if !strings.Contains(out, "View @other/billing: "+server.URL+"/integrations/00000000-0000-4000-8000-000000000032") {
		t.Fatalf("expected selected workspace service link, got %q", out)
	}
}

func TestChooseRegistryServiceUsesExactQualifiedSlugWithoutPrompt(t *testing.T) {
	results := []serviceSearchResult{
		{Name: "Acme Billing", Slug: "@acme/billing", ServiceID: "service-acme"},
		{Name: "Other Billing", Slug: "@other/billing", ServiceID: "service-other"},
	}
	selected, err := chooseRegistryService("@other/billing", results, false)
	if err != nil {
		t.Fatal(err)
	}
	if selected.ServiceID != "service-other" {
		t.Fatalf("selected %#v, want exact qualified slug match", selected)
	}
}

func TestChooseRegistryServiceAcceptsEveryExactIdentity(t *testing.T) {
	results := []serviceSearchResult{
		{Name: "Acme Billing", Slug: "@acme/billing", ServiceID: "service-acme"},
		{Name: "Other Billing", Slug: "@other/billing", ServiceID: "service-other"},
	}
	for _, query := range []string{"OTHER BILLING", "SERVICE-OTHER"} {
		selected, err := chooseRegistryService(query, results, false)
		if err != nil {
			t.Fatalf("choose exact identity %q: %v", query, err)
		}
		if selected.ServiceID != "service-other" {
			t.Fatalf("query %q selected %#v, want exact identity", query, selected)
		}
	}
}

func TestChooseRegistryServiceInteractiveUsesSharedSelector(t *testing.T) {
	oldSelect := selectWorkspaceRegistryService
	selectWorkspaceRegistryService = func(results []serviceSearchResult) (serviceSearchResult, error) {
		return results[1], nil
	}
	t.Cleanup(func() { selectWorkspaceRegistryService = oldSelect })
	results := []serviceSearchResult{
		{Name: "Acme Billing", Slug: "@acme/billing", ServiceID: "service-acme"},
		{Name: "Other Billing", Slug: "@other/billing", ServiceID: "service-other"},
	}
	selected, err := chooseRegistryService("bill", results, true)
	if err != nil {
		t.Fatal(err)
	}
	if selected.ServiceID != "service-other" {
		t.Fatalf("selected %#v, want interactive choice", selected)
	}
}

func TestWorkspaceAddServiceStopsOnRegistryPermissionFailure(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/engine/graphql" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"workspaceServices":[]}}`))
			return
		}
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
	}))
	defer server.Close()

	errText := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "add", "billing", "-f", path})
	if !strings.Contains(strings.ToLower(errText), "forbidden") && !strings.Contains(errText, "403") {
		t.Fatalf("expected Registry permission failure, got %q", errText)
	}
	assertWorkspaceConfigContainsNoServices(t, path)
}

func TestWorkspaceAddServiceStopsOnWorkspacePermissionFailure(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	registryCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engine/graphql":
			http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		case "/graphql":
			registryCalls++
			http.Error(w, "unexpected Registry fallback", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	errText := runCommandInDirExpectError(t, dir, server.URL, []string{"workspace", "service", "add", "billing", "-f", path})
	if !strings.Contains(strings.ToLower(errText), "forbidden") && !strings.Contains(errText, "403") {
		t.Fatalf("expected workspace permission failure, got %q", errText)
	}
	if registryCalls != 0 {
		t.Fatalf("workspace permission failure must not fall through to Registry, got %d calls", registryCalls)
	}
	assertWorkspaceConfigContainsNoServices(t, path)
}

func TestWorkspaceAddServiceNoInputAutoAddsUniqueRegistryMatch(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	oldNoInput := NoInput
	NoInput = true
	t.Cleanup(func() { NoInput = oldNoInput })
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	server, _ := newWorkspaceServiceDiscoveryServer(t, `[]`, `[
		{"id":"00000000-0000-4000-8000-000000000023","name":"Drive","slug":"drive","provider":{"handle":"google"},"is_owner":false,"is_public":true}
	]`)
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"workspace", "service", "add", "drive", "-f", path})
	assertWorkspaceConfigContains(t, path, "@google/drive:", false)
}

func TestWorkspaceAddServiceExplicitServiceIDSkipsDiscoveryAndPreservesYAML(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	serviceID := "00000000-0000-4000-8000-000000000024"

	out := runCommandInDirOutput(t, dir, "http://127.0.0.1:1", []string{
		"workspace", "service", "add", "private-service", "--service-id", serviceID, "--version", "1.2.3", "-f", path,
	})
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service, ok := parsed.Workspace.Services["private-service"]
	if !ok || service.ServiceID != serviceID || len(service.Versions) != 1 || service.Versions[0].Version != "1.2.3" {
		t.Fatalf("explicit service identity was not preserved: %#v", parsed.Workspace.Services)
	}
	if !strings.Contains(out, "Added service private-service with version 1.2.3") {
		t.Fatalf("unexpected explicit add output %q", out)
	}
	if !strings.Contains(out, "View private-service: http://127.0.0.1:1/integrations/"+serviceID) {
		t.Fatalf("expected explicit service link, got %q", out)
	}
}

func TestWorkspaceServiceViewURLNormalizesBaseAndEscapesID(t *testing.T) {
	got := workspaceServiceViewURL("HTTPS://ENGINE.EXAMPLE/base/?transport=api#fragment", "service/id")
	want := "https://engine.example/base/integrations/service%2Fid"
	if got != want {
		t.Fatalf("workspaceServiceViewURL() = %q, want %q", got, want)
	}
	if got := workspaceServiceViewURL("not-a-url", "service-id"); got != "" {
		t.Fatalf("invalid Engine URL produced link %q", got)
	}
}

func TestWorkspaceAddServiceUUIDReferenceSkipsDiscovery(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	serviceID := "00000000-0000-4000-8000-000000000043"

	runCommandInDir(t, dir, "http://127.0.0.1:1", []string{
		"workspace", "service", "add", serviceID, "-f", path,
	})
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service, ok := parsed.Workspace.Services[serviceID]
	if !ok || service.ServiceID != serviceID {
		t.Fatalf("UUID service reference was not preserved: %#v", parsed.Workspace.Services)
	}
}

func TestWorkspaceAddServicePreservesExistingLocalServiceMetadata(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	serviceID := "00000000-0000-4000-8000-000000000041"
	path := writeSprintConfig(t, dir, "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  billing:
    service_id: "00000000-0000-4000-8000-000000000041"
    public: true
    versions:
      - version: "1.0.0"
`)
	runCommandInDir(t, dir, "http://127.0.0.1:1", []string{
		"workspace", "service", "add", "billing", "--service-id", serviceID, "--version", "2.0.0", "-f", path,
	})
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service := parsed.Workspace.Services["billing"]
	if service.Public == nil || !*service.Public || service.ServiceID != serviceID {
		t.Fatalf("existing service metadata was not preserved: %#v", service)
	}
	if len(service.Versions) != 2 || service.Versions[0].Version != "1.0.0" || service.Versions[1].Version != "2.0.0" {
		t.Fatalf("versions were not merged additively: %#v", service.Versions)
	}
}

func TestWorkspaceAddServiceTrimsWhitespaceVersion(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	out := runCommandInDirOutput(t, dir, "http://127.0.0.1:1", []string{
		"workspace", "service", "add", "billing", "--service-id", "00000000-0000-4000-8000-000000000042", "--version", "   ", "-f", path,
	})
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if versions := parsed.Workspace.Services["billing"].Versions; len(versions) != 0 {
		t.Fatalf("whitespace version was persisted: %#v", versions)
	}
	if !strings.Contains(out, "planning will resolve its latest public version") {
		t.Fatalf("unexpected output %q", out)
	}
}

func TestWorkspaceAddServiceRejectsInvalidExplicitServiceID(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "workspace.yaml", "apiVersion: fused/v1\nkind: workspace\nservices: {}\n")
	errText := runCommandInDirExpectError(t, dir, "http://127.0.0.1:1", []string{
		"workspace", "service", "add", "billing", "--service-id", "not-a-uuid", "-f", path,
	})
	if !strings.Contains(errText, "--service-id must be a valid Registry service UUID") {
		t.Fatalf("expected UUID validation error, got %q", errText)
	}
	assertWorkspaceConfigContainsNoServices(t, path)
}

func TestWorkspaceAddServiceInteractiveIsRejectedInCI(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	t.Setenv("CI", "true")
	errText := runCommandInDirExpectError(t, t.TempDir(), "http://127.0.0.1:1", []string{"workspace", "service", "add", "drive", "--interactive", "-f", "workspace.yaml"})
	if !strings.Contains(errText, "interactive input is disabled") {
		t.Fatalf("expected CI gating error, got %q", errText)
	}
}

func TestWorkspaceAddServiceInteractiveIsRejectedWithNoInput(t *testing.T) {
	resetWorkspaceServiceAddState(t)
	oldNoInput := NoInput
	NoInput = true
	t.Cleanup(func() { NoInput = oldNoInput })
	errText := runCommandInDirExpectError(t, t.TempDir(), "http://127.0.0.1:1", []string{"workspace", "service", "add", "drive", "--interactive", "-f", "workspace.yaml"})
	if !strings.Contains(errText, "interactive input is disabled") {
		t.Fatalf("expected --no-input gating error, got %q", errText)
	}
}

func assertWorkspaceConfigContains(t *testing.T, path, serviceKey string, expectServiceID bool) {
	t.Helper()
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service, ok := parsed.Workspace.Services[strings.TrimSuffix(serviceKey, ":")]
	if !ok {
		t.Fatalf("workspace config does not contain %q", serviceKey)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hasServiceID := service.ServiceID != "" || strings.Contains(string(body), "service_id:")
	if hasServiceID != expectServiceID {
		t.Fatalf("service_id presence = %t, want %t:\n%s", hasServiceID, expectServiceID, body)
	}
}

func assertWorkspaceConfigContainsNoServices(t *testing.T, path string) {
	t.Helper()
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Workspace.Services) != 0 {
		t.Fatalf("workspace config changed unexpectedly: %#v", parsed.Workspace.Services)
	}
}
