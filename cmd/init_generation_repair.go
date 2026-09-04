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

// unifiedInitSnapshotTarget binds one configured service tag to its exact active workspace UUIDs.
type unifiedInitSnapshotTarget struct {
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

// unifiedInitCanRefreshAPISchemaSnapshot limits automatic repair to direct API init and the Engine's exact OpenAPI projection failure.
func unifiedInitCanRefreshAPISchemaSnapshot(mode unifiedInitMode, parsed *configfile.ParsedConfig, cause error) bool {
	// Only an explicit package-free SDK config represents the direct REST API lifecycle.
	if mode != unifiedInitModeAPI || parsed == nil || parsed.SDK == nil || sdkGeneratesPackage(parsed.SDK) {
		return false
	}
	var apiErr *api.APIError
	// Transport and unrelated plan failures provide no authority to mutate service snapshots.
	if !errors.As(cause, &apiErr) {
		return false
	}
	return apiErr.Code == "app_openapi_schema_unavailable"
}

// refreshUnifiedInitGenerationSnapshots refreshes every exact selected version once and reports each completed mutation.
func refreshUnifiedInitGenerationSnapshots(cmd *cobra.Command, client *api.Client, parsed *configfile.ParsedConfig) ([]string, error) {
	return refreshUnifiedInitAppSnapshots(cmd, client, parsed, "immutable SDK generation snapshot")
}

// refreshUnifiedInitAPISchemaSnapshots refreshes exact selected versions without adding package-generation behavior to a REST API.
func refreshUnifiedInitAPISchemaSnapshots(cmd *cobra.Command, client *api.Client, parsed *configfile.ParsedConfig) ([]string, error) {
	return refreshUnifiedInitAppSnapshots(cmd, client, parsed, "immutable REST API OpenAPI snapshot")
}

// refreshUnifiedInitAppSnapshots performs one fully resolved sequence of exact snapshot mutations with mode-specific output.
func refreshUnifiedInitAppSnapshots(cmd *cobra.Command, client *api.Client, parsed *configfile.ParsedConfig, description string) ([]string, error) {
	targets, err := resolveUnifiedInitSnapshotTargets(client, parsed)
	// Identity resolution must be complete before the first refresh so ambiguity cannot cause a partial mutation.
	if err != nil {
		return nil, err
	}
	refreshed := make([]string, 0, len(targets))
	for _, target := range targets {
		label := target.serviceKey + "@" + target.version
		fmt.Fprintf(cmd.OutOrStdout(), "Refreshing %s for %s...\n", description, label)
		result, err := client.RefreshServiceContract(target.serviceID, target.serviceVersionID)
		// A failed exact refresh stops the repair; the caller will not retry app planning with incomplete snapshots.
		if err != nil {
			return refreshed, fmt.Errorf("refresh %s for %s: %w", description, label, err)
		}
		// The returned tag must agree with the configured tag even though the API client already verifies both stable UUIDs.
		if result.Version != target.version {
			return refreshed, fmt.Errorf("refresh %s for %s returned version %q", description, label, result.Version)
		}
		refreshed = append(refreshed, label)
		fmt.Fprintf(cmd.OutOrStdout(), "Refreshed %s for %s.\n", description, label)
	}
	return refreshed, nil
}

// refreshUnifiedInitRuntimeSnapshots repairs only the selected exact versions before retrying scaffold enrichment once.
func refreshUnifiedInitRuntimeSnapshots(cmd *cobra.Command, client *api.Client, parsed *configfile.ParsedConfig) ([]string, error) {
	targets, err := resolveUnifiedInitSnapshotTargets(client, parsed)
	// Exact workspace identity resolution must finish before the first repair mutation.
	if err != nil {
		return nil, err
	}
	refreshed := make([]string, 0, len(targets))
	for _, target := range targets {
		label := target.serviceKey + "@" + target.version
		fmt.Fprintf(cmd.OutOrStdout(), "Refreshing runtime contract for %s...\n", label)
		result, err := client.RefreshServiceContract(target.serviceID, target.serviceVersionID)
		// A failed exact refresh stops the retry and reports every earlier completed repair.
		if err != nil {
			return refreshed, fmt.Errorf("refresh runtime contract for %s: %w", label, err)
		}
		// A mismatched returned tag cannot authorize enrichment against a different immutable version.
		if result.Version != target.version {
			return refreshed, fmt.Errorf("refresh runtime contract for %s returned version %q", label, result.Version)
		}
		refreshed = append(refreshed, label)
		fmt.Fprintf(cmd.OutOrStdout(), "Refreshed runtime contract for %s.\n", label)
	}
	return refreshed, nil
}

// unifiedInitCanRefreshRuntimeSnapshots admits only Engine scaffold dependency failures into the exact one-retry repair path.
func unifiedInitCanRefreshRuntimeSnapshots(cause error) bool {
	var apiErr *api.APIError
	// Local validation and transport failures provide no evidence that a selected Engine snapshot needs repair.
	if !errors.As(cause, &apiErr) {
		return false
	}
	// Only the stable scaffold dependency code identifies the cold runtime-contract cache repaired by this path.
	return apiErr.Code == "graphql_dependency_failed"
}

// resolveUnifiedInitSnapshotTargets maps config keys and tags onto exact active Engine workspace identities.
func resolveUnifiedInitSnapshotTargets(client *api.Client, parsed *configfile.ParsedConfig) ([]unifiedInitSnapshotTarget, error) {
	// Repair is valid only for a parsed app candidate whose complete service selection was already semantically checked.
	if parsed == nil {
		return nil, errors.New("app runtime snapshot refresh requires a parsed SDK or MCP config")
	}
	app := parsed.SDK
	// MCP shares the same service-selection contract but is projected into a distinct parsed field.
	if app == nil {
		app = parsed.MCP
	}
	// Workspace configs and malformed parser state cannot authorize a runtime snapshot mutation.
	if app == nil {
		return nil, errors.New("app runtime snapshot refresh requires a parsed SDK or MCP config")
	}
	serviceKeys := make([]string, 0, len(app.Services))
	for serviceKey := range app.Services {
		serviceKeys = append(serviceKeys, serviceKey)
	}
	sort.Strings(serviceKeys)
	// A pin failure without selected services is inconsistent and must not broaden the mutation to the whole workspace.
	if len(serviceKeys) == 0 {
		return nil, errors.New("app runtime snapshot refresh has no selected services")
	}
	workspaceServices, err := client.ListWorkspaceServices()
	// A failed workspace read provides no safe UUID authority for the refresh mutation.
	if err != nil {
		return nil, fmt.Errorf("list enabled workspace services for app runtime snapshot refresh: %w", err)
	}
	targets := make([]unifiedInitSnapshotTarget, 0, len(serviceKeys))
	for _, serviceKey := range serviceKeys {
		configured := app.Services[serviceKey]
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
		targets = append(targets, unifiedInitSnapshotTarget{
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
