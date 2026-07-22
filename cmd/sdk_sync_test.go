package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

func mustMergeSDKServicesFromRemote(t *testing.T, cfg *configfile.SDKConfig, sdkVersion string, remote []sdkSyncRemoteService) sdkSyncResult {
	t.Helper()
	result, err := mergeSDKServicesFromRemote(cfg, sdkVersion, remote, false)
	if err != nil {
		t.Fatalf("mergeSDKServicesFromRemote: %v", err)
	}
	return result
}

// TestMergeSDKServicesFromRemote_AddsNewRemoteService is Task 4c's core AC
// (engine_workspace_registration_plan.md): a service present in the most
// recently generated remote SDK but absent locally gets added, with the
// Registry's resolved data (version tag + enumerated operations) as the
// source of truth.
func TestMergeSDKServicesFromRemote_AddsNewRemoteService(t *testing.T) {
	cfg := &configfile.SDKConfig{Services: map[string]configfile.SDKService{}}
	remote := []sdkSyncRemoteService{
		{Name: "Stripe", Ref: "stripe", Version: "2026-01-01", Operations: []string{"createCharge", "listCharges"}},
	}

	result := mustMergeSDKServicesFromRemote(t, cfg, "1.2.0", remote)

	if !reflect.DeepEqual(result.Added, []string{"stripe"}) {
		t.Errorf("expected Added=[stripe], got %v", result.Added)
	}
	if len(result.Updated) != 0 || len(result.Removed) != 0 {
		t.Errorf("expected no updates/removals, got updated=%v removed=%v", result.Updated, result.Removed)
	}
	got, ok := cfg.Services["stripe"]
	if !ok {
		t.Fatal("expected stripe to be added to cfg.Services")
	}
	if got.Version != "2026-01-01" || !reflect.DeepEqual(got.Operations, []string{"createCharge", "listCharges"}) {
		t.Errorf("unexpected merged entry: %+v", got)
	}
}

func TestMergeSDKServicesFromRemote_UsesSlugRefAsConfigKey(t *testing.T) {
	cfg := &configfile.SDKConfig{Services: map[string]configfile.SDKService{}}
	remote := []sdkSyncRemoteService{
		{Name: "GitHub REST API", Ref: "github-rest-api", Version: "1.1.4", Operations: []string{"apps/delete-installation"}},
		{Name: "Acme CRM", Ref: "@acme/crm", Version: "2026-07-01", Operations: []string{"contacts/list"}},
	}

	result := mustMergeSDKServicesFromRemote(t, cfg, "1.0.0", remote)

	if !reflect.DeepEqual(result.Added, []string{"@acme/crm", "github-rest-api"}) {
		t.Fatalf("expected slug refs in Added, got %v", result.Added)
	}
	if _, ok := cfg.Services["GitHub REST API"]; ok {
		t.Fatal("display name should not be used as an sdk config key")
	}
	if _, ok := cfg.Services["github-rest-api"]; !ok {
		t.Fatalf("expected owned service keyed by slug, got %+v", cfg.Services)
	}
	if _, ok := cfg.Services["@acme/crm"]; !ok {
		t.Fatalf("expected foreign service keyed by provider-qualified slug, got %+v", cfg.Services)
	}
}

func TestMergeSDKServicesFromRemote_RejectsMissingSlugRef(t *testing.T) {
	cfg := &configfile.SDKConfig{Services: map[string]configfile.SDKService{}}
	remote := []sdkSyncRemoteService{{Name: "GitHub REST API", Version: "1.1.4"}}

	_, err := mergeSDKServicesFromRemote(cfg, "1.0.0", remote, false)

	if err == nil || !strings.Contains(err.Error(), "missing service slug") {
		t.Fatalf("expected missing slug error, got %v", err)
	}
}

