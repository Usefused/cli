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
	"github.com/Usefused/cli/internal/pagination"
	"github.com/Usefused/cli/internal/ratelimitpolicy"
	"github.com/Usefused/cli/internal/retrypolicy"
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

// testRateLimit builds the smallest canonical quota dimension needed by sync tests.
func testRateLimit(limit int64) *ratelimitpolicy.Config {
	return &ratelimitpolicy.Config{Version: ratelimitpolicy.Version, Policies: []ratelimitpolicy.Policy{{
		Name: "requests", Mode: "enforce", Unit: "requests",
		Identity:    ratelimitpolicy.BucketIdentity{Inputs: []ratelimitpolicy.IdentityInput{{Kind: "service_version"}}},
		Cost:        ratelimitpolicy.CostPlan{Default: 1, Rules: []ratelimitpolicy.CostRule{}},
		Algorithm:   "fixed_window",
		FixedWindow: &ratelimitpolicy.FixedWindow{Limit: limit, DurationMs: 60_000},
	}}}
}

// testRetry uses ordered v3 rules because workspace sync must never recreate
// the removed unversioned retry shorthand.
func testRetry(maxAttempts int) *retrypolicy.Config {
	return &retrypolicy.Config{Version: retrypolicy.Version, Rules: []retrypolicy.Rule{{
		Predicates: retrypolicy.Predicates{Methods: []string{"GET"}},
		Action: retrypolicy.Action{MaxAttempts: maxAttempts, Backoff: retrypolicy.Backoff{
			Strategy: "exponential", BaseDelayMs: 500, MaxDelayMs: 5_000,
		}},
	}}}
}

// testTokenPagination keeps sync assertions on the composable state machine,
// rather than the retired one-strategy cursor object.
func testTokenPagination(requestName, itemsPath, nextPath string, maxPages int) *pagination.Config {
	return &pagination.Config{
		Version: paginationVersion,
		Request: []pagination.RequestStep{{State: "cursor", Target: pagination.RequestTarget{Location: "query", Name: requestName}, ValueType: "string", Apply: "all"}},
		Response: pagination.ResponsePlan{Items: pagination.ItemsSource{Path: itemsPath}, Values: []pagination.ResponseValue{{
			Name: "next_cursor", Source: pagination.ValueSource{Location: "body", Path: nextPath, ValueType: "string"},
		}}},
		Continuation: []pagination.ContinuationStep{{Kind: "token", State: "cursor", ResponseValue: "next_cursor"}},
		Termination:  pagination.Termination{StopOnMissingValues: []string{"next_cursor"}, RepeatedValue: "error"},
		Limits:       pagination.Limits{MaxPages: maxPages},
	}
}

// testLinkPagination models next-link continuation without a legacy next_url branch.
func testLinkPagination(itemsPath string, maxPages int) *pagination.Config {
	return &pagination.Config{
		Version: paginationVersion,
		Request: []pagination.RequestStep{},
		Response: pagination.ResponsePlan{Items: pagination.ItemsSource{Path: itemsPath}, Values: []pagination.ResponseValue{{
			Name: "next_link", Source: pagination.ValueSource{Location: "link", Relation: "next", ValueType: "url"},
		}}},
		Continuation: []pagination.ContinuationStep{{Kind: "rfc_link", State: "next_url", ResponseValue: "next_link", Origin: &pagination.OriginPolicy{Mode: "same_origin"}}},
		Termination:  pagination.Termination{StopOnMissingValues: []string{"next_link"}, RepeatedValue: "stop"},
		Limits:       pagination.Limits{MaxPages: maxPages},
	}
}

