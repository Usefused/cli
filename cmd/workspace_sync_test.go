package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func remoteVersions(values ...string) []api.WorkspaceServiceVersion {
	out := make([]api.WorkspaceServiceVersion, len(values))
	for i, value := range values {
		out[i] = api.WorkspaceServiceVersion{Version: value}
	}
	return out
}

// remoteVersionsWithIDs creates explicit version identity pairs so sync tests
// can prove config stores immutable Engine IDs, not just labels.
func remoteVersionsWithIDs(values ...string) []api.WorkspaceServiceVersion {
	out := make([]api.WorkspaceServiceVersion, 0, len(values)/2)
	for i := 0; i+1 < len(values); i += 2 {
		out = append(out, api.WorkspaceServiceVersion{Version: values[i], ServiceVersionID: values[i+1]})
	}
	return out
}

func workspaceSyncVisibility(remote ...api.WorkspaceService) map[string]api.ServiceVisibility {
	visibility := make(map[string]api.ServiceVisibility, len(remote))
	for _, svc := range remote {
		visibility[svc.ServiceID] = api.ServiceVisibility{ServiceID: svc.ServiceID, Slug: svc.ServiceName}
	}
	return visibility
}

func mustMergeWorkspaceServicesFromRemote(t *testing.T, cfg *configfile.WorkspaceConfig, remote []api.WorkspaceService, visibility map[string]api.ServiceVisibility) workspaceSyncResult {
	t.Helper()
	if visibility == nil {
		visibility = workspaceSyncVisibility(remote...)
	}
	result, err := mergeWorkspaceServicesFromRemote(cfg, remote, visibility)
	if err != nil {
		t.Fatalf("mergeWorkspaceServicesFromRemote: %v", err)
	}
	return result
}

// TestMergeWorkspaceServicesFromRemote_AddsNewRemoteService is Task 4's core
// AC: a service enabled remotely
// but absent locally gets added, with the Engine's data as the source of
// truth for ServiceID/Versions.
func TestMergeWorkspaceServicesFromRemote_AddsNewRemoteService(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{}}
	remote := []api.WorkspaceService{
		{ServiceName: "stripe", ServiceID: "svc-1", Version: "2026-01-01", EnabledVersions: remoteVersions("2026-01-01")},
	}

	result := mustMergeWorkspaceServicesFromRemote(t, cfg, remote, nil)

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
	if got.ServiceID != "svc-1" || !reflect.DeepEqual(got.Versions, []string{"2026-01-01"}) {
		t.Errorf("unexpected merged entry: %+v", got)
	}
}

// TestMergeWorkspaceServicesFromRemote_WritesResolvedVersionIDs guards the
// config field Engine apply needs to avoid registry-version drift.
func TestMergeWorkspaceServicesFromRemote_WritesResolvedVersionIDs(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{}}
	remote := []api.WorkspaceService{{
		ServiceName:     "stripe",
		ServiceID:       "svc-1",
		Version:         "2026-01-01",
		EnabledVersions: remoteVersionsWithIDs("2026-01-01", "ver-1"),
	}}

	mustMergeWorkspaceServicesFromRemote(t, cfg, remote, nil)

	got := cfg.Services["stripe"].ResolvedVersions
	if len(got) != 1 || got[0].Version != "2026-01-01" || got[0].ServiceVersionID != "ver-1" {
		t.Fatalf("expected resolved version IDs from remote, got %+v", got)
	}
}

func TestMergeWorkspaceServicesFromRemote_UsesSlugRefAsConfigKey(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{}}
	remote := []api.WorkspaceService{
		{ServiceName: "GitHub REST API", ServiceID: "svc-1", Version: "1.1.4", EnabledVersions: remoteVersions("1.1.4")},
		{ServiceName: "Acme CRM", ServiceID: "svc-2", Version: "2026-07-01", EnabledVersions: remoteVersions("2026-07-01")},
	}
	visibility := map[string]api.ServiceVisibility{
		"svc-1": {ServiceID: "svc-1", Slug: "github-rest-api", IsOwner: true},
		"svc-2": {ServiceID: "svc-2", Slug: "crm", Provider: "acme", IsOwner: false},
	}

	result := mustMergeWorkspaceServicesFromRemote(t, cfg, remote, visibility)

	if !reflect.DeepEqual(result.Added, []string{"@acme/crm", "github-rest-api"}) {
		t.Fatalf("expected slug refs in Added, got %v", result.Added)
	}
	if _, ok := cfg.Services["GitHub REST API"]; ok {
		t.Fatal("display name should not be used as a workspace config key")
	}
	if _, ok := cfg.Services["github-rest-api"]; !ok {
		t.Fatalf("expected owned service keyed by slug, got %+v", cfg.Services)
	}
	if _, ok := cfg.Services["@acme/crm"]; !ok {
		t.Fatalf("expected foreign service keyed by provider-qualified slug, got %+v", cfg.Services)
	}
}

