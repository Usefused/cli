package cmd

import (
	"reflect"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

// TestMergeSDKServicesFromRemote_AddsNewRemoteService is Task 4c's core AC
// (engine_workspace_registration_plan.md): a service present in the most
// recently generated remote SDK but absent locally gets added, with the
// Registry's resolved data (version tag + enumerated operations) as the
// source of truth.
func TestMergeSDKServicesFromRemote_AddsNewRemoteService(t *testing.T) {
	cfg := &configfile.SDKConfig{Services: map[string]configfile.SDKService{}}
	remote := []sdkSyncRemoteService{
		{Name: "stripe", Version: "2026-01-01", Operations: []string{"createCharge", "listCharges"}},
	}

	result := mergeSDKServicesFromRemote(cfg, "1.2.0", remote)

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

// TestMergeSDKServicesFromRemote_RemoteWinsOnConflict mirrors workspace
// sync's "remote wins" semantics: a local entry with a stale version or
// operation list is fully overwritten, not merged field-by-field.
func TestMergeSDKServicesFromRemote_RemoteWinsOnConflict(t *testing.T) {
	cfg := &configfile.SDKConfig{Services: map[string]configfile.SDKService{
		"stripe": {Version: "2025-01-01", Operations: []string{"createCharge"}},
	}}
	remote := []sdkSyncRemoteService{
		{Name: "stripe", Version: "2026-01-01", Operations: []string{"createCharge", "listCharges"}},
	}

	result := mergeSDKServicesFromRemote(cfg, "1.2.0", remote)

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
		{Name: "stripe", Version: "2026-01-01", Operations: []string{"createCharge", "listCharges"}},
	}

	result := mergeSDKServicesFromRemote(cfg, "1.2.0", remote)

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
		{Name: "stripe", Version: "2026-01-01", Operations: []string{"listCharges", "createCharge"}},
	}

	result := mergeSDKServicesFromRemote(cfg, "1.2.0", remote)

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
		"legacy": {Version: "1.0.0", Operations: []string{"oldOp"}},
	}}
	remote := []sdkSyncRemoteService{
		{Name: "stripe", Version: "2026-01-01", Operations: []string{"createCharge"}},
	}

	result := mergeSDKServicesFromRemote(cfg, "1.2.0", remote)

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

// TestMergeSDKServicesFromRemote_EmptyRemoteRemovesEverything covers the
// edge case of an SDK whose latest generation has no selections left.
func TestMergeSDKServicesFromRemote_EmptyRemoteRemovesEverything(t *testing.T) {
	cfg := &configfile.SDKConfig{Services: map[string]configfile.SDKService{
		"stripe": {Version: "2026-01-01", Operations: []string{"createCharge"}},
	}}

	result := mergeSDKServicesFromRemote(cfg, "1.2.0", nil)

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
		{Name: "stripe", Version: "2026-01-01", Operations: []string{"createCharge"}},
	}

	mergeSDKServicesFromRemote(cfg, "1.2.0", remote)

	if cfg.Services == nil {
		t.Fatal("expected Services map to be initialized")
	}
	if _, ok := cfg.Services["stripe"]; !ok {
		t.Error("expected stripe to be added")
	}
}

// TestMergeSDKServicesFromRemote_BumpsSDKVersion confirms the top-level
// sdkVersion field is synced to the remote generated SDK's version, and the
// result records the before/after for reporting.
func TestMergeSDKServicesFromRemote_BumpsSDKVersion(t *testing.T) {
	cfg := &configfile.SDKConfig{SDKVersion: "1.1.0", Services: map[string]configfile.SDKService{}}

	result := mergeSDKServicesFromRemote(cfg, "1.2.0", nil)

	if cfg.SDKVersion != "1.2.0" {
		t.Errorf("expected cfg.SDKVersion to be bumped to 1.2.0, got %s", cfg.SDKVersion)
	}
	if result.SDKVersionFrom != "1.1.0" || result.SDKVersionTo != "1.2.0" {
		t.Errorf("expected SDKVersionFrom/To to record the change, got %+v", result)
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

func TestValidateSDKDownloadArgs_RejectsVersionSuffix(t *testing.T) {
	if err := validateSDKDownloadArgs([]string{"billing@1.2.3"}); err == nil {
		t.Fatal("expected version-suffixed sdk download argument to be rejected")
	}
}