const paginationVersion = 3

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
	result, err := mergeWorkspaceServicesFromRemote(cfg, remote, visibility, nil)
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
	if got.ServiceID != "svc-1" || !reflect.DeepEqual(got.Versions, []configfile.WorkspaceServiceVersion{{Version: "2026-01-01"}}) {
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

	got := cfg.Services["stripe"].Versions
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
		"svc-1": {ServiceID: "svc-1", Slug: "github-rest-api", Provider: &api.ServiceProviderIdentity{Handle: "acme"}, IsOwner: true},
		"svc-2": {ServiceID: "svc-2", Slug: "crm", Provider: &api.ServiceProviderIdentity{Handle: "acme"}, IsOwner: false},
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
		"stripe": {ServiceID: "stale-id", Versions: []configfile.WorkspaceServiceVersion{{Version: "2025-01-01"}}},
	}}
	remote := []api.WorkspaceService{
		{ServiceName: "stripe", ServiceID: "svc-1", Version: "2026-01-01", EnabledVersions: remoteVersions("2025-01-01", "2026-01-01")},
	}

	result := mustMergeWorkspaceServicesFromRemote(t, cfg, remote, nil)

	if !reflect.DeepEqual(result.Updated, []string{"stripe"}) {
		t.Errorf("expected Updated=[stripe], got %v", result.Updated)
	}
	got := cfg.Services["stripe"]
	if got.ServiceID != "svc-1" || !sameVersionNameSet(got.Versions, "2025-01-01", "2026-01-01") {
		t.Errorf("expected remote values to win, got %+v", got)
	}
}

// sameVersionNameSet checks a Versions list's Version names as an unordered
// set, mirroring what sameStringSet did back when Versions was []string.
func sameVersionNameSet(got []configfile.WorkspaceServiceVersion, want ...string) bool {
	names := make([]string, 0, len(got))
	for _, v := range got {
		names = append(names, v.Version)
	}
	return sameStringSet(names, want)
}

// TestMergeWorkspaceServicesFromRemote_PreservesRuntimeConfig and
// TestMergeWorkspaceServicesFromRemote_PreservesRuntimeConfigAcrossSlugKeyChange
// were removed along with RuntimeConfig/runtime_config.webhooks (no backward
// compatibility -- see plans/plan-webhook-kind.md): there is nothing left to
// preserve across a sync once the field no longer exists.

// TestMergeWorkspaceConnectConfigsFromRemoteWritesProfilesWithoutBucketMaterial
// proves sync exports routing policy without inventing $ENV placeholders for
// Engine-held OAuth app material.
func TestMergeWorkspaceConnectConfigsFromRemoteWritesProfilesWithoutBucketMaterial(t *testing.T) {
	service := api.WorkspaceService{
		ServiceID:       "svc-github",
		EnabledVersions: remoteVersionsWithIDs("1.1.4", "ver-github"),
	}
	profile := map[string]interface{}{
		"auth_type": "oauth",
		"resource_input": map[string]interface{}{
			"fields":            []interface{}{map[string]interface{}{"name": "your-domain", "required": true}},
			"base_url_template": "https://{your-domain}.atlassian.net",
			"allowed_hosts":     []interface{}{"*.atlassian.net"},
		},
		"bindings": []interface{}{map[string]interface{}{"value": "${resource.base_url}", "location": "base_url", "mode": "force"}},
	}
	remote := []api.WorkspaceConnectConfig{{
		BucketID: "bucket-1", BucketName: "customer-accounts", ServiceID: service.ServiceID,
		AuthType: "oauth", Enabled: true, RedirectURI: "https://engine.example.com/workspace/connect/callback",
		HasClientID: true, HasClientSecret: true,
		Profiles: []api.WorkspaceConnectProfile{{ServiceVersionID: "ver-github", AuthType: "oauth", Provenance: "workspace", Profile: profile}},
	}}
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"github-user": {ServiceID: service.ServiceID, Versions: []configfile.WorkspaceServiceVersion{{Version: "1.1.4", ServiceVersionID: "ver-github"}}},
	}}

	updated, err := mergeWorkspaceConnectConfigsFromRemote(cfg, []api.WorkspaceService{service}, remote)

	if err != nil || !reflect.DeepEqual(updated, []string{"github-user"}) {
		t.Fatalf("merge connect config: updated=%v err=%v", updated, err)
	}
	if len(cfg.Buckets) != 0 {
		t.Fatalf("sync must not export bucket connect material refs: %+v", cfg.Buckets)
	}
	profiles := cfg.Services["github-user"].Versions[0].ConnectionProfiles
	if len(profiles) != 1 || !reflect.DeepEqual(profiles[0]["profile"], profile) {
		t.Fatalf("expected attached profile snapshot, got %#v", profiles)
	}
}