// TestMergeWorkspaceServicesFromRemote_RemotWinsOnConflict is the "Engine's
// data wins on any conflict" AC: a local entry with different values than
// the remote gets fully overwritten, not merged field-by-field.
func TestMergeWorkspaceServicesFromRemote_RemoteWinsOnConflict(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"stripe": {ServiceID: "stale-id", Versions: []string{"2025-01-01"}},
	}}
	remote := []api.WorkspaceService{
		{ServiceName: "stripe", ServiceID: "svc-1", Version: "2026-01-01", EnabledVersions: remoteVersions("2025-01-01", "2026-01-01")},
	}

	result := mustMergeWorkspaceServicesFromRemote(t, cfg, remote, nil)

	if !reflect.DeepEqual(result.Updated, []string{"stripe"}) {
		t.Errorf("expected Updated=[stripe], got %v", result.Updated)
	}
	got := cfg.Services["stripe"]
	if got.ServiceID != "svc-1" || !sameStringSet(got.Versions, []string{"2025-01-01", "2026-01-01"}) {
		t.Errorf("expected remote values to win, got %+v", got)
	}
}

func TestMergeWorkspaceServicesFromRemote_PreservesRuntimeConfig(t *testing.T) {
	runtimeConfig := &configfile.RuntimeConfig{Connect: &configfile.ConnectConfig{
		Bucket:       "github",
		AuthType:     "oauth",
		ClientID:     "$GITHUB_APP_CLIENT_ID",
		ClientSecret: "$GITHUB_APP_CLIENT_SECRET",
		RedirectURI:  "http://127.0.0.1:8081/workspace/connect/callback",
	}}
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"github-user": {ServiceID: "svc-github", Versions: []string{"1.1.3"}, RuntimeConfig: runtimeConfig},
	}}
	remote := []api.WorkspaceService{{
		ServiceName:     "github-user",
		ServiceID:       "svc-github",
		Version:         "1.1.4",
		EnabledVersions: remoteVersionsWithIDs("1.1.4", "ver-github"),
	}}

	result := mustMergeWorkspaceServicesFromRemote(t, cfg, remote, nil)

	if !reflect.DeepEqual(result.Updated, []string{"github-user"}) {
		t.Fatalf("expected github-user to be updated, got %+v", result)
	}
	got := cfg.Services["github-user"]
	if got.RuntimeConfig == nil || got.RuntimeConfig.Connect == nil {
		t.Fatalf("workspace sync dropped runtime_config: %+v", got)
	}
	if got.RuntimeConfig.Connect.ClientSecret != "$GITHUB_APP_CLIENT_SECRET" {
		t.Fatalf("workspace sync changed connect material ref: %+v", got.RuntimeConfig.Connect)
	}
}

func TestMergeWorkspaceServicesFromRemote_PreservesRuntimeConfigAcrossSlugKeyChange(t *testing.T) {
	runtimeConfig := &configfile.RuntimeConfig{Connect: &configfile.ConnectConfig{
		Bucket:       "github",
		AuthType:     "oauth",
		ClientID:     "$GITHUB_APP_CLIENT_ID",
		ClientSecret: "$GITHUB_APP_CLIENT_SECRET",
		RedirectURI:  "http://127.0.0.1:8081/workspace/connect/callback",
	}}
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"GitHub REST API": {ServiceID: "svc-github", Versions: []string{"1.1.4"}, RuntimeConfig: runtimeConfig},
	}}
	remote := []api.WorkspaceService{{
		ServiceName:     "GitHub REST API",
		ServiceID:       "svc-github",
		Version:         "1.1.4",
		EnabledVersions: remoteVersionsWithIDs("1.1.4", "ver-github"),
	}}
	visibility := map[string]api.ServiceVisibility{
		"svc-github": {ServiceID: "svc-github", Slug: "github-user", IsOwner: true},
	}

	result := mustMergeWorkspaceServicesFromRemote(t, cfg, remote, visibility)

	if !reflect.DeepEqual(result.Added, []string{"github-user"}) || !reflect.DeepEqual(result.Removed, []string{"GitHub REST API"}) {
		t.Fatalf("expected key migration, got %+v", result)
	}
	got := cfg.Services["github-user"]
	if got.RuntimeConfig == nil || got.RuntimeConfig.Connect == nil {
		t.Fatalf("workspace sync dropped runtime_config during key migration: %+v", got)
	}
}

