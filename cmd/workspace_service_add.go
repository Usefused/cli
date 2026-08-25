package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/Usefused/cli/internal/api"
	"github.com/charmbracelet/huh"
	"github.com/google/uuid"
)

type workspaceServiceAddTarget struct {
	slug             string
	serviceID        string
	configServiceID  string
	resolutionSource string
	requestedRefs    []string
}

const (
	workspaceServiceApplyErrorCode  = "workspace_service_apply_partial"
	workspaceServiceApplyPhase      = "engine_scoped_activation"
	workspaceServiceSafeRef         = "<service>"
	workspaceServiceSafeID          = "<service-id>"
	workspaceServiceSafeValue       = "<value>"
	workspaceServiceSafeRecovery    = "recovery unavailable; rerun with exact service IDs"
	workspaceServiceMaxFieldBytes   = 128
	workspaceServiceMaxCommandBytes = 4096
)

type workspaceServiceApplyOutcomeError struct {
	code                 string
	phase                string
	requestID            string
	committed            []string
	failed               string
	failedCommitState    string
	failedCommitPossible bool
	unattempted          []string
	recovery             string
	cause                error
}

// Error reports the complete bounded composite outcome and its exact safe rerun.
func (err *workspaceServiceApplyOutcomeError) Error() string {
	return fmt.Sprintf(
		"workspace service apply partially completed: code=%s; phase=%s; request_id=%s; committed=%s; failed=%s (%s, commit_possible=%t); unattempted=%s; %s; recovery=`%s`",
		safeWorkspaceOutcomeToken(err.code, workspaceServiceApplyErrorCode),
		safeWorkspaceOutcomeToken(err.phase, workspaceServiceApplyPhase),
		safeWorkspaceRequestID(err.requestID), workspaceServiceOutcomeList(err.committed), safeWorkspaceServiceRef(err.failed),
		safeWorkspaceOutcomeToken(err.failedCommitState, "unknown"), err.failedCommitPossible,
		workspaceServiceOutcomeList(err.unattempted), workspaceServiceFailureSummary(err.cause), safeWorkspaceRecoveryCommand(err.recovery),
	)
}

// Unwrap retains the original Engine/API error for internal classification
// without rendering its potentially unsafe message.
func (err *workspaceServiceApplyOutcomeError) Unwrap() error { return err.cause }

// workspaceServiceOutcomeList renders an explicit none marker so partial state
// is never confused with a formatting omission.
func workspaceServiceOutcomeList(values []string) string {
	// Empty committed or unattempted sets are materially different from unknown
	// state, so keep that distinction visible in the slim result.
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(safeWorkspaceServiceRefs(values), ",")
}

// safeWorkspaceServiceRefs projects outcome groups through the shared strict
// reference renderer while preserving their committed positional order.
func safeWorkspaceServiceRefs(values []string) []string {
	safeValues := make([]string, 0, len(values))
	for _, value := range values {
		safeValues = append(safeValues, safeWorkspaceServiceRef(value))
	}
	return safeValues
}

// workspaceServiceApplyRecoveryCommand builds idempotent exact-ID additions for
// failed and unattempted targets without repeating an ambiguous discovery query.
func workspaceServiceApplyRecoveryCommand(targets []workspaceServiceAddTarget, version, configPath string) string {
	commands := make([]string, 0, len(targets))
	for _, target := range targets {
		parts := []string{
			"fused-cli workspace service add", shellQuoteWorkspaceServiceArg(safeWorkspaceServiceRef(target.slug)),
			"--service-id", shellQuoteWorkspaceServiceArg(safeWorkspaceServiceID(target.serviceID)),
		}
		// Preserve an explicit version on every exact retry; omitted version keeps
		// Engine's existing current-version resolution behavior.
		if strings.TrimSpace(version) != "" {
			parts = append(parts, "--version", shellQuoteWorkspaceServiceArg(safeWorkspaceRecoveryValue(version)))
		}
		parts = append(parts, "--apply", "-f", shellQuoteWorkspaceServiceArg(safeWorkspaceRecoveryValue(configPath)))
		commands = append(commands, strings.Join(parts, " "))
	}
	return strings.Join(commands, " && ")
}