// TestMergeWorkspaceConnectConfigsFromRemoteWritesPublicForPublishedProfile
// covers the connection-profile half of task 11: a profile the Engine has
// recorded as published (is_public: true, set only after a successful,
// owner-gated PublishConnectionProfile call) round-trips into
// connection_profiles[*].public: true so a subsequent `fused apply` doesn't
// silently drop the publish intent.
func TestMergeWorkspaceConnectConfigsFromRemoteWritesPublicForPublishedProfile(t *testing.T) {
	service := api.WorkspaceService{ServiceID: "svc-github", EnabledVersions: remoteVersionsWithIDs("1.1.4", "ver-github")}
	profile := map[string]interface{}{"auth_type": "oauth"}
	remote := []api.WorkspaceConnectConfig{{
		BucketID: "bucket-1", BucketName: "customer-accounts", ServiceID: service.ServiceID, AuthType: "oauth",
		Profiles: []api.WorkspaceConnectProfile{
			{ServiceVersionID: "ver-github", AuthType: "oauth", Provenance: "provider", IsPublic: true, Profile: profile},
		},
	}}
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"github-user": {ServiceID: service.ServiceID, Versions: []configfile.WorkspaceServiceVersion{{Version: "1.1.4", ServiceVersionID: "ver-github"}}},
	}}

	if _, err := mergeWorkspaceConnectConfigsFromRemote(cfg, []api.WorkspaceService{service}, remote); err != nil {
		t.Fatalf("merge connect config: %v", err)
	}

	profiles := cfg.Services["github-user"].Versions[0].ConnectionProfiles
	if len(profiles) != 1 {
		t.Fatalf("expected one profile, got %#v", profiles)
	}
	if public, _ := profiles[0]["public"].(bool); !public {
		t.Fatalf("expected public:true to round-trip, got %#v", profiles[0])
	}
}

// TestMergeWorkspaceConnectConfigsFromRemoteOmitsPublicForUnpublishedProfile
// ensures an ordinary (non-published) profile gets no public key at all,
// rather than an explicit false, matching the omitempty YAML convention used
// elsewhere in workspace.yaml.
func TestMergeWorkspaceConnectConfigsFromRemoteOmitsPublicForUnpublishedProfile(t *testing.T) {
	service := api.WorkspaceService{ServiceID: "svc-github", EnabledVersions: remoteVersionsWithIDs("1.1.4", "ver-github")}
	remote := []api.WorkspaceConnectConfig{{
		BucketID: "bucket-1", BucketName: "customer-accounts", ServiceID: service.ServiceID, AuthType: "oauth",
		Profiles: []api.WorkspaceConnectProfile{
			{ServiceVersionID: "ver-github", AuthType: "oauth", Provenance: "workspace", Profile: map[string]interface{}{"auth_type": "oauth"}},
		},
	}}
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"github-user": {ServiceID: service.ServiceID, Versions: []configfile.WorkspaceServiceVersion{{Version: "1.1.4", ServiceVersionID: "ver-github"}}},
	}}

	if _, err := mergeWorkspaceConnectConfigsFromRemote(cfg, []api.WorkspaceService{service}, remote); err != nil {
		t.Fatalf("merge connect config: %v", err)
	}

	profiles := cfg.Services["github-user"].Versions[0].ConnectionProfiles
	if len(profiles) != 1 {
		t.Fatalf("expected one profile, got %#v", profiles)
	}
	if _, ok := profiles[0]["public"]; ok {
		t.Fatalf("expected no public key for an unpublished profile, got %#v", profiles[0])
	}
}