// TestMergeWorkspaceConnectConfigsFromRemoteWritesFreshBucketConfig proves a
// new checkout can reconstruct UI-authored connect state without secret reads.
func TestMergeWorkspaceConnectConfigsFromRemoteWritesFreshBucketConfig(t *testing.T) {
	service := api.WorkspaceService{
		ServiceID:       "svc-github",
		EnabledVersions: remoteVersionsWithIDs("1.1.4", "ver-github"),
	}
	profile := map[string]interface{}{
		"auth_type": "oauth",
		"bindings":  []interface{}{map[string]interface{}{"value": "${resource.base_url}", "location": "base_url", "mode": "force"}},
	}
	remote := []api.WorkspaceConnectConfig{{
		BucketID: "bucket-1", BucketName: "customer-accounts", ServiceID: service.ServiceID,
		AuthType: "oauth", Enabled: true, RedirectURI: "https://engine.example.com/workspace/connect/callback",
		HasClientID: true, HasClientSecret: true,
		Profiles: []api.WorkspaceConnectProfile{{ServiceVersionID: "ver-github", AuthType: "oauth", Provenance: "workspace", Profile: profile}},
	}}
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"github-user": {ServiceID: service.ServiceID},
	}}

	updated, err := mergeWorkspaceConnectConfigsFromRemote(cfg, []api.WorkspaceService{service}, remote)

	if err != nil || !reflect.DeepEqual(updated, []string{"github-user"}) {
		t.Fatalf("merge connect config: updated=%v err=%v", updated, err)
	}
	connect := cfg.Services["github-user"].RuntimeConfig.Connect
	if connect.Bucket != "customer-accounts" || connect.ClientID != "$FUSED_GITHUB_USER_CONNECT_CLIENT_ID" || connect.ClientSecret != "$FUSED_GITHUB_USER_CONNECT_CLIENT_SECRET" {
		t.Fatalf("unexpected synced connect config: %+v", connect)
	}
	if !reflect.DeepEqual(connect.Profile, profile) {
		t.Fatalf("expected attached profile snapshot, got %#v", connect.Profile)
	}
}

// TestMergeWorkspaceConnectConfigsFromRemotePreservesSafeRefs verifies masked
// Engine material does not churn env names already chosen by the operator.
func TestMergeWorkspaceConnectConfigsFromRemotePreservesSafeRefs(t *testing.T) {
	service := api.WorkspaceService{ServiceID: "svc-github", EnabledVersions: remoteVersionsWithIDs("1.1.4", "ver-github")}
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"github-user": {
			ServiceID: service.ServiceID,
			RuntimeConfig: &configfile.RuntimeConfig{
				BaseURL: "https://proxy.example.com",
				Connect: &configfile.ConnectConfig{Bucket: "customer-accounts", AuthType: "oauth", ClientID: "$GITHUB_ID", ClientSecret: "$GITHUB_SECRET"},
			},
		},
	}}
	remote := []api.WorkspaceConnectConfig{{
		BucketName: "customer-accounts", ServiceID: service.ServiceID, AuthType: "oauth", Enabled: true,
		RedirectURI: "https://engine.example.com/callback", HasClientID: true, HasClientSecret: true,
	}}

	_, err := mergeWorkspaceConnectConfigsFromRemote(cfg, []api.WorkspaceService{service}, remote)

	if err != nil {
		t.Fatalf("merge connect config: %v", err)
	}
	runtime := cfg.Services["github-user"].RuntimeConfig
	if runtime.BaseURL != "https://proxy.example.com" || runtime.Connect.ClientID != "$GITHUB_ID" || runtime.Connect.ClientSecret != "$GITHUB_SECRET" {
		t.Fatalf("expected non-connect runtime and safe refs to survive: %+v", runtime)
	}
}

