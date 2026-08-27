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
	Added   []string
	Updated []string
	Removed []string
}

// sdkSyncRemoteService is the fully-resolved remote view of one service's
// selection on one exact SDK app version -- select_all vs. explicit
// endpoint/webhook IDs already collapsed into concrete operation names, and
// the pinned version identity already resolved to its human-readable version
// tag (see fetchSDKSyncData).
type sdkSyncRemoteService struct {
	Name              string
	Ref               string
	Version           string
	Operations        []string
	Webhooks          []string
	SelectAll         bool
	WebhooksSelectAll bool
	Auth              *configfile.AppAuth
	Connect           *configfile.AppConnect
	Injections        []configfile.InjectionConfig
}

// mergeSDKServicesFromRemote is Task 4's core logic for `sdk sync`
// (engine_workspace_registration_plan.md): full-mirrors one exact Engine app
// version's selections into cfg.Services. Pure and side-effect-free (no file
// or network I/O) so it is directly testable, mirroring
// mergeWorkspaceServicesFromRemote's structure (workspace_sync.go).
func mergeSDKServicesFromRemote(cfg *configfile.SDKConfig, appVersion string, remote []sdkSyncRemoteService) (sdkSyncResult, error) {
	if cfg.Services == nil {
		cfg.Services = map[string]configfile.SDKService{}
	}
	result := sdkSyncResult{}
	if cfg.Version != appVersion {
		return result, fmt.Errorf("resolved SDK version %q does not match local config version %q", appVersion, cfg.Version)
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
			Version:           svc.Version,
			Operations:        operations,
			Webhooks:          sortedStrings(svc.Webhooks),
			SelectAll:         svc.SelectAll,
			WebhooksSelectAll: svc.WebhooksSelectAll,
			Auth:              cloneAppAuth(svc.Auth),
			Connect:           cloneAppConnect(svc.Connect),
			Injections:        append([]configfile.InjectionConfig(nil), svc.Injections...),
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
// compared as a set, not an ordered list -- Engine selection persistence has
// no ordering guarantee, so a pure reordering must not be reported as a change.
func sdkServiceEqual(a, b configfile.SDKService) bool {
	return a.Version == b.Version && sameStringSet(a.Operations, b.Operations) &&
		sameStringSet(a.Webhooks, b.Webhooks) && a.SelectAll == b.SelectAll &&
		a.WebhooksSelectAll == b.WebhooksSelectAll && appAuthEqual(a.Auth, b.Auth) &&
		appConnectEqual(a.Connect, b.Connect) && appInjectionsEqual(a.Injections, b.Injections)
}

func appInjectionsEqual(a, b []configfile.InjectionConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func appAuthEqual(a, b *configfile.AppAuth) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Type == b.Type && a.Name == b.Name
}

func cloneAppAuth(auth *configfile.AppAuth) *configfile.AppAuth {
	if auth == nil {
		return nil
	}
	copy := *auth
	return &copy
}

func cloneAppConnect(connect *configfile.AppConnect) *configfile.AppConnect {
	if connect == nil {
		return nil
	}
	return &configfile.AppConnect{Scopes: sortedStrings(connect.Scopes)}
}

func appConnectEqual(a, b *configfile.AppConnect) bool {
	if a == nil || b == nil {
		return a == b
	}
	return sameStringSet(a.Scopes, b.Scopes)
}

func sortedStrings(values []string) []string {
	out := append([]string{}, values...)
	sort.Strings(out)
	return out
}

// fetchSDKSyncData joins two bounded Engine catalogue reads by service ID. The
// app carries the immutable selection policy while appServices carries the
// local human-readable service metadata; neither query depends on selection
// count and Registry archives are not involved.
func fetchSDKSyncData(client *api.Client, sdkName, sdkVersion string) (appVersion, targetLanguage string, remote []sdkSyncRemoteService, err error) {
	appID, err := client.ResolveSDKAppReference(sdkName, sdkVersion)
	if err != nil {
		return "", "", nil, fmt.Errorf("resolving sdk %q version %q: %w", sdkName, sdkVersion, err)
	}
	app, err := client.GetApp(appID)
	if err != nil {
		return "", "", nil, fmt.Errorf("fetching sdk %q version %q: %w", sdkName, sdkVersion, err)
	}
	services, err := client.ListAppServices(appID)
	if err != nil {
		return "", "", nil, fmt.Errorf("fetching services for sdk %q version %q: %w", sdkName, sdkVersion, err)
	}
	servicesByID := make(map[string]api.AppServiceSummary, len(services))
	for _, service := range services {
		servicesByID[service.ServiceID] = service
	}

	for _, sel := range app.Selections {
		if err := validateSDKSyncDefinition(sdkName, sel); err != nil {
			return "", "", nil, err
		}
		service, ok := servicesByID[sel.ServiceID]
		if !ok {
			return "", "", nil, fmt.Errorf("sdk sync missing service metadata for service %s", sel.ServiceID)
		}
		ref := strings.TrimSpace(service.ServiceSlug)
		// Full-mirror removal is destructive, so incomplete remote identity must
		// fail before merge can mistake a skipped service for a remote deletion.
		if ref == "" {
			return "", "", nil, fmt.Errorf("sdk sync missing service slug for service %q (%s)", service.ServiceName, service.ServiceID)
		}
		remote = append(remote, sdkSyncRemoteService{
			Name: service.ServiceName, Ref: ref, Version: service.Version,
			Operations: sel.OperationNames, Webhooks: sel.WebhookNames,
			SelectAll: sel.SelectAll, WebhooksSelectAll: sel.WebhookSelectAll,
			Auth: sdkSyncAuth(sel), Connect: sdkSyncConnect(sel), Injections: sdkSyncInjections(sel.Injections),
		})
	}

	return app.Version, app.TargetLanguage, remote, nil
}

func validateSDKSyncDefinition(sdkName string, selection api.AppSelection) error {
	if selection.SchemaVersion == api.AppSelectionSchemaVersion {
		return nil
	}
	if selection.SchemaVersion > api.AppSelectionSchemaVersion {
		// A newer selection may carry semantics this CLI cannot preserve, so sync
		// must fail instead of silently rewriting the local declaration.
		return fmt.Errorf("sdk %q uses unsupported selection schema_version %d; upgrade fused-cli before sync", sdkName, selection.SchemaVersion)
	}
	// An unversioned row may have discarded security policy, so recovering
	// operation names alone cannot make a trustworthy config. Re-applying the
	// original declaration is the only migration that preserves auth, consent
	// ceilings, and injections.
	return fmt.Errorf("sdk %q requires definition refresh before sync; run `fused-cli sdk plan` and `fused-cli sdk apply` from its original config, then retry sync", sdkName)
}

func sdkSyncInjections(injections []api.InjectionConfig) []configfile.InjectionConfig {
	out := make([]configfile.InjectionConfig, len(injections))
	for i, injection := range injections {
		out[i] = configfile.InjectionConfig{Location: injection.Location, Name: injection.Name, Value: injection.Value, Mode: injection.Mode}
	}
	return out
}

func sdkSyncAuth(selection api.AppSelection) *configfile.AppAuth {
	if strings.TrimSpace(selection.AuthType) == "" {
		return nil
	}
	return &configfile.AppAuth{Type: selection.AuthType, Name: selection.AuthName}
}

func sdkSyncConnect(selection api.AppSelection) *configfile.AppConnect {
	if len(selection.ConnectScopes) == 0 {
		return nil
	}
	return &configfile.AppConnect{Scopes: sortedStrings(selection.ConnectScopes)}
}

var sdkSyncCmd = &cobra.Command{
	Use:   "sync <sdk-name>",
	Short: "Full-mirror the local SDK config from its exact Engine app version",
	Long: `Overwrites the local SDK config's services with the selections on the most
recently applied Engine app version declared by the local config: adds or updates every
selected service (the Engine's resolved version and operations win on any
conflict) and removes any local service entry the remote SDK no longer
selects. No implicit latest version is selected.

Definitions created before portable selection metadata was versioned are
rejected before this command writes the config. Re-apply the original SDK
declaration to refresh that same app version, then retry sync.`,
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
		appVersion, targetLanguage, remote, err := fetchSDKSyncData(client, args[0], cfg.Version)
		if err != nil {
			return err
		}
		if strings.TrimSpace(targetLanguage) != "" {
			cfg.Language = targetLanguage
		}
		result, err := mergeSDKServicesFromRemote(cfg, appVersion, remote)
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
