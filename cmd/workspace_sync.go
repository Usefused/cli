package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// workspaceSyncResult summarizes what mergeWorkspaceServicesFromRemote did,
// so the command can report a diff without re-deriving it itself.
type workspaceSyncResult struct {
	Added   []string
	Updated []string
	Missing []string
}

// workspaceSyncTarget describes one fully preflighted local document mutation.
type workspaceSyncTarget struct {
	Path           string
	Services       []api.WorkspaceService
	SelectFromFile bool
	RemoveKeys     map[string]bool
}

// workspaceSyncMove transfers one existing declaration while preserving its authored local policy.
type workspaceSyncMove struct {
	SourcePath string
	TargetPath string
	ServiceKey string
}

// preparedWorkspaceSyncWrite carries validated content and audit metadata into the all-target commit stage.
type preparedWorkspaceSyncWrite struct {
	target       workspaceSyncTarget
	result       workspaceSyncResult
	profileCount int
}

// workspaceSyncFileBackup retains exact pre-sync bytes so a failed later write can restore the local set.
type workspaceSyncFileBackup struct {
	path    string
	data    []byte
	mode    os.FileMode
	existed bool
}

// workspaceSyncSnapshot keeps one complete Engine read reusable across every locally scoped services-file write.
type workspaceSyncSnapshot struct {
	Services            []api.WorkspaceService
	Profiles            []api.WorkspaceConnectionProfile
	Visibility          map[string]api.ServiceVisibility
	VersionsByServiceID map[string][]api.ServiceVersion
}

// workspaceSyncServiceRequest aggregates repeated exact-version selectors before one service is projected.
type workspaceSyncServiceRequest struct {
	service     api.WorkspaceService
	allVersions bool
	versions    []string
	seenVersion map[string]bool
}

var workspaceSyncServices []string
var workspaceSyncAll bool

// mergeWorkspaceServicesFromRemote refreshes selected Engine services without interpreting remote absence as local deletion intent.
func mergeWorkspaceServicesFromRemote(cfg *configfile.WorkspaceConfig, remote []api.WorkspaceService, visibility map[string]api.ServiceVisibility, versionsByServiceID map[string][]api.ServiceVersion) (workspaceSyncResult, error) {
	// A nil map is valid for a new services file and must become writable before selected services are projected.
	if cfg.Services == nil {
		cfg.Services = map[string]configfile.WorkspaceService{}
	}
	var result workspaceSyncResult
	localByServiceID := workspaceServicesByID(cfg.Services)

	// Selected remote services are the only entries this pull may add or refresh.
	for _, svc := range remote {
		key, changed, err := mergeRemoteWorkspaceService(cfg.Services, svc, visibility, versionsByServiceID, localByServiceID)
		// Missing canonical Registry identity cannot safely generate a local service key.
		if err != nil {
			return result, err
		}
		// Report the exact local mutation without letting map order affect output.
		switch changed {
		case workspaceSyncAdded:
			result.Added = append(result.Added, key)
		case workspaceSyncUpdated:
			result.Updated = append(result.Updated, key)
		}
	}

	sort.Strings(result.Added)
	sort.Strings(result.Updated)
	sort.Strings(result.Missing)
	return result, nil
}

// workspaceSyncChange keeps add/update reporting separate from service data.
type workspaceSyncChange int

const (
	workspaceSyncUnchanged workspaceSyncChange = iota
	workspaceSyncAdded
	workspaceSyncUpdated
)

// workspaceServicesByID retains local state across a display-name-to-slug key
// migration by indexing the stable Engine identity.
func workspaceServicesByID(services map[string]configfile.WorkspaceService) map[string]configfile.WorkspaceService {
	byID := make(map[string]configfile.WorkspaceService, len(services))
	for _, svc := range services {
		if svc.ServiceID != "" {
			byID[svc.ServiceID] = svc
		}
	}
	return byID
}

// mergeRemoteWorkspaceService updates Engine-owned identity and version fields
// while carrying local runtime intent forward until its dedicated sync runs.
func mergeRemoteWorkspaceService(services map[string]configfile.WorkspaceService, svc api.WorkspaceService, visibility map[string]api.ServiceVisibility, versionsByServiceID map[string][]api.ServiceVersion, localByServiceID map[string]configfile.WorkspaceService) (string, workspaceSyncChange, error) {
	key, err := workspaceServiceConfigKey(svc, visibility)
	// Canonical keys are required before any existing entry can be rekeyed or updated.
	if err != nil {
		return "", workspaceSyncUnchanged, err
	}
	newEntry := workspaceServiceFromRemote(svc, visibility, versionsByServiceID)
	// Runtime config is local deployment intent, not Engine inventory. Sync
	// refreshes the Engine-owned identity/version fields while keeping
	// bucket/connect/webhook settings that a later apply must not erase.
	existing, existed := services[key]
	existingKey := key
	// Stable identity locates an existing local entry even when its canonical Registry slug changed.
	if !existed {
		existing = localByServiceID[svc.ServiceID]
		for candidateKey, candidate := range services {
			// Only the exact immutable service identity can authorize a local key migration.
			if candidate.ServiceID == svc.ServiceID {
				existingKey = candidateKey
				existed = true
				break
			}
		}
	}
	newEntry = workspaceServiceWithLocalState(newEntry, existing)
	// A canonical slug change updates the key in place without treating unrelated omitted services as removable.
	if existed && existingKey != key {
		delete(services, existingKey)
	}
	services[key] = newEntry
	// A stable-ID key migration is an update, not a newly adopted service.
	if existed && existingKey != key {
		return key, workspaceSyncUpdated, nil
	}
	if !existed {
		return key, workspaceSyncAdded, nil
	}
	if !workspaceServiceEqual(existing, newEntry) {
		return key, workspaceSyncUpdated, nil
	}
	return key, workspaceSyncUnchanged, nil
}

// workspaceServiceWithLocalState preserves runtime configuration because the
// service inventory GraphQL projection does not own those fields. Version
// identity (Version/ServiceVersionID) and per-version Public are never
// carried forward from local -- both always come from whatever
// workspaceServiceVersionsFromRemote just computed a moment earlier in the
// same merge, since Public mirrors live Registry visibility truth (exactly
// like the service-level Public field above, which has no local-carry path
// at all). ExecutionPolicy and ConnectionProfiles are different: an
// unpublished or non-owner ExecutionPolicy has no Registry truth to defer
// to, and ConnectionProfiles isn't (re)derived here in the first place --
// that's mergeWorkspaceConnectionProfilesFromRemote's job, from a separate
// remote source, later in the same sync. So those two carry forward from the
// existing local entry independently, field-by-field, each only when that
// field is actually set locally -- never as an all-or-nothing bundle keyed
// off whether *any* field was set. That coarser bundle rule is what the old
// flat version_policies list required (the whole list entry really was one
// local override with one provenance); nesting Public alongside these two
// doesn't mean all three still share that provenance, so bundling them
// together here would let a stale local Public silently overwrite a fresh
// Registry-derived value just because ExecutionPolicy or ConnectionProfiles
// also happened to be set on the same version.
func workspaceServiceWithLocalState(remote, local configfile.WorkspaceService) configfile.WorkspaceService {
	if local.ExecutionPolicy != nil {
		remote.ExecutionPolicy = local.ExecutionPolicy
	}
	localByVersion := make(map[string]configfile.WorkspaceServiceVersion, len(local.Versions))
	for _, v := range local.Versions {
		localByVersion[v.Version] = v
	}
	for i, v := range remote.Versions {
		existing, ok := localByVersion[v.Version]
		if !ok {
			// No local entry for this version at all -- nothing to carry
			// forward, keep the freshly-computed remote entry as-is.
			continue
		}
		// ExecutionPolicy: local-authored value wins whenever set, regardless
		// of what (if anything) Registry published -- see doc comment above.
		if existing.ExecutionPolicy != nil {
			remote.Versions[i].ExecutionPolicy = existing.ExecutionPolicy
		}
		// ConnectionProfiles stay as a placeholder until the independent profile export supplies live data.
		if len(existing.ConnectionProfiles) > 0 {
			remote.Versions[i].ConnectionProfiles = existing.ConnectionProfiles
		}
		// Public is deliberately NOT copied from existing here -- see doc
		// comment above: it always reflects the value workspaceServiceVersionsFromRemote
		// just derived from live Registry state, never a stale local one.
	}
	remoteVersions := make(map[string]bool, len(remote.Versions))
	for _, version := range remote.Versions {
		remoteVersions[version.Version] = true
	}
	for _, version := range local.Versions {
		// Remote absence and exact-version sync scope are never authority to erase another local version declaration.
		if !remoteVersions[version.Version] {
			remote.Versions = append(remote.Versions, version)
		}
	}
	return remote
}

