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
	return remote
}

// mergeWorkspaceConnectConfigsFromRemote replaces only the bucket connect
// portion of runtime config, leaving other local runtime settings intact. The
// Engine is authoritative for connect state because UI and CLI edits both
// persist there before a fresh checkout can have any local state to preserve.
func mergeWorkspaceConnectConfigsFromRemote(cfg *configfile.WorkspaceConfig, services []api.WorkspaceService, configs []api.WorkspaceConnectConfig) ([]string, error) {
	configsByService, err := workspaceConnectConfigsByService(configs)
	if err != nil {
		return nil, err
	}
	remoteServices := remoteWorkspaceServicesByID(services)
	updated := make([]string, 0)
	for key, service := range cfg.Services {
		remoteConfig, configured := configsByService[service.ServiceID]
		nextRuntime, err := workspaceRuntimeWithRemoteConnect(key, service.RuntimeConfig, remoteServices[service.ServiceID], remoteConfig, configured)
		if err != nil {
			return nil, err
		}
		// Only report a service when its serialized runtime config changes; this
		// keeps repeated sync executions quiet and audit counts meaningful.
		if !reflect.DeepEqual(service.RuntimeConfig, nextRuntime) {
			service.RuntimeConfig = nextRuntime
			cfg.Services[key] = service
			updated = append(updated, key)
		}
	}
	sort.Strings(updated)
	return updated, nil
}

