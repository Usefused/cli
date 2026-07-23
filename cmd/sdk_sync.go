package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

// sdkSyncResult summarizes what mergeSDKServicesFromRemote did, so the
// command can report a diff without re-deriving it itself.
type sdkSyncResult struct {
	ArtifactVersionFrom string
	ArtifactVersionTo   string
	RemoteVersion       string
	SyncVersion         bool
	Added               []string
	Updated             []string
	Removed             []string
}

// sdkSyncRemoteService is the fully-resolved remote view of one service's
// selection on the most recently generated SDK -- select_all vs. explicit
// endpoint/webhook IDs already collapsed into concrete operation names, and
// the pinned version identity already resolved to its human-readable version
// tag (see fetchSDKSyncData).
type sdkSyncRemoteService struct {
	Name       string
	Ref        string
	Version    string
	Operations []string
}

var sdkSyncVersion bool

// mergeSDKServicesFromRemote is Task 4's core logic for `sdk sync`
// (engine_workspace_registration_plan.md): full-mirrors the most recently
// generated remote SDK's selections into cfg.Services -- every service the
// remote SDK currently selects is added or updated locally with the
// Registry's resolved data winning on any conflict, and any local service
// entry the remote SDK no longer selects is removed, not just flagged. The
// artifact version is identity-bearing, so sync only changes it when the caller
// explicitly opts in with --sync-version. Pure and side-effect-free
// (no file or network I/O) so it's directly testable, mirroring
// mergeWorkspaceServicesFromRemote's structure (workspace_sync.go).
func mergeSDKServicesFromRemote(cfg *configfile.SDKConfig, artifactVersion string, remote []sdkSyncRemoteService, syncVersion bool) (sdkSyncResult, error) {
	if cfg.Services == nil {
		cfg.Services = map[string]configfile.SDKService{}
	}
	result := newSDKSyncResult(cfg.Version, artifactVersion, syncVersion)
	if syncVersion {
		// The top-level version is part of the config identity, so default sync
		// preserves it and only changes it after an explicit operator choice.
		cfg.Version = artifactVersion
	}
	remoteByName, err := sdkSyncRemoteByName(remote)
	if err != nil {
		return result, err
	}

	// Remove local entries the remote SDK no longer selects.
	for name := range cfg.Services {
		if _, ok := remoteByName[name]; !ok {
			delete(cfg.Services, name)
			result.Removed = append(result.Removed, name)
		}
	}

	// Add/update from remote -- remote always wins over whatever was there.
	for _, svc := range remote {
		key, err := sdkSyncServiceConfigKey(svc)
		if err != nil {
			return result, err
		}
		operations := append([]string{}, svc.Operations...)
		sort.Strings(operations)
		newEntry := configfile.SDKService{
			Version:    svc.Version,
			Operations: operations,
		}
		existing, existed := cfg.Services[key]
		cfg.Services[key] = newEntry
		switch {
		case !existed:
			result.Added = append(result.Added, key)
		case !sdkServiceEqual(existing, newEntry):
			result.Updated = append(result.Updated, key)
		}
	}

	sort.Strings(result.Added)
	sort.Strings(result.Updated)
	sort.Strings(result.Removed)
	return result, nil
}

func newSDKSyncResult(localVersion, remoteVersion string, syncVersion bool) sdkSyncResult {
	result := sdkSyncResult{
		ArtifactVersionFrom: localVersion,
		ArtifactVersionTo:   localVersion,
		RemoteVersion:       remoteVersion,
		SyncVersion:         syncVersion,
	}
	if syncVersion {
		result.ArtifactVersionTo = remoteVersion
	}
	return result
}

func sdkSyncRemoteByName(remote []sdkSyncRemoteService) (map[string]sdkSyncRemoteService, error) {
	remoteByName := make(map[string]sdkSyncRemoteService, len(remote))
	for _, svc := range remote {
		key, err := sdkSyncServiceConfigKey(svc)
		if err != nil {
			return nil, err
		}
		// Service refs are stable config keys; display names can drift or collide
		// across providers and would make sync rewrite YAML unpredictably.
		remoteByName[key] = svc
	}
	return remoteByName, nil
}

func sdkSyncServiceConfigKey(svc sdkSyncRemoteService) (string, error) {
	if ref := strings.TrimSpace(svc.Ref); ref != "" {
		return ref, nil
	}
	return "", fmt.Errorf("sdk sync missing service slug for service %s", svc.Name)
}