// TestMergeWorkspaceConnectConfigsFromRemotePreservesRegistryIdentity proves
// sync emits profile_id instead of downgrading a Registry profile to inline.
func TestMergeWorkspaceConnectConfigsFromRemotePreservesRegistryIdentity(t *testing.T) {
	service := api.WorkspaceService{ServiceID: "svc-jira", EnabledVersions: remoteVersionsWithIDs("v1", "ver-1")}
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"jira": {ServiceID: service.ServiceID, Versions: []configfile.WorkspaceServiceVersion{{Version: "v1", ServiceVersionID: "ver-1"}}},
	}}
	profileID := "00000000-0000-0000-0000-000000000123"
	updated, err := mergeWorkspaceConnectConfigsFromRemote(cfg, []api.WorkspaceService{service}, []api.WorkspaceConnectConfig{{
		BucketName: "customers", ServiceID: service.ServiceID, AuthType: "oauth", Enabled: true,
		RedirectURI: "https://engine.example.com/callback", HasClientID: true, HasClientSecret: true,
		Profiles: []api.WorkspaceConnectProfile{{ServiceVersionID: "ver-1", RegistryProfileID: profileID, Profile: map[string]interface{}{"auth_type": "oauth", "auth_name": "jiraOAuth"}}},
	}})
	if err != nil || !reflect.DeepEqual(updated, []string{"jira"}) {
		t.Fatalf("merge Registry profile: updated=%v err=%v", updated, err)
	}
	profiles := cfg.Services["jira"].Versions[0].ConnectionProfiles
	if len(profiles) != 1 || profiles[0]["profile_id"] != profileID || profiles[0]["profile"] != nil || profiles[0]["auth_name"] != "jiraOAuth" {
		t.Fatalf("Registry profile identity was not preserved: %#v", profiles)
	}
}