// workspaceConnectConfigsByService enforces the one-connect-config shape the
// current workspace YAML can represent instead of silently choosing a bucket.
func workspaceConnectConfigsByService(configs []api.WorkspaceConnectConfig) (map[string]api.WorkspaceConnectConfig, error) {
	byService := make(map[string]api.WorkspaceConnectConfig, len(configs))
	for _, config := range configs {
		// Multiple bucket configs for one service cannot fit in the singular
		// runtime_config.connect field, so sync must fail without rewriting YAML.
		if _, exists := byService[config.ServiceID]; exists {
			return nil, fmt.Errorf("workspace sync cannot represent multiple connect buckets for service_id %s", config.ServiceID)
		}
		byService[config.ServiceID] = config
	}
	return byService, nil
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

// workspaceRuntimeWithRemoteConnect copies the runtime container before
// changing its connect field so callers never mutate shared nested pointers.
func workspaceRuntimeWithRemoteConnect(key string, current *configfile.RuntimeConfig, service api.WorkspaceService, remote api.WorkspaceConnectConfig, configured bool) (*configfile.RuntimeConfig, error) {
	if !configured {
		// A missing remote projection is not a deletion instruction. Preserving
		// local intent keeps sync from erasing unapplied or locally-authored config.
		return current, nil
	}
	if service.ServiceID == "" {
		return nil, fmt.Errorf("workspace sync received connect config for inactive service_id %s", remote.ServiceID)
	}
	var next configfile.RuntimeConfig
	// Preserve non-connect runtime intent because those fields are not part of
	// this Engine GraphQL read model and therefore are not safe to overwrite.
	if current != nil {
		next = *current
	}
	connect, err := workspaceConnectFromRemote(key, service, remote, localConnectForSameIdentity(current, remote))
	if err != nil {
		return nil, err
	}
	next.Connect = connect
	return &next, nil
}

// localConnectForSameIdentity returns local material references only when
// they describe the same bucket/auth tuple; stale references must not migrate
// to a different remote connection accidentally.
func localConnectForSameIdentity(runtime *configfile.RuntimeConfig, remote api.WorkspaceConnectConfig) *configfile.ConnectConfig {
	if runtime == nil || runtime.Connect == nil {
		return nil
	}
	local := runtime.Connect
	if local.Bucket != remote.BucketName || local.AuthType != remote.AuthType {
		return nil
	}
	return local
}

// workspaceConnectFromRemote projects masked GraphQL state into safe YAML.
// Credential ciphertext never leaves Engine; sync writes env references that
// operators resolve only during a later apply.
func workspaceConnectFromRemote(key string, service api.WorkspaceService, remote api.WorkspaceConnectConfig, local *configfile.ConnectConfig) (*configfile.ConnectConfig, error) {
	profile, err := uniformWorkspaceConnectProfile(service, remote)
	if err != nil {
		return nil, err
	}
	return &configfile.ConnectConfig{
		Bucket: remote.BucketName, AuthType: remote.AuthType, Enabled: boolPtr(remote.Enabled),
		ClientID:     connectMaterialEnvRef(key, "CLIENT_ID", remote.HasClientID, localClientID(local)),
		ClientSecret: connectMaterialEnvRef(key, "CLIENT_SECRET", remote.HasClientSecret, localClientSecret(local)),
		RedirectURI:  remote.RedirectURI, Profile: profile.Inline, ProfileID: profile.RegistryID,
	}, nil
}

// localClientID safely reads an optional local connect config without adding
// nil branches to the projection function.
func localClientID(connect *configfile.ConnectConfig) string {
	if connect == nil {
		return ""
	}
	return connect.ClientID
}

// localClientSecret safely reads an optional local connect config without
// exposing any Engine-side secret material.
func localClientSecret(connect *configfile.ConnectConfig) string {
	if connect == nil {
		return ""
	}
	return connect.ClientSecret
}

// connectMaterialEnvRef preserves a user's existing env variable name when
// possible and otherwise emits a deterministic placeholder for masked data.
func connectMaterialEnvRef(serviceKey, suffix string, present bool, existing string) string {
	if !present {
		return ""
	}
	if configfile.IsEnvironmentReference(existing) {
		return existing
	}
	return "$FUSED_" + workspaceEnvToken(serviceKey) + "_CONNECT_" + suffix
}

// workspaceEnvToken converts qualified service refs into portable uppercase
// environment-variable segments without leaking display names into secrets.
func workspaceEnvToken(serviceKey string) string {
	var token strings.Builder
	previousUnderscore := false
	for _, char := range strings.ToUpper(serviceKey) {
		valid := (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')
		if valid {
			token.WriteRune(char)
			previousUnderscore = false
			continue
		}
		// Collapse punctuation runs so provider-qualified refs remain readable
		// and stable across operating systems.
		if token.Len() > 0 && !previousUnderscore {
			token.WriteByte('_')
			previousUnderscore = true
		}
	}
	value := strings.Trim(token.String(), "_")
	if value == "" {
		return "SERVICE"
	}
	return value
}

type workspaceConnectProfileProjection struct {
	Inline     map[string]interface{}
	RegistryID string
}

type workspaceConnectProfileAccumulator struct {
	inline      map[string]interface{}
	registryID  string
	identitySet bool
}

// uniformWorkspaceConnectProfile returns one declarative profile selection
// that every enabled version can safely share without losing Registry lineage.
func uniformWorkspaceConnectProfile(service api.WorkspaceService, config api.WorkspaceConnectConfig) (workspaceConnectProfileProjection, error) {
	if len(config.Profiles) == 0 {
		return workspaceConnectProfileProjection{}, nil
	}
	if len(config.Profiles) != len(service.EnabledVersions) {
		return workspaceConnectProfileProjection{}, fmt.Errorf("workspace sync cannot represent partial connection profile coverage for service_id %s", service.ServiceID)
	}
	enabled, err := workspaceEnabledVersionIDs(service)
	if err != nil {
		return workspaceConnectProfileProjection{}, err
	}
	var selected workspaceConnectProfileAccumulator
	for _, profile := range config.Profiles {
		if _, ok := enabled[profile.ServiceVersionID]; !ok {
			return workspaceConnectProfileProjection{}, fmt.Errorf("workspace sync received a connection profile for an inactive version of service_id %s", service.ServiceID)
		}
		if err := selected.add(profile, service.ServiceID); err != nil {
			return workspaceConnectProfileProjection{}, err
		}
	}
	return selected.projection(), nil
}

// workspaceEnabledVersionIDs validates the resolved identities once before
// profile coverage is compared, keeping the merge loop focused and bounded.
func workspaceEnabledVersionIDs(service api.WorkspaceService) (map[string]struct{}, error) {
	enabled := make(map[string]struct{}, len(service.EnabledVersions))
	for _, version := range service.EnabledVersions {
		if version.ServiceVersionID == "" {
			return nil, fmt.Errorf("workspace sync requires resolved version IDs to export connection profiles for service_id %s", service.ServiceID)
		}
		enabled[version.ServiceVersionID] = struct{}{}
	}
	return enabled, nil
}

// add fails closed when one YAML profile selection would collapse divergent
// behavior or Registry identities from separate enabled versions.
func (a *workspaceConnectProfileAccumulator) add(profile api.WorkspaceConnectProfile, serviceID string) error {
	if a.inline == nil {
		a.inline = profile.Profile
	} else if !reflect.DeepEqual(a.inline, profile.Profile) {
		return fmt.Errorf("workspace sync cannot represent different connection profiles across versions for service_id %s", serviceID)
	}
	currentID := strings.TrimSpace(profile.RegistryProfileID)
	if a.identitySet && currentID != a.registryID {
		return fmt.Errorf("workspace sync cannot represent different connection profile identities across versions for service_id %s", serviceID)
	}
	a.registryID, a.identitySet = currentID, true
	return nil
}

// projection preserves Registry identity when present; only workspace-local
// attachments are exported as inline profile declarations.
func (a workspaceConnectProfileAccumulator) projection() workspaceConnectProfileProjection {
	if a.registryID != "" {
		return workspaceConnectProfileProjection{RegistryID: a.registryID}
	}
	return workspaceConnectProfileProjection{Inline: a.inline}
}

// workspaceServiceConfigKey requires Registry slug identity so sync never
// falls back to a mutable display name as a declarative resource key.
func workspaceServiceConfigKey(svc api.WorkspaceService, visibility map[string]api.ServiceVisibility) (string, error) {
	if vis, ok := visibility[svc.ServiceID]; ok {
		if ref := serviceConfigRef(vis.Slug, vis.Provider); ref != "" {
			return ref, nil
		}
	}
	return "", fmt.Errorf("workspace sync missing service slug for service_id %s", svc.ServiceID)
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
	}
	return newEntry
}

// workspaceServiceEqual compares the fields sync actually touches.
// Versions is compared as a set so harmless remote ordering changes do not
// churn local files.
func workspaceServiceEqual(a, b configfile.WorkspaceService) bool {
	return a.ServiceID == b.ServiceID &&
		sameStringSet(a.Versions, b.Versions) &&
		sameResolvedVersions(a.ResolvedVersions, b.ResolvedVersions) &&
		sameBoolPtr(a.Public, b.Public) &&
		reflect.DeepEqual(a.RuntimeConfig, b.RuntimeConfig)
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
		path, cfg, err := loadWorkspaceConfigForSync(ConfigFile)
		if err != nil {
			return err
		}
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		remote, err := client.ListWorkspaceServices()
		if err != nil {
			return err
		}
		connectConfigs, err := client.ListWorkspaceConnectConfigs()
		if err != nil {
			return err
		}
		visibility, err := client.ServiceVisibilities(serviceIDsFromWorkspaceServices(remote))
		if err != nil {
			return err
		}
		result, err := mergeWorkspaceServicesFromRemote(cfg, remote, visibility)
		if err != nil {
			return err
		}
		connectUpdates, err := mergeWorkspaceConnectConfigsFromRemote(cfg, remote, connectConfigs)
		if err != nil {
			return err
		}
		result.Updated = mergeWorkspaceSyncUpdates(result, connectUpdates)
		if err := writeWorkspaceConfig(path, cfg); err != nil {
			return err
		}
		recordWorkspaceSyncWrite(cmd.Context(), result, len(connectConfigs))
		printWorkspaceSyncResult(cmd, result)
		return nil
	}),
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
