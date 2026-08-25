package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/charmbracelet/huh"
	"github.com/google/uuid"
)

type workspaceServiceAddTarget struct {
	slug             string
	serviceID        string
	configServiceID  string
	resolutionSource string
}

type workspaceServiceApplyOutcomeError struct {
	committed         []string
	failed            string
	failedCommitState string
	unattempted       []string
	recovery          string
	cause             error
}

// Error reports the complete bounded composite outcome and its exact safe rerun.
func (err *workspaceServiceApplyOutcomeError) Error() string {
	return fmt.Sprintf(
		"workspace service apply partially completed: committed=%s; failed=%s (%s); unattempted=%s; recovery=`%s`: %v",
		workspaceServiceOutcomeList(err.committed), err.failed, err.failedCommitState,
		workspaceServiceOutcomeList(err.unattempted), err.recovery, err.cause,
	)
}

// Unwrap retains the safe Engine/API error for telemetry classification.
func (err *workspaceServiceApplyOutcomeError) Unwrap() error { return err.cause }

// workspaceServiceOutcomeList renders an explicit none marker so partial state
// is never confused with a formatting omission.
func workspaceServiceOutcomeList(values []string) string {
	// Empty committed or unattempted sets are materially different from unknown
	// state, so keep that distinction visible in the slim result.
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ",")
}

// workspaceServiceApplyRecoveryCommand builds idempotent exact-ID additions for
// failed and unattempted targets without repeating an ambiguous discovery query.
func workspaceServiceApplyRecoveryCommand(targets []workspaceServiceAddTarget, version, configPath string) string {
	commands := make([]string, 0, len(targets))
	for _, target := range targets {
		parts := []string{
			"fused-cli workspace service add", shellQuoteWorkspaceServiceArg(target.slug),
			"--service-id", shellQuoteWorkspaceServiceArg(target.serviceID),
		}
		// Preserve an explicit version on every exact retry; omitted version keeps
		// Engine's existing current-version resolution behavior.
		if strings.TrimSpace(version) != "" {
			parts = append(parts, "--version", shellQuoteWorkspaceServiceArg(version))
		}
		parts = append(parts, "--apply", "-f", shellQuoteWorkspaceServiceArg(configPath))
		commands = append(commands, strings.Join(parts, " "))
	}
	return strings.Join(commands, " && ")
}

// shellQuoteWorkspaceServiceArg makes Registry identity and config paths inert
// when copied from recovery output into a POSIX-compatible shell.
func shellQuoteWorkspaceServiceArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

var selectWorkspaceRegistryService = promptWorkspaceRegistryService
var confirmWorkspaceRegistryService = promptWorkspaceRegistryServiceAdd
var selectExistingWorkspaceService = promptExistingWorkspaceService

// resolveWorkspaceServiceAddTargets preserves workspace-first resolution while
// batching the Engine and Registry reads needed by a multi-service composite.
func resolveWorkspaceServiceAddTargets(queries []string, explicitServiceID string, interactive bool) ([]workspaceServiceAddTarget, error) {
	// A singular explicit ID cannot safely describe more than one positional
	// service reference, so multi-add requires each UUID as its own argument.
	if strings.TrimSpace(explicitServiceID) != "" && len(queries) != 1 {
		return nil, errors.New("--service-id can only be used with one service reference")
	}
	// Validate the singular explicit escape hatch here because it intentionally
	// bypasses the shared batch preparer that rejects blank positional refs.
	if len(queries) == 1 && strings.TrimSpace(queries[0]) == "" {
		return nil, errors.New("service query must not be empty")
	}
	// The explicit flag remains a singular no-discovery escape hatch; every
	// ordinary singular or multi ref uses the same batched lookup policy below.
	if strings.TrimSpace(explicitServiceID) != "" {
		target, _, err := explicitWorkspaceServiceTarget(strings.TrimSpace(queries[0]), explicitServiceID)
		return []workspaceServiceAddTarget{target}, err
	}
	return resolveBatchWorkspaceServiceAddTargets(queries, interactive)
}

