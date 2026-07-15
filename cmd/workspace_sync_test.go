package cmd

import (
	"reflect"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

func remoteVersions(values ...string) []api.WorkspaceServiceVersion {
	out := make([]api.WorkspaceServiceVersion, len(values))
	for i, value := range values {
		out[i] = api.WorkspaceServiceVersion{Version: value}
	}
	return out
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

	result := mergeWorkspaceServicesFromRemote(cfg, remote)

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

	result := mergeWorkspaceServicesFromRemote(cfg, remote)

	if !reflect.DeepEqual(result.Updated, []string{"stripe"}) {
		t.Errorf("expected Updated=[stripe], got %v", result.Updated)
	}
	got := cfg.Services["stripe"]
	if got.ServiceID != "svc-1" || !sameStringSet(got.Versions, []string{"2025-01-01", "2026-01-01"}) {
		t.Errorf("expected remote values to win, got %+v", got)
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

	result := mergeWorkspaceServicesFromRemote(cfg, remote)

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

	result := mergeWorkspaceServicesFromRemote(cfg, remote)

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
		"legacy": {ServiceID: "svc-2", Versions: []string{"1.0.0"}},
	}}
	remote := []api.WorkspaceService{
		{ServiceName: "stripe", ServiceID: "svc-1", Version: "2026-01-01", EnabledVersions: remoteVersions("2026-01-01")},
	}

	result := mergeWorkspaceServicesFromRemote(cfg, remote)

	if !reflect.DeepEqual(result.Removed, []string{"legacy"}) {
		t.Errorf("expected Removed=[legacy], got %v", result.Removed)
	}
	if _, ok := cfg.Services["legacy"]; ok {
		t.Error("expected legacy to be removed from cfg.Services")
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

	mergeWorkspaceServicesFromRemote(cfg, remote)

	got := cfg.Services["stripe"]
	if !containsString(got.Versions, "2026-01-01") {
		t.Errorf("expected latest version to be included in Versions, got %v", got.Versions)
	}
}

// TestMergeWorkspaceServicesFromRemote_EmptyRemoteRemovesEverything covers
// the edge case of a workspace with nothing currently activated.
func TestMergeWorkspaceServicesFromRemote_EmptyRemoteRemovesEverything(t *testing.T) {
	cfg := &configfile.WorkspaceConfig{Services: map[string]configfile.WorkspaceService{
		"stripe": {ServiceID: "svc-1", Versions: []string{"2026-01-01"}},
	}}

	result := mergeWorkspaceServicesFromRemote(cfg, nil)

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

	mergeWorkspaceServicesFromRemote(cfg, remote)

	if cfg.Services == nil {
		t.Fatal("expected Services map to be initialized")
	}
	if _, ok := cfg.Services["stripe"]; !ok {
		t.Error("expected stripe to be added")
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