// TestMergeWorkspaceConnectConfigsFromRemoteRepresentsMultipleBucketsAndProfiles
// proves the bucket-owned shape can safely export state the old singular
// runtime_config.connect field could not represent.
func TestMergeWorkspaceConnectConfigsFromRemoteRepresentsMultipleBucketsAndProfiles(t *testing.T) {
	service := api.WorkspaceService{ServiceID: "svc-jira", EnabledVersions: remoteVersionsWithIDs("v1", "ver-1", "v2", "ver-2")}
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"jira": {ServiceID: service.ServiceID, Versions: []configfile.WorkspaceServiceVersion{
			{Version: "v1", ServiceVersionID: "ver-1"}, {Version: "v2", ServiceVersionID: "ver-2"},
		}},
	}}

	updated, err := mergeWorkspaceConnectConfigsFromRemote(cfg, []api.WorkspaceService{service}, []api.WorkspaceConnectConfig{
		{BucketName: "a", ServiceID: service.ServiceID, AuthType: "oauth"},
		{BucketName: "b", ServiceID: service.ServiceID, AuthType: "oauth"},
	})
	if err != nil || len(updated) != 0 || len(cfg.Buckets) != 0 {
		t.Fatalf("bucket connect material should not be exported, updated=%v buckets=%+v err=%v", updated, cfg.Buckets, err)
	}

	_, err = mergeWorkspaceConnectConfigsFromRemote(cfg, []api.WorkspaceService{service}, []api.WorkspaceConnectConfig{{
		BucketName: "a", ServiceID: service.ServiceID, AuthType: "oauth",
		Profiles: []api.WorkspaceConnectProfile{
			{ServiceVersionID: "ver-1", Profile: map[string]interface{}{"auth_type": "oauth", "metadata": map[string]interface{}{"region": "one"}}},
			{ServiceVersionID: "ver-2", Profile: map[string]interface{}{"auth_type": "oauth", "metadata": map[string]interface{}{"region": "two"}}},
		},
	}})
	// The two profiles now attach to their own distinct Versions entry
	// (ver-1, ver-2) rather than sharing one flat service-level list.
	gotProfiles := 0
	for _, v := range cfg.Services["jira"].Versions {
		gotProfiles += len(v.ConnectionProfiles)
	}
	if err != nil || gotProfiles != 2 {
		t.Fatalf("expected divergent profiles to be represented, versions=%#v err=%v", cfg.Services["jira"].Versions, err)
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
		"stripe": {ServiceID: "svc-1", Versions: []configfile.WorkspaceServiceVersion{{Version: "2026-01-01"}}},
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
		"stripe": {ServiceID: "svc-1", Versions: []configfile.WorkspaceServiceVersion{{Version: "2026-01-01"}, {Version: "2025-06-01"}}},
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
		"stripe": {ServiceID: "svc-1", Versions: []configfile.WorkspaceServiceVersion{{Version: "2026-01-01"}}},
		"stale":  {ServiceID: "svc-2", Versions: []configfile.WorkspaceServiceVersion{{Version: "1.0.0"}}},
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
	if !sameVersionNameSet(got.Versions, "2026-01-01") {
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

func TestWorkspaceSyncDoesNotWriteConnectEnvRefs(t *testing.T) {
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engine/graphql":
			body := decodeTestGraphQLBody(t, r)
			if strings.Contains(body.Query, "workspaceConnectConfigs") {
				_, _ = w.Write([]byte(`{"data":{"workspaceConnectConfigs":[{"bucket_id":"bucket-1","bucket_name":"customer-accounts","service_id":"svc-github","auth_type":"oauth","enabled":true,"redirect_uri":"https://engine.example.test/callback","has_client_id":true,"has_client_secret":true,"profiles":[]}]}}`))
				return
			}
			if strings.Contains(body.Query, "workspaceServices") {
				_, _ = w.Write([]byte(`{"data":{"workspaceServices":[{"service_name":"github","service_id":"svc-github","version":"1.1.4","enabled_versions":[{"version":"1.1.4","service_version_id":"ver-github"}]}]}}`))
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
		t.Fatalf("expected workspace config to be written: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "$FUSED_") || strings.Contains(text, "connect:") || strings.Contains(text, "client_secret") {
		t.Fatalf("workspace sync must not write connect env refs/material:\n%s", text)
	}
}

// TestMergeWorkspaceServicesFromRemote_EmptyRemoteRemovesEverything covers
// the edge case of a workspace with nothing currently activated.
func TestMergeWorkspaceServicesFromRemote_EmptyRemoteRemovesEverything(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"stripe": {ServiceID: "svc-1", Versions: []configfile.WorkspaceServiceVersion{{Version: "2026-01-01"}}},
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
		"svc-foreign": {ServiceID: "svc-foreign", Slug: "billing", Provider: &api.ServiceProviderIdentity{Handle: "acme"}, IsOwner: false, IsPublic: true},
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

// TestMergeWorkspaceServicesFromRemote_WritesExecutionPolicyForOwnedServiceWithRegistryPolicy
// covers the execution-policy sync-back half of task 11: a service with a
// published Registry execution policy (rate_limit/retry) round-trips as
// execution_policy.public: true, with the actual values preserved so a
// subsequent `fused apply` is a no-op rather than wiping the Registry policy.
func TestMergeWorkspaceServicesFromRemote_WritesExecutionPolicyForOwnedServiceWithRegistryPolicy(t *testing.T) {
	timeoutMs := 45000
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{}}
	remote := []api.WorkspaceService{
		{ServiceName: "stripe", ServiceID: "svc-owned", Version: "2026-01-01"},
		{ServiceName: "@acme/billing", ServiceID: "svc-foreign", Version: "2026-02-01"},
	}
	visibility := map[string]api.ServiceVisibility{
		"svc-owned": {
			ServiceID: "svc-owned", Slug: "stripe", IsOwner: true, IsPublic: true,
			RateLimit:   testRateLimit(300),
			RetryConfig: testRetry(3),
			TimeoutMs:   &timeoutMs,
		},
		"svc-foreign": {
			ServiceID: "svc-foreign", Slug: "billing", Provider: &api.ServiceProviderIdentity{Handle: "acme"}, IsOwner: false,
			// A foreign service should never surface its owner's execution
			// policy back into this workspace's config even if present.
			RateLimit: testRateLimit(5),
		},
	}

	mustMergeWorkspaceServicesFromRemote(t, cfg, remote, visibility)

	owned := cfg.Services["stripe"]
	assertOwnedExecutionPolicy(t, owned, timeoutMs)
	foreign := cfg.Services["@acme/billing"]
	if foreign.ExecutionPolicy != nil {
		t.Fatalf("expected non-owned service execution policy to be omitted, got %#v", foreign.ExecutionPolicy)
	}
}

// assertOwnedExecutionPolicy keeps the sync scenario readable while checking
// each canonical policy family independently.
func assertOwnedExecutionPolicy(t *testing.T, owned configfile.WorkspaceService, timeoutMs int) {
	t.Helper()
	if owned.ExecutionPolicy == nil || owned.ExecutionPolicy.Public == nil || !*owned.ExecutionPolicy.Public {
		t.Fatalf("expected owned service execution_policy.public=true, got %#v", owned.ExecutionPolicy)
	}
	if got := owned.ExecutionPolicy.RateLimit; got == nil || got.Policies[0].FixedWindow.Limit != 300 {
		t.Fatalf("expected rate limit values to round-trip, got %#v", owned.ExecutionPolicy.RateLimit)
	}
	if owned.ExecutionPolicy.Retry == nil || owned.ExecutionPolicy.Retry.Rules[0].Action.MaxAttempts != 3 {
		t.Fatalf("expected retry values to round-trip, got %#v", owned.ExecutionPolicy.Retry)
	}
	if owned.ExecutionPolicy.TimeoutMs == nil || *owned.ExecutionPolicy.TimeoutMs != timeoutMs {
		t.Fatalf("expected timeout_ms to round-trip, got %v", owned.ExecutionPolicy.TimeoutMs)
	}
}

// TestMergeWorkspaceServicesFromRemote_WritesPaginationForOwnedService covers
// plans/plan-service-config-restructure.md item 1: pagination round-trips
// through execution_policy the same way rate_limit/retry already do,
// including when it's the *only* published field (no rate_limit/retry set).
func TestMergeWorkspaceServicesFromRemote_WritesPaginationForOwnedService(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{}}
	remote := []api.WorkspaceService{{ServiceName: "stripe", ServiceID: "svc-owned", Version: "2026-01-01"}}
	visibility := map[string]api.ServiceVisibility{
		"svc-owned": {
			ServiceID: "svc-owned", Slug: "stripe", IsOwner: true, IsPublic: true,
			Pagination: testTokenPagination("after", "$.data", "$.page.next", 100),
		},
	}

	mustMergeWorkspaceServicesFromRemote(t, cfg, remote, visibility)

	owned := cfg.Services["stripe"]
	if owned.ExecutionPolicy == nil || owned.ExecutionPolicy.Public == nil || !*owned.ExecutionPolicy.Public {
		t.Fatalf("expected owned service execution_policy.public=true, got %#v", owned.ExecutionPolicy)
	}
	if owned.ExecutionPolicy.Pagination == nil || owned.ExecutionPolicy.Pagination.Request[0].Target.Name != "after" ||
		owned.ExecutionPolicy.Pagination.Response.Values[0].Source.Path != "$.page.next" {
		t.Fatalf("expected pagination values to round-trip, got %#v", owned.ExecutionPolicy.Pagination)
	}
}

// TestMergeWorkspaceServicesFromRemote_OmitsExecutionPolicyWithNoRegistryPolicy
// ensures an owned service with no published policy gets no execution_policy
// block at all, rather than an empty public:true stub.
func TestMergeWorkspaceServicesFromRemote_OmitsExecutionPolicyWithNoRegistryPolicy(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{}}
	remote := []api.WorkspaceService{{ServiceName: "stripe", ServiceID: "svc-owned", Version: "2026-01-01"}}
	visibility := map[string]api.ServiceVisibility{
		"svc-owned": {ServiceID: "svc-owned", Slug: "stripe", IsOwner: true},
	}

	mustMergeWorkspaceServicesFromRemote(t, cfg, remote, visibility)

	if owned := cfg.Services["stripe"]; owned.ExecutionPolicy != nil {
		t.Fatalf("expected no execution policy without a Registry-side policy, got %#v", owned.ExecutionPolicy)
	}
}

// TestMergeWorkspaceServicesFromRemote_WritesVersionPoliciesForOwnedService
// covers task 17's sync-back half: a version made private, or one with a
// published per-version execution policy, round-trips into that version's
// own Public/ExecutionPolicy override for an owned service only -- while a
// version that's still at the Registry default (public, no policy) gets no
// override at all, mirroring
// TestMergeWorkspaceServicesFromRemote_OmitsExecutionPolicyWithNoRegistryPolicy's
// "don't write boilerplate" rule. Only versions actually enabled for this
// workspace can carry an override -- Registry's ServiceVersions call can
// return versions beyond what's enabled here, and nesting the override
// inside Versions makes attaching one to an unenabled version structurally
// impossible (see workspaceServiceVersionsFromRemote), unlike the old flat
// version_policies list, which could reference one and would then fail the
// very next plan/apply's validation.
func TestMergeWorkspaceServicesFromRemote_WritesVersionPoliciesForOwnedService(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{}}
	remote := []api.WorkspaceService{
		{ServiceName: "stripe", ServiceID: "svc-owned", Version: "2026-01-01", EnabledVersions: remoteVersions("2026-01-01", "2025-12-01", "2025-11-01")},
		{ServiceName: "@acme/billing", ServiceID: "svc-foreign", Version: "2026-02-01"},
	}
	visibility := map[string]api.ServiceVisibility{
		"svc-owned": {
			ServiceID: "svc-owned", Slug: "stripe", IsOwner: true, IsPublic: true,
			Pagination: testTokenPagination("cursor", "$.items", "$.next", 100),
		},
		"svc-foreign": {ServiceID: "svc-foreign", Slug: "billing", Provider: &api.ServiceProviderIdentity{Handle: "acme"}, IsOwner: false, IsPublic: true},
	}
	versionsByServiceID := map[string][]api.ServiceVersion{
		"svc-owned": {
			{Name: "2026-01-01", IsPublic: true},
			{Name: "2025-12-01", IsPublic: false},
			{Name: "2025-11-01", IsPublic: true, RateLimit: testRateLimit(200), Pagination: testLinkPagination("$.values", 25)},
		},
		// A foreign service's version data should never surface into this
		// workspace's config even if the fetch somehow returned it.
		"svc-foreign": {{Name: "2026-02-01", IsPublic: false}},
	}

	if _, err := mergeWorkspaceServicesFromRemote(cfg, remote, visibility, versionsByServiceID); err != nil {
		t.Fatalf("mergeWorkspaceServicesFromRemote: %v", err)
	}

	owned := cfg.Services["stripe"]
	assertServiceTokenPagination(t, owned)
	byVersion := indexWorkspaceVersions(owned.Versions)
	assertVersionPolicyOverrides(t, byVersion)
	assertForeignVersionPoliciesAbsent(t, cfg.Services["@acme/billing"].Versions)
}