// TestMergeWorkspaceConnectConfigsFromRemotePreservesOmittedConnect ensures an
// empty remote projection cannot erase pending local bucket configuration.
func TestMergeWorkspaceConnectConfigsFromRemotePreservesOmittedConnect(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"github-user": {ServiceID: "svc-github", RuntimeConfig: &configfile.RuntimeConfig{Connect: &configfile.ConnectConfig{Bucket: "stale", AuthType: "oauth"}}},
	}}

	updated, err := mergeWorkspaceConnectConfigsFromRemote(cfg, []api.WorkspaceService{{ServiceID: "svc-github"}}, nil)

	if err != nil || len(updated) != 0 {
		t.Fatalf("preserve omitted connect: updated=%v err=%v", updated, err)
	}
	if cfg.Services["github-user"].RuntimeConfig.Connect.Bucket != "stale" {
		t.Fatalf("omitted connect config was overwritten: %+v", cfg.Services["github-user"].RuntimeConfig)
	}
}

// TestMergeWorkspaceConnectConfigsFromRemotePreservesRegistryIdentity proves
// sync emits profile_id instead of downgrading a Registry profile to inline.
func TestMergeWorkspaceConnectConfigsFromRemotePreservesRegistryIdentity(t *testing.T) {
	service := api.WorkspaceService{ServiceID: "svc-jira", EnabledVersions: remoteVersionsWithIDs("v1", "ver-1")}
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{"jira": {ServiceID: service.ServiceID}}}
	profileID := "00000000-0000-0000-0000-000000000123"
	updated, err := mergeWorkspaceConnectConfigsFromRemote(cfg, []api.WorkspaceService{service}, []api.WorkspaceConnectConfig{{
		BucketName: "customers", ServiceID: service.ServiceID, AuthType: "oauth", Enabled: true,
		RedirectURI: "https://engine.example.com/callback", HasClientID: true, HasClientSecret: true,
		Profiles: []api.WorkspaceConnectProfile{{ServiceVersionID: "ver-1", RegistryProfileID: profileID, Profile: map[string]interface{}{"auth_type": "oauth"}}},
	}})
	if err != nil || !reflect.DeepEqual(updated, []string{"jira"}) {
		t.Fatalf("merge Registry profile: updated=%v err=%v", updated, err)
	}
	connect := cfg.Services["jira"].RuntimeConfig.Connect
	if connect.ProfileID != profileID || connect.Profile != nil {
		t.Fatalf("Registry profile identity was not preserved: %#v", connect)
	}
}

// TestMergeWorkspaceConnectConfigsFromRemoteRejectsUnrepresentableState keeps
// sync from silently selecting one of multiple buckets or version profiles.
func TestMergeWorkspaceConnectConfigsFromRemoteRejectsUnrepresentableState(t *testing.T) {
	service := api.WorkspaceService{ServiceID: "svc-jira", EnabledVersions: remoteVersionsWithIDs("v1", "ver-1", "v2", "ver-2")}
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{"jira": {ServiceID: service.ServiceID}}}

	_, err := mergeWorkspaceConnectConfigsFromRemote(cfg, []api.WorkspaceService{service}, []api.WorkspaceConnectConfig{
		{BucketName: "a", ServiceID: service.ServiceID, AuthType: "oauth"},
		{BucketName: "b", ServiceID: service.ServiceID, AuthType: "oauth"},
	})
	if err == nil || !strings.Contains(err.Error(), "multiple connect buckets") {
		t.Fatalf("expected multiple bucket error, got %v", err)
	}

	_, err = mergeWorkspaceConnectConfigsFromRemote(cfg, []api.WorkspaceService{service}, []api.WorkspaceConnectConfig{{
		BucketName: "a", ServiceID: service.ServiceID, AuthType: "oauth",
		Profiles: []api.WorkspaceConnectProfile{
			{ServiceVersionID: "ver-1", Profile: map[string]interface{}{"auth_type": "oauth", "metadata": map[string]interface{}{"region": "one"}}},
			{ServiceVersionID: "ver-2", Profile: map[string]interface{}{"auth_type": "oauth", "metadata": map[string]interface{}{"region": "two"}}},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "different connection profiles") {
		t.Fatalf("expected divergent profile error, got %v", err)
	}
}

// TestRecordWorkspaceSyncWriteEmitsAuditEvent proves a user-triggered local
// mutation is visible in OTEL without recording service names or credentials.
func TestRecordWorkspaceSyncWriteEmitsAuditEvent(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := provider.Tracer("test").Start(context.Background(), "workspace-sync")

	recordWorkspaceSyncWrite(ctx, workspaceSyncResult{Added: []string{"github"}}, 1)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 || len(spans[0].Events) != 1 || spans[0].Events[0].Name != "workspace_config_written" {
		t.Fatalf("expected workspace write audit event, got %#v", spans)
	}
	attributes := map[string]interface{}{}
	for _, value := range spans[0].Attributes {
		attributes[string(value.Key)] = value.Value.AsInterface()
	}
	if attributes["user_action"] != "workspace.sync" || attributes["config_changed"] != true || attributes["connect_config_count"] != int64(1) {
		t.Fatalf("unexpected workspace sync OTEL attributes: %#v", attributes)
	}
}

// TestMergeWorkspaceServicesFromRemote_UnchangedServiceNotReportedAsUpdated
// guards against noisy output: re-running sync when nothing actually
// changed shouldn't list every service as "updated".
func TestMergeWorkspaceServicesFromRemote_UnchangedServiceNotReportedAsUpdated(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"stripe": {ServiceID: "svc-1", Versions: []string{"2026-01-01"}},
	}}
	remote := []api.WorkspaceService{
		{ServiceName: "stripe", ServiceID: "svc-1", Version: "2026-01-01", EnabledVersions: remoteVersions("2026-01-01")},
	}

	result := mustMergeWorkspaceServicesFromRemote(t, cfg, remote, nil)

	if len(result.Added) != 0 || len(result.Updated) != 0 || len(result.Removed) != 0 {
		t.Errorf("expected no changes reported for an already-in-sync service, got %+v", result)
	}
}