// mergeWorkspaceConnectionProfilesFromRemote mirrors routing policy without coupling sync to credential storage.
func mergeWorkspaceConnectionProfilesFromRemote(cfg *configfile.WorkspaceConfig, services []api.WorkspaceService, profiles []api.WorkspaceConnectionProfile) ([]string, error) {
	remoteServices := remoteWorkspaceServicesByID(services)
	serviceKeys := workspaceServiceKeysByID(cfg.Services)
	updated, err := mergeWorkspaceConnectionProfileRows(cfg, remoteServices, serviceKeys, profiles)
	// Invalid remote ownership or version identity must stop before rewriting YAML.
	if err != nil {
		return nil, err
	}
	sort.Strings(updated)
	return uniqueStrings(updated), nil
}

// workspaceServiceKeysByID indexes the already-synced service map so remote
// profile rows can be validated without scanning every service per row.
func workspaceServiceKeysByID(services map[string]configfile.WorkspaceService) map[string]string {
	byID := make(map[string]string, len(services))
	for key, service := range services {
		if service.ServiceID != "" {
			byID[service.ServiceID] = key
		}
	}
	return byID
}

// remoteWorkspaceServicesByID indexes the already-batched GraphQL result so
// profile coverage checks stay constant-time and require no extra reads.
func remoteWorkspaceServicesByID(services []api.WorkspaceService) map[string]api.WorkspaceService {
	byID := make(map[string]api.WorkspaceService, len(services))
	for _, service := range services {
		byID[service.ServiceID] = service
	}
	return byID
}

// uniqueStrings preserves first-seen order while preventing duplicate profile
// versions from causing noisy sync diffs.
func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// mergeWorkspaceConnectionProfileRows splices each effective profile into its immutable service version.
func mergeWorkspaceConnectionProfileRows(cfg *configfile.WorkspaceConfig, services map[string]api.WorkspaceService, serviceKeys map[string]string, profiles []api.WorkspaceConnectionProfile) ([]string, error) {
	grouped, err := workspaceConnectionProfilesByServiceVersion(services, serviceKeys, profiles)
	// Grouping validates every immutable identity before mutating the config.
	if err != nil {
		return nil, err
	}
	updated := make([]string, 0, len(grouped))
	for serviceID, byVersionID := range grouped {
		key := serviceKeys[serviceID]
		service := cfg.Services[key]
		changed := false
		for i := range service.Versions {
			profiles, ok := byVersionID[service.Versions[i].ServiceVersionID]
			// Unchanged or absent snapshots should not create noisy sync output.
			if !ok || reflect.DeepEqual(service.Versions[i].ConnectionProfiles, profiles) {
				continue
			}
			service.Versions[i].ConnectionProfiles = profiles
			changed = true
		}
		// Services without profile changes retain their existing declarative entry.
		if !changed {
			continue
		}
		cfg.Services[key] = service
		updated = append(updated, key)
	}
	return updated, nil
}

// workspaceConnectionProfilesByServiceVersion validates ownership and groups rows without per-profile reads.
func workspaceConnectionProfilesByServiceVersion(services map[string]api.WorkspaceService, serviceKeys map[string]string, profiles []api.WorkspaceConnectionProfile) (map[string]map[string][]map[string]interface{}, error) {
	grouped := map[string]map[string][]map[string]interface{}{}
	seen := map[string]bool{}
	for _, profile := range profiles {
		// Sync must not attach routing policy to a service absent from the authoritative service list.
		if serviceKeys[profile.ServiceID] == "" {
			return nil, fmt.Errorf("workspace sync received a connection profile for inactive service_id %s", profile.ServiceID)
		}
		enabledVersionIDs := workspaceEnabledVersionIDs(services[profile.ServiceID])
		// Profile identity is immutable-version scoped, so inactive versions cannot be reconstructed safely.
		if !enabledVersionIDs[profile.ServiceVersionID] {
			return nil, fmt.Errorf("workspace sync received a connection profile for an inactive version of service_id %s", profile.ServiceID)
		}
		dedupeKey := profile.ServiceID + "\x00" + profile.ServiceVersionID + "\x00" + profile.AuthType
		// Duplicate effective rows must not create unstable YAML arrays.
		if seen[dedupeKey] {
			continue
		}
		seen[dedupeKey] = true
		if grouped[profile.ServiceID] == nil {
			grouped[profile.ServiceID] = map[string][]map[string]interface{}{}
		}
		grouped[profile.ServiceID][profile.ServiceVersionID] = append(grouped[profile.ServiceID][profile.ServiceVersionID], workspaceConnectionProfileIntent(profile))
	}
	return grouped, nil
}

// workspaceEnabledVersionIDs requires resolved IDs because connection
// profiles attach to immutable service versions, not mutable display names.
func workspaceEnabledVersionIDs(service api.WorkspaceService) map[string]bool {
	ids := make(map[string]bool, len(service.EnabledVersions)+1)
	for _, version := range service.EnabledVersions {
		// Only immutable IDs can prove a profile belongs to the selected version.
		if version.ServiceVersionID != "" {
			ids[version.ServiceVersionID] = true
		}
	}
	// The primary version is a compatibility fallback when older Engine projections omit it from enabled_versions.
	if service.ServiceVersionID != "" {
		ids[service.ServiceVersionID] = true
	}
	return ids
}

// workspaceConnectionProfileIntent preserves Registry identity when present;
// only workspace-local attachments need to carry inline profile JSON.
// Version is deliberately absent from the intent map -- it's implied by
// which Versions entry this profile list gets attached to.
func workspaceConnectionProfileIntent(profile api.WorkspaceConnectionProfile) map[string]interface{} {
	intent := map[string]interface{}{"auth_type": profile.AuthType}
	// Exact scheme identity is exported only when present in the safe snapshot.
	if authName := workspaceConnectionProfileAuthName(profile); authName != "" {
		// The outer selector lets Engine resolve the same named Registry stream after sync replaces an inline body with profile_id.
		intent["auth_name"] = authName
	}
	// is_public reflects a prior successful, owner-gated publish recorded by
	// the Engine (MarkWorkspaceProfilePublished only runs after
	// PublishConnectionProfile succeeds), so sync can round-trip it here
	// without re-deriving or re-checking ownership itself.
	if profile.IsPublic {
		intent["public"] = true
	}
	if id := strings.TrimSpace(profile.RegistryProfileID); id != "" {
		intent["profile_id"] = id
		return intent
	}
	intent["profile"] = profile.Profile
	return intent
}

// workspaceConnectionProfileAuthName recovers the exact scheme identity from the safe profile snapshot without introducing another Engine sync field.
func workspaceConnectionProfileAuthName(profile api.WorkspaceConnectionProfile) string {
	authName, _ := profile.Profile["auth_name"].(string)
	return strings.TrimSpace(authName)
}

// workspaceServiceConfigKey requires Registry slug identity so sync never
// falls back to a mutable display name as a declarative resource key.
func workspaceServiceConfigKey(svc api.WorkspaceService, visibility map[string]api.ServiceVisibility) (string, error) {
	if vis, ok := visibility[svc.ServiceID]; ok {
		provider := ""
		// Registry returns provider identity for owned services too; only foreign
		// slugs need qualification because ownership already scopes local names.
		if !vis.IsOwner {
			provider = providerHandle(vis.Provider)
		}
		if ref := serviceConfigRef(vis.Slug, provider); ref != "" {
			return ref, nil
		}
	}
	return "", fmt.Errorf("workspace sync missing service slug for service_id %s", svc.ServiceID)
}