// sdkServiceEqual compares the fields sync actually touches. Operations is
// compared as a set, not an ordered list -- sdkSelectionResources gives no
// ordering guarantee, so a pure reordering must not be reported as a change.
func sdkServiceEqual(a, b configfile.SDKService) bool {
	return a.Version == b.Version && sameStringSet(a.Operations, b.Operations)
}

func resolveServiceVersionName(sel api.SDKSelectionDetail) (string, error) {
	if strings.TrimSpace(sel.ServiceVersionID) == "" {
		return "", fmt.Errorf("missing service_version_id")
	}
	name := strings.TrimSpace(sel.ServiceVersionName)
	if name == "" {
		return "", fmt.Errorf("missing service_version_name for service_version_id %s", sel.ServiceVersionID)
	}
	return name, nil
}

// fetchSDKSyncData is the impure orchestration layer for `sdk sync`: fetches
// the most recently generated SDK by name, resolves each selected service's
// persisted version tag, and enumerates each selection's operations
// (select_all included) into concrete names.
func fetchSDKSyncData(client *api.Client, sdkName string) (artifactVersion string, remote []sdkSyncRemoteService, err error) {
	sdk, err := client.GetSDKSelectionsByName(sdkName)
	if err != nil {
		return "", nil, fmt.Errorf("fetching sdk %q: %w", sdkName, err)
	}

	for _, sel := range sdk.DetailedSelections {
		versionTag, err := resolveServiceVersionName(sel)
		if err != nil {
			return "", nil, fmt.Errorf("resolving version for service %s: %w", sel.ServiceName, err)
		}

		names, err := client.GetSDKSelectionResourceNames(sdk.ID, sel.ServiceID)
		if err != nil {
			return "", nil, fmt.Errorf("resolving operations for service %s: %w", sel.ServiceName, err)
		}

		ref := serviceConfigRef(sel.ServiceSlug, sel.ServiceProvider)
		if ref == "" {
			return "", nil, fmt.Errorf("sdk sync missing service slug for service %s", sel.ServiceName)
		}
		remote = append(remote, sdkSyncRemoteService{
			Name:       sel.ServiceName,
			Ref:        ref,
			Version:    versionTag,
			Operations: names,
		})
	}

	return sdk.Version, remote, nil
}

var sdkSyncCmd = &cobra.Command{
	Use:   "sync <name>",
	Short: "Full-mirror the local SDK config from the most recently generated remote SDK",
	Long: `Overwrites the local SDK config's services with the selections on the most
recently generated remote SDK of the given name: adds or updates every
selected service (the Registry's resolved version and operations win on any
conflict) and removes any local service entry the remote SDK no longer
selects. The local artifact version is preserved unless --sync-version is set.`,
	Args: cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.sdk.sync", func(cmd *cobra.Command, args []string) error {
		path, cfg, err := loadSDKConfigForSync(ConfigFile, args[0])
		if err != nil {
			return err
		}
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		artifactVersion, remote, err := fetchSDKSyncData(client, args[0])
		if err != nil {
			return err
		}
		result, err := mergeSDKServicesFromRemote(cfg, artifactVersion, remote, sdkSyncVersion)
		if err != nil {
			return err
		}
		if err := writeSDKConfig(path, cfg); err != nil {
			return err
		}
		printSDKSyncResult(cmd, result)
		return nil
	}),
}

func printSDKSyncResult(cmd *cobra.Command, result sdkSyncResult) {
	if result.ArtifactVersionFrom != result.ArtifactVersionTo {
		fmt.Fprintf(cmd.OutOrStdout(), "~ version: %s -> %s\n", result.ArtifactVersionFrom, result.ArtifactVersionTo)
	} else if result.RemoteVersion != "" && result.RemoteVersion != result.ArtifactVersionFrom {
		fmt.Fprintf(cmd.OutOrStdout(), "remote SDK version is %s; local config version remains %s (use --sync-version to update it)\n", result.RemoteVersion, result.ArtifactVersionFrom)
	}
	if len(result.Added) == 0 && len(result.Updated) == 0 && len(result.Removed) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "SDK config already in sync.")
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
	sdkSyncCmd.Flags().BoolVar(&sdkSyncVersion, "sync-version", false, "Update the local SDK artifact version to match the remote SDK")
	sdkCmd.AddCommand(sdkSyncCmd)
}