// TestMergeWorkspaceServicesFromRemote_UnchangedIgnoresVersionOrder confirms
// Versions is compared as a set, not an ordered list -- the Engine's
// ListActivatedServices has no guaranteed ordering.
func TestMergeWorkspaceServicesFromRemote_UnchangedIgnoresVersionOrder(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"stripe": {ServiceID: "svc-1", Versions: []string{"2026-01-01", "2025-06-01"}},
	}}
	remote := []api.WorkspaceService{
		{ServiceName: "stripe", ServiceID: "svc-1", Version: "2026-01-01", EnabledVersions: remoteVersions("2025-06-01", "2026-01-01")},
	}

	result := mustMergeWorkspaceServicesFromRemote(t, cfg, remote, nil)

	if len(result.Updated) != 0 {
		t.Errorf("expected version-order difference alone not to count as a change, got Updated=%v", result.Updated)
	}
}

// TestMergeWorkspaceServicesFromRemote_RemovesLocalEntryNoLongerActivated is
// the removal AC: a locally-configured service the Engine no longer reports
// as activated must be dropped, not just flagged.
func TestMergeWorkspaceServicesFromRemote_RemovesLocalEntryNoLongerActivated(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"stripe": {ServiceID: "svc-1", Versions: []string{"2026-01-01"}},
		"stale":  {ServiceID: "svc-2", Versions: []string{"1.0.0"}},
	}}
	remote := []api.WorkspaceService{
		{ServiceName: "stripe", ServiceID: "svc-1", Version: "2026-01-01", EnabledVersions: remoteVersions("2026-01-01")},
	}

	result := mustMergeWorkspaceServicesFromRemote(t, cfg, remote, nil)

	if !reflect.DeepEqual(result.Removed, []string{"stale"}) {
		t.Errorf("expected Removed=[stale], got %v", result.Removed)
	}
	if _, ok := cfg.Services["stale"]; ok {
		t.Error("expected stale service to be removed from cfg.Services")
	}
	if _, ok := cfg.Services["stripe"]; !ok {
		t.Error("expected stripe to remain")
	}
}

func TestMergeWorkspaceServicesFromRemote_LatestVersionIncludedInVersions(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{}}
	remote := []api.WorkspaceService{
		{ServiceName: "stripe", ServiceID: "svc-1", Version: "2026-01-01"},
	}

	mustMergeWorkspaceServicesFromRemote(t, cfg, remote, nil)

	got := cfg.Services["stripe"]
	if !containsString(got.Versions, "2026-01-01") {
		t.Errorf("expected latest version to be included in Versions, got %v", got.Versions)
	}
}