// assertServiceTokenPagination proves the service default remains distinct
// from version-level continuation policies.
func assertServiceTokenPagination(t *testing.T, owned configfile.WorkspaceService) {
	t.Helper()
	if owned.ExecutionPolicy == nil || owned.ExecutionPolicy.Pagination == nil || owned.ExecutionPolicy.Pagination.Continuation[0].Kind != "token" {
		t.Fatalf("expected service-default cursor pagination to round-trip, got %#v", owned.ExecutionPolicy)
	}
	if len(owned.Versions) != 3 {
		t.Fatalf("expected 3 enabled versions, got %#v", owned.Versions)
	}
}

// indexWorkspaceVersions makes assertions independent of deterministic output
// order while preserving the production order contract separately.
func indexWorkspaceVersions(versions []configfile.WorkspaceServiceVersion) map[string]configfile.WorkspaceServiceVersion {
	byVersion := map[string]configfile.WorkspaceServiceVersion{}
	for _, v := range versions {
		byVersion[v.Version] = v
	}
	return byVersion
}

// assertVersionPolicyOverrides checks canonical v3 policy content without
// folding unrelated ownership assertions into the same branch-heavy test.
func assertVersionPolicyOverrides(t *testing.T, byVersion map[string]configfile.WorkspaceServiceVersion) {
	t.Helper()
	privateVersion := byVersion["2025-12-01"]
	if privateVersion.Public == nil || *privateVersion.Public {
		t.Fatalf("expected 2025-12-01 public=false to round-trip, got %#v", privateVersion)
	}
	policyVersion := byVersion["2025-11-01"]
	if policyVersion.ExecutionPolicy == nil || policyVersion.ExecutionPolicy.RateLimit == nil {
		t.Fatalf("expected 2025-11-01 execution policy to round-trip, got %#v", policyVersion)
	}
	if policyVersion.ExecutionPolicy.RateLimit.Policies[0].FixedWindow.Limit != 200 {
		t.Fatalf("expected version quota to round-trip, got %#v", policyVersion.ExecutionPolicy.RateLimit)
	}
	if got := policyVersion.ExecutionPolicy.Pagination; got == nil || got.Continuation[0].Kind != "rfc_link" {
		t.Fatalf("expected version next-url pagination to remain distinct from the service default, got %#v", got)
	}
	defaultVersion := byVersion["2026-01-01"]
	if defaultVersion.Public != nil || defaultVersion.ExecutionPolicy != nil {
		t.Fatalf("expected default-public version with no policy to have no override, got %#v", defaultVersion)
	}
}

