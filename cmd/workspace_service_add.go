package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/charmbracelet/huh"
	"github.com/google/uuid"
)

type workspaceServiceAddTarget struct {
	slug            string
	configServiceID string
	source          string
}

var selectWorkspaceRegistryService = promptWorkspaceRegistryService
var confirmWorkspaceRegistryService = promptWorkspaceRegistryServiceAdd
var selectExistingWorkspaceService = promptExistingWorkspaceService

// resolveWorkspaceServiceAddTarget keeps workspace-first discovery behind the
// existing add command. Explicit IDs retain their automation escape hatch;
// ordinary references use the access-filtered Engine view before Registry.
func resolveWorkspaceServiceAddTarget(query, explicitServiceID string, interactive bool) (workspaceServiceAddTarget, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return workspaceServiceAddTarget{}, errors.New("service query must not be empty")
	}
	if target, ok, err := explicitWorkspaceServiceTarget(query, explicitServiceID); err != nil || ok {
		return target, err
	}
	client, err := getAPIClient()
	if err != nil {
		return workspaceServiceAddTarget{}, err
	}
	if target, found, err := findWorkspaceServiceTarget(client, query, interactive); err != nil || found {
		return target, err
	}
	return findRegistryServiceTarget(client, query, interactive)
}

func explicitWorkspaceServiceTarget(query, serviceID string) (workspaceServiceAddTarget, bool, error) {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID != "" {
		if _, err := uuid.Parse(serviceID); err != nil {
			return workspaceServiceAddTarget{}, false, errors.New("--service-id must be a valid Registry service UUID")
		}
		return workspaceServiceAddTarget{slug: query, configServiceID: serviceID, source: "explicit"}, true, nil
	}
	if _, err := uuid.Parse(query); err == nil {
		return workspaceServiceAddTarget{slug: query, configServiceID: query, source: "explicit"}, true, nil
	}
	return workspaceServiceAddTarget{}, false, nil
}

func findWorkspaceServiceTarget(client *api.Client, query string, interactive bool) (workspaceServiceAddTarget, bool, error) {
	services, err := client.ListWorkspaceServices(workspaceServiceLookupName(query))
	if err != nil {
		return workspaceServiceAddTarget{}, false, err
	}
	services = exactWorkspaceServiceMatches(query, services)
	if len(services) == 0 {
		return workspaceServiceAddTarget{}, false, nil
	}
	if len(services) > 1 {
		if !interactive {
			return workspaceServiceAddTarget{}, false, fmt.Errorf("service reference %q matches multiple workspace services; pass --service-id or use --interactive", query)
		}
		selected, err := selectExistingWorkspaceService(services)
		if err != nil {
			return workspaceServiceAddTarget{}, false, err
		}
		services = []api.WorkspaceService{selected}
	}
	service := services[0]
	if strings.TrimSpace(service.ServiceID) == "" {
		return workspaceServiceAddTarget{}, false, errors.New("Engine returned a workspace service without an ID")
	}
	slug := strings.TrimSpace(service.ServiceSlug)
	if slug == "" {
		slug = query
	}
	return workspaceServiceAddTarget{
		slug: slug, source: "workspace",
	}, true, nil
}

func promptExistingWorkspaceService(services []api.WorkspaceService) (api.WorkspaceService, error) {
	options := make([]huh.Option[int], len(services))
	for i, service := range services {
		options[i] = huh.NewOption(fmt.Sprintf("%s (%s)", service.ServiceName, service.ServiceSlug), i)
	}
	selected := 0
	err := huh.NewSelect[int]().Title("Select an enabled workspace service").Options(options...).Value(&selected).Run()
	if err != nil {
		return api.WorkspaceService{}, err
	}
	return services[selected], nil
}