// safeWorkspaceServiceRef admits only bounded UUIDs or canonical slug grammar
// so remote catalogue text cannot inject URLs or terminal controls into output.
func safeWorkspaceServiceRef(value string) string {
	value = strings.TrimSpace(value)
	// Credential-shaped values are never legitimate display slugs and must not
	// survive merely because their characters overlap the slug alphabet.
	if strings.Contains(strings.ToLower(value), "fsk_") {
		return workspaceServiceSafeRef
	}
	// Exact IDs are safe canonical references even though they are not slugs.
	if parsedID, err := uuid.Parse(value); err == nil {
		return parsedID.String()
	}
	parsed := api.ParseServiceReference(value)
	// Provider-qualified display requires two safe slug segments; incomplete or
	// unsafe identities collapse to a fixed non-secret marker.
	if parsed.Qualified {
		if safeWorkspaceServiceSegment(parsed.Provider) && safeWorkspaceServiceSegment(parsed.Slug) {
			return "@" + parsed.Provider + "/" + parsed.Slug
		}
		return workspaceServiceSafeRef
	}
	// Bare resolved slugs use the same strict segment grammar and byte bound.
	if safeWorkspaceServiceSegment(value) {
		return value
	}
	return workspaceServiceSafeRef
}

// safeWorkspaceServiceSegment accepts the canonical ASCII slug alphabet while
// rejecting URL punctuation, whitespace, controls, and unbounded remote text.
func safeWorkspaceServiceSegment(value string) bool {
	// Service identity fields are non-empty and small enough for one terminal line.
	if value == "" || len(value) > workspaceServiceMaxFieldBytes {
		return false
	}
	for _, char := range value {
		// Registry slugs and provider handles are intentionally narrower than free
		// text so recovery output cannot become an executable URL or escape sequence.
		if !isWorkspaceServiceTokenChar(char) {
			return false
		}
	}
	return true
}

// isWorkspaceServiceTokenChar centralizes the canonical ASCII alphabet shared
// by safe slug and classifier-token rendering.
func isWorkspaceServiceTokenChar(char rune) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
		char >= '0' && char <= '9' || strings.ContainsRune("._-", char)
}

// safeWorkspaceServiceID returns one canonical UUID or a fixed inert marker for
// malformed remote identity that must never be copied into a recovery command.
func safeWorkspaceServiceID(value string) string {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	// Recovery is ID-pinned only when the immutable identity is a valid UUID.
	if err != nil {
		return workspaceServiceSafeID
	}
	return parsed.String()
}

// safeWorkspaceRecoveryValue bounds locally supplied version and config-path
// arguments before POSIX quoting so copied recovery never contains controls.
func safeWorkspaceRecoveryValue(value string) string {
	value = strings.TrimSpace(value)
	// URLs, credential-shaped text, empty values, and long fields are unsafe to
	// reflect even inside quotes because terminal output is still user-visible.
	if value == "" || len(value) > workspaceServiceMaxCommandBytes ||
		strings.Contains(strings.ToLower(value), "://") || strings.Contains(strings.ToLower(value), "fsk_") {
		return workspaceServiceSafeValue
	}
	for _, char := range value {
		// Shell quoting does not neutralize terminal controls such as newlines.
		if unicode.IsControl(char) {
			return workspaceServiceSafeValue
		}
	}
	return value
}

// safeWorkspaceRecoveryCommand verifies the internally generated command again
// at the presentation boundary so manually wrapped errors also remain inert.
func safeWorkspaceRecoveryCommand(value string) string {
	value = strings.TrimSpace(value)
	// A recovery string is useful only when bounded and free from URLs, secrets,
	// or terminal controls; unsafe values are replaced rather than partially edited.
	if value == "" || len(value) > workspaceServiceMaxCommandBytes ||
		strings.Contains(strings.ToLower(value), "://") || strings.Contains(strings.ToLower(value), "fsk_") {
		return workspaceServiceSafeRecovery
	}
	for _, char := range value {
		// Commands may contain ordinary spaces, but never line or terminal controls.
		if unicode.IsControl(char) {
			return workspaceServiceSafeRecovery
		}
	}
	return value
}

// safeWorkspaceOutcomeToken admits only bounded machine-token characters for
// stable code, phase, and commit-state fields in human and JSON output.
func safeWorkspaceOutcomeToken(value, fallback string) string {
	value = strings.TrimSpace(value)
	// Stable classifier fields must never contain remote prose or delimiters.
	if value == "" || len(value) > workspaceServiceMaxFieldBytes {
		return fallback
	}
	for _, char := range value {
		// Token grammar intentionally excludes URL and terminal punctuation.
		if !isWorkspaceServiceTokenChar(char) {
			return fallback
		}
	}
	return value
}