func providerHandle(provider *api.ServiceProviderIdentity) string {
	if provider == nil {
		return ""
	}
	return provider.Handle
}

// serviceConfigRef qualifies foreign service slugs by provider while keeping
// locally-owned service keys concise.
func serviceConfigRef(slug, provider string) string {
	slug = strings.TrimSpace(slug)
	provider = strings.TrimSpace(provider)
	if slug == "" {
		return ""
	}
	if provider == "" {
		return slug
	}
	return "@" + provider + "/" + slug
}

// workspaceServiceFromRemote projects only fields owned by Engine and
// Registry, leaving runtime config reconciliation to its separate concern.
func workspaceServiceFromRemote(svc api.WorkspaceService, visibility map[string]api.ServiceVisibility, versionsByServiceID map[string][]api.ServiceVersion) configfile.WorkspaceService {
	newEntry := configfile.WorkspaceService{
		ServiceID: svc.ServiceID,
		Versions:  workspaceServiceVersionsFromRemote(svc, visibility, versionsByServiceID),
	}
	if vis, ok := visibility[svc.ServiceID]; ok && vis.IsOwner {
		newEntry.Public = boolPtr(vis.IsPublic)
		newEntry.ExecutionPolicy = workspaceExecutionPolicyFromRemote(vis)
	}
	return newEntry
}

// workspaceServiceVersionsFromRemote builds one merged entry per enabled
// version: its display name, the Engine-resolved ServiceVersionID, and --
// for a service this workspace owns -- its per-version visibility/
// execution-policy override. resolved_versions and version_policies used to
// be two separate sibling lists, each keyed by a repeated `version` string;
// this is their unified replacement, split into two focused passes below
// (seed identity, then layer on owner-only overrides) so each stays easy to
// reason about on its own rather than one function doing both at once.
func workspaceServiceVersionsFromRemote(svc api.WorkspaceService, visibility map[string]api.ServiceVisibility, versionsByServiceID map[string][]api.ServiceVersion) []configfile.WorkspaceServiceVersion {
	byName, order := workspaceEnabledVersionsByName(svc)
	attachWorkspaceVersionPolicyOverrides(svc, visibility, versionsByServiceID, byName)
	out := make([]configfile.WorkspaceServiceVersion, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out
}

// workspaceEnabledVersionsByName seeds one entry per version identity --
// display name plus the Engine-resolved ServiceVersionID (so CLI/CI can
// re-apply without a fresh registry lookup that could drift under a reused
// version label) -- from the Engine's EnabledVersions list, falling back to
// the currently-active Version as a safety net in case that list ever omits
// it. order preserves first-seen insertion order so output stays
// deterministic regardless of map iteration.
func workspaceEnabledVersionsByName(svc api.WorkspaceService) (map[string]*configfile.WorkspaceServiceVersion, []string) {
	byName := map[string]*configfile.WorkspaceServiceVersion{}
	order := make([]string, 0, len(svc.EnabledVersions)+1)
	ensure := func(name string) *configfile.WorkspaceServiceVersion {
		if v, ok := byName[name]; ok {
			// Already seeded (e.g. by an earlier EnabledVersions entry) --
			// return the existing pointer so later mutations land on the
			// same entry instead of creating a duplicate.
			return v
		}
		v := &configfile.WorkspaceServiceVersion{Version: name}
		byName[name] = v
		order = append(order, name)
		return v
	}
	for _, ev := range svc.EnabledVersions {
		v := ensure(ev.Version)
		if ev.ServiceVersionID != "" {
			v.ServiceVersionID = ev.ServiceVersionID
		}
	}
	if svc.Version != "" {
		// Belt-and-suspenders: the currently-active version must always have
		// an entry even if the Engine's enabled-list didn't separately
		// include it (documented workspace sync behavior).
		ensure(svc.Version)
	}
	return byName, order
}

// attachWorkspaceVersionPolicyOverrides layers each version's own
// Public/ExecutionPolicy override onto the already-seeded entries.
//
// Unlike the service-level Public field (always written for an owned
// service, true or false), a version's public status defaults to true on
// the Registry, so a version only gets its Public/ExecutionPolicy fields
// populated when it deviates from that default (is_public=false) or has a
// published execution policy to round-trip -- otherwise every sync would
// grow workspace.yaml with boilerplate for every enabled version even when
// nothing was ever set.
func attachWorkspaceVersionPolicyOverrides(svc api.WorkspaceService, visibility map[string]api.ServiceVisibility, versionsByServiceID map[string][]api.ServiceVersion, byName map[string]*configfile.WorkspaceServiceVersion) {
	vis, ok := visibility[svc.ServiceID]
	if !ok || !vis.IsOwner {
		// version_policies data has no meaning for a service this workspace
		// doesn't own -- nothing to attach.
		return
	}
	for _, remoteVersion := range versionsByServiceID[svc.ServiceID] {
		// Registry's ServiceVersions call can return versions beyond what's
		// actually enabled for this workspace; only an already-enabled
		// version gets a policy override attached here.
		v, enabled := byName[remoteVersion.Name]
		if !enabled {
			continue
		}
		if !remoteVersion.IsPublic {
			v.Public = boolPtr(false)
		}
		if ep := workspaceVersionExecutionPolicyFromRemote(remoteVersion); ep != nil {
			v.ExecutionPolicy = ep
		}
	}
}

// mapExecRateLimit/mapExecRetry/mapExecPagination/mapExecWebhookConfig do the
// api.* -> configfile.* field mapping shared by workspaceExecutionPolicyFromRemote
// and workspaceVersionExecutionPolicyFromRemote below -- both Registry types
// (ServiceVisibility, ServiceVersion) carry identically-shaped execution
// policy fields, so this keeps the two round-trip functions from duplicating
// the same field-by-field copy four times over.
func mapExecRateLimit(rl *api.ServiceRateLimit) *configfile.RateLimitConfig {
	// API and configfile intentionally alias the same canonical v3 type. The
	// Engine remains the sole place that interprets or enforces the policy.
	return rl
}

func mapExecRetry(rc *api.ServiceRetryConfig) *configfile.RetryConfig {
	// Both API and workspace config alias the canonical retry contract. Passing
	// it through keeps ordered v3 rules intact and avoids another policy mapper.
	return rc
}

func mapExecPagination(p *api.ServicePagination) *configfile.PaginationConfig {
	// API and configfile intentionally alias the shared transport type. Returning
	// the value unchanged keeps Registry/Engine authoritative over pagination
	// semantics and prevents sync from normalizing policy locally.
	return p
}

// mapExecWebhookConfig round-trips the provider's own webhook verification
// recipe (plans/plan-service-config-restructure.md item 3) -- never a
// secret, only the auth mechanism, so it's safe to carry back into
// workspace.yaml the same way rate_limit/retry/pagination already are.
func mapExecWebhookConfig(w *api.ServiceIncomingWebhookConfig) *configfile.WebhookVerify {
	if w == nil {
		return nil
	}
	return &configfile.WebhookVerify{
		AuthType: w.AuthType, AuthLocation: w.AuthLocation, AuthKeyName: w.AuthKeyName,
		SignatureHeader: w.SignatureHeader, VerificationHeaders: w.VerificationHeaders,
	}
}

// workspaceVersionExecutionPolicyFromRemote mirrors
// workspaceExecutionPolicyFromRemote, scoped to one version's already-published
// rate_limit/retry/pagination/webhook_config instead of the service-wide
// defaults.
func workspaceVersionExecutionPolicyFromRemote(v api.ServiceVersion) *configfile.ExecutionPolicy {
	if v.RateLimit == nil && v.RetryConfig == nil && v.TimeoutMs == nil && v.Pagination == nil && v.IncomingWebhookConfig == nil && v.BaseURLOverride == nil {
		return nil
	}
	return &configfile.ExecutionPolicy{
		Public:                boolPtr(true),
		RateLimit:             mapExecRateLimit(v.RateLimit),
		Retry:                 mapExecRetry(v.RetryConfig),
		TimeoutMs:             v.TimeoutMs,
		Pagination:            mapExecPagination(v.Pagination),
		BaseURL:               v.BaseURLOverride,
		EventExtractionPath:   v.EventExtractionPath,
		IncomingWebhookConfig: mapExecWebhookConfig(v.IncomingWebhookConfig),
	}
}

// workspaceExecutionPolicyFromRemote round-trips a previously published
// execution policy (rate_limit/retry/pagination/webhook_config set on the
// Registry for an owned service) back into workspace.yaml as
// execution_policy.public: true, so a subsequent `fused apply` stays a no-op
// instead of silently dropping the published policy on the next sync.
// Returns nil when the Registry has no policy set for this service, leaving
// any local execution_policy untouched via workspaceServiceWithLocalState.
func workspaceExecutionPolicyFromRemote(vis api.ServiceVisibility) *configfile.ExecutionPolicy {
	if vis.RateLimit == nil && vis.RetryConfig == nil && vis.TimeoutMs == nil && vis.Pagination == nil && vis.IncomingWebhookConfig == nil && vis.BaseURLOverride == nil {
		return nil
	}
	return &configfile.ExecutionPolicy{
		Public:                boolPtr(true),
		RateLimit:             mapExecRateLimit(vis.RateLimit),
		Retry:                 mapExecRetry(vis.RetryConfig),
		TimeoutMs:             vis.TimeoutMs,
		Pagination:            mapExecPagination(vis.Pagination),
		BaseURL:               vis.BaseURLOverride,
		EventExtractionPath:   vis.EventExtractionPath,
		IncomingWebhookConfig: mapExecWebhookConfig(vis.IncomingWebhookConfig),
	}
}

// workspaceServiceEqual compares the fields sync actually touches. Versions
// is compared as a set so harmless remote ordering changes do not churn
// local files.
func workspaceServiceEqual(a, b configfile.WorkspaceService) bool {
	return a.ServiceID == b.ServiceID &&
		sameBoolPtr(a.Public, b.Public) &&
		reflect.DeepEqual(a.ExecutionPolicy, b.ExecutionPolicy) &&
		sameWorkspaceServiceVersions(a.Versions, b.Versions)
}

// sameWorkspaceServiceVersions treats Versions as a set keyed by Version
// (remote ordering is not part of the workspace contract), then deep-compares
// each matched pair's identity and override fields.
func sameWorkspaceServiceVersions(a, b []configfile.WorkspaceServiceVersion) bool {
	if len(a) != len(b) {
		return false
	}
	byVersion := make(map[string]configfile.WorkspaceServiceVersion, len(a))
	for _, v := range a {
		byVersion[v.Version] = v
	}
	for _, v := range b {
		existing, ok := byVersion[v.Version]
		if !ok || !reflect.DeepEqual(existing, v) {
			return false
		}
	}
	return true
}

// boolPtr preserves an explicit false value through YAML omitempty handling.
func boolPtr(value bool) *bool {
	return &value
}

// sameBoolPtr distinguishes unmanaged nil visibility from explicit true or
// false declarations.
func sameBoolPtr(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// sameStringSet compares unordered declarative selections without sorting and
// mutating either caller-owned slice.
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, s := range a {
		set[s] = true
	}
	for _, s := range b {
		if !set[s] {
			return false
		}
	}
	return true
}

// workspaceSyncCmd pulls selected Engine state into local workspace documents without changing the Engine.
var workspaceSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Pull selected Engine service configuration into local workspace files",
	Long: `Refreshes only services already declared in discovered workspace files by default.
Use --file to scope the pull to one document, --service <service>[@<version>]
to create or update type: services files, or --all to explicitly import every active workspace service.
Sync never changes Engine state and never deletes a local service declaration.`,
	Args: cobra.NoArgs,
	RunE: WithTelemetry("cli.workspace.sync", func(cmd *cobra.Command, args []string) error {
		serviceRefs := append([]string(nil), workspaceSyncServices...)
		all := workspaceSyncAll
		// Package-level Cobra bindings are reset after execution so repeated in-process command runs cannot inherit scope.
		defer resetWorkspaceSyncFlags(cmd)
		// Full import and exact service selection are mutually exclusive scopes.
		if all && len(serviceRefs) > 0 {
			return fmt.Errorf("--all cannot be combined with --service")
		}
		client, err := getAPIClient()
		// Client construction must succeed before any local target is created.
		if err != nil {
			return err
		}
		result, err := performWorkspaceSyncSelection(cmd.Context(), client, ConfigFile, serviceRefs, all)
		// A failed remote snapshot or local write remains a command failure with no Engine mutation.
		if err != nil {
			return err
		}
		if result != nil {
			printWorkspaceSyncResult(cmd, *result)
		}
		return nil
	}),
}