// exactWorkspaceServiceMatches validates the enriched identity returned by
// Engine. The store intentionally strips a provider qualifier for its bounded
// lookup, so accepting the row without this check could turn @other/billing
// into an already-enabled @acme/billing service.
func exactWorkspaceServiceMatches(query string, services []api.WorkspaceService) []api.WorkspaceService {
	query = strings.ToLower(strings.TrimSpace(query))
	qualified := strings.HasPrefix(query, "@")
	matches := make([]api.WorkspaceService, 0, len(services))
	for _, service := range services {
		slug := strings.ToLower(strings.TrimSpace(service.ServiceSlug))
		name := strings.ToLower(strings.TrimSpace(service.ServiceName))
		if query == name || query == slug || !qualified && query == strings.ToLower(workspaceServiceLookupName(slug)) {
			matches = append(matches, service)
		}
	}
	return matches
}

func findRegistryServiceTarget(client *api.Client, query string, interactive bool) (workspaceServiceAddTarget, error) {
	results, err := searchServiceResults(client, query)
	if err != nil {
		return workspaceServiceAddTarget{}, err
	}
	selected, err := chooseRegistryService(query, results, interactive)
	if err != nil {
		return workspaceServiceAddTarget{}, err
	}
	if strings.TrimSpace(selected.ServiceID) == "" || strings.TrimSpace(selected.Slug) == "" {
		return workspaceServiceAddTarget{}, errors.New("Registry returned a service without a reusable ID and slug")
	}
	if interactive {
		confirmed, err := confirmWorkspaceRegistryService(selected)
		if err != nil {
			return workspaceServiceAddTarget{}, err
		}
		if !confirmed {
			return workspaceServiceAddTarget{}, errors.New("service addition cancelled")
		}
	}
	return workspaceServiceAddTarget{
		slug: selected.Slug, source: "registry",
	}, nil
}

func chooseRegistryService(query string, results []serviceSearchResult, interactive bool) (serviceSearchResult, error) {
	matches := exactRegistryServiceMatches(query, results)
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(results) == 1 {
		return results[0], nil
	}
	if len(results) == 0 {
		return serviceSearchResult{}, fmt.Errorf("service %q was not found in the workspace or Registry", query)
	}
	if !interactive {
		return serviceSearchResult{}, fmt.Errorf("service query %q matched %d Registry services; pass an exact slug, service ID, or use --interactive", query, len(results))
	}
	return selectWorkspaceRegistryService(results)
}

func exactRegistryServiceMatches(query string, results []serviceSearchResult) []serviceSearchResult {
	query = strings.ToLower(strings.TrimSpace(query))
	matches := make([]serviceSearchResult, 0, 1)
	for _, result := range results {
		if query == strings.ToLower(result.ServiceID) || query == strings.ToLower(result.Slug) || query == strings.ToLower(result.Name) {
			matches = append(matches, result)
		}
	}
	return matches
}

func promptWorkspaceRegistryService(results []serviceSearchResult) (serviceSearchResult, error) {
	options := make([]huh.Option[int], len(results))
	for i, result := range results {
		options[i] = huh.NewOption(fmt.Sprintf("%s (%s)", result.Name, result.Slug), i)
	}
	selected := 0
	err := huh.NewSelect[int]().Title("Select a Registry service").Options(options...).Value(&selected).Run()
	if err != nil {
		return serviceSearchResult{}, err
	}
	return results[selected], nil
}

func promptWorkspaceRegistryServiceAdd(service serviceSearchResult) (bool, error) {
	confirmed := true
	err := huh.NewConfirm().
		Title(fmt.Sprintf("Add %s (%s) to the workspace config?", service.Name, service.Slug)).
		Affirmative("Add").Negative("Cancel").Value(&confirmed).Run()
	return confirmed, err
}

func workspaceServiceAddResult(target workspaceServiceAddTarget, version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return fmt.Sprintf("Added service %s to workspace config; planning will resolve its latest public version", target.slug)
	}
	return fmt.Sprintf("Added service %s with version %s to workspace config", target.slug, version)
}
