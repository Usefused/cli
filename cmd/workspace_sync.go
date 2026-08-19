package cmd

import (
	"context"
	"fmt"
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
	Removed []string
}

// mergeWorkspaceServicesFromRemote is Task 4's core logic
// (engine_workspace_registration_plan.md): full-mirrors the Engine GraphQL
// workspaceServices list into cfg.Services --
// every remote workspace service is added or updated locally with the
// Engine's data winning on any conflict, and any local entry whose service
// is no longer enabled remotely is removed, not just flagged. Pure and
// side-effect-free (no file or network I/O) so it's directly testable.
func mergeWorkspaceServicesFromRemote(cfg *configfile.WorkspaceConfig, remote []api.WorkspaceService, visibility map[string]api.ServiceVisibility, versionsByServiceID map[string][]api.ServiceVersion) (workspaceSyncResult, error) {
	if cfg.Services == nil {
		cfg.Services = map[string]configfile.WorkspaceService{}
	}
	var result workspaceSyncResult

	remoteByName, err := workspaceServicesByConfigKey(remote, visibility)
	if err != nil {
		return result, err
	}
	localByServiceID := workspaceServicesByID(cfg.Services)

	// Remove local entries no longer enabled remotely.
	for name := range cfg.Services {
		if _, ok := remoteByName[name]; !ok {
			delete(cfg.Services, name)
			result.Removed = append(result.Removed, name)
		}
	}

	// Add/update from remote -- remote always wins over whatever was there.
	for _, svc := range remote {
		key, changed, err := mergeRemoteWorkspaceService(cfg.Services, svc, visibility, versionsByServiceID, localByServiceID)
		if err != nil {
			return result, err
		}
		switch changed {
		case workspaceSyncAdded:
			result.Added = append(result.Added, key)
		case workspaceSyncUpdated:
			result.Updated = append(result.Updated, key)
		}
	}

	sort.Strings(result.Added)
	sort.Strings(result.Updated)
	sort.Strings(result.Removed)
	return result, nil
}

// workspaceSyncChange keeps add/update reporting separate from service data.
type workspaceSyncChange int

const (
	workspaceSyncUnchanged workspaceSyncChange = iota
	workspaceSyncAdded
	workspaceSyncUpdated
)

// workspaceServicesByConfigKey resolves every remote service to its canonical
// YAML key before destructive full-mirror removal decisions are made.
func workspaceServicesByConfigKey(remote []api.WorkspaceService, visibility map[string]api.ServiceVisibility) (map[string]api.WorkspaceService, error) {
	byName := make(map[string]api.WorkspaceService, len(remote))
	for _, svc := range remote {
		key, err := workspaceServiceConfigKey(svc, visibility)
		if err != nil {
			return nil, err
		}
		byName[key] = svc
	}
	return byName, nil
}

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
	if err != nil {
		return "", workspaceSyncUnchanged, err
	}
	newEntry := workspaceServiceFromRemote(svc, visibility, versionsByServiceID)
	// Runtime config is local deployment intent, not Engine inventory. Sync
	// refreshes the Engine-owned identity/version fields while keeping
	// bucket/connect/webhook settings that a later apply must not erase.
	existing, existed := services[key]
	if !existed {
		existing = localByServiceID[svc.ServiceID]
	}
	newEntry = workspaceServiceWithLocalState(newEntry, existing)
	services[key] = newEntry
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
		// ConnectionProfiles: carried forward as a placeholder in case the
		// separate connect-config merge pass has nothing for this version;
		// that pass overwrites it later if remote does have data.
		if len(existing.ConnectionProfiles) > 0 {
			remote.Versions[i].ConnectionProfiles = existing.ConnectionProfiles
		}
		// Public is deliberately NOT copied from existing here -- see doc
		// comment above: it always reflects the value workspaceServiceVersionsFromRemote
		// just derived from live Registry state, never a stale local one.
	}
	return remote
}