// safeWorkspaceRequestID preserves a validated correlation value or emits an
// explicit unavailable marker without reflecting malformed text.
func safeWorkspaceRequestID(value string) string {
	value = strings.TrimSpace(value)
	// Root validation owns the request-ID grammar; rechecking protects errors
	// created directly by tests or future internal callers. Credential-shaped
	// values are excluded even though their characters pass that grammar.
	if !validRequestID(value) || value == "" || strings.Contains(strings.ToLower(value), "fsk_") {
		return "unavailable"
	}
	return value
}

// workspaceServiceFailureSummary exposes only bounded classifier metadata from
// the wrapped cause while Unwrap retains the original error for errors.As.
func workspaceServiceFailureSummary(cause error) string {
	var apiError *api.APIError
	// Typed API status is safe numeric context; remote messages, URLs, and detail
	// strings are intentionally excluded from the human composite outcome.
	if errors.As(cause, &apiError) {
		code := safeWorkspaceOutcomeToken(apiError.Code, "request_failed")
		if apiError.HTTPStatus >= 100 && apiError.HTTPStatus <= 599 {
			return fmt.Sprintf("failure_code=%s; http_status=%d", code, apiError.HTTPStatus)
		}
		return "failure_code=" + code
	}
	return "failure_code=request_failed"
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
		parsedReference := api.ParseServiceReference(trimmedQuery)
		// Composite add uses the identity-oriented set resolver, so a leading @
		// must be a complete provider/service reference rather than lexical text.
		if parsedReference.ProviderPrefixed && !parsedReference.Qualified {
			return nil, nil, nil, nil, errors.New("provider-qualified service references must use @provider/service-slug")
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
		lookupNames = append(lookupNames, api.ServiceLookupName(trimmedQuery))
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
	seen := make(map[string]int, len(targets))
	result := make([]workspaceServiceAddTarget, 0, len(targets))
	for _, target := range targets {
		// Stable Registry identity, not display slug, determines whether two
		// independently resolved references point at the same service. Preserve
		// every requested alias so config identity checks cannot be bypassed by dedupe.
		if existingIndex, exists := seen[target.serviceID]; exists {
			result[existingIndex].requestedRefs = appendUniqueWorkspaceServiceRefs(result[existingIndex].requestedRefs, target.requestedRefs...)
			continue
		}
		seen[target.serviceID] = len(result)
		result = append(result, target)
	}
	return result
}

// appendUniqueWorkspaceServiceRefs retains stable positional aliases without
// duplicating identity checks or exposing them as separate activation targets.
func appendUniqueWorkspaceServiceRefs(existing []string, refs ...string) []string {
	seen := make(map[string]bool, len(existing)+len(refs))
	// Seed the set from aliases already retained by an earlier target.
	for _, ref := range existing {
		seen[ref] = true
	}
	// Append only new aliases in their original positional order.
	for _, ref := range refs {
		// Repeated aliases add no validation coverage and should not inflate the
		// bounded config-edit DTO.
		if seen[ref] {
			continue
		}
		seen[ref] = true
		existing = append(existing, ref)
	}
	return existing
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
		return workspaceServiceAddTarget{slug: query, serviceID: serviceID, configServiceID: serviceID, resolutionSource: "explicit", requestedRefs: []string{query}}, true, nil
	}
	// Exact positional UUIDs are already authoritative and must not trigger
	// catalogue lookup or interactive ambiguity handling.
	if _, err := uuid.Parse(query); err == nil {
		return workspaceServiceAddTarget{slug: query, serviceID: query, configServiceID: query, resolutionSource: "explicit", requestedRefs: []string{query}}, true, nil
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
		slug: slug, serviceID: service.ServiceID, resolutionSource: "workspace", requestedRefs: []string{query},
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
	parsed := api.ParseServiceReference(query)
	query = strings.ToLower(parsed.Raw)
	matches := make([]api.WorkspaceService, 0, len(services))
	for _, service := range services {
		slug := strings.ToLower(strings.TrimSpace(service.ServiceSlug))
		name := strings.ToLower(strings.TrimSpace(service.ServiceName))
		// Qualified identities must match the enriched display slug exactly;
		// ordinary text may also match the provider-free Engine lookup segment.
		if query == name || query == slug || !parsed.Qualified && query == strings.ToLower(api.ServiceLookupName(slug)) {
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
		slug: selected.Slug, serviceID: selected.ServiceID, resolutionSource: "registry", requestedRefs: []string{query},
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
