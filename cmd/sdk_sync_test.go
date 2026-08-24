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
	if cfg.Version == "" {
		cfg.Version = sdkVersion
	}
	result, err := mergeSDKServicesFromRemote(cfg, sdkVersion, remote)
	if err != nil {
		t.Fatalf("mergeSDKServicesFromRemote: %v", err)
	}
	return result
}

// TestMergeSDKServicesFromRemote_AddsNewRemoteService is Task 4c's core AC
// (engine_workspace_registration_plan.md): a service present in the most
// recently generated remote SDK but absent locally gets added, with the
// Engine's resolved data (version tag + enumerated operations) as the
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
	cfg := &configfile.SDKConfig{Version: "1.0.0", Services: map[string]configfile.SDKService{}}
	remote := []sdkSyncRemoteService{{Name: "GitHub REST API", Version: "1.1.4"}}

	_, err := mergeSDKServicesFromRemote(cfg, "1.0.0", remote)

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

func TestMergeSDKServicesFromRemote_RestoresPortableMetadata(t *testing.T) {
	cfg := &configfile.SDKConfig{Services: map[string]configfile.SDKService{
		"jira": {Version: "1.0.0", Auth: &configfile.AppAuth{Type: "bearer"}},
	}}
	remote := []sdkSyncRemoteService{{
		Name: "Jira", Ref: "jira", Version: "1.0.0", Operations: []string{"listIssues"},
		Auth:       &configfile.AppAuth{Type: "oauth", Name: "jiraOAuth"},
		Connect:    &configfile.AppConnect{Scopes: []string{"write:jira-work", "read:jira-work"}},
		Injections: []configfile.InjectionConfig{{Location: "header", Name: "X-Tenant", Value: "$connection.tenant", Mode: "replace"}},
	}}

	result := mustMergeSDKServicesFromRemote(t, cfg, "1.0.0", remote)
	service := cfg.Services["jira"]
	if !reflect.DeepEqual(result.Updated, []string{"jira"}) || service.Auth == nil || service.Auth.Type != "oauth" || service.Auth.Name != "jiraOAuth" {
		t.Fatalf("remote auth did not win: result=%+v service=%+v", result, service)
	}
	if service.Connect == nil || !reflect.DeepEqual(service.Connect.Scopes, []string{"read:jira-work", "write:jira-work"}) {
		t.Fatalf("remote connect scopes were not restored: %+v", service.Connect)
	}
	if len(service.Injections) != 1 || service.Injections[0].Mode != "replace" {
		t.Fatalf("remote injections were not restored: %+v", service.Injections)
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
// Operations is compared as a set because Engine selection persistence gives
// no ordering guarantee.
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
// edge case of an exact SDK app version with no selections left.
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

func TestMergeSDKServicesFromRemote_RejectsDifferentAppVersion(t *testing.T) {
	cfg := &configfile.SDKConfig{Version: "1.1.0", Services: map[string]configfile.SDKService{}}

	_, err := mergeSDKServicesFromRemote(cfg, "1.2.0", nil)
	if err == nil || !strings.Contains(err.Error(), "does not match local config version") {
		t.Fatalf("expected exact app version mismatch, got %v", err)
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

func TestValidateSDKSyncDefinitionRequiresExactCurrentSchema(t *testing.T) {
	tests := []struct {
		name       string
		version    int
		wantError  bool
		wantDetail string
	}{
		{name: "current", version: api.AppSelectionSchemaVersion},
		{name: "missing", version: 0, wantError: true, wantDetail: "requires definition refresh"},
		{name: "old", version: api.AppSelectionSchemaVersion - 1, wantError: true, wantDetail: "requires definition refresh"},
		{name: "future", version: api.AppSelectionSchemaVersion + 1, wantError: true, wantDetail: "upgrade fused-cli"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateSDKSyncDefinition("security-sdk", api.AppSelection{SchemaVersion: test.version})
			if !test.wantError && err != nil {
				t.Fatalf("current schema was rejected: %v", err)
			}
			if test.wantError && (err == nil || !strings.Contains(err.Error(), test.wantDetail)) {
				t.Fatalf("expected error containing %q, got %v", test.wantDetail, err)
			}
		})
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

func TestResolveSDKDownloadTargets_RejectsFamilyNameWithoutVersion(t *testing.T) {
	if _, err := resolveSDKDownloadTargets([]string{"security-sdk"}, ""); err == nil {
		t.Fatal("expected a family name without an exact version to be rejected")
	}
}

func TestResolveSDKDownloadTargets_AcceptsExactAppUUID(t *testing.T) {
	const appID = "b531e354-126b-458f-920a-2d5aa987bbc3"
	targets, err := resolveSDKDownloadTargets([]string{appID}, "")
	if err != nil {
		t.Fatalf("resolveSDKDownloadTargets failed: %v", err)
	}
	if len(targets) != 1 || targets[0].Name != appID || targets[0].Version != "" {
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
    versions: [{version: "1.1.4"}]
`)

	if _, err := resolveSDKDownloadTargets(nil, path); err == nil {
		t.Fatal("expected workspace-only config to be rejected")
	}
}

func TestSDKNameDownloadResolvesExactEngineAppBeforeDownload(t *testing.T) {
	dir := t.TempDir()
	var sawGraphQL, sawDownload bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engine/graphql":
			sawGraphQL = true
			var body struct {
				Variables map[string]any `json:"variables"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode graphql request: %v", err)
			}
			if body.Variables["reference"] != "security-sdk" || body.Variables["version"] != "1.2.0" || body.Variables["kind"] != "sdk" {
				t.Fatalf("unexpected SDK reference variables: %+v", body.Variables)
			}
			_, _ = w.Write([]byte(`{"data":{"appReference":{"id":"sdk-record-123","kind":"app"}}}`))
		case "/sdks/sdk-record-123/download":
			sawDownload = true
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write([]byte{0x50, 0x4b, 0x05, 0x06, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"sdk", "download", "security-sdk@1.2.0"})

	if !sawGraphQL || !sawDownload {
		t.Fatalf("expected graphql and download requests, saw graphql=%v download=%v", sawGraphQL, sawDownload)
	}
	if info, err := os.Stat(filepath.Join(dir, "fused-sdks", "security-sdk")); err != nil || !info.IsDir() {
		t.Fatalf("failed to find extracted sdk directory: %v", err)
	}
}

func TestSDKSyncCreatesDefaultFusedConfig(t *testing.T) {
	dir := t.TempDir()
	server := newSDKSyncServer(t, "1.0.0")
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"sdk", "sync", "security-sdk"})

	data, err := os.ReadFile(filepath.Join(dir, ".fused", "sdks", "security-sdk.yaml"))
	if err != nil {
		t.Fatalf("expected default sdk config to be created: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "version: 1.0.0") || !strings.Contains(text, "kind: sdk") || !strings.Contains(text, "name: security-sdk") || !strings.Contains(text, "language: python") || !strings.Contains(text, "github-rest-api:") || !strings.Contains(text, "repos_list_for_authenticated_user") || !strings.Contains(text, "type: oauth") || !strings.Contains(text, "X-Tenant") {
		t.Fatalf("unexpected sdk sync file:\n%s", text)
	}
}

func TestSDKSyncDoesNotOfferImplicitVersionUpgrade(t *testing.T) {
	dir := t.TempDir()
	server := newSDKSyncServer(t, "1.0.0")
	defer server.Close()

	message := runCommandInDirExpectError(t, dir, server.URL, []string{"sdk", "sync", "security-sdk", "--sync-version"})
	if !strings.Contains(message, "unknown flag") {
		t.Fatalf("expected removed implicit upgrade flag to be rejected, got %q", message)
	}
}

func TestSDKSyncHistoricalDefinitionDoesNotOverwriteConfig(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "security.yaml", `
apiVersion: fused/v1
kind: sdk
name: security-sdk
version: "1.0.0"
language: typescript
services:
  github:
    version: "1.1.4"
    operations: ["known_operation"]
`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch {
		case strings.Contains(body.Query, "appReference"):
			_, _ = w.Write([]byte(`{"data":{"appReference":{"id":"app-1","kind":"app"}}}`))
		case strings.Contains(body.Query, "query App("):
			_, _ = w.Write([]byte(`{"data":{"app":{"app_family_id":"family-1","app_id":"app-1","name":"security-sdk","version":"1.0.0","kind":"sdk","status":"active","created_at":"now","target_language":"python","selections":[{"service_id":"svc-github","service_version_id":"sv-1","endpoint_ids":[],"operation_names":[],"webhook_ids":[],"webhook_names":[],"select_all":false,"webhook_select_all":false,"connect_scopes":[],"injections":[]}]}}}`))
		case strings.Contains(body.Query, "appServices"):
			_, _ = w.Write([]byte(`{"data":{"appServices":[{"service_id":"svc-github","service_slug":"github","service_name":"GitHub","version":"1.1.4","select_all":false,"endpoint_count":0,"webhook_count":0}]}}`))
		default:
			t.Fatalf("unexpected query %s", body.Query)
		}
	}))
	defer server.Close()

	message := runCommandInDirExpectError(t, dir, server.URL, []string{"sdk", "sync", "security-sdk", "-f", path})
	if !strings.Contains(message, "requires definition refresh") {
		t.Fatalf("expected definition refresh guidance, got %q", message)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("historical sync modified the existing config:\nbefore:\n%s\nafter:\n%s", before, after)
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
	if r.URL.Path != "/engine/graphql" {
		t.Fatalf("unexpected path %s", r.URL.Path)
	}
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode graphql request: %v", err)
	}
	switch {
	case strings.Contains(body.Query, "appReference"):
		_, _ = w.Write([]byte(`{"data":{"appReference":{"id":"app-1","kind":"app"}}}`))
	case strings.Contains(body.Query, "query App("):
		if !strings.Contains(body.Query, "required_auth") {
			t.Fatal("app query omitted immutable required auth policy")
		}
		if !strings.Contains(body.Query, "schema_version") || strings.Contains(body.Query, "definition_schema_version") {
			t.Fatalf("app query does not use the exact selection schema field: %s", body.Query)
		}
		_, _ = w.Write([]byte(`{"data":{"app":{"app_family_id":"family-1","app_id":"app-1","name":"security-sdk","version":"` + sdkVersion + `","kind":"sdk","status":"active","created_at":"now","target_language":"python","selections":[{"service_id":"svc-github","service_version_id":"sv-1","schema_version":3,"endpoint_ids":["ep-1"],"operation_names":["repos_list_for_authenticated_user"],"webhook_ids":[],"webhook_names":[],"select_all":false,"webhook_select_all":false,"auth_type":"oauth","auth_name":"githubOAuth","required_auth":[{"auth_type":"oauth","auth_name":"githubOAuth","basic_password_mode":""}],"connect_scopes":["repo"],"injections":[{"location":"header","name":"X-Tenant","value":"$connection.tenant","mode":"replace"}]}]}}}`))
	case strings.Contains(body.Query, "appServices"):
		_, _ = w.Write([]byte(`{"data":{"appServices":[{"service_id":"svc-github","service_slug":"github-rest-api","service_name":"GitHub REST API","version":"1.1.4","select_all":false,"endpoint_count":1,"webhook_count":0}]}}`))
	default:
		t.Fatalf("unexpected graphql query %s", body.Query)
	}
}