// resolveBatchWorkspaceServiceAddTargets coordinates the bounded Engine and
// Registry batch reads after singular flags and escape hatches are handled.
func resolveBatchWorkspaceServiceAddTargets(queries []string, interactive bool) ([]workspaceServiceAddTarget, error) {
	client, err := getAPIClient()
	if err != nil {
		return nil, err
	}
	targets, pending, lookupNames, normalizedQueries, err := prepareWorkspaceServiceAddTargets(queries)
	if err != nil {
		return nil, err
	}
	// A list containing only exact UUIDs is already fully resolved and must not
	// accidentally turn an empty Engine filter into an unbounded workspace read.
	if len(pending) == 0 {
		return deduplicateWorkspaceServiceTargets(targets), nil
	}

	workspaceServices, err := client.ListWorkspaceServices(lookupNames...)
	if err != nil {
		return nil, err
	}
	unresolved, err := resolveWorkspaceAddTargetsFromEngine(targets, pending, normalizedQueries, workspaceServices, interactive)
	if err != nil {
		return nil, err
	}
	// Registry is consulted only for references absent from the bounded Engine
	// result, preserving permission failures and workspace-first semantics.
	if len(unresolved) == 0 {
		return deduplicateWorkspaceServiceTargets(targets), nil
	}
	registryQueries := workspaceServiceQueriesAt(normalizedQueries, unresolved)
	registryResults, err := client.SearchServicesBatch(registryQueries)
	if err != nil {
		return nil, err
	}
	if err := resolveWorkspaceAddTargetsFromRegistry(targets, unresolved, normalizedQueries, registryResults, interactive); err != nil {
		return nil, err
	}
	return deduplicateWorkspaceServiceTargets(targets), nil
}

// prepareWorkspaceServiceAddTargets resolves exact UUIDs locally and builds the
// normalized positional inputs for the one bounded Engine discovery request.
func prepareWorkspaceServiceAddTargets(queries []string) ([]workspaceServiceAddTarget, []int, []string, []string, error) {
	targets := make([]workspaceServiceAddTarget, len(queries))
	pending := make([]int, 0, len(queries))
	lookupNames := make([]string, 0, len(queries))
	normalizedQueries := make([]string, len(queries))
	for index, query := range queries {
		trimmedQuery := strings.TrimSpace(query)
		// Reject empty positional references before any network or config work so
		// a quoted whitespace argument cannot broaden a catalogue search.
		if trimmedQuery == "" {
			return nil, nil, nil, nil, errors.New("service query must not be empty")
		}
		normalizedQueries[index] = trimmedQuery
		target, explicit, err := explicitWorkspaceServiceTarget(trimmedQuery, "")
		// Exact UUIDs are authoritative and need neither Engine nor Registry
		// discovery; all ordinary references share the batched lookup below.
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if explicit {
			targets[index] = target
			continue
		}
		pending = append(pending, index)
		lookupNames = append(lookupNames, workspaceServiceLookupName(trimmedQuery))
	}
	return targets, pending, lookupNames, normalizedQueries, nil
}

// resolveWorkspaceAddTargetsFromEngine maps one bounded Engine result set onto
// positional requests and returns only the references needing Registry fallback.
func resolveWorkspaceAddTargetsFromEngine(targets []workspaceServiceAddTarget, pending []int, queries []string, services []api.WorkspaceService, interactive bool) ([]int, error) {
	unresolved := make([]int, 0, len(pending))
	for _, index := range pending {
		target, found, err := workspaceServiceTargetFromResults(queries[index], services, interactive)
		if err != nil {
			return nil, err
		}
		// A miss is kept positional so the one batched Registry request can fill
		// exactly that slot without re-resolving successful workspace matches.
		if !found {
			unresolved = append(unresolved, index)
			continue
		}
		targets[index] = target
	}
	return unresolved, nil
}

// resolveWorkspaceAddTargetsFromRegistry finishes only Engine misses using the
// existing ambiguity, exact-match, provider-qualified, and confirmation rules.
func resolveWorkspaceAddTargetsFromRegistry(targets []workspaceServiceAddTarget, unresolved []int, queries []string, results map[string][]api.Service, interactive bool) error {
	for _, index := range unresolved {
		query := queries[index]
		target, err := registryServiceTargetFromResults(query, serviceSearchResultsFromAPI(results[query]), interactive)
		if err != nil {
			return err
		}
		targets[index] = target
	}
	return nil
}

// workspaceServiceQueriesAt projects unresolved positional references without
// re-querying or broadening the Registry catalogue request.
func workspaceServiceQueriesAt(queries []string, indexes []int) []string {
	result := make([]string, 0, len(indexes))
	for _, index := range indexes {
		result = append(result, queries[index])
	}
	return result
}

