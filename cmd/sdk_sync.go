package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

// sdkSyncResult summarizes what mergeSDKServicesFromRemote did, so the
// command can report a diff without re-deriving it itself.
type sdkSyncResult struct {
	SDKVersionFrom string
	SDKVersionTo   string
	Added          []string
	Updated        []string
	Removed        []string
}

// sdkSyncRemoteService is the fully-resolved remote view of one service's
// selection on the most recently generated SDK -- select_all vs. explicit
// endpoint/webhook IDs already collapsed into concrete operation names, and
// the pinned api_version_id already resolved to its human-readable version
// tag (see fetchSDKSyncData).
type sdkSyncRemoteService struct {
	Name       string
	Version    string
	Operations []string
}

// mergeSDKServicesFromRemote is Task 4's core logic for `sdk sync`
// (engine_workspace_registration_plan.md): full-mirrors the most recently
// generated remote SDK's selections into cfg.Services -- every service the
// remote SDK currently selects is added or updated locally with the
// Registry's resolved data winning on any conflict, and any local service
// entry the remote SDK no longer selects is removed, not just flagged. Also
// bumps cfg.SDKVersion to the synced SDK's version. Pure and side-effect-free
// (no file or network I/O) so it's directly testable, mirroring
// mergeWorkspaceServicesFromRemote's structure (workspace_sync.go).
func mergeSDKServicesFromRemote(cfg *configfile.SDKConfig, sdkVersion string, remote []sdkSyncRemoteService) sdkSyncResult {
	if cfg.Services == nil {
		cfg.Services = map[string]configfile.SDKService{}
	}
	result := sdkSyncResult{SDKVersionFrom: cfg.SDKVersion, SDKVersionTo: sdkVersion}
	cfg.SDKVersion = sdkVersion

	remoteByName := make(map[string]sdkSyncRemoteService, len(remote))
	for _, svc := range remote {
		remoteByName[svc.Name] = svc
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
		operations := append([]string{}, svc.Operations...)
		sort.Strings(operations)
		newEntry := configfile.SDKService{
			Version:    svc.Version,
			Operations: operations,
		}
		existing, existed := cfg.Services[svc.Name]
		cfg.Services[svc.Name] = newEntry
		switch {
		case !existed:
			result.Added = append(result.Added, svc.Name)
		case !sdkServiceEqual(existing, newEntry):
			result.Updated = append(result.Updated, svc.Name)
		}
	}

	sort.Strings(result.Added)
	sort.Strings(result.Updated)
	sort.Strings(result.Removed)
	return result
}

// sdkServiceEqual compares the fields sync actually touches. Operations is
// compared as a set, not an ordered list -- sdkSelectionResources gives no
// ordering guarantee, so a pure reordering must not be reported as a change.
func sdkServiceEqual(a, b configfile.SDKService) bool {
	return a.Version == b.Version && sameStringSet(a.Operations, b.Operations)
}

// resolveVersionTag turns a selection's api_version_id (may be empty,
// meaning "whatever the Registry resolved as current/default at generation
// time") into the human-readable version tag local sdk.yaml expects. A
// non-empty apiVersionID must match one of the service's current versions
// exactly -- silently falling back to the default would mask a version
// that's since been deleted or deprecated away.
func resolveVersionTag(apiVersionID string, versions []api.ServiceApiVersion) (string, error) {
	if apiVersionID != "" {
		for _, v := range versions {
			if v.ID == apiVersionID {
				return v.Name, nil
			}
		}
		return "", fmt.Errorf("pinned api version %s not found among this service's current versions", apiVersionID)
	}
	for _, v := range versions {
		if v.IsDefault {
			return v.Name, nil
		}
	}
	return "", fmt.Errorf("no default api version is configured for this service, and the selection has no pinned version")
}

// fetchSDKSyncData is the impure orchestration layer for `sdk sync`: fetches
// the most recently generated SDK by name, resolves each selected service's
// pinned/default api version to a version tag, and enumerates each
// selection's operations (select_all included) into concrete names.
func fetchSDKSyncData(client *api.Client, sdkName string) (sdkVersion string, remote []sdkSyncRemoteService, err error) {
	sdk, err := client.GetSDKSelectionsByName(sdkName)
	if err != nil {
		return "", nil, fmt.Errorf("fetching sdk %q: %w", sdkName, err)
	}

	versionCache := make(map[string][]api.ServiceApiVersion, len(sdk.DetailedSelections))
	for _, sel := range sdk.DetailedSelections {
		versions, ok := versionCache[sel.ServiceID]
		if !ok {
			versions, err = client.ServiceApiVersions(sel.ServiceID)
			if err != nil {
				return "", nil, fmt.Errorf("resolving api versions for service %s: %w", sel.ServiceName, err)
			}
			versionCache[sel.ServiceID] = versions
		}

		versionTag, err := resolveVersionTag(sel.ApiVersionID, versions)
		if err != nil {
			return "", nil, fmt.Errorf("resolving version for service %s: %w", sel.ServiceName, err)
		}

		names, err := client.GetSDKSelectionResourceNames(sdk.ID, sel.ServiceID)
		if err != nil {
			return "", nil, fmt.Errorf("resolving operations for service %s: %w", sel.ServiceName, err)
		}

		remote = append(remote, sdkSyncRemoteService{
			Name:       sel.ServiceName,
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
selects. Also bumps the local sdkVersion to match.`,
	Args: cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.sdk.sync", func(cmd *cobra.Command, args []string) error {
		cfg, err := loadSDKConfigForEdit(ConfigFile)
		if err != nil {
			return err
		}
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		sdkVersion, remote, err := fetchSDKSyncData(client, args[0])
		if err != nil {
			return err
		}
		result := mergeSDKServicesFromRemote(cfg, sdkVersion, remote)
		if err := writeSDKConfig(ConfigFile, cfg); err != nil {
			return err
		}
		printSDKSyncResult(cmd, result)
		return nil
	}),
}

func printSDKSyncResult(cmd *cobra.Command, result sdkSyncResult) {
	if result.SDKVersionFrom != result.SDKVersionTo {
		fmt.Fprintf(cmd.OutOrStdout(), "~ sdkVersion: %s -> %s\n", result.SDKVersionFrom, result.SDKVersionTo)
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
	sdkCmd.AddCommand(sdkSyncCmd)
}
