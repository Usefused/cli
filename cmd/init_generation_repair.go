package cmd

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

// unifiedInitGenerationSnapshotTarget binds one configured service tag to its exact active workspace UUIDs.
type unifiedInitGenerationSnapshotTarget struct {
	serviceKey       string
	version          string
	serviceID        string
	serviceVersionID string
}

// unifiedInitCanRefreshGenerationSnapshot limits automatic repair to generated SDK init and one typed Engine condition.
func unifiedInitCanRefreshGenerationSnapshot(mode unifiedInitMode, parsed *configfile.ParsedConfig, cause error) bool {
	// API and MCP modes must never acquire package-generation behavior through shared kind or lifecycle code.
	if mode != unifiedInitModeSDK || parsed == nil || parsed.SDK == nil || !sdkGeneratesPackage(parsed.SDK) {
		return false
	}
	var apiErr *api.APIError
	// Transport, malformed-response, and unrelated Engine failures retain their normal plan semantics without mutation.
	if !errors.As(cause, &apiErr) {
		return false
	}
	return apiErr.Code == "generation_contract_pin_unavailable"
}

// refreshUnifiedInitGenerationSnapshots refreshes every exact selected version once and reports each completed mutation.
func refreshUnifiedInitGenerationSnapshots(cmd *cobra.Command, client *api.Client, parsed *configfile.ParsedConfig) ([]string, error) {
	targets, err := resolveUnifiedInitGenerationSnapshotTargets(client, parsed)
	// Identity resolution must be complete before the first refresh so ambiguity cannot cause a partial mutation.
	if err != nil {
		return nil, err
	}
	refreshed := make([]string, 0, len(targets))
	for _, target := range targets {
		label := target.serviceKey + "@" + target.version
		fmt.Fprintf(cmd.OutOrStdout(), "Refreshing immutable SDK generation snapshot for %s...\n", label)
		result, err := client.RefreshServiceContract(target.serviceID, target.serviceVersionID)
		// A failed exact refresh stops the repair; the caller will not retry app planning with incomplete snapshots.
		if err != nil {
			return refreshed, fmt.Errorf("refresh immutable SDK generation snapshot for %s: %w", label, err)
		}
		// The returned tag must agree with the configured tag even though the API client already verifies both stable UUIDs.
		if result.Version != target.version {
			return refreshed, fmt.Errorf("refresh immutable SDK generation snapshot for %s returned version %q", label, result.Version)
		}
		refreshed = append(refreshed, label)
		fmt.Fprintf(cmd.OutOrStdout(), "Refreshed immutable SDK generation snapshot for %s.\n", label)
	}
	return refreshed, nil
}

// resolveUnifiedInitGenerationSnapshotTargets maps config keys and tags onto exact active Engine workspace identities.
func resolveUnifiedInitGenerationSnapshotTargets(client *api.Client, parsed *configfile.ParsedConfig) ([]unifiedInitGenerationSnapshotTarget, error) {
	// Repair is valid only for a parsed SDK candidate whose complete service selection was already semantically checked.
	if parsed == nil || parsed.SDK == nil {
		return nil, errors.New("generated SDK snapshot refresh requires a parsed SDK config")
	}
	serviceKeys := make([]string, 0, len(parsed.SDK.Services))
	for serviceKey := range parsed.SDK.Services {
		serviceKeys = append(serviceKeys, serviceKey)
	}
	sort.Strings(serviceKeys)
	// A pin failure without selected services is inconsistent and must not broaden the mutation to the whole workspace.
	if len(serviceKeys) == 0 {
		return nil, errors.New("generated SDK snapshot refresh has no selected services")
	}
	workspaceServices, err := client.ListWorkspaceServices()
	// A failed workspace read provides no safe UUID authority for the refresh mutation.
	if err != nil {
		return nil, fmt.Errorf("list enabled workspace services for SDK generation snapshot refresh: %w", err)
	}
	targets := make([]unifiedInitGenerationSnapshotTarget, 0, len(serviceKeys))
	for _, serviceKey := range serviceKeys {
		configured := parsed.SDK.Services[serviceKey]
		matches := matchingUnifiedInitWorkspaceServices(workspaceServices, serviceKey)
		// Exactly one workspace service must own the selected key before any UUID is used for mutation.
		if len(matches) != 1 {
			return nil, fmt.Errorf("selected service %s resolves to %d enabled workspace services; run 'fused-cli workspace service list' and retry with an unambiguous service reference", serviceKey, len(matches))
		}
		versions := matchingUnifiedInitWorkspaceVersions(matches[0].EnabledVersions, configured.Version)
		// The refresh endpoint accepts only an active exact version, so absence and duplicate identity both fail closed.
		if len(versions) != 1 {
			return nil, fmt.Errorf("selected service %s@%s resolves to %d exact enabled workspace versions; enable that exact version and retry", serviceKey, configured.Version, len(versions))
		}
		targets = append(targets, unifiedInitGenerationSnapshotTarget{
			serviceKey: serviceKey, version: configured.Version,
			serviceID: matches[0].ServiceID, serviceVersionID: versions[0].ServiceVersionID,
		})
	}
	return targets, nil
}

// matchingUnifiedInitWorkspaceServices selects only workspace entries that can represent the configured service identity.
func matchingUnifiedInitWorkspaceServices(services []api.WorkspaceService, serviceKey string) []api.WorkspaceService {
	reference := api.ParseServiceReference(serviceKey)
	matches := make([]api.WorkspaceService, 0, 1)
	for _, service := range services {
		matchesSlug := strings.EqualFold(strings.TrimSpace(service.ServiceSlug), strings.TrimSpace(serviceKey))
		// Qualified references may not fall back to a display name because that would discard provider identity.
		if reference.Qualified {
			// Only the provider-qualified slug projection can prove a qualified match.
			if matchesSlug {
				matches = append(matches, service)
			}
			continue
		}
		matchesName := strings.EqualFold(strings.TrimSpace(service.ServiceName), strings.TrimSpace(serviceKey))
		// Bare configured keys may match either the Registry slug projection or the stable workspace display name.
		if matchesSlug || matchesName {
			matches = append(matches, service)
		}
	}
	return matches
}

// matchingUnifiedInitWorkspaceVersions returns the one active workspace row for an exact configured version tag.
func matchingUnifiedInitWorkspaceVersions(versions []api.WorkspaceServiceVersion, selectedVersion string) []api.WorkspaceServiceVersion {
	matches := make([]api.WorkspaceServiceVersion, 0, 1)
	for _, version := range versions {
		// Engine defines active workspace versions as every exact row not explicitly deprecated; visibility labels remain active.
		if version.Version == selectedVersion && !strings.EqualFold(version.Status, "deprecated") {
			matches = append(matches, version)
		}
	}
	return matches
}