// deduplicateWorkspaceServiceTargets prevents repeated CLI arguments from
// causing duplicate config output or redundant scoped activation mutations.
func deduplicateWorkspaceServiceTargets(targets []workspaceServiceAddTarget) []workspaceServiceAddTarget {
	seen := make(map[string]bool, len(targets))
	result := make([]workspaceServiceAddTarget, 0, len(targets))
	for _, target := range targets {
		// Stable Registry identity, not display slug, determines whether two
		// independently resolved references point at the same service.
		if seen[target.serviceID] {
			continue
		}
		seen[target.serviceID] = true
		result = append(result, target)
	}
	return result
}

// explicitWorkspaceServiceTarget recognizes the two exact-ID escape hatches
// and marks explicit flag IDs for persistence in declarative configuration.
func explicitWorkspaceServiceTarget(query, serviceID string) (workspaceServiceAddTarget, bool, error) {
	serviceID = strings.TrimSpace(serviceID)
	// A flag-supplied ID is attached to the human-readable config key, while an
	// ID positional reference remains its own stable key for backward compatibility.
	if serviceID != "" {
		if _, err := uuid.Parse(serviceID); err != nil {
			return workspaceServiceAddTarget{}, false, errors.New("--service-id must be a valid Registry service UUID")
		}
		return workspaceServiceAddTarget{slug: query, serviceID: serviceID, configServiceID: serviceID, resolutionSource: "explicit"}, true, nil
	}
	// Exact positional UUIDs are already authoritative and must not trigger
	// catalogue lookup or interactive ambiguity handling.
	if _, err := uuid.Parse(query); err == nil {
		return workspaceServiceAddTarget{slug: query, serviceID: query, configServiceID: query, resolutionSource: "explicit"}, true, nil
	}
	return workspaceServiceAddTarget{}, false, nil
}

// workspaceServiceTargetFromResults applies exact provider-aware identity and
// the shared interactive ambiguity rule to an already bounded Engine result.
func workspaceServiceTargetFromResults(query string, services []api.WorkspaceService, interactive bool) (workspaceServiceAddTarget, bool, error) {
	services = exactWorkspaceServiceMatches(query, services)
	// Zero exact identities is a genuine workspace miss; the caller may use the
	// Registry fallback without treating an authorization error as absence.
	if len(services) == 0 {
		return workspaceServiceAddTarget{}, false, nil
	}
	// Multiple provider-scoped identities require an explicit choice because a
	// bare product slug is not globally unique.
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
		slug: slug, serviceID: service.ServiceID, resolutionSource: "workspace",
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

// registryServiceTargetFromResults converts the shared Registry search result
// into the one canonical config/activation identity selected by existing rules.
func registryServiceTargetFromResults(query string, results []serviceSearchResult, interactive bool) (workspaceServiceAddTarget, error) {
	selected, err := chooseRegistryService(query, results, interactive)
	if err != nil {
		return workspaceServiceAddTarget{}, err
	}
	// Both identities are required because config uses canonical slug while
	// scoped activation is anchored to immutable Registry service identity.
	if strings.TrimSpace(selected.ServiceID) == "" || strings.TrimSpace(selected.Slug) == "" {
		return workspaceServiceAddTarget{}, errors.New("Registry returned a service without a reusable ID and slug")
	}
	// Interactive Registry fallback retains its explicit confirmation step even
	// when several service references are composed in one command.
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
		slug: selected.Slug, serviceID: selected.ServiceID, resolutionSource: "registry",
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

// workspaceServiceAddResult explains whether Engine or a later declarative plan
// resolves an omitted version while retaining the established success wording.
func workspaceServiceAddResult(target workspaceServiceAddTarget, version string, apply bool) string {
	version = strings.TrimSpace(version)
	// Immediate activation delegates latest-version resolution to the scoped
	// Engine endpoint, while config-only authoring leaves it for workspace plan.
	if version == "" {
		if apply {
			return fmt.Sprintf("Added service %s to workspace config; activation will resolve its latest public version", target.slug)
		}
		return fmt.Sprintf("Added service %s to workspace config; planning will resolve its latest public version", target.slug)
	}
	return fmt.Sprintf("Added service %s with version %s to workspace config", target.slug, version)
}

func workspaceServiceViewURL(engineURL, serviceID string) string {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return ""
	}
	base, err := canonicalEngineURL(engineURL)
	if err != nil {
		return ""
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return ""
	}
	// A configured query belongs to API transport, not to the UI destination.
	// Keeping the service ID as one escaped segment prevents malformed IDs from
	// changing the route while still leaving the complete URL useful to agents.
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/") + "/integrations/" + url.PathEscape(serviceID)
}
