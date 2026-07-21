package cmd

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
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

type workspaceSyncChange int

const (
	workspaceSyncUnchanged workspaceSyncChange = iota
	workspaceSyncAdded
	workspaceSyncUpdated
)

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

func workspaceServicesByID(services map[string]configfile.WorkspaceService) map[string]configfile.WorkspaceService {
	byID := make(map[string]configfile.WorkspaceService, len(services))
	for _, svc := range services {
		if svc.ServiceID != "" {
			byID[svc.ServiceID] = svc
		}
	}
	return byID
}

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

func workspaceServiceWithLocalState(remote, local configfile.WorkspaceService) configfile.WorkspaceService {
	if local.RuntimeConfig != nil {
		remote.RuntimeConfig = local.RuntimeConfig
	}
	return remote
}

func workspaceServiceConfigKey(svc api.WorkspaceService, visibility map[string]api.ServiceVisibility) (string, error) {
	if vis, ok := visibility[svc.ServiceID]; ok {
		if ref := serviceConfigRef(vis.Slug, vis.Provider); ref != "" {
			return ref, nil
		}
	}
	return "", fmt.Errorf("workspace sync missing service slug for service_id %s", svc.ServiceID)
}

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

func boolPtr(value bool) *bool {
	return &value
}

func sameBoolPtr(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

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
		visibility, err := client.ServiceVisibilities(serviceIDsFromWorkspaceServices(remote))
		if err != nil {
			return err
		}
		result, err := mergeWorkspaceServicesFromRemote(cfg, remote, visibility)
		if err != nil {
			return err
		}
		if err := writeWorkspaceConfig(path, cfg); err != nil {
			return err
		}
		printWorkspaceSyncResult(cmd, result)
		return nil
	}),
}

func serviceIDsFromWorkspaceServices(services []api.WorkspaceService) []string {
	out := make([]string, 0, len(services))
	for _, svc := range services {
		if svc.ServiceID != "" {
			out = append(out, svc.ServiceID)
		}
	}
	return out
}

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

func init() {
	workspaceCmd.AddCommand(workspaceSyncCmd)
}