// resetWorkspaceSyncFlags restores command scope defaults for tests and embedded callers that execute Cobra more than once.
func resetWorkspaceSyncFlags(cmd *cobra.Command) {
	workspaceSyncServices = nil
	workspaceSyncAll = false
	for _, name := range []string{"service", "all"} {
		// Clearing Changed lets the next parse treat a newly supplied StringArray as its first value.
		if flag := cmd.Flags().Lookup(name); flag != nil {
			flag.Changed = false
		}
	}
}

// PerformWorkspaceSync preserves the programmatic full-import entry point while using non-destructive local merge semantics.
func PerformWorkspaceSync(ctx context.Context, client *api.Client, configPath string) (*workspaceSyncResult, error) {
	return performWorkspaceSyncSelection(ctx, client, configPath, nil, true)
}

// performWorkspaceSyncSelection resolves local scope before writing selected non-secret Engine state.
func performWorkspaceSyncSelection(ctx context.Context, client *api.Client, configPath string, serviceRefs []string, all bool) (*workspaceSyncResult, error) {
	snapshot, err := fetchWorkspaceSyncSnapshot(client)
	// A complete remote snapshot is required before any destination file is created or replaced.
	if err != nil {
		return nil, err
	}
	paths, err := configfile.DiscoverWorkspaceConfigPaths(".fused")
	// Every existing workspace document is validated together before sync chooses or mutates ownership.
	if err == nil {
		err = configfile.ValidateWorkspaceConfigPaths(paths)
	}
	// Invalid or duplicate local ownership must fail before any destination file is created or replaced.
	if err != nil {
		return nil, err
	}
	targets, moves, err := workspaceSyncTargets(configPath, serviceRefs, all, snapshot)
	// Ambiguous service identity or an absent local scope must fail before filesystem mutation.
	if err != nil {
		return nil, err
	}
	configs := make(map[string]*configfile.WorkspaceConfig, len(targets))
	for _, target := range targets {
		cfg, loadErr := loadWorkspaceSyncTarget(target.Path, target.SelectFromFile || len(target.Services) > 0)
		// Every target is loaded before the first write so a later invalid document cannot cause a partial sync.
		if loadErr != nil {
			return nil, loadErr
		}
		configs[target.Path] = cfg
	}
	for _, move := range moves {
		source, target := configs[move.SourcePath], configs[move.TargetPath]
		service, exists := source.Services[move.ServiceKey]
		// Ownership was resolved from this exact source during planning; disappearance indicates inconsistent local state.
		if !exists {
			return nil, fmt.Errorf("workspace service %s is no longer declared in %s", move.ServiceKey, move.SourcePath)
		}
		// Preserve locally authored policy in the destination before Engine-owned fields are refreshed.
		target.Services[move.ServiceKey] = service
		delete(source.Services, move.ServiceKey)
	}
	aggregate := workspaceSyncResult{}
	prepared := make([]preparedWorkspaceSyncWrite, 0, len(targets))
	for _, target := range targets {
		cfg := configs[target.Path]
		targetServices := target.Services
		// Default file-scoped sync derives its remote selection from services already declared in that file.
		if target.SelectFromFile {
			var selectErr error
			targetServices, aggregate.Missing, selectErr = selectWorkspaceServicesForConfig(cfg, snapshot, aggregate.Missing)
			// A local key that cannot be compared with canonical remote identity must not be guessed.
			if selectErr != nil {
				return nil, selectErr
			}
		}
		result := workspaceSyncResult{}
		profileCount := 0
		var mergeErr error
		// Removal-only source files preserve their remaining declarations without refreshing unrelated Engine state.
		if len(targetServices) > 0 {
			result, profileCount, mergeErr = mergeWorkspaceSnapshot(cfg, snapshot, targetServices)
		}
		// Identity or profile conflicts must stop before the current target is published.
		if mergeErr != nil {
			return nil, mergeErr
		}
		prepared = append(prepared, preparedWorkspaceSyncWrite{target: target, result: result, profileCount: profileCount})
		aggregate.Added = append(aggregate.Added, result.Added...)
		aggregate.Updated = append(aggregate.Updated, result.Updated...)
	}
	// The batch restores earlier targets if a later filesystem replacement fails.
	if err := writeWorkspaceSyncBatch(prepared, configs); err != nil {
		return nil, err
	}
	for _, write := range prepared {
		recordWorkspaceSyncWrite(ctx, write.result, write.profileCount, len(write.target.RemoveKeys) > 0)
	}
	aggregate.Added = uniqueSortedStrings(aggregate.Added)
	aggregate.Updated = uniqueSortedStrings(aggregate.Updated)
	aggregate.Missing = uniqueSortedStrings(aggregate.Missing)
	return &aggregate, nil
}