func TestWorkspaceSyncCreatesDefaultFusedConfig(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engine/graphql":
			body := decodeTestGraphQLBody(t, r)
			if strings.Contains(body.Query, "workspaceConnectConfigs") {
				_, _ = w.Write([]byte(`{"data":{"workspaceConnectConfigs":[]}}`))
				return
			}
			if strings.Contains(body.Query, "workspaceServices") {
				_, _ = w.Write([]byte(`{"data":{"workspaceServices":[{"service_name":"github","service_id":"svc-github","version":"1.1.4","enabled_versions":[{"version":"1.1.4"}]}]}}`))
				return
			}
			t.Fatalf("unexpected engine graphql query")
		case "/graphql":
			_, _ = w.Write([]byte(`{"data":{"servicesByIds":[{"id":"svc-github","slug":"github-rest-api","provider":null,"is_owner":true,"is_public":false}]}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"workspace", "sync"})

	data, err := os.ReadFile(filepath.Join(dir, ".fused", "workspace.yaml"))
	if err != nil {
		t.Fatalf("expected default workspace config to be created: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "kind: workspace") || !strings.Contains(text, "github-rest-api:") || !strings.Contains(text, "service_id: svc-github") {
		t.Fatalf("unexpected workspace sync file:\n%s", text)
	}
}

// TestMergeWorkspaceServicesFromRemote_EmptyRemoteRemovesEverything covers
// the edge case of a workspace with nothing currently activated.
func TestMergeWorkspaceServicesFromRemote_EmptyRemoteRemovesEverything(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"stripe": {ServiceID: "svc-1", Versions: []string{"2026-01-01"}},
	}}

	result := mustMergeWorkspaceServicesFromRemote(t, cfg, nil, nil)

	if !reflect.DeepEqual(result.Removed, []string{"stripe"}) {
		t.Errorf("expected Removed=[stripe], got %v", result.Removed)
	}
	if len(cfg.Services) != 0 {
		t.Errorf("expected an empty services map, got %+v", cfg.Services)
	}
}

// TestMergeWorkspaceServicesFromRemote_NilServicesMapInitialized guards a
// freshly-parsed config whose Services map might be nil (e.g. an empty
// `services: {}` block or a bare workspace.yaml skeleton).
func TestMergeWorkspaceServicesFromRemote_NilServicesMapInitialized(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{}
	remote := []api.WorkspaceService{
		{ServiceName: "stripe", ServiceID: "svc-1", Version: "2026-01-01", EnabledVersions: remoteVersions("2026-01-01")},
	}

	mustMergeWorkspaceServicesFromRemote(t, cfg, remote, nil)

	if cfg.Services == nil {
		t.Fatal("expected Services map to be initialized")
	}
	if _, ok := cfg.Services["stripe"]; !ok {
		t.Error("expected stripe to be added")
	}
}

func TestMergeWorkspaceServicesFromRemote_WritesPublicOnlyForOwnedServices(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{}}
	remote := []api.WorkspaceService{
		{ServiceName: "stripe", ServiceID: "svc-owned", Version: "2026-01-01"},
		{ServiceName: "@acme/billing", ServiceID: "svc-foreign", Version: "2026-02-01"},
	}
	visibility := map[string]api.ServiceVisibility{
		"svc-owned":   {ServiceID: "svc-owned", Slug: "stripe", IsOwner: true, IsPublic: false},
		"svc-foreign": {ServiceID: "svc-foreign", Slug: "billing", Provider: "acme", IsOwner: false, IsPublic: true},
	}

	mustMergeWorkspaceServicesFromRemote(t, cfg, remote, visibility)

	owned := cfg.Services["stripe"]
	if owned.Public == nil || *owned.Public != false {
		t.Fatalf("expected owned service public=false to be written, got %#v", owned.Public)
	}
	foreign := cfg.Services["@acme/billing"]
	if foreign.Public != nil {
		t.Fatalf("expected non-owned service public metadata to be omitted, got %#v", foreign.Public)
	}
}

func TestMergeWorkspaceServicesFromRemote_RejectsMissingSlugRef(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{}}
	remote := []api.WorkspaceService{{ServiceName: "GitHub REST API", ServiceID: "svc-github"}}

	_, err := mergeWorkspaceServicesFromRemote(cfg, remote, map[string]api.ServiceVisibility{})

	if err == nil || !strings.Contains(err.Error(), "missing service slug") {
		t.Fatalf("expected missing slug error, got %v", err)
	}
}

func TestSameStringSet(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
		want bool
	}{
		{"identical order", []string{"a", "b"}, []string{"a", "b"}, true},
		{"different order", []string{"a", "b"}, []string{"b", "a"}, true},
		{"different length", []string{"a"}, []string{"a", "b"}, false},
		{"different contents", []string{"a", "b"}, []string{"a", "c"}, false},
		{"both empty", []string{}, []string{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameStringSet(tt.a, tt.b); got != tt.want {
				t.Errorf("sameStringSet(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
