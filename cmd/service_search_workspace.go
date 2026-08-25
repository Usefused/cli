package cmd

import (
	"strings"

	cliapi "github.com/Usefused/cli/internal/api"
)

const (
	serviceWorkspaceEnabled   = "enabled"
	serviceWorkspaceAvailable = "available_to_add"
)

// addWorkspaceStatusToServiceSearch composes Registry search with one bounded
// Engine lookup over only the query and Registry matches. This exposes whether
// each result is already enabled without loading the entire workspace into the
// CLI or introducing a second service-discovery endpoint.
func addWorkspaceStatusToServiceSearch(client *cliapi.Client, query string, registryResults []serviceSearchResult) ([]serviceSearchResult, error) {
	workspaceServices, err := client.ListWorkspaceServices(workspaceServiceSearchKeys(query, registryResults)...)
	if err != nil {
		return nil, err
	}
	enabledByID := workspaceServiceIDSet(workspaceServices)
	registryIDs := make(map[string]bool, len(registryResults))
	enabled := make([]serviceSearchResult, 0, len(registryResults))
	available := make([]serviceSearchResult, 0, len(registryResults))
	for _, result := range registryResults {
		registryIDs[result.ServiceID] = true
		if enabledByID[result.ServiceID] {
			result.WorkspaceStatus = serviceWorkspaceEnabled
			enabled = append(enabled, result)
			continue
		}
		result.WorkspaceStatus = serviceWorkspaceAvailable
		available = append(available, result)
	}
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

func appendWorkspaceOnlySearchResults(results []serviceSearchResult, query string, services []cliapi.WorkspaceService, registryIDs map[string]bool) []serviceSearchResult {
	for _, service := range exactWorkspaceServiceMatches(query, services) {
		if service.ServiceID == "" || registryIDs[service.ServiceID] {
			continue
		}
		slug := service.ServiceSlug
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
