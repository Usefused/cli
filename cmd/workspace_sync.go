package cmd

import (
	"fmt"
	"sort"

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
// (engine_workspace_registration_plan.md): full-mirrors the Engine's
// authoritative GET /workspace/services list (Fact 9) into cfg.Services --
// every remotely-activated service is added or updated locally with the
// Engine's data winning on any conflict, and any local entry whose service
// is no longer activated remotely is removed, not just flagged. Pure and
// side-effect-free (no file or network I/O) so it's directly testable.
func mergeWorkspaceServicesFromRemote(cfg *configfile.WorkspaceConfig, remote []api.WorkspaceService) workspaceSyncResult {
	if cfg.Services == nil {
		cfg.Services = map[string]configfile.WorkspaceService{}
	}
	var result workspaceSyncResult

	remoteByName := make(map[string]api.WorkspaceService, len(remote))
	for _, svc := range remote {
		remoteByName[svc.ServiceName] = svc
	}

	// Remove local entries no longer activated remotely.
	for name := range cfg.Services {
		if _, ok := remoteByName[name]; !ok {
			delete(cfg.Services, name)
			result.Removed = append(result.Removed, name)
		}
	}

	// Add/update from remote -- remote always wins over whatever was there.
	for _, svc := range remote {
		versions := svc.Versions
		if svc.Version != "" && !containsString(versions, svc.Version) {
			versions = append(append([]string{}, versions...), svc.Version)
		}
		newEntry := configfile.WorkspaceService{
			ServiceID: svc.ServiceID,
			Versions:  versions,
			Default:   svc.Version,
		}
		existing, existed := cfg.Services[svc.ServiceName]
		cfg.Services[svc.ServiceName] = newEntry
		switch {
		case !existed:
			result.Added = append(result.Added, svc.ServiceName)
		case !workspaceServiceEqual(existing, newEntry):
			result.Updated = append(result.Updated, svc.ServiceName)
		}
	}

	sort.Strings(result.Added)
	sort.Strings(result.Updated)
	sort.Strings(result.Removed)
	return result
}

// workspaceServiceEqual compares the fields sync actually touches.
// Versions is compared as a set, not an ordered list -- the Engine's
// ListActivatedServices/ListActivationVersionsForServices give no ordering
// guarantee, so a pure reordering must not be reported as a change.
func workspaceServiceEqual(a, b configfile.WorkspaceService) bool {
	return a.ServiceID == b.ServiceID && a.Default == b.Default && sameStringSet(a.Versions, b.Versions)
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
	Short: "Full-mirror the local workspace config from the Engine's current activation state",
	Long: `Overwrites the local workspace config's services with whatever is currently
activated remotely: adds or updates every remotely-activated service (the
Engine's data wins on any conflict) and removes any local service entry
that's no longer activated remotely.`,
	RunE: WithTelemetry("cli.workspace.sync", func(cmd *cobra.Command, args []string) error {
		cfg, err := loadWorkspaceConfigForEdit(ConfigFile)
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
		result := mergeWorkspaceServicesFromRemote(cfg, remote)
		if err := writeWorkspaceConfig(ConfigFile, cfg); err != nil {
			return err
		}
		printWorkspaceSyncResult(cmd, result)
		return nil
	}),
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