// mergeWorkspaceConnectConfigsFromRemote mirrors exportable routing policy
// (connection profiles). Bucket-owned OAuth app registration is no longer a
// workspace.yaml concept at all -- it's registered directly via `fused-cli
// connect set <slug>` -- so there is nothing bucket-scoped left to strip here.
func mergeWorkspaceConnectConfigsFromRemote(cfg *configfile.WorkspaceConfig, services []api.WorkspaceService, configs []api.WorkspaceConnectConfig) ([]string, error) {
	remoteServices := remoteWorkspaceServicesByID(services)
	serviceKeys := workspaceServiceKeysByID(cfg.Services)
	for _, remoteConfig := range configs {
		if serviceKeys[remoteConfig.ServiceID] == "" {
			return nil, fmt.Errorf("workspace sync received connect config for inactive service_id %s", remoteConfig.ServiceID)
		}
	}
	updated, err := mergeWorkspaceConnectionProfilesFromRemote(cfg, remoteServices, serviceKeys, configs)
	if err != nil {
		return nil, err
	}
	sort.Strings(updated)
	return uniqueStrings(updated), nil
}

// workspaceServiceKeysByID indexes the already-synced service map so remote
// profile/config rows can be validated without scanning every service per row.
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

// mergeWorkspaceConnectionProfilesFromRemote splices each version's routing
// policy onto the matching entry in that service's already-merged Versions
// list (by service_version_id), even when several buckets return the same
// effective profile snapshot for that service/auth tuple. Connection profiles
// used to be one flat service-level list carrying its own `version` field per
// entry; now that Versions is itself the per-version container, an entry
// only needs auth_type to disambiguate itself from siblings on the same
// version. Exact scheme names belong to bucket Connect config, while the
// effective routing profile remains shared by authentication family.
func mergeWorkspaceConnectionProfilesFromRemote(cfg *configfile.WorkspaceConfig, services map[string]api.WorkspaceService, serviceKeys map[string]string, configs []api.WorkspaceConnectConfig) ([]string, error) {
	grouped, err := workspaceConnectionProfilesByServiceVersion(services, serviceKeys, configs)
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
			if !ok || reflect.DeepEqual(service.Versions[i].ConnectionProfiles, profiles) {
				continue
			}
			service.Versions[i].ConnectionProfiles = profiles
			changed = true
		}
		if !changed {
			continue
		}
		cfg.Services[key] = service
		updated = append(updated, key)
	}
	return updated, nil
}

// workspaceConnectionProfilesByServiceVersion validates profile/version
// ownership in one pass, de-duplicates rows repeated through multiple bucket
// configs, and groups by the exact service_version_id each profile attaches
// to so the caller can splice profiles onto the right Versions entry instead
// of a flat service-level list.
func workspaceConnectionProfilesByServiceVersion(services map[string]api.WorkspaceService, serviceKeys map[string]string, configs []api.WorkspaceConnectConfig) (map[string]map[string][]map[string]interface{}, error) {
	grouped := map[string]map[string][]map[string]interface{}{}
	seen := map[string]bool{}
	for _, config := range configs {
		if serviceKeys[config.ServiceID] == "" && len(config.Profiles) > 0 {
			return nil, fmt.Errorf("workspace sync received a connection profile for inactive service_id %s", config.ServiceID)
		}
		enabledVersionIDs := workspaceEnabledVersionIDs(services[config.ServiceID])
		for _, profile := range config.Profiles {
			if !enabledVersionIDs[profile.ServiceVersionID] {
				return nil, fmt.Errorf("workspace sync received a connection profile for an inactive version of service_id %s", config.ServiceID)
			}
			dedupeKey := config.ServiceID + "\x00" + profile.ServiceVersionID + "\x00" + profile.AuthType
			if seen[dedupeKey] {
				continue
			}
			seen[dedupeKey] = true
			if grouped[config.ServiceID] == nil {
				grouped[config.ServiceID] = map[string][]map[string]interface{}{}
			}
			grouped[config.ServiceID][profile.ServiceVersionID] = append(grouped[config.ServiceID][profile.ServiceVersionID], workspaceConnectionProfileIntent(profile))
		}
	}
	return grouped, nil
}