// assertForeignVersionPoliciesAbsent guards the ownership boundary for every
// enabled version without duplicating it in the main merge scenario.
func assertForeignVersionPoliciesAbsent(t *testing.T, versions []configfile.WorkspaceServiceVersion) {
	t.Helper()
	for _, v := range versions {
		if v.Public != nil || v.ExecutionPolicy != nil {
			t.Fatalf("expected non-owned service versions to have no policy override, got %#v", versions)
		}
	}
}

// TestMergeWorkspaceServicesFromRemote_VersionPublicNeverStaleFromLocal guards
// against a real bug found while re-examining workspaceServiceWithLocalState
// after the versions[] nesting redesign: it used to carry Public,
// ExecutionPolicy, and ConnectionProfiles forward from the existing local
// entry as one all-or-nothing bundle (inherited from the old flat
// version_policies list, where the whole entry really was one local
// override). That let a stale local `public: false` silently overwrite a
// freshly Registry-derived Public value, purely because the same version
// entry also happened to have an ExecutionPolicy set. Public has no
// local-carry path at all now -- like the service-level Public field --
// while ExecutionPolicy still carries forward from local independently,
// proving the fields are no longer bundled together.
func TestMergeWorkspaceServicesFromRemote_VersionPublicNeverStaleFromLocal(t *testing.T) {
	localExecutionPolicy := &configfile.ExecutionPolicy{
		RateLimit: testRateLimit(9), ServerVariables: map[string]string{"tenant": "acme"},
	}
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"stripe": {
			ServiceID: "svc-owned",
			Versions: []configfile.WorkspaceServiceVersion{
				// Stale local state: this version was private and had no
				// published execution policy the last time this file was
				// written by hand or by a prior sync.
				{Version: "2026-01-01", Public: boolPtr(false), ExecutionPolicy: localExecutionPolicy},
			},
		},
	}}
	remote := []api.WorkspaceService{
		{ServiceName: "stripe", ServiceID: "svc-owned", Version: "2026-01-01", EnabledVersions: remoteVersions("2026-01-01")},
	}
	visibility := map[string]api.ServiceVisibility{
		"svc-owned": {ServiceID: "svc-owned", Slug: "stripe", IsOwner: true, IsPublic: true},
	}
	versionsByServiceID := map[string][]api.ServiceVersion{
		// Registry now reports this version as public with still no
		// published execution policy -- the version was made public again
		// since the local file was last written.
		"svc-owned": {{Name: "2026-01-01", IsPublic: true}},
	}

	if _, err := mergeWorkspaceServicesFromRemote(cfg, remote, visibility, versionsByServiceID); err != nil {
		t.Fatalf("mergeWorkspaceServicesFromRemote: %v", err)
	}

	got := cfg.Services["stripe"].Versions[0]
	if got.Public != nil {
		t.Fatalf("expected stale local public:false to be dropped in favor of fresh Registry truth, got %#v", got.Public)
	}
	if got.ExecutionPolicy == nil || got.ExecutionPolicy.RateLimit == nil || got.ExecutionPolicy.RateLimit.Policies[0].FixedWindow.Limit != 9 {
		t.Fatalf("expected local-only execution policy to still carry forward independently, got %#v", got.ExecutionPolicy)
	}
	if !reflect.DeepEqual(got.ExecutionPolicy.ServerVariables, map[string]string{"tenant": "acme"}) {
		t.Fatalf("expected Engine-local server_variables to survive sync, got %#v", got.ExecutionPolicy.ServerVariables)
	}
}

func TestMergeWorkspaceServicesFromRemote_RejectsMissingSlugRef(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{}}
	remote := []api.WorkspaceService{{ServiceName: "GitHub REST API", ServiceID: "svc-github"}}

	_, err := mergeWorkspaceServicesFromRemote(cfg, remote, map[string]api.ServiceVisibility{}, nil)

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
