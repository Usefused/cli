package cmd

import (
	"strings"

	cliapi "github.com/Usefused/cli/internal/api"
)

const (
	serviceWorkspaceEnabled   = "enabled"
	serviceWorkspaceAvailable = "available_to_add"
)

// addWorkspaceStatusToServiceSearch combines Engine free-text results with exact Registry membership checks.
func addWorkspaceStatusToServiceSearch(client *cliapi.Client, query string, registryResults []serviceSearchResult) ([]serviceSearchResult, error) {
	workspaceServices, err := client.SearchWorkspaceServices(query)
	// Failed authorization or search must not be reported as an empty workspace.
	if err != nil {
		return nil, err
	}
	membership, err := client.ListWorkspaceServices(workspaceServiceSearchKeys(query, registryResults)...)
	// Registry matches can come from descriptions, so exact membership checks supplement local name/slug search.
	if err != nil {
		return nil, err
	}
	enabledByID := workspaceServiceIDSet(append(append([]cliapi.WorkspaceService{}, workspaceServices...), membership...))
	registryIDs := make(map[string]bool, len(registryResults))
	enabled := make([]serviceSearchResult, 0, len(registryResults))
	available := make([]serviceSearchResult, 0, len(registryResults))
	for _, result := range registryResults {
		registryIDs[result.ServiceID] = true
		// Workspace membership is authoritative even when the Registry matched a different metadata field.
		if enabledByID[result.ServiceID] {
			result.WorkspaceStatus = serviceWorkspaceEnabled
			enabled = append(enabled, result)
			continue
		}
		result.WorkspaceStatus = serviceWorkspaceAvailable
		available = append(available, result)
	}
	// Exact qualified references may resolve a locally stored bare slug; preserve that lookup compatibility.
	workspaceServices = append(workspaceServices, exactWorkspaceServiceMatches(query, membership)...)
	enabled = appendWorkspaceOnlySearchResults(enabled, query, workspaceServices, registryIDs)
	return append(enabled, available...), nil
}

func workspaceServiceSearchKeys(query string, results []serviceSearchResult) []string {
	seen := map[string]bool{}
	keys := make([]string, 0, 1+len(results)*2)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			keys = append(keys, value)
		}
	}
	add(cliapi.ServiceLookupName(query))
	for _, result := range results {
		add(result.Slug)
		add(result.Name)
	}
	return keys
}

func workspaceServiceIDSet(services []cliapi.WorkspaceService) map[string]bool {
	ids := make(map[string]bool, len(services))
	for _, service := range services {
		if service.ServiceID != "" {
			ids[service.ServiceID] = true
		}
	}
	return ids
}

// appendWorkspaceOnlySearchResults trusts Engine matching and deduplicates local and Registry identities.
func appendWorkspaceOnlySearchResults(results []serviceSearchResult, query string, services []cliapi.WorkspaceService, registryIDs map[string]bool) []serviceSearchResult {
	for _, service := range services {
		// Repeated identities must appear once regardless of which search found them.
		if service.ServiceID == "" || registryIDs[service.ServiceID] {
			continue
		}
		registryIDs[service.ServiceID] = true
		slug := service.ServiceSlug
		// Legacy memberships without a stored slug retain the user-provided reference.
		if strings.TrimSpace(slug) == "" {
			slug = query
		}
		results = append(results, serviceSearchResult{
			Name: service.ServiceName, Slug: slug, ServiceID: service.ServiceID,
			WorkspaceStatus: serviceWorkspaceEnabled,
		})
	}
	return results
}