// workspaceEnabledVersionIDs requires resolved IDs because connection
// profiles attach to immutable service versions, not mutable display names.
func workspaceEnabledVersionIDs(service api.WorkspaceService) map[string]bool {
	ids := make(map[string]bool, len(service.EnabledVersions))
	for _, version := range service.EnabledVersions {
		if version.ServiceVersionID != "" {
			ids[version.ServiceVersionID] = true
		}
	}
	return ids
}

// workspaceConnectionProfileIntent preserves Registry identity when present;
// only workspace-local attachments need to carry inline profile JSON.
// Version is deliberately absent from the intent map -- it's implied by
// which Versions entry this profile list gets attached to.
func workspaceConnectionProfileIntent(profile api.WorkspaceConnectProfile) map[string]interface{} {
	intent := map[string]interface{}{"auth_type": profile.AuthType}
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
func workspaceConnectionProfileAuthName(profile api.WorkspaceConnectProfile) string {
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

// workspaceSyncCmd full-mirrors Engine GraphQL state into local workspace YAML.
var workspaceSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Full-mirror the local workspace config from the Engine's current service state",
	Long: `Overwrites the local workspace config's services with whatever is currently
enabled remotely: adds or updates every remote workspace service (the
Engine's data wins on any conflict) and removes any local service entry
	that's no longer enabled remotely.`,
	Args: cobra.NoArgs,
	RunE: WithTelemetry("cli.workspace.sync", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		result, err := PerformWorkspaceSync(cmd.Context(), client, ConfigFile)
		if err != nil {
			return err
		}
		if result != nil {
			printWorkspaceSyncResult(cmd, *result)
		}
		return nil
	}),
}

func PerformWorkspaceSync(ctx context.Context, client *api.Client, configPath string) (*workspaceSyncResult, error) {
	path, cfg, err := loadWorkspaceConfigForSync(configPath)
	if err != nil {
		return nil, err
	}
	remote, err := client.ListWorkspaceServices()
	if err != nil {
		return nil, err
	}
	connectConfigs, err := client.ListWorkspaceConnectConfigs()
	if err != nil {
		return nil, err
	}
	visibility, err := client.ServiceVisibilities(serviceIDsFromWorkspaceServices(remote))
	if err != nil {
		return nil, err
	}
	versionsByServiceID, err := fetchOwnedServiceVersions(client, remote, visibility)
	if err != nil {
		return nil, err
	}
	result, err := mergeWorkspaceServicesFromRemote(cfg, remote, visibility, versionsByServiceID)
	if err != nil {
		return nil, err
	}
	connectUpdates, err := mergeWorkspaceConnectConfigsFromRemote(cfg, remote, connectConfigs)
	if err != nil {
		return nil, err
	}
	result.Updated = mergeWorkspaceSyncUpdates(result, connectUpdates)
	if err := writeWorkspaceConfig(path, cfg); err != nil {
		return nil, err
	}
	recordWorkspaceSyncWrite(ctx, result, len(connectConfigs))
	return &result, nil
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
func recordWorkspaceSyncWrite(ctx context.Context, result workspaceSyncResult, connectConfigCount int) {
	span := trace.SpanFromContext(ctx)
	changed := len(result.Added)+len(result.Updated)+len(result.Removed) > 0
	span.SetAttributes(
		attribute.String("user_action", "workspace.sync"),
		attribute.Bool("config_changed", changed),
		attribute.Int("service_added_count", len(result.Added)),
		attribute.Int("service_updated_count", len(result.Updated)),
		attribute.Int("service_removed_count", len(result.Removed)),
		attribute.Int("connect_config_count", connectConfigCount),
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
	if len(result.Added) == 0 && len(result.Updated) == 0 && len(result.Removed) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Workspace config already in sync.")
		return
	}
	for _, name := range result.Added {
		fmt.Fprintf(cmd.OutOrStdout(), "+ added %s\n", name)
	}
	for _, name := range result.Updated {
		fmt.Fprintf(cmd.OutOrStdout(), "~ updated %s\n", name)
	}
	for _, name := range result.Removed {
		fmt.Fprintf(cmd.OutOrStdout(), "- removed %s\n", name)
	}
}

// init registers workspace sync under the existing workspace command tree.
func init() {
	workspaceCmd.AddCommand(workspaceSyncCmd)
}