// writeWorkspaceSyncBatch replaces every validated target and rolls back exact prior bytes after a later write failure.
func writeWorkspaceSyncBatch(writes []preparedWorkspaceSyncWrite, configs map[string]*configfile.WorkspaceConfig) error {
	backups := make([]workspaceSyncFileBackup, 0, len(writes))
	for _, write := range writes {
		backup := workspaceSyncFileBackup{path: write.target.Path, mode: 0o644}
		data, err := os.ReadFile(backup.path)
		// A missing destination is represented explicitly so rollback removes only a file created by this batch.
		if err == nil {
			backup.data = data
			backup.existed = true
			if info, statErr := os.Stat(backup.path); statErr == nil {
				backup.mode = info.Mode().Perm()
			} else {
				return statErr
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		backups = append(backups, backup)
	}
	for index, write := range writes {
		// Every target is replaced only after the complete multi-file selection and backup set are valid.
		if err := writeWorkspaceConfig(write.target.Path, configs[write.target.Path]); err != nil {
			rollbackErr := rollbackWorkspaceSyncFiles(backups[:index+1])
			// Preserve both the triggering failure and any restoration failure for manual recovery.
			if rollbackErr != nil {
				return fmt.Errorf("workspace sync write failed: %w; rollback failed: %v", err, rollbackErr)
			}
			return err
		}
	}
	return nil
}

// rollbackWorkspaceSyncFiles restores successfully written targets in reverse order to their exact prior state.
func rollbackWorkspaceSyncFiles(backups []workspaceSyncFileBackup) error {
	var firstErr error
	for index := len(backups) - 1; index >= 0; index-- {
		backup := backups[index]
		var err error
		// Existing files regain their exact bytes and mode; newly created files are removed to restore absence.
		if backup.existed {
			err = atomicWriteFile(backup.path, backup.data, backup.mode, nil)
		} else {
			err = os.Remove(backup.path)
			// An already absent generated target satisfies the rollback invariant.
			if os.IsNotExist(err) {
				err = nil
			}
		}
		// Continue restoring every earlier target while retaining the first diagnostic.
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// workspaceSyncTargets maps command scope to local destinations without sending those paths to the Engine.
func workspaceSyncTargets(configPath string, serviceRefs []string, all bool, snapshot workspaceSyncSnapshot) ([]workspaceSyncTarget, []workspaceSyncMove, error) {
	// Explicit full import intentionally retains the legacy single-file destination unless the caller chooses another file.
	if all {
		target := configPath
		// The conventional root path keeps an explicit --all invocation predictable in a new repository.
		if strings.TrimSpace(target) == "" {
			target = filepath.Join(".fused", "workspace.yaml")
		}
		return []workspaceSyncTarget{{Path: target, Services: snapshot.Services}}, nil, nil
	}
	// Exact service pulls may update an existing owner document or create deterministic one-service files.
	if len(serviceRefs) > 0 {
		services, err := resolveWorkspaceSyncServices(serviceRefs, snapshot)
		// An absent or ambiguous reference must fail before choosing a destination.
		if err != nil {
			return nil, nil, err
		}
		// An explicit file collects every selected service into that caller-owned destination.
		if strings.TrimSpace(configPath) != "" {
			targets := []workspaceSyncTarget{{Path: configPath, Services: services}}
			moves := make([]workspaceSyncMove, 0)
			sourceIndexes := make(map[string]int)
			for _, service := range services {
				ownerPath, ownerKey, found, findErr := findWorkspaceServiceConfigOwner(service, snapshot.Visibility)
				// Invalid local workspace documents cannot be ignored while checking duplicate ownership.
				if findErr != nil {
					return nil, nil, findErr
				}
				// A caller-selected destination explicitly authorizes a local ownership transfer without changing Engine state.
				if found && !sameWorkspaceSyncPath(ownerPath, configPath) {
					index, exists := sourceIndexes[ownerPath]
					// Each source file appears once even when several of its declarations move together.
					if !exists {
						index = len(targets)
						sourceIndexes[ownerPath] = index
						targets = append(targets, workspaceSyncTarget{Path: ownerPath, RemoveKeys: map[string]bool{}})
					}
					targets[index].RemoveKeys[ownerKey] = true
					moves = append(moves, workspaceSyncMove{SourcePath: ownerPath, TargetPath: configPath, ServiceKey: ownerKey})
				}
			}
			return targets, moves, nil
		}
		targetsByPath := make(map[string]*workspaceSyncTarget, len(services))
		claimedPaths := make(map[string]string, len(services))
		for _, service := range services {
			target, _, found, findErr := findWorkspaceServiceConfigOwner(service, snapshot.Visibility)
			// Local discovery failure prevents a safe choice between update and create.
			if findErr != nil {
				return nil, nil, findErr
			}
			// A previously unmanaged service receives one deterministic type: services file.
			if !found {
				key, keyErr := workspaceServiceConfigKey(service, snapshot.Visibility)
				// Canonical identity is required before the CLI can derive a stable local filename.
				if keyErr != nil {
					return nil, nil, keyErr
				}
				target, keyErr = workspaceSyncServiceFilePath(key, service.ServiceID, claimedPaths)
				// Filename derivation must preserve one-file-per-service ownership even when readable slugs collide.
				if keyErr != nil {
					return nil, nil, keyErr
				}
			}
			entry := targetsByPath[target]
			// Repeated exact-version selectors intentionally share their service's one destination.
			if entry == nil {
				entry = &workspaceSyncTarget{Path: target}
				targetsByPath[target] = entry
			}
			entry.Services = append(entry.Services, service)
		}
		targetPaths := make([]string, 0, len(targetsByPath))
		for path := range targetsByPath {
			targetPaths = append(targetPaths, path)
		}
		sort.Strings(targetPaths)
		targets := make([]workspaceSyncTarget, 0, len(targetPaths))
		for _, path := range targetPaths {
			targets = append(targets, *targetsByPath[path])
		}
		return targets, nil, nil
	}
	// File-only sync refreshes exactly the services already declared in that document.
	if strings.TrimSpace(configPath) != "" {
		// A missing scoped file cannot identify any services and should not silently become empty YAML.
		if _, err := os.Stat(configPath); err != nil {
			return nil, nil, fmt.Errorf("workspace sync file %s is unavailable: %w", configPath, err)
		}
		return []workspaceSyncTarget{{Path: configPath, SelectFromFile: true}}, nil, nil
	}
	paths, err := configfile.DiscoverWorkspaceConfigPaths(".fused")
	// Default sync is intentionally local-first: discovery failure cannot expand scope to all Engine services.
	if err != nil {
		return nil, nil, err
	}
	// UI-only workspaces have nothing local to refresh until --service or --all is requested.
	if len(paths) == 0 {
		return nil, nil, fmt.Errorf("no local workspace config found; use --service to create one or --all to import every active service")
	}
	targets := make([]workspaceSyncTarget, 0, len(paths))
	for _, path := range paths {
		targets = append(targets, workspaceSyncTarget{Path: path, SelectFromFile: true})
	}
	return targets, nil, nil
}

// sameWorkspaceSyncPath compares absolute cleaned paths so mixed relative and absolute spellings cannot trigger a self-move.
func sameWorkspaceSyncPath(left, right string) bool {
	leftPath, leftErr := filepath.Abs(left)
	rightPath, rightErr := filepath.Abs(right)
	// Failure to canonicalize either path must fail closed as non-equivalence.
	if leftErr != nil || rightErr != nil {
		return false
	}
	return filepath.Clean(leftPath) == filepath.Clean(rightPath)
}

// workspaceSyncServiceFilePath keeps readable default names while disambiguating collisions with stable service identity.
func workspaceSyncServiceFilePath(key, serviceID string, claimed map[string]string) (string, error) {
	fileName := safeConfigFileName(strings.TrimPrefix(key, "@"))
	// An unusable canonical key cannot become a hidden or directory-valued services path.
	if fileName == "" {
		return "", fmt.Errorf("workspace service %s has no safe local filename", serviceID)
	}
	target := filepath.Join(".fused", "services", fileName+".yaml")
	owner := claimed[target]
	collides := owner != "" && owner != serviceID
	// An existing non-empty file cannot receive an undeclared service implicitly; --file is required for intentional grouping.
	if !collides {
		if info, statErr := os.Stat(target); statErr == nil && !info.IsDir() {
			cfg, loadErr := loadWorkspaceSyncTarget(target, true)
			// Invalid existing content must be repaired instead of bypassed with a suffixed path.
			if loadErr != nil {
				return "", loadErr
			}
			collides = len(cfg.Services) > 0
		} else if statErr != nil && !os.IsNotExist(statErr) {
			return "", statErr
		}
	}
	// A colliding readable slug gains the complete immutable identity so distinct services cannot share a default file.
	if collides {
		suffix := safeConfigFileName(serviceID)
		// Stable service identity must also be usable as a local filename component.
		if suffix == "" {
			return "", fmt.Errorf("workspace service %s has no safe local filename suffix", serviceID)
		}
		target = filepath.Join(".fused", "services", fileName+"-"+suffix+".yaml")
		// A second collision at the identity-qualified path is ambiguous and must not silently group declarations.
		if existingOwner := claimed[target]; existingOwner != "" && existingOwner != serviceID {
			return "", fmt.Errorf("workspace services %s and %s resolve to the same local filename", existingOwner, serviceID)
		}
	}
	claimed[target] = serviceID
	return target, nil
}

// loadWorkspaceSyncTarget reads an existing document or initializes the requested local document type.
func loadWorkspaceSyncTarget(path string, servicesDocument bool) (*configfile.WorkspaceConfig, error) {
	_, statErr := os.Stat(path)
	missing := os.IsNotExist(statErr)
	// Filesystem errors other than absence must fail before the generic loader can treat the path as creatable.
	if statErr != nil && !missing {
		return nil, statErr
	}
	_, cfg, err := loadWorkspaceConfigForSync(path)
	// Parse and read failures must not be hidden by document-type initialization.
	if err != nil {
		return nil, err
	}
	// Newly created scoped files use type: services; existing documents retain their authored discriminator.
	if missing && servicesDocument {
		cfg.BaseConfig = configfile.BaseConfig{APIVersion: configfile.APIVersionV1, Type: configfile.KindServices}
	}
	return cfg, err
}

// resolveWorkspaceSyncServices resolves active workspace services by immutable ID, canonical slug, or unambiguous display name.
func resolveWorkspaceSyncServices(refs []string, snapshot workspaceSyncSnapshot) ([]api.WorkspaceService, error) {
	requests := make(map[string]*workspaceSyncServiceRequest)
	order := make([]string, 0, len(refs))
	for _, rawRef := range refs {
		selector, selectorErr := parseServiceSelector(rawRef, false)
		// Shared selector grammar keeps sync consistent with init and extend before remote matching begins.
		if selectorErr != nil {
			return nil, selectorErr
		}
		ref := selector.name
		matches := make([]api.WorkspaceService, 0, 1)
		for _, service := range snapshot.Services {
			key, keyErr := workspaceServiceConfigKey(service, snapshot.Visibility)
			// Missing Registry slug identity is a snapshot integrity failure, not a non-match.
			if keyErr != nil {
				return nil, keyErr
			}
			// All supported identities are exact and case-insensitive; sync never performs Registry discovery.
			if strings.EqualFold(ref, service.ServiceID) || strings.EqualFold(ref, service.ServiceName) || strings.EqualFold(ref, service.ServiceSlug) || strings.EqualFold(ref, key) {
				matches = append(matches, service)
			}
		}
		// Only active workspace services can be reconstructed by sync.
		if len(matches) == 0 {
			return nil, fmt.Errorf("workspace service %q is not active", ref)
		}
		// Ambiguous display names require a canonical slug or immutable ID rather than an interactive guess.
		if len(matches) > 1 {
			return nil, fmt.Errorf("workspace service %q is ambiguous; use a canonical slug or service ID", ref)
		}
		service := matches[0]
		request := requests[service.ServiceID]
		// The first alias establishes deterministic output order and the complete remote service identity.
		if request == nil {
			request = &workspaceSyncServiceRequest{service: service, seenVersion: map[string]bool{}}
			requests[service.ServiceID] = request
			order = append(order, service.ServiceID)
		}
		// An unversioned selector intentionally widens this service to every active version.
		if selector.version == "" {
			request.allVersions = true
			request.versions = nil
			continue
		}
		// Once all versions are selected, narrower aliases cannot reduce the established scope.
		if request.allVersions {
			continue
		}
		// Repeated exact selectors are idempotent while preserving first-seen version order.
		if !request.seenVersion[selector.version] {
			request.versions = append(request.versions, selector.version)
			request.seenVersion[selector.version] = true
		}
	}
	selected := make([]api.WorkspaceService, 0, len(order))
	for _, serviceID := range order {
		request := requests[serviceID]
		// Unversioned selections retain the complete active Engine projection.
		if request.allVersions {
			selected = append(selected, request.service)
			continue
		}
		service, err := selectWorkspaceServiceVersions(request.service, request.versions)
		// Every exact version must already be active because sync cannot create Engine state.
		if err != nil {
			return nil, err
		}
		selected = append(selected, service)
	}
	return selected, nil
}

// selectWorkspaceServiceVersions narrows one remote service projection to requested active versions without changing Engine state.
func selectWorkspaceServiceVersions(service api.WorkspaceService, requested []string) (api.WorkspaceService, error) {
	// Internal callers must never turn an empty exact selection into an index panic or accidental full-service pull.
	if len(requested) == 0 {
		return api.WorkspaceService{}, fmt.Errorf("workspace service %q requires at least one selected version", service.ServiceName)
	}
	wanted := make(map[string]bool, len(requested))
	for _, version := range requested {
		wanted[version] = true
	}
	selected := make([]api.WorkspaceServiceVersion, 0, len(requested))
	seen := make(map[string]bool, len(requested))
	for _, version := range service.EnabledVersions {
		// Remote order remains authoritative, but only explicitly selected active versions enter the local projection.
		if wanted[version.Version] {
			selected = append(selected, version)
			seen[version.Version] = true
		}
	}
	// Some Engine versions expose the primary active version separately from an incomplete enabled_versions list.
	if wanted[service.Version] && !seen[service.Version] {
		selected = append(selected, api.WorkspaceServiceVersion{Version: service.Version, ServiceVersionID: service.ServiceVersionID})
		seen[service.Version] = true
	}
	for _, version := range requested {
		// Sync rejects inactive versions instead of turning a pull into activation or speculative local intent.
		if !seen[version] {
			return api.WorkspaceService{}, fmt.Errorf("workspace service %q version %q is not active", service.ServiceName, version)
		}
	}
	service.EnabledVersions = selected
	service.Version = selected[0].Version
	service.ServiceVersionID = selected[0].ServiceVersionID
	return service, nil
}

// findWorkspaceServiceConfigOwner locates the exact existing declaration and key before sync creates or transfers ownership.
func findWorkspaceServiceConfigOwner(service api.WorkspaceService, visibility map[string]api.ServiceVisibility) (string, string, bool, error) {
	paths, err := configfile.DiscoverWorkspaceConfigPaths(".fused")
	// Discovery errors make duplicate ownership checks incomplete.
	if err != nil {
		return "", "", false, err
	}
	key, err := workspaceServiceConfigKey(service, visibility)
	// Canonical identity is required for legacy entries that have no stored service ID yet.
	if err != nil {
		return "", "", false, err
	}
	foundPath := ""
	foundKey := ""
	for _, path := range paths {
		parsed, parseErr := configfile.ParseFile(path)
		// Invalid local workspace files must be repaired instead of bypassed by creating a duplicate.
		if parseErr != nil {
			return "", "", false, parseErr
		}
		for localKey, localService := range parsed.Workspace.Services {
			// Stable ID wins, while the canonical key supports concise pre-sync declarations.
			if localService.ServiceID != service.ServiceID && localKey != key {
				continue
			}
			// More than one matching file is ambiguous regardless of whether their keys differ.
			if foundPath != "" && filepath.Clean(foundPath) != filepath.Clean(path) {
				return "", "", false, fmt.Errorf("workspace service %s is declared in both %s and %s", key, foundPath, path)
			}
			foundPath = path
			foundKey = localKey
		}
	}
	return foundPath, foundKey, foundPath != "", nil
}

// selectWorkspaceServicesForConfig restricts a default pull to local declarations and reports declarations absent from Engine state.
func selectWorkspaceServicesForConfig(cfg *configfile.WorkspaceConfig, snapshot workspaceSyncSnapshot, missing []string) ([]api.WorkspaceService, []string, error) {
	byID := make(map[string]api.WorkspaceService, len(snapshot.Services))
	byKey := make(map[string]api.WorkspaceService, len(snapshot.Services))
	for _, service := range snapshot.Services {
		key, err := workspaceServiceConfigKey(service, snapshot.Visibility)
		// Every compared remote service needs canonical slug identity.
		if err != nil {
			return nil, missing, err
		}
		byID[service.ServiceID] = service
		byKey[key] = service
	}
	selected := make([]api.WorkspaceService, 0, len(cfg.Services))
	seen := make(map[string]bool)
	for localKey, localService := range cfg.Services {
		service, exists := byID[localService.ServiceID]
		// Slug-key lookup supports concise declarations before their first resolution.
		if !exists {
			service, exists = byKey[localKey]
		}
		// Remote absence is drift to report, never authority to erase local intent.
		if !exists {
			missing = append(missing, localKey)
			continue
		}
		// Aliased local keys cannot cause the same remote service to be merged twice.
		if !seen[service.ServiceID] {
			selected = append(selected, service)
			seen[service.ServiceID] = true
		}
	}
	return selected, missing, nil
}

// workspaceProfilesForServices filters the global safe profile snapshot to exact selected service versions while retaining integrity checks.
func workspaceProfilesForServices(profiles []api.WorkspaceConnectionProfile, services, activeServices []api.WorkspaceService) ([]api.WorkspaceConnectionProfile, error) {
	selectedIDs := make(map[string]bool, len(services))
	selectedVersionIDs := make(map[string]map[string]bool, len(services))
	for _, service := range services {
		selectedIDs[service.ServiceID] = true
		selectedVersionIDs[service.ServiceID] = workspaceEnabledVersionIDs(service)
	}
	activeVersionIDs := make(map[string]map[string]bool, len(activeServices))
	for _, service := range activeServices {
		activeVersionIDs[service.ServiceID] = workspaceEnabledVersionIDs(service)
	}
	out := make([]api.WorkspaceConnectionProfile, 0)
	for _, profile := range profiles {
		// Unselected service profiles must not require or mutate unrelated local declarations.
		if !selectedIDs[profile.ServiceID] {
			continue
		}
		// Profiles for active but unselected versions are valid remote state outside this exact pull scope.
		if !selectedVersionIDs[profile.ServiceID][profile.ServiceVersionID] && activeVersionIDs[profile.ServiceID][profile.ServiceVersionID] {
			continue
		}
		// A profile outside the complete active snapshot remains an integrity failure rather than disappearing during filtering.
		if !activeVersionIDs[profile.ServiceID][profile.ServiceVersionID] {
			return nil, fmt.Errorf("workspace sync received a connection profile for an inactive version of service_id %s", profile.ServiceID)
		}
		out = append(out, profile)
	}
	return out, nil
}

// uniqueSortedStrings produces deterministic command targets and summaries without duplicate aliases.
func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		// First occurrence is sufficient because output is sorted after collection.
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

// mergeWorkspaceStateFromEngine reconstructs the complete non-secret remote workspace state in memory without publishing a file.
func mergeWorkspaceStateFromEngine(client *api.Client, cfg *configfile.WorkspaceConfig) (workspaceSyncResult, int, error) {
	snapshot, err := fetchWorkspaceSyncSnapshot(client)
	// Service inventory and its policy projections must be captured consistently before merge.
	if err != nil {
		return workspaceSyncResult{}, 0, err
	}
	return mergeWorkspaceSnapshot(cfg, snapshot, snapshot.Services)
}

// fetchWorkspaceSyncSnapshot reads every non-secret source needed to reconstruct selected workspace services.
func fetchWorkspaceSyncSnapshot(client *api.Client) (workspaceSyncSnapshot, error) {
	remote, err := client.ListWorkspaceServices()
	// An incomplete membership page cannot safely drive local import decisions.
	if err != nil {
		return workspaceSyncSnapshot{}, err
	}
	profiles, err := client.ListWorkspaceConnectionProfiles()
	// Connection profiles must move with their services so a sync cannot silently lose routing intent.
	if err != nil {
		return workspaceSyncSnapshot{}, err
	}
	visibility, err := client.ServiceVisibilities(serviceIDsFromWorkspaceServices(remote))
	// Provider ownership controls which policy fields can be round-tripped into desired state.
	if err != nil {
		return workspaceSyncSnapshot{}, err
	}
	versionsByServiceID, err := fetchOwnedServiceVersions(client, remote, visibility)
	// Owned version policy is required for a complete selected projection and cannot be guessed locally.
	if err != nil {
		return workspaceSyncSnapshot{}, err
	}
	return workspaceSyncSnapshot{Services: remote, Profiles: profiles, Visibility: visibility, VersionsByServiceID: versionsByServiceID}, nil
}

// mergeWorkspaceSnapshot projects only selected services and their profiles into one local document.
func mergeWorkspaceSnapshot(cfg *configfile.WorkspaceConfig, snapshot workspaceSyncSnapshot, selected []api.WorkspaceService) (workspaceSyncResult, int, error) {
	result, err := mergeWorkspaceServicesFromRemote(cfg, selected, snapshot.Visibility, snapshot.VersionsByServiceID)
	// Conflicting remote identity must fail closed before profiles or the local file are changed.
	if err != nil {
		return workspaceSyncResult{}, 0, err
	}
	profiles, profileSelectErr := workspaceProfilesForServices(snapshot.Profiles, selected, snapshot.Services)
	// Exact version scope filters valid sibling profiles while retaining inactive-profile safety checks.
	if profileSelectErr != nil {
		return workspaceSyncResult{}, 0, profileSelectErr
	}
	profileUpdates, err := mergeWorkspaceConnectionProfilesFromRemote(cfg, selected, profiles)
	// Invalid profile bindings cannot be published as an otherwise successful workspace sync.
	if err != nil {
		return workspaceSyncResult{}, 0, err
	}
	result.Updated = mergeWorkspaceSyncUpdates(result, profileUpdates)
	return result, len(profiles), nil
}

// mergeWorkspaceStateForAdditiveInit unions live Engine membership into authored local intent without turning init into a removal command.
func mergeWorkspaceStateForAdditiveInit(client *api.Client, cfg *configfile.WorkspaceConfig) error {
	remote := &configfile.WorkspaceConfig{
		BaseConfig: configfile.BaseConfig{APIVersion: configfile.APIVersionV1, Kind: configfile.KindWorkspace},
		Services:   map[string]configfile.WorkspaceService{},
	}
	_, _, err := mergeWorkspaceStateFromEngine(client, remote)
	// A partial remote snapshot cannot safely establish the minimum live membership that init must preserve.
	if err != nil {
		return err
	}
	// Older or minimally authored files may omit the map even though additive merge requires a writable destination.
	if cfg.Services == nil {
		cfg.Services = map[string]configfile.WorkspaceService{}
	}
	localKeysByID := workspaceServiceKeysByID(cfg.Services)
	for remoteKey, remoteService := range remote.Services {
		localKey := remoteKey
		// Stable service identity preserves an authored key when Registry visibility changes its canonical qualification.
		if keyByID, exists := localKeysByID[remoteService.ServiceID]; exists {
			localKey = keyByID
		}
		localService, exists := cfg.Services[localKey]
		// A wholly absent live service is copied into the draft so applying another addition cannot remove it.
		if !exists {
			cfg.Services[remoteKey] = remoteService
			continue
		}
		merged, mergeErr := mergeWorkspaceServiceForAdditiveInit(localKey, localService, remoteService)
		// Conflicting immutable identities fail before the workspace plan can observe an unsafe draft.
		if mergeErr != nil {
			return mergeErr
		}
		cfg.Services[localKey] = merged
	}
	return nil
}

// mergeWorkspaceServiceForAdditiveInit retains authored policy while restoring every currently enabled remote version identity.
func mergeWorkspaceServiceForAdditiveInit(key string, local, remote configfile.WorkspaceService) (configfile.WorkspaceService, error) {
	// Equal config keys cannot silently represent different Registry services.
	if local.ServiceID != "" && remote.ServiceID != "" && local.ServiceID != remote.ServiceID {
		return configfile.WorkspaceService{}, fmt.Errorf("workspace service %s conflicts with live service identity %s", key, remote.ServiceID)
	}
	// A missing local identity is safely completed from the live Engine snapshot.
	if local.ServiceID == "" {
		local.ServiceID = remote.ServiceID
	}
	// Omitted authored visibility or policy inherits the live value, while explicit local intent remains authoritative for the pending plan.
	if local.Public == nil {
		local.Public = remote.Public
	}
	if local.ExecutionPolicy == nil {
		local.ExecutionPolicy = remote.ExecutionPolicy
	}
	localVersions := make(map[string]int, len(local.Versions))
	for index, version := range local.Versions {
		localVersions[version.Version] = index
	}
	for _, remoteVersion := range remote.Versions {
		index, exists := localVersions[remoteVersion.Version]
		// Missing live versions must be retained verbatim so an unrelated init cannot deactivate them.
		if !exists {
			local.Versions = append(local.Versions, remoteVersion)
			continue
		}
		localVersion := local.Versions[index]
		// Reused version labels with different immutable IDs are unsafe to reconcile automatically.
		if localVersion.ServiceVersionID != "" && remoteVersion.ServiceVersionID != "" && localVersion.ServiceVersionID != remoteVersion.ServiceVersionID {
			return configfile.WorkspaceService{}, fmt.Errorf("workspace service %s version %s conflicts with live version identity %s", key, remoteVersion.Version, remoteVersion.ServiceVersionID)
		}
		// Missing identity and remote-derived fields are filled without overwriting explicit local desired state.
		if localVersion.ServiceVersionID == "" {
			localVersion.ServiceVersionID = remoteVersion.ServiceVersionID
		}
		if localVersion.Public == nil {
			localVersion.Public = remoteVersion.Public
		}
		if localVersion.ExecutionPolicy == nil {
			localVersion.ExecutionPolicy = remoteVersion.ExecutionPolicy
		}
		if len(localVersion.ConnectionProfiles) == 0 {
			localVersion.ConnectionProfiles = remoteVersion.ConnectionProfiles
		}
		local.Versions[index] = localVersion
	}
	return local, nil
}

// mergeWorkspaceSyncUpdates adds connect changes to the service-level result
// without reporting newly-added services twice.
func mergeWorkspaceSyncUpdates(result workspaceSyncResult, updates []string) []string {
	merged := append([]string{}, result.Updated...)
	for _, key := range updates {
		// An added service already communicates the complete file change, while
		// existing updates should appear at most once in command output.
		if containsString(result.Added, key) || containsString(merged, key) {
			continue
		}
		merged = append(merged, key)
	}
	sort.Strings(merged)
	return merged
}

// recordWorkspaceSyncWrite attaches mutation counts to the command span and
// emits an audit event only when the user-triggered sync changed local YAML.
func recordWorkspaceSyncWrite(ctx context.Context, result workspaceSyncResult, connectionProfileCount int, movedDeclarations bool) {
	span := trace.SpanFromContext(ctx)
	// A source file changed during regrouping even though service removal is not part of the user-facing sync result.
	changed := movedDeclarations || len(result.Added)+len(result.Updated) > 0
	span.SetAttributes(
		attribute.String("user_action", "workspace.sync"),
		attribute.Bool("config_changed", changed),
		attribute.Int("service_added_count", len(result.Added)),
		attribute.Int("service_updated_count", len(result.Updated)),
		attribute.Int("connection_profile_count", connectionProfileCount),
	)
	if changed {
		span.AddEvent("workspace_config_written")
	}
}

// fetchOwnedServiceVersions batches one ServiceVersions call per owned
// service so workspaceServiceFromRemote can round-trip version_policies.
// Only owned services are fetched: version_policies (like the service-level
// Public/ExecutionPolicy fields it sits beside) can only ever be set for
// services this workspace owns, so there's nothing to round-trip for a
// service enabled from another provider.
func fetchOwnedServiceVersions(client *api.Client, remote []api.WorkspaceService, visibility map[string]api.ServiceVisibility) (map[string][]api.ServiceVersion, error) {
	out := map[string][]api.ServiceVersion{}
	for _, svc := range remote {
		if _, done := out[svc.ServiceID]; done {
			continue
		}
		vis, ok := visibility[svc.ServiceID]
		if !ok || !vis.IsOwner || strings.TrimSpace(vis.Slug) == "" {
			continue
		}
		versions, err := client.ServiceVersions(vis.Slug)
		if err != nil {
			return nil, err
		}
		out[svc.ServiceID] = versions
	}
	return out, nil
}

// serviceIDsFromWorkspaceServices builds one Registry GraphQL batch input so
// visibility and slug lookup never becomes an N+1 request pattern.
func serviceIDsFromWorkspaceServices(services []api.WorkspaceService) []string {
	out := make([]string, 0, len(services))
	for _, svc := range services {
		if svc.ServiceID != "" {
			out = append(out, svc.ServiceID)
		}
	}
	return out
}

// printWorkspaceSyncResult renders the already-computed diff without reading
// the file again or duplicating merge decisions.
func printWorkspaceSyncResult(cmd *cobra.Command, result workspaceSyncResult) {
	// A clean result includes no inactive local declarations as well as no file mutations.
	if len(result.Added) == 0 && len(result.Updated) == 0 && len(result.Missing) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Workspace config already in sync.")
		return
	}
	for _, name := range result.Added {
		fmt.Fprintf(cmd.OutOrStdout(), "+ added %s\n", name)
	}
	for _, name := range result.Updated {
		fmt.Fprintf(cmd.OutOrStdout(), "~ updated %s\n", name)
	}
	for _, name := range result.Missing {
		fmt.Fprintf(cmd.OutOrStdout(), "! inactive remotely; retained %s\n", name)
	}
}

// init registers workspace sync under the existing workspace command tree.
func init() {
	workspaceSyncCmd.Flags().StringSliceVar(&workspaceSyncServices, "service", nil, "Active service as <service>[@<version>]; comma-separated or repeatable")
	workspaceSyncCmd.Flags().BoolVar(&workspaceSyncAll, "all", false, "Explicitly import every active workspace service")
	workspaceCmd.AddCommand(workspaceSyncCmd)
}