// TestMergeSDKServicesFromRemote_RemoteWinsOnConflict mirrors workspace
// sync's "remote wins" semantics: a local entry with a stale version or
// operation list is fully overwritten, not merged field-by-field.
func TestMergeSDKServicesFromRemote_RemoteWinsOnConflict(t *testing.T) {
	cfg := &configfile.SDKConfig{Services: map[string]configfile.SDKService{
		"stripe": {Version: "2025-01-01", Operations: []string{"createCharge"}},
	}}
	remote := []sdkSyncRemoteService{
		{Name: "Stripe", Ref: "stripe", Version: "2026-01-01", Operations: []string{"createCharge", "listCharges"}},
	}

	result := mustMergeSDKServicesFromRemote(t, cfg, "1.2.0", remote)

	if !reflect.DeepEqual(result.Updated, []string{"stripe"}) {
		t.Errorf("expected Updated=[stripe], got %v", result.Updated)
	}
	got := cfg.Services["stripe"]
	if got.Version != "2026-01-01" || !reflect.DeepEqual(got.Operations, []string{"createCharge", "listCharges"}) {
		t.Errorf("expected remote values to win, got %+v", got)
	}
}

// TestMergeSDKServicesFromRemote_UnchangedServiceNotReportedAsUpdated guards
// against noisy output on a no-op re-sync.
func TestMergeSDKServicesFromRemote_UnchangedServiceNotReportedAsUpdated(t *testing.T) {
	cfg := &configfile.SDKConfig{Services: map[string]configfile.SDKService{
		"stripe": {Version: "2026-01-01", Operations: []string{"createCharge", "listCharges"}},
	}}
	remote := []sdkSyncRemoteService{
		{Name: "Stripe", Ref: "stripe", Version: "2026-01-01", Operations: []string{"createCharge", "listCharges"}},
	}

	result := mustMergeSDKServicesFromRemote(t, cfg, "1.2.0", remote)

	if len(result.Added) != 0 || len(result.Updated) != 0 || len(result.Removed) != 0 {
		t.Errorf("expected no changes reported for an already-in-sync service, got %+v", result)
	}
}

// TestMergeSDKServicesFromRemote_UnchangedIgnoresOperationOrder confirms
// Operations is compared as a set, not an ordered list -- sdkSelectionResources
// gives no ordering guarantee.
func TestMergeSDKServicesFromRemote_UnchangedIgnoresOperationOrder(t *testing.T) {
	cfg := &configfile.SDKConfig{Services: map[string]configfile.SDKService{
		"stripe": {Version: "2026-01-01", Operations: []string{"createCharge", "listCharges"}},
	}}
	remote := []sdkSyncRemoteService{
		{Name: "Stripe", Ref: "stripe", Version: "2026-01-01", Operations: []string{"listCharges", "createCharge"}},
	}

	result := mustMergeSDKServicesFromRemote(t, cfg, "1.2.0", remote)

	if len(result.Updated) != 0 {
		t.Errorf("expected operation-order difference alone not to count as a change, got Updated=%v", result.Updated)
	}
	// Result should still be sorted deterministically for yaml stability.
	got := cfg.Services["stripe"]
	if !reflect.DeepEqual(got.Operations, []string{"createCharge", "listCharges"}) {
		t.Errorf("expected sorted operations, got %v", got.Operations)
	}
}

// TestMergeSDKServicesFromRemote_RemovesLocalEntryNoLongerInRemote is the
// removal AC: a locally-configured service the remote SDK's selections no
// longer include must be dropped, not just flagged.
func TestMergeSDKServicesFromRemote_RemovesLocalEntryNoLongerInRemote(t *testing.T) {
	cfg := &configfile.SDKConfig{Services: map[string]configfile.SDKService{
		"stripe": {Version: "2026-01-01", Operations: []string{"createCharge"}},
		"stale":  {Version: "1.0.0", Operations: []string{"oldOp"}},
	}}
	remote := []sdkSyncRemoteService{
		{Name: "Stripe", Ref: "stripe", Version: "2026-01-01", Operations: []string{"createCharge"}},
	}

	result := mustMergeSDKServicesFromRemote(t, cfg, "1.2.0", remote)

	if !reflect.DeepEqual(result.Removed, []string{"stale"}) {
		t.Errorf("expected Removed=[stale], got %v", result.Removed)
	}
	if _, ok := cfg.Services["stale"]; ok {
		t.Error("expected stale service to be removed")
	}
	if _, ok := cfg.Services["stripe"]; !ok {
		t.Error("expected stripe to remain")
	}
}

