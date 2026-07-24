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
func mergeWorkspaceServicesFromRemote(cfg *configfile.WorkspaceConfig, remote []api.WorkspaceService, visibility map[string]api.ServiceVisibility) (workspaceSyncResult, error) {
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
		key, changed, err := mergeRemoteWorkspaceService(cfg.Services, svc, visibility, localByServiceID)
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
func mergeRemoteWorkspaceService(services map[string]configfile.WorkspaceService, svc api.WorkspaceService, visibility map[string]api.ServiceVisibility, localByServiceID map[string]configfile.WorkspaceService) (string, workspaceSyncChange, error) {
	key, err := workspaceServiceConfigKey(svc, visibility)
	if err != nil {
		return "", workspaceSyncUnchanged, err
	}
	newEntry := workspaceServiceFromRemote(svc, visibility)
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
// service inventory GraphQL projection does not own those fields.
func workspaceServiceWithLocalState(remote, local configfile.WorkspaceService) configfile.WorkspaceService {
	if local.RuntimeConfig != nil {
		remote.RuntimeConfig = local.RuntimeConfig
	}
	if len(local.ConnectionProfiles) > 0 {
		remote.ConnectionProfiles = local.ConnectionProfiles
	}
	if local.ExecutionPolicy != nil {
		remote.ExecutionPolicy = local.ExecutionPolicy
	}
	if len(local.VersionPolicies) > 0 {
		remote.VersionPolicies = local.VersionPolicies
	}
	return remote
}

// mergeWorkspaceConnectConfigsFromRemote mirrors exportable routing policy but
// intentionally leaves bucket OAuth material out of YAML. The Engine never
// returns ciphertext/plaintext client credentials, and inventing $ENV refs here
// makes an unrelated service-policy apply depend on local secrets the user did
// not intend to touch.
func mergeWorkspaceConnectConfigsFromRemote(cfg *configfile.WorkspaceConfig, services []api.WorkspaceService, configs []api.WorkspaceConnectConfig) ([]string, error) {
	remoteServices := remoteWorkspaceServicesByID(services)
	serviceKeys := workspaceServiceKeysByID(cfg.Services)
	for _, remoteConfig := range configs {
		if serviceKeys[remoteConfig.ServiceID] == "" {
			return nil, fmt.Errorf("workspace sync received connect config for inactive service_id %s", remoteConfig.ServiceID)
		}
	}
	// Sync is a read/export flow, not a secret rehydration flow. Strip any old
	// bucket-owned Connect material refs so a later service-only apply does not
	// require local OAuth app secrets for unrelated, unchanged services.
	updated := stripWorkspaceBucketConnectConfigs(cfg)
	profileUpdates, err := mergeWorkspaceConnectionProfilesFromRemote(cfg, remoteServices, serviceKeys, configs)
	if err != nil {
		return nil, err
	}
	updated = append(updated, profileUpdates...)
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

func stripWorkspaceBucketConnectConfigs(cfg *configfile.WorkspaceConfig) []string {
	if cfg.Buckets == nil {
		return nil
	}
	updated := make([]string, 0)
	for bucketName, bucket := range cfg.Buckets {
		changed := stripBucketConnectConfigs(bucket.ServiceConfig, &updated)
		if !changed {
			continue
		}
		if len(bucket.ServiceConfig) == 0 {
			delete(cfg.Buckets, bucketName)
			continue
		}
		cfg.Buckets[bucketName] = bucket
	}
	return uniqueStrings(updated)
}

func stripBucketConnectConfigs(serviceConfigs map[string]configfile.BucketServiceConfig, updated *[]string) bool {
	changed := false
	for serviceKey, serviceConfig := range serviceConfigs {
		if serviceConfig.Connect == nil {
			continue
		}
		serviceConfig.Connect = nil
		changed = true
		*updated = append(*updated, serviceKey)
		if serviceConfig.Auth == nil {
			delete(serviceConfigs, serviceKey)
			continue
		}
		serviceConfigs[serviceKey] = serviceConfig
	}
	return changed
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

// mergeWorkspaceConnectionProfilesFromRemote writes service-level routing
// policy once per service, even when several buckets return the same effective
// profile snapshot for that service/auth tuple.
func mergeWorkspaceConnectionProfilesFromRemote(cfg *configfile.WorkspaceConfig, services map[string]api.WorkspaceService, serviceKeys map[string]string, configs []api.WorkspaceConnectConfig) ([]string, error) {
	grouped, err := workspaceConnectionProfilesByService(services, serviceKeys, configs)
	if err != nil {
		return nil, err
	}
	updated := make([]string, 0, len(grouped))
	for serviceID, profiles := range grouped {
		key := serviceKeys[serviceID]
		service := cfg.Services[key]
		if reflect.DeepEqual(service.ConnectionProfiles, profiles) {
			continue
		}
		service.ConnectionProfiles = profiles
		cfg.Services[key] = service
		updated = append(updated, key)
	}
	return updated, nil
}

// workspaceConnectionProfilesByService validates profile/version ownership in
// one pass and de-duplicates rows repeated through multiple bucket configs.
func workspaceConnectionProfilesByService(services map[string]api.WorkspaceService, serviceKeys map[string]string, configs []api.WorkspaceConnectConfig) (map[string][]map[string]interface{}, error) {
	grouped := map[string][]map[string]interface{}{}
	seen := map[string]bool{}
	for _, config := range configs {
		service := services[config.ServiceID]
		versionNames, err := workspaceEnabledVersionNames(service)
		if err != nil {
			return nil, err
		}
		for _, profile := range config.Profiles {
			key := config.ServiceID + "\x00" + profile.ServiceVersionID + "\x00" + profile.AuthType
			if seen[key] {
				continue
			}
			intent, err := workspaceConnectionProfileIntent(config.ServiceID, profile, versionNames)
			if err != nil {
				return nil, err
			}
			seen[key] = true
			grouped[config.ServiceID] = append(grouped[config.ServiceID], intent)
		}
		if serviceKeys[config.ServiceID] == "" && len(config.Profiles) > 0 {
			return nil, fmt.Errorf("workspace sync received a connection profile for inactive service_id %s", config.ServiceID)
		}
	}
	return grouped, nil
}

// workspaceEnabledVersionNames requires resolved IDs because connection
// profiles attach to immutable service versions, not mutable display names.
func workspaceEnabledVersionNames(service api.WorkspaceService) (map[string]string, error) {
	names := make(map[string]string, len(service.EnabledVersions))
	for _, version := range service.EnabledVersions {
		if version.ServiceVersionID == "" {
			return nil, fmt.Errorf("workspace sync requires resolved version IDs to export connection profiles for service_id %s", service.ServiceID)
		}
		names[version.ServiceVersionID] = version.Version
	}
	return names, nil
}

// workspaceConnectionProfileIntent preserves Registry identity when present;
// only workspace-local attachments need to carry inline profile JSON.
func workspaceConnectionProfileIntent(serviceID string, profile api.WorkspaceConnectProfile, versionNames map[string]string) (map[string]interface{}, error) {
	version := versionNames[profile.ServiceVersionID]
	if version == "" {
		return nil, fmt.Errorf("workspace sync received a connection profile for an inactive version of service_id %s", serviceID)
	}
	intent := map[string]interface{}{"version": version, "auth_type": profile.AuthType}
	// is_public reflects a prior successful, owner-gated publish recorded by
	// the Engine (MarkWorkspaceProfilePublished only runs after
	// PublishConnectionProfile succeeds), so sync can round-trip it here
	// without re-deriving or re-checking ownership itself.
	if profile.IsPublic {
		intent["public"] = true
	}
	if id := strings.TrimSpace(profile.RegistryProfileID); id != "" {
		intent["profile_id"] = id
		return intent, nil
	}
	intent["profile"] = profile.Profile
	return intent, nil
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
func workspaceServiceFromRemote(svc api.WorkspaceService, visibility map[string]api.ServiceVisibility) configfile.WorkspaceService {
	versions := workspaceServiceVersionNames(svc)
	if svc.Version != "" && !containsString(versions, svc.Version) {
		versions = append(append([]string{}, versions...), svc.Version)
	}
	newEntry := configfile.WorkspaceService{
		ServiceID:        svc.ServiceID,
		Versions:         versions,
		ResolvedVersions: workspaceServiceResolvedVersions(svc),
	}
	if vis, ok := visibility[svc.ServiceID]; ok && vis.IsOwner {
		newEntry.Public = boolPtr(vis.IsPublic)
		newEntry.ExecutionPolicy = workspaceExecutionPolicyFromRemote(vis)
	}
	return newEntry
}

// workspaceExecutionPolicyFromRemote round-trips a previously published
// execution policy (rate_limit/retry set on the Registry for an owned
// service) back into workspace.yaml as execution_policy.public: true, so a
// subsequent `fused apply` stays a no-op instead of silently dropping the
// published policy on the next sync. Returns nil when the Registry has no
// policy set for this service, leaving any local execution_policy untouched
// via workspaceServiceWithLocalState.
func workspaceExecutionPolicyFromRemote(vis api.ServiceVisibility) *configfile.ExecutionPolicy {
	if vis.RateLimit == nil && vis.RetryConfig == nil {
		return nil
	}
	policy := &configfile.ExecutionPolicy{Public: boolPtr(true)}
	if vis.RateLimit != nil {
		policy.RateLimit = &configfile.RateLimitConfig{
			Strategy: vis.RateLimit.Strategy, RequestsPerSecond: vis.RateLimit.RequestsPerSecond,
			RequestsPerMinute: vis.RateLimit.RequestsPerMinute,
		}
	}
	if vis.RetryConfig != nil {
		policy.Retry = &configfile.RetryConfig{
			Strategy: vis.RetryConfig.Strategy, MaxRetries: vis.RetryConfig.MaxRetries,
			BackoffMs: vis.RetryConfig.BackoffMs,
		}
	}
	return policy
}

// workspaceServiceEqual compares the fields sync actually touches.
// Versions is compared as a set so harmless remote ordering changes do not
// churn local files.
func workspaceServiceEqual(a, b configfile.WorkspaceService) bool {
	return a.ServiceID == b.ServiceID &&
		sameStringSet(a.Versions, b.Versions) &&
		sameResolvedVersions(a.ResolvedVersions, b.ResolvedVersions) &&
		sameBoolPtr(a.Public, b.Public) &&
		reflect.DeepEqual(a.RuntimeConfig, b.RuntimeConfig) &&
		reflect.DeepEqual(a.ConnectionProfiles, b.ConnectionProfiles) &&
		reflect.DeepEqual(a.ExecutionPolicy, b.ExecutionPolicy) &&
		reflect.DeepEqual(a.VersionPolicies, b.VersionPolicies)
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

// workspaceServiceVersionNames projects the version labels in Engine order;
// comparison later treats them as a set to avoid file churn.
func workspaceServiceVersionNames(svc api.WorkspaceService) []string {
	versions := make([]string, 0, len(svc.EnabledVersions))
	for _, version := range svc.EnabledVersions {
		versions = append(versions, version.Version)
	}
	return versions
}

// workspaceServiceResolvedVersions carries Engine-resolved IDs into config so
// future applies do not depend on mutable version display names.
func workspaceServiceResolvedVersions(svc api.WorkspaceService) []configfile.WorkspaceResolvedVersion {
	versions := make([]configfile.WorkspaceResolvedVersion, 0, len(svc.EnabledVersions))
	for _, version := range svc.EnabledVersions {
		if version.ServiceVersionID == "" {
			// Older Engines may not return IDs; omitting incomplete pairs keeps
			// sync backward-compatible instead of writing config that apply will
			// reject as a malformed resolved version.
			continue
		}
		versions = append(versions, configfile.WorkspaceResolvedVersion{
			Version:          version.Version,
			ServiceVersionID: version.ServiceVersionID,
		})
	}
	return versions
}

// sameResolvedVersions compares identity pairs as a set because remote order
// is not part of the workspace contract.
func sameResolvedVersions(a, b []configfile.WorkspaceResolvedVersion) bool {
	if len(a) != len(b) {
		return false
	}
	// Treat resolved versions as identity pairs rather than ordered YAML
	// slices so a harmless Engine ordering change does not churn local config.
	byVersion := make(map[string]string, len(a))
	for _, version := range a {
		byVersion[version.Version] = version.ServiceVersionID
	}
	for _, version := range b {
		if byVersion[version.Version] != version.ServiceVersionID {
			return false
		}
	}
	return true
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
	result, err := mergeWorkspaceServicesFromRemote(cfg, remote, visibility)
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