// TestMergeSDKServicesFromRemote_EmptyRemoteRemovesEverything covers the
// edge case of an SDK whose latest generation has no selections left.
func TestMergeSDKServicesFromRemote_EmptyRemoteRemovesEverything(t *testing.T) {
	cfg := &configfile.SDKConfig{Services: map[string]configfile.SDKService{
		"stripe": {Version: "2026-01-01", Operations: []string{"createCharge"}},
	}}

	result := mustMergeSDKServicesFromRemote(t, cfg, "1.2.0", nil)

	if !reflect.DeepEqual(result.Removed, []string{"stripe"}) {
		t.Errorf("expected Removed=[stripe], got %v", result.Removed)
	}
	if len(cfg.Services) != 0 {
		t.Errorf("expected an empty services map, got %+v", cfg.Services)
	}
}

// TestMergeSDKServicesFromRemote_NilServicesMapInitialized guards a
// freshly-parsed config whose Services map might be nil.
func TestMergeSDKServicesFromRemote_NilServicesMapInitialized(t *testing.T) {
	cfg := &configfile.SDKConfig{}
	remote := []sdkSyncRemoteService{
		{Name: "Stripe", Ref: "stripe", Version: "2026-01-01", Operations: []string{"createCharge"}},
	}

	mustMergeSDKServicesFromRemote(t, cfg, "1.2.0", remote)

	if cfg.Services == nil {
		t.Fatal("expected Services map to be initialized")
	}
	if _, ok := cfg.Services["stripe"]; !ok {
		t.Error("expected stripe to be added")
	}
}

func TestMergeSDKServicesFromRemote_PreservesArtifactVersionByDefault(t *testing.T) {
	cfg := &configfile.SDKConfig{Version: "1.1.0", Services: map[string]configfile.SDKService{}}

	result := mustMergeSDKServicesFromRemote(t, cfg, "1.2.0", nil)

	if cfg.Version != "1.1.0" {
		t.Errorf("expected cfg.Version to stay 1.1.0, got %s", cfg.Version)
	}
	if result.RemoteVersion != "1.2.0" || result.ArtifactVersionTo != "1.1.0" {
		t.Errorf("expected remote version to be reported without changing local version, got %+v", result)
	}
}

func TestMergeSDKServicesFromRemote_SyncVersionBumpsArtifactVersion(t *testing.T) {
	cfg := &configfile.SDKConfig{Version: "1.1.0", Services: map[string]configfile.SDKService{}}

	result, err := mergeSDKServicesFromRemote(cfg, "1.2.0", nil, true)
	if err != nil {
		t.Fatalf("mergeSDKServicesFromRemote: %v", err)
	}

	if cfg.Version != "1.2.0" {
		t.Errorf("expected cfg.Version to be bumped to 1.2.0, got %s", cfg.Version)
	}
	if result.ArtifactVersionFrom != "1.1.0" || result.ArtifactVersionTo != "1.2.0" {
		t.Errorf("expected artifact version change to be recorded, got %+v", result)
	}
}

func TestSDKServiceEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b configfile.SDKService
		want bool
	}{
		{"identical", configfile.SDKService{Version: "v1", Operations: []string{"a", "b"}}, configfile.SDKService{Version: "v1", Operations: []string{"a", "b"}}, true},
		{"different order", configfile.SDKService{Version: "v1", Operations: []string{"a", "b"}}, configfile.SDKService{Version: "v1", Operations: []string{"b", "a"}}, true},
		{"different version", configfile.SDKService{Version: "v1", Operations: []string{"a"}}, configfile.SDKService{Version: "v2", Operations: []string{"a"}}, false},
		{"different operations", configfile.SDKService{Version: "v1", Operations: []string{"a"}}, configfile.SDKService{Version: "v1", Operations: []string{"b"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sdkServiceEqual(tt.a, tt.b); got != tt.want {
				t.Errorf("sdkServiceEqual(%+v, %+v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestResolveServiceVersionName_ReturnsPersistedName(t *testing.T) {
	got, err := resolveServiceVersionName(api.SDKSelectionDetail{
		ServiceVersionID:   "v-1",
		ServiceVersionName: "2025-01-01",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2025-01-01" {
		t.Errorf("expected 2025-01-01, got %s", got)
	}
}

func TestResolveServiceVersionName_MissingIDReturnsError(t *testing.T) {
	_, err := resolveServiceVersionName(api.SDKSelectionDetail{
		ServiceVersionName: "2025-01-01",
	})
	if err == nil {
		t.Fatal("expected an error for a selection without service_version_id")
	}
}

func TestResolveServiceVersionName_MissingNameReturnsError(t *testing.T) {
	_, err := resolveServiceVersionName(api.SDKSelectionDetail{
		ServiceVersionID: "v-1",
	})
	if err == nil {
		t.Fatal("expected an error for a selection without service_version_name")
	}
}

func TestValidateSDKDownloadArgs_RejectsMalformedVersionSuffix(t *testing.T) {
	if err := validateSDKDownloadArgs([]string{"billing@"}); err == nil {
		t.Fatal("expected empty version suffix to be rejected")
	}
	if err := validateSDKDownloadArgs([]string{"@1.2.3"}); err == nil {
		t.Fatal("expected empty sdk name to be rejected")
	}
}

func TestResolveSDKDownloadConfigKeys_UsesExplicitName(t *testing.T) {
	targets, err := resolveSDKDownloadTargets([]string{"security-sdk"}, "")
	if err != nil {
		t.Fatalf("resolveSDKDownloadTargets failed: %v", err)
	}
	if len(targets) != 1 || targets[0].Name != "security-sdk" || targets[0].Version != "" {
		t.Fatalf("unexpected download targets: %+v", targets)
	}
}

func TestResolveSDKDownloadTargets_UsesVersionSuffix(t *testing.T) {
	targets, err := resolveSDKDownloadTargets([]string{"security-sdk@1.2.0"}, "")
	if err != nil {
		t.Fatalf("resolveSDKDownloadTargets failed: %v", err)
	}
	if len(targets) != 1 || targets[0].Name != "security-sdk" || targets[0].Version != "1.2.0" {
		t.Fatalf("unexpected download targets: %+v", targets)
	}
}

func TestResolveSDKDownloadConfigKeys_UsesSingleConfigFile(t *testing.T) {
	path := writeSprintConfig(t, t.TempDir(), "security.yaml", `
apiVersion: fused/v1
kind: sdk
name: security-sdk
version: "1.0.0"
language: typescript
services:
  github:
    operations: ["repos_list_for_authenticated_user"]
`)

	targets, err := resolveSDKDownloadTargets(nil, path)
	if err != nil {
		t.Fatalf("resolveSDKDownloadTargets failed: %v", err)
	}
	if len(targets) != 1 || targets[0].Name != "security-sdk" || targets[0].Version != "1.0.0" {
		t.Fatalf("unexpected download targets: %+v", targets)
	}
}

func TestResolveSDKDownloadConfigKeys_RejectsWorkspaceConfigFile(t *testing.T) {
	path := writeSprintConfig(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  github:
    versions: ["1.1.4"]
`)

	if _, err := resolveSDKDownloadTargets(nil, path); err == nil {
		t.Fatal("expected workspace-only config to be rejected")
	}
}

func TestSDKNameDownloadRoutesThroughEngineToRegistrySDKRecord(t *testing.T) {
	dir := t.TempDir()
	var sawGraphQL, sawDownload bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/graphql":
			sawGraphQL = true
			var body struct {
				Variables map[string]any `json:"variables"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode graphql request: %v", err)
			}
			if body.Variables["name"] != "security-sdk" || body.Variables["version"] != "1.2.0" {
				t.Fatalf("unexpected sdkByName variables: %+v", body.Variables)
			}
			_, _ = w.Write([]byte(`{"data":{"sdkByName":{"id":"sdk-record-123","sandbox_url":""}}}`))
		case "/sdks/sdk-record-123/download":
			sawDownload = true
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write([]byte("registry-zip"))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"sdk", "security-sdk@1.2.0", "download"})

	if !sawGraphQL || !sawDownload {
		t.Fatalf("expected graphql and download requests, saw graphql=%v download=%v", sawGraphQL, sawDownload)
	}
	content, err := os.ReadFile(filepath.Join(dir, "security-sdk.zip"))
	if err != nil {
		t.Fatalf("read downloaded sdk: %v", err)
	}
	if string(content) != "registry-zip" {
		t.Fatalf("unexpected downloaded content: %q", string(content))
	}
}

func TestSDKSyncCreatesDefaultFusedConfig(t *testing.T) {
	sdkSyncVersion = false
	dir := t.TempDir()
	server := newSDKSyncServer(t, "1.2.0")
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"sdk", "sync", "security-sdk"})

	data, err := os.ReadFile(filepath.Join(dir, ".fused", "sdks", "security-sdk.yaml"))
	if err != nil {
		t.Fatalf("expected default sdk config to be created: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "version: 1.0.0") || !strings.Contains(text, "kind: sdk") || !strings.Contains(text, "name: security-sdk") || !strings.Contains(text, "github-rest-api:") || !strings.Contains(text, "repos_list_for_authenticated_user") {
		t.Fatalf("unexpected sdk sync file:\n%s", text)
	}
}

func TestSDKSyncVersionFlagUpdatesArtifactVersion(t *testing.T) {
	sdkSyncVersion = false
	t.Cleanup(func() { sdkSyncVersion = false })
	dir := t.TempDir()
	server := newSDKSyncServer(t, "1.2.0")
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"sdk", "sync", "security-sdk", "--sync-version"})

	data, err := os.ReadFile(filepath.Join(dir, ".fused", "sdks", "security-sdk.yaml"))
	if err != nil {
		t.Fatalf("expected default sdk config to be created: %v", err)
	}
	if text := string(data); !strings.Contains(text, "version: 1.2.0") {
		t.Fatalf("expected --sync-version to update artifact version:\n%s", text)
	}
}

func newSDKSyncServer(t *testing.T, sdkVersion string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleSDKSyncGraphQL(t, w, r, sdkVersion)
	}))
}

func handleSDKSyncGraphQL(t *testing.T, w http.ResponseWriter, r *http.Request, sdkVersion string) {
	t.Helper()
	if r.URL.Path != "/graphql" {
		t.Fatalf("unexpected path %s", r.URL.Path)
	}
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode graphql request: %v", err)
	}
	switch {
	case strings.Contains(body.Query, "sdkByName"):
		_, _ = w.Write([]byte(`{"data":{"sdkByName":{"id":"sdk-record-123","version":"` + sdkVersion + `","detailed_selections":[{"service_id":"svc-github","service_name":"GitHub REST API","service_slug":"github-rest-api","service_provider":null,"endpoint_ids":["ep-1"],"webhook_ids":[],"select_all":false,"service_version_id":"sv-1","service_version_name":"1.1.4"}]}}}`))
	case strings.Contains(body.Query, "sdkSelectionResources"):
		_, _ = w.Write([]byte(`{"data":{"sdkSelectionResources":[{"name":"repos_list_for_authenticated_user"}]}}`))
	default:
		t.Fatalf("unexpected graphql query %s", body.Query)
	}
}
