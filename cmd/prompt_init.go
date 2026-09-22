package cmd

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

type promptInitOptions struct {
	update   string
	kind     string
	name     string
	version  string
	language string
	bucket   string
}

type promptInitPlan struct {
	goal          string
	mode          unifiedInitMode
	primary       scaffoldRequest
	webhook       *scaffoldRequest
	reusesWebhook bool
	resolved      []sdkInitResolvedService
	baseHash      string
	baseVersion   string
}

var selectPromptWebhookAttachment = promptWebhookAttachment
var confirmPromptPlan = promptInitConfirmation

// newPromptInitCommand creates or extends apps while retaining the reviewed deterministic lifecycle.
func newPromptInitCommand() *cobra.Command {
	opts := &promptInitOptions{version: defaultScaffoldVersion}
	command := &cobra.Command{
		Use:   "prompt <goal>",
		Short: "Create or update an SDK, MCP server, or REST app from a natural-language goal",
		Long: `Create or update an SDK, MCP server, or direct REST app from a natural-language goal.

Use --update <app-name> or name an existing app in an update goal. Updates resolve a local
config (use -f to disambiguate), preserve its settings, and publish an immutable successor.
Sequential runtime intent can produce a Unified Operation for TypeScript/Python SDKs or MCP.
Composition uses exact operation contracts and the Registry's configured drafting model.

Prompt uses Jev through Fused Registry to select operations from search intent and operation names/descriptions.
No additional API key is required. Prompt resolves exact selections before showing its proposal.
If an SDK or MCP goal asks to receive provider events, prompt also creates or reuses a webhook
registration and attaches it to the app. Direct REST apps cannot receive webhook events.
Every proposal requires interactive terminal confirmation before changes are applied.`,
		Args: cobra.MinimumNArgs(1),
		RunE: WithTelemetry("cli.prompt", func(cmd *cobra.Command, args []string) error {
			// LLM-derived scope always requires a human to review the grounded proposal before mutation.
			if err := requireInteractive("prompt requires a terminal confirmation; use sdk init, mcp init, or init --api for deterministic automation"); err != nil {
				return err
			}
			client, err := getAPIClient()
			// Intent parsing and Registry resolution must share the authenticated command client.
			if err != nil {
				return err
			}
			goal := strings.TrimSpace(strings.Join(args, " "))
			plan, err := buildPromptInitPlan(cmd, client, goal, opts)
			// Resolution must complete before a proposal can imply the goal is executable.
			if err != nil {
				return err
			}
			// Proposal rendering is part of informed authorization and must succeed before confirmation.
			if err := printPromptInitPlan(cmd, plan); err != nil {
				return err
			}
			confirmed, err := confirmPromptPlan("Apply this proposed Fused configuration?")
			// Terminal failures cannot be interpreted as affirmative authorization.
			if err != nil {
				return err
			}
			// Cancellation is explicit and occurs before either local or remote resource changes.
			if !confirmed {
				return errors.New("prompt initialization cancelled")
			}
			return executePromptInitPlan(cmd, plan)
		}),
	}
	command.Flags().StringVar(&opts.kind, "kind", "", "Constrain the primary output to sdk, mcp, or rest")
	command.Flags().StringVar(&opts.update, "update", "", "Update an existing app by name, preserving its local config")
	command.Flags().StringVarP(&opts.name, "name", "n", "", "Override the suggested app name")
	command.Flags().StringVarP(&opts.version, "version", "v", defaultScaffoldVersion, "App version")
	command.Flags().StringVarP(&opts.language, "language", "l", "", "Override the generated SDK language")
	command.Flags().StringVar(&opts.bucket, "bucket", "", "Existing bucket to bind to the app")
	return command
}

// buildPromptInitPlan discloses model processing and grounds creation or additive updates before any mutation.
func buildPromptInitPlan(cmd *cobra.Command, client *api.Client, goal string, opts *promptInitOptions) (promptInitPlan, error) {
	// Empty whitespace cannot be classified into a safe app capability boundary.
	if goal == "" {
		return promptInitPlan{}, errors.New("prompt requires a non-empty goal")
	}
	// An explicitly empty update flag must never fall through to app creation.
	if cmd.Flags().Changed("update") && strings.TrimSpace(opts.update) == "" {
		return promptInitPlan{}, errors.New("--update requires an existing app name")
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "Prompt operation selection uses Jev via Fused Registry. Search intent, operation names, and descriptions are sent to Jev. No extra API key is needed.")
	fmt.Fprintln(cmd.ErrOrStderr(), "Intent parsing uses Registry's configured language model with your goal and, for updates, the app name, kind, and service names.")
	target, err := promptExplicitUpdateTarget(opts)
	// Explicit update targets must exist before intent parsing can interpret their service context.
	if err != nil {
		return promptInitPlan{}, err
	}
	intent, err := client.ParsePromptIntentWithContext(goal, promptUpdateContext(target))
	// Registry model failures must remain upstream of service activation and config publication.
	if err != nil {
		return promptInitPlan{}, fmt.Errorf("parse prompt intent: %w", err)
	}
	hadContext := target != nil
	target, err = promptIntentUpdateTarget(target, intent)
	// A missing or ambiguous update target cannot silently become a newly created app.
	if err != nil {
		return promptInitPlan{}, err
	}
	// A naturally named update gets the same service context as --update before capability resolution.
	if target != nil && !hadContext {
		intent, err = client.ParsePromptIntentWithContext(goal, promptUpdateContext(target))
		if err != nil {
			return promptInitPlan{}, fmt.Errorf("parse update intent: %w", err)
		}
		target, err = promptIntentUpdateTarget(target, intent)
		if err != nil {
			return promptInitPlan{}, err
		}
	}
	// An intent without services cannot produce an executable app and must not become an empty local skeleton.
	if len(intent.Services) == 0 {
		return promptInitPlan{}, errors.New("prompt did not identify a Registry service; name at least one service")
	}
	mode, err := resolvePromptInitMode(opts.kind, intent.Kind)
	// Existing authored kind takes precedence over an inferred output kind.
	if target != nil {
		mode, err = promptUpdateMode(target, opts.kind)
	}
	// Unknown or forbidden primary outcomes must fail before webhook compatibility is evaluated.
	if err != nil {
		return promptInitPlan{}, err
	}
	webhookRequested := intent.WebhookRequested || promptIntentHasEvents(intent.Services)
	// Direct REST execution has no open receiver transport; SDK streams and MCP resource notifications do.
	if webhookRequested && mode == unifiedInitModeAPI {
		return promptInitPlan{}, fmt.Errorf("%s apps cannot receive webhook events; request an SDK or MCP app", promptModeLabel(mode))
	}
	name := resolvePromptInitName(opts.name, intent.Name, intent.Services, mode)
	// An update retains the stable family name, never the parser's suggested new name.
	if target != nil {
		name, _ = unifiedExtendConfigName(target.config)
	}
	path, err := scaffoldTargetPath(promptConfigKind(mode), name, ConfigFile)
	// The primary path must be safe before any Registry selection or secondary webhook path is prepared.
	if err != nil {
		return promptInitPlan{}, err
	}
	language, err := resolvePromptInitLanguage(mode, opts.language, intent.Language)
	// Unsupported or inapplicable emitters are intent errors, not values to silently ignore.
	if err != nil {
		return promptInitPlan{}, err
	}
	request := scaffoldRequest{
		kind: promptConfigKind(mode), name: name, path: path, services: promptScaffoldServices(intent.Services),
		version: opts.version, versionSet: true, description: strings.TrimSpace(intent.Description), language: language,
		bucket: strings.TrimSpace(opts.bucket), bucketSet: strings.TrimSpace(opts.bucket) != "",
		generate: mode == unifiedInitModeSDK, generateSet: mode == unifiedInitModeSDK || mode == unifiedInitModeAPI,
		skipConfirmation: true,
	}
	// Update identity, service pins, and omitted settings come from the existing authored document.
	if target != nil {
		request, err = promptUpdateRequest(cmd, request, target, opts)
		if err != nil {
			return promptInitPlan{}, err
		}
	}
	// Blank model service names cannot be resolved and must not degrade into an empty app scaffold.
	if len(request.services) == 0 {
		return promptInitPlan{}, errors.New("prompt did not identify a valid Registry service name")
	}
	// MCP requires a user-facing runtime description, so the original goal is the stable fallback.
	if mode == unifiedInitModeMCP && request.description == "" {
		request.description = goal
	}
	request.descriptionSet = mode == unifiedInitModeMCP && target == nil
	request.languageSet = mode == unifiedInitModeSDK && target == nil
	resolvedRequest, resolved, err := resolveSDKInitServices(request, client)
	// Ambiguous services and immutable-version failures must stop before endpoint or event expansion.
	if err != nil {
		return promptInitPlan{}, err
	}
	// Canonical service aliases must inherit the app's existing immutable pin before operation classification.
	resolvedRequest, resolved = pinPromptUpdateServices(resolvedRequest, resolved, target)
	resolvedRequest, webhookServices, err := resolvePromptSelections(client, resolvedRequest, resolved, intent.Services, webhookRequested)
	// Exact operation and event resolution is required for a reviewable proposal.
	if err != nil {
		return promptInitPlan{}, err
	}
	plan := promptInitPlan{goal: goal, mode: mode, primary: resolvedRequest, resolved: resolved}
	// Retain an exact baseline so a changed local file requires a fresh human review.
	if target != nil {
		plan.baseHash = target.config.SourceHash
		_, plan.baseVersion, _, _ = unifiedExtendIdentity(target.config)
	}
	// A plain capability list keeps independent methods; only explicit ordered intent enters composition.
	if intent.Sequential {
		plan.primary.unifiedOperations, err = draftPromptUnifiedOperation(cmd, client, plan)
		if err != nil {
			return promptInitPlan{}, err
		}
	}
	// An app without inbound events needs no registration or attachment orchestration.
	if !webhookRequested {
		return finalizePromptPlan(client, plan)
	}
	attachment, reuse, err := resolvePromptWebhookAttachment(client, name, resolved, webhookServices)
	// Registration lookup failures cannot be treated as proof that a new webhook should be created.
	if err != nil {
		return promptInitPlan{}, err
	}
	plan.primary.webhookAttachment = attachment
	plan.primary.webhookAttachmentSet = true
	plan.reusesWebhook = reuse
	// Existing coverage is attached directly; only a missing common registration needs its own init lifecycle.
	if reuse {
		return finalizePromptPlan(client, plan)
	}
	webhookPath, err := scaffoldTargetPath(configfile.KindWebhook, attachment, "")
	// The secondary config always uses webhook discovery paths, even when -f overrides the primary app path.
	if err != nil {
		return promptInitPlan{}, err
	}
	webhookRequest := scaffoldRequest{
		kind: configfile.KindWebhook, name: attachment, path: webhookPath,
		services: promptWebhookScaffoldServices(resolved, webhookServices), webhookSecrets: map[string]string{}, skipConfirmation: true,
	}
	plan.webhook = &webhookRequest
	return finalizePromptPlan(client, plan)
}

// resolvePromptInitMode applies an explicit constraint or validates the Registry's primary output classification.
func resolvePromptInitMode(override, inferred string) (unifiedInitMode, error) {
	value := strings.ToLower(strings.TrimSpace(inferred))
	// A user-supplied kind is authoritative but still limited to prompt's three primary outputs.
	if strings.TrimSpace(override) != "" {
		value = strings.ToLower(strings.TrimSpace(override))
	}
	// Only these three outcomes have complete init lifecycles and user-facing runtime semantics in prompt.
	switch value {
	case "sdk":
		return unifiedInitModeSDK, nil
	case "mcp":
		return unifiedInitModeMCP, nil
	case "rest", "api":
		return unifiedInitModeAPI, nil
	default:
		return "", fmt.Errorf("prompt kind must be sdk, mcp, or rest; got %q", value)
	}
}

// promptConfigKind maps user-facing REST to the shared SDK config schema with generation disabled.
func promptConfigKind(mode unifiedInitMode) configfile.ConfigKind {
	// MCP is the only prompt output with a distinct config kind.
	if mode == unifiedInitModeMCP {
		return configfile.KindMCP
	}
	return configfile.KindSDK
}

// promptModeLabel returns the public spelling used in proposal and compatibility errors.
func promptModeLabel(mode unifiedInitMode) string {
	// Direct API is presented as REST even though its internal lifecycle mode is api.
	if mode == unifiedInitModeAPI {
		return "REST"
	}
	return strings.ToUpper(string(mode))
}

// resolvePromptInitName prefers explicit and inferred names before deriving a stable service-based fallback.
func resolvePromptInitName(override, inferred string, services []api.IntentService, mode unifiedInitMode) string {
	// Explicit naming remains authoritative over model suggestions.
	if value := strings.TrimSpace(override); value != "" {
		return value
	}
	// Registry suggestions preserve the user's semantic goal when available.
	if value := strings.TrimSpace(inferred); value != "" {
		return value
	}
	base := safeConfigFileName(strings.ToLower(strings.TrimSpace(services[0].Name)))
	// A malformed service suggestion still gets a deterministic app prefix for later validation.
	if base == "" {
		base = "fused"
	}
	return base + "-" + strings.ToLower(promptModeLabel(mode))
}

// resolvePromptInitLanguage validates package emitters and prevents REST or MCP requests from silently accepting SDK-only overrides.
func resolvePromptInitLanguage(mode unifiedInitMode, override, inferred string) (string, error) {
	selected := strings.ToLower(strings.TrimSpace(inferred))
	// A language flag is meaningful only for generated SDK output.
	if strings.TrimSpace(override) != "" && mode != unifiedInitModeSDK {
		return "", errors.New("--language can only be used with --kind sdk")
	}
	// Explicit language selection overrides the inferred emitter.
	if strings.TrimSpace(override) != "" {
		selected = strings.ToLower(strings.TrimSpace(override))
	}
	// Non-SDK configs omit package language entirely.
	if mode == unifiedInitModeMCP {
		return "", nil
	}
	// Direct REST uses the shared SDK schema default without generating a package.
	if mode == unifiedInitModeAPI {
		return defaultScaffoldLanguage, nil
	}
	// An omitted SDK language uses the established TypeScript default.
	if selected == "" {
		selected = defaultScaffoldLanguage
	}
	if !promptSupportsSDKLanguage(selected) {
		return "", fmt.Errorf("SDK language must be typescript, python, or go; got %q", selected)
	}
	return selected, nil
}

// promptSupportsSDKLanguage mirrors the package emitters accepted by SDK config validation.
func promptSupportsSDKLanguage(language string) bool {
	return language == "typescript" || language == "python" || language == "go"
}

// promptScaffoldServices preserves first service order while removing duplicate natural-language mentions.
func promptScaffoldServices(intents []api.IntentService) []scaffoldService {
	services := make([]scaffoldService, 0, len(intents))
	seen := make(map[string]bool, len(intents))
	for _, intent := range intents {
		name := strings.TrimSpace(intent.Name)
		identity := strings.ToLower(name)
		// Empty and duplicate model entries cannot add an independent Registry selection.
		if name == "" || seen[identity] {
			continue
		}
		seen[identity] = true
		services = append(services, scaffoldService{name: name})
	}
	return services
}

// promptIntentHasEvents tolerates an event-bearing service even if the model omitted the redundant top-level boolean.
func promptIntentHasEvents(intents []api.IntentService) bool {
	for _, intent := range intents {
		// Any explicit event query is sufficient evidence that inbound delivery was requested.
		if len(promptEventQueries(intent)) > 0 {
			return true
		}
	}
	return false
}

// resolvePromptSelections uses licensed Jev discovery for operations and preserves explicit operation/event boundaries.
func resolvePromptSelections(client *api.Client, request scaffoldRequest, resolved []sdkInitResolvedService, intents []api.IntentService, webhookRequested bool) (scaffoldRequest, map[string]bool, error) {
	intentByAlias := promptIntentAliases(intents)
	explicitEventServices := make(map[string]bool)
	for _, service := range resolved {
		intent := promptIntentForResolvedService(service, intentByAlias)
		queries := promptOperationQueries(intent)
		// Conflicting model fields cannot be interpreted as both a narrow and complete capability boundary.
		if intent.SelectAllOperations && len(queries) > 0 {
			return scaffoldRequest{}, nil, fmt.Errorf("prompt returned both specific operations and all operations for %s@%s", service.target.slug, service.version)
		}
		// Explicit complete-catalogue language is preserved as select-all rather than searched as prose.
		if intent.SelectAllOperations {
			request.selectAll = appendUniquePromptString(request.selectAll, service.target.slug)
		}
		for _, query := range queries {
			// Each intent uses the licensed classifier over this exact service version's complete catalogue.
			operation, err := client.ClassifyPromptOperation(service.target.serviceID, service.version, query)
			// Provider failures must stop proposal creation rather than fall back to ranked text results.
			if err != nil {
				return scaffoldRequest{}, nil, fmt.Errorf("classify operation for %s@%s: %w", service.target.slug, service.version, err)
			}
			// A no-match is actionable input feedback, never permission to select every operation.
			if strings.TrimSpace(operation) == "" {
				return scaffoldRequest{}, nil, fmt.Errorf("no operation matched %q for %s@%s", query, service.target.slug, service.version)
			}
			request.operations = appendUniquePromptOperation(request.operations, scaffoldOperation{service: service.target.slug, operation: operation})
		}
		// Per-service event queries establish which services need attachment coverage when a goal names several providers.
		if len(promptEventQueries(intent)) > 0 {
			explicitEventServices[service.target.slug] = true
		}
	}
	webhookServices := make(map[string]bool)
	// Operation-only apps require explicit capabilities; missing parser output must not widen scope.
	if !webhookRequested {
		for _, service := range resolved {
			intent := promptIntentForResolvedService(service, intentByAlias)
			// Missing operation intent requires clarification instead of assuming complete access.
			if !promptIntentHasOperationSelection(intent) {
				return scaffoldRequest{}, nil, fmt.Errorf("no operations specified for %s@%s; name the operations or explicitly request all operations", service.target.slug, service.version)
			}
		}
		return request, webhookServices, nil
	}
	for _, service := range resolved {
		intent := promptIntentForResolvedService(service, intentByAlias)
		includeEvents := explicitEventServices[service.target.slug]
		// A general goal such as "receive Stripe webhooks" has no narrower event-service hint and applies to all services.
		if len(explicitEventServices) == 0 {
			includeEvents = true
		}
		// Services outside explicit event intent retain operation behavior but need no registration coverage.
		if !includeEvents {
			// Services outside event scope need their own explicit operation request.
			if !promptIntentHasOperationSelection(intent) {
				return scaffoldRequest{}, nil, fmt.Errorf("no operations specified for %s@%s; name the operations or explicitly request all operations", service.target.slug, service.version)
			}
			continue
		}
		available, err := client.FetchWebhooks(service.target.serviceID, service.version)
		// Catalogue failures must not become an empty or wildcard subscription.
		if err != nil {
			return scaffoldRequest{}, nil, fmt.Errorf("fetch webhook events for %s@%s: %w", service.target.slug, service.version, err)
		}
		// A service without declared inbound events cannot satisfy an SDK receiver goal.
		if len(available) == 0 {
			return scaffoldRequest{}, nil, fmt.Errorf("service %s version %s has no webhook events", service.target.slug, service.version)
		}
		selected, err := matchPromptWebhookEvents(promptEventQueries(intent), available)
		// Ambiguous or missing semantic matches require a clearer prompt before mutation.
		if err != nil {
			return scaffoldRequest{}, nil, fmt.Errorf("resolve webhook events for %s@%s: %w", service.target.slug, service.version, err)
		}
		for _, event := range selected {
			request.events = appendUniquePromptEvent(request.events, scaffoldEvent{service: service.target.slug, event: event})
		}
		webhookServices[service.target.slug] = true
		// Omitting an operation query for an event-bearing service preserves event-only scope instead of selecting every operation.
		if !promptIntentHasOperationSelection(intent) {
			continue
		}
	}
	return request, webhookServices, nil
}

// promptIntentAliases merges duplicate model entries and indexes both original and case-folded service references.
func promptIntentAliases(intents []api.IntentService) map[string]api.IntentService {
	merged := make(map[string]api.IntentService, len(intents))
	for _, intent := range intents {
		name := strings.TrimSpace(intent.Name)
		// Blank service entries cannot participate in canonical resolution.
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		current := merged[key]
		// The first spelling remains the evidence-bearing reference supplied to Registry resolution.
		if current.Name == "" {
			current.Name = name
		}
		current.EndpointQueries = appendUniquePromptStrings(current.EndpointQueries, promptOperationQueries(intent)...)
		current.EventQueries = appendUniquePromptStrings(current.EventQueries, promptEventQueries(intent)...)
		current.SelectAllOperations = current.SelectAllOperations || intent.SelectAllOperations
		merged[key] = current
	}
	return merged
}

// promptOperationQueries normalizes the plural prompt contract while retaining the legacy singular parser field.
func promptOperationQueries(intent api.IntentService) []string {
	queries := appendUniquePromptStrings(nil, intent.EndpointQueries...)
	// Plural output is authoritative because the singular field may combine those same intents for older clients.
	if len(queries) > 0 {
		return queries
	}
	// Older Registry deployments may still return only the singular field.
	return appendUniquePromptStrings(nil, intent.EndpointQuery)
}

// promptEventQueries removes blank or duplicate model output before it can imply webhook intent.
func promptEventQueries(intent api.IntentService) []string {
	return appendUniquePromptStrings(nil, intent.EventQueries...)
}

// promptIntentHasOperationSelection distinguishes explicit narrow or complete scope from an unqualified service request.
func promptIntentHasOperationSelection(intent api.IntentService) bool {
	return intent.SelectAllOperations || len(promptOperationQueries(intent)) > 0
}

// appendUniquePromptStrings trims blank model values and preserves first-seen order across repeated intent entries.
func appendUniquePromptStrings(values []string, candidates ...string) []string {
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		// Blank output has no capability meaning and exact duplicates must not trigger repeated Registry searches.
		if candidate == "" || containsString(values, candidate) {
			continue
		}
		values = append(values, candidate)
	}
	return values
}

// promptIntentForResolvedService maps canonical Registry resolution back to any original service spelling.
func promptIntentForResolvedService(service sdkInitResolvedService, intents map[string]api.IntentService) api.IntentService {
	// The canonical slug may already be the model's service reference.
	if intent, exists := intents[service.target.slug]; exists {
		return intent
	}
	// Case-only model variation must not detach the requested operation or event scope.
	if intent, exists := intents[strings.ToLower(service.target.slug)]; exists {
		return intent
	}
	for _, alias := range service.target.requestedRefs {
		// Resolver aliases preserve the exact input spelling used in the parsed intent.
		if intent, exists := intents[alias]; exists {
			return intent
		}
		// Duplicate service mentions may differ only in case after model extraction.
		if intent, exists := intents[strings.ToLower(alias)]; exists {
			return intent
		}
	}
	return api.IntentService{Name: service.target.slug}
}

// matchPromptWebhookEvents returns every exact event for a general request or one unambiguous match per event query.
func matchPromptWebhookEvents(queries []string, available []api.Webhook) ([]string, error) {
	// An unqualified inbound-event request intentionally selects the complete advertised webhook catalogue.
	if len(queries) == 0 {
		selected := make([]string, 0, len(available))
		for _, webhook := range available {
			selected = appendUniquePromptString(selected, webhook.Name)
		}
		return selected, nil
	}
	selected := make([]string, 0, len(queries))
	for _, query := range queries {
		matches := bestPromptWebhookMatches(query, available)
		if len(matches) == 0 {
			return nil, fmt.Errorf("no webhook event matched %q", query)
		}
		// Tied natural-language matches require a clearer goal instead of silently subscribing to the wrong event.
		if len(matches) > 1 {
			return nil, fmt.Errorf("webhook event %q is ambiguous; matches %s", query, strings.Join(matches, ", "))
		}
		selected = appendUniquePromptString(selected, matches[0])
	}
	return selected, nil
}

// bestPromptWebhookMatches scores normalized name and description tokens and returns every top-scoring exact event name.
func bestPromptWebhookMatches(query string, available []api.Webhook) []string {
	queryTokens := promptMatchTokens(query)
	bestScore := 0
	matches := []string{}
	for _, webhook := range available {
		// An exact provider event identifier must win even when prose descriptions share its concepts.
		if strings.EqualFold(strings.TrimSpace(query), strings.TrimSpace(webhook.Name)) {
			return []string{webhook.Name}
		}
		nameScore := promptTokenOverlap(queryTokens, promptMatchTokens(webhook.Name))
		descriptionScore := promptTokenOverlap(queryTokens, promptMatchTokens(webhook.Description))
		// Name concepts carry twice the weight of explanatory prose because they form the runtime identifier.
		score := nameScore*2 + descriptionScore
		// A stronger semantic overlap supersedes all weaker candidates.
		if score > bestScore {
			bestScore = score
			matches = []string{webhook.Name}
			continue
		}
		// Equal positive matches remain visible so the caller can reject ambiguity.
		if score > 0 && score == bestScore {
			matches = append(matches, webhook.Name)
		}
	}
	return matches
}

// promptMatchTokens normalizes punctuation, inflection, and common event-state synonyms for deterministic matching.
func promptMatchTokens(value string) map[string]bool {
	fields := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	tokens := make(map[string]bool, len(fields))
	for _, field := range fields {
		token := promptMatchStem(field)
		// Empty or generic event words contribute no useful discrimination.
		if token == "" || token == "event" || token == "webhook" {
			continue
		}
		tokens[token] = true
	}
	return tokens
}

// promptMatchStem collapses common webhook state variants without inventing provider-specific aliases.
func promptMatchStem(token string) string {
	switch token {
	case "succeeded", "successful", "successfully", "success":
		return "success"
	case "failed", "failure", "failures":
		return "fail"
	case "created", "creation":
		return "create"
	case "updated":
		return "update"
	case "deleted", "deletion":
		return "delete"
	case "completed", "completion":
		return "complete"
	}
	// Simple plural folding makes "payments" match provider identifiers such as payment.succeeded.
	if len(token) > 3 && strings.HasSuffix(token, "s") {
		return strings.TrimSuffix(token, "s")
	}
	return token
}

// promptTokenOverlap counts query concepts present in one event's name or description.
func promptTokenOverlap(query, candidate map[string]bool) int {
	score := 0
	for token := range query {
		// Each normalized concept contributes once even if repeated in the provider description.
		if candidate[token] {
			score++
		}
	}
	return score
}

// resolvePromptWebhookAttachment reuses common coverage or derives a new collision-resistant registration label.
func resolvePromptWebhookAttachment(client *api.Client, appName string, resolved []sdkInitResolvedService, selected map[string]bool) (string, bool, error) {
	common := map[string]bool{}
	used := map[string]bool{}
	first := true
	for _, service := range resolved {
		// Operation-only services do not need registration coverage from the SDK attachment.
		if !selected[service.target.slug] {
			continue
		}
		registrations, err := client.ListWorkspaceWebhooks(service.target.serviceID)
		// Engine read failures cannot be interpreted as absence because that could create a conflicting registration.
		if err != nil {
			return "", false, fmt.Errorf("list webhook registrations for %s: %w", service.target.slug, err)
		}
		labels := make(map[string]bool, len(registrations))
		for _, registration := range registrations {
			label := strings.TrimSpace(registration.Label)
			// Blank labels cannot be referenced by webhook_attachment.
			if label == "" {
				continue
			}
			labels[label] = true
			used[label] = true
		}
		// The first selected service establishes the candidate registration set.
		if first {
			common = labels
			first = false
			continue
		}
		for label := range common {
			// A reusable attachment must register every event-bearing service.
			if !labels[label] {
				delete(common, label)
			}
		}
	}
	labels := make([]string, 0, len(common))
	for label := range common {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	preferred := safeConfigFileName(appName) + "-webhooks"
	// The app-derived label is the least surprising reuse when it covers every selected service.
	if common[preferred] {
		return preferred, true, nil
	}
	// A sole complete registration is unambiguous regardless of its label.
	if len(labels) == 1 {
		return labels[0], true, nil
	}
	// Multiple complete candidates may encode distinct signing policies and therefore require visible selection.
	if len(labels) > 1 {
		selectedLabel, err := selectPromptWebhookAttachment(labels)
		return selectedLabel, err == nil, err
	}
	label := preferred
	// Avoid reusing a partial registration label whose existing config does not cover every selected service.
	for suffix := 2; used[label]; suffix++ {
		label = fmt.Sprintf("%s-%d", preferred, suffix)
	}
	return label, false, nil
}

// promptWebhookAttachment asks which existing registration bundle should receive the generated SDK attachment.
func promptWebhookAttachment(labels []string) (string, error) {
	// Non-interactive ambiguity cannot be resolved safely by choosing a registration arbitrarily.
	if err := requireInteractive("multiple webhook registrations match; rerun in a terminal to choose one"); err != nil {
		return "", err
	}
	selected := labels[0]
	options := make([]huh.Option[string], 0, len(labels))
	for _, label := range labels {
		options = append(options, huh.NewOption(label, label))
	}
	err := huh.NewSelect[string]().Title("Which webhook registration should the SDK use?").Options(options...).Value(&selected).Run()
	return selected, err
}

// promptWebhookScaffoldServices carries only event-bearing services into the registration config.
func promptWebhookScaffoldServices(resolved []sdkInitResolvedService, selected map[string]bool) []scaffoldService {
	services := make([]scaffoldService, 0, len(selected))
	for _, service := range resolved {
		// The attachment need not register operation-only services from the same SDK.
		if !selected[service.target.slug] {
			continue
		}
		services = append(services, scaffoldService{name: service.target.slug, version: service.version})
	}
	return services
}

// appendUniquePromptOperation preserves Registry order while deduplicating repeated search results.
func appendUniquePromptOperation(values []scaffoldOperation, candidate scaffoldOperation) []scaffoldOperation {
	for _, value := range values {
		// Service-qualified identity prevents same-named operations from different providers from collapsing.
		if value.service == candidate.service && value.operation == candidate.operation {
			return values
		}
	}
	return append(values, candidate)
}

// appendUniquePromptEvent preserves intent order while deduplicating exact service event names.
func appendUniquePromptEvent(values []scaffoldEvent, candidate scaffoldEvent) []scaffoldEvent {
	for _, value := range values {
		// Event identity is scoped to its immutable service version.
		if value.service == candidate.service && value.event == candidate.event {
			return values
		}
	}
	return append(values, candidate)
}

// appendUniquePromptString adds one stable selection only when it is not already present.
func appendUniquePromptString(values []string, candidate string) []string {
	for _, value := range values {
		// Exact Registry identifiers are case-sensitive and therefore compared without normalization.
		if value == candidate {
			return values
		}
	}
	return append(values, candidate)
}

// printPromptInitPlan renders the exact resources and immutable selections authorized by the user.
func printPromptInitPlan(cmd *cobra.Command, plan promptInitPlan) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Prompt proposal: %s %s version %s\n", promptModeLabel(plan.mode), plan.primary.name, plan.primary.version)
	// The version transition and retained settings distinguish an additive update from app creation.
	if plan.primary.extend {
		fmt.Fprintf(out, "Update %s: %s -> %s (existing settings and selections preserved)\n", plan.primary.path, plan.baseVersion, plan.primary.version)
	}
	// Full mappings are part of the approval surface, not hidden model-generated implementation details.
	if err := printPromptUnifiedOperations(out, plan.primary.unifiedOperations); err != nil {
		return err
	}
	for _, service := range plan.resolved {
		operations := sdkInitExplicitOperations(plan.primary, service.target.slug)
		selection := strings.Join(operations, ", ")
		// Select-all remains explicit in review rather than expanding an evolving Registry surface.
		if containsString(plan.primary.selectAll, service.target.slug) {
			selection = "all operations"
		}
		// Event-only services must not be described as having an operation grant.
		if selection == "" {
			selection = "no operations"
		}
		fmt.Fprintf(out, "  - %s@%s: %s\n", service.target.slug, service.version, selection)
		for _, event := range plan.primary.events {
			// Events are grouped under their exact service to make attachment coverage reviewable.
			if event.service == service.target.slug {
				fmt.Fprintf(out, "    event: %s\n", event.event)
			}
		}
	}
	// Attachment review distinguishes a new registration from reuse of existing signing policy.
	if strings.TrimSpace(plan.primary.webhookAttachment) != "" {
		action := "create"
		// Reuse avoids replacing the signing policy of an existing Engine-owned registration.
		if plan.reusesWebhook {
			action = "reuse"
		}
		fmt.Fprintf(out, "Webhook attachment: %s (%s)\n", plan.primary.webhookAttachment, action)
	}
	return nil
}

// promptInitConfirmation presents one authorization for the composed webhook and primary app lifecycle.
func promptInitConfirmation(message string) (bool, error) {
	confirmed := true
	err := huh.NewConfirm().Title(message).Affirmative("Apply proposal").Negative("Cancel").Value(&confirmed).Run()
	return confirmed, err
}

// executePromptInitPlan applies a missing webhook registration before the SDK that references it.
func executePromptInitPlan(cmd *cobra.Command, plan promptInitPlan) error {
	// Creation requires absence; updates require the exact local baseline the user reviewed.
	if err := validatePromptPlanBaseline(plan); err != nil {
		return err
	}
	// A missing attachment is the only case that introduces the registration lifecycle.
	if plan.webhook != nil {
		// Secondary path collisions must fail before the webhook lifecycle can activate workspace state.
		if err := ensureUnifiedInitTargetAbsent(plan.webhook.path); err != nil {
			return err
		}
		// SDK planning requires attachment coverage, so webhook apply is the first ordered commit boundary.
		if err := runUnifiedInitLifecycle(cmd, unifiedInitModeWebhook, *plan.webhook); err != nil {
			return fmt.Errorf("create webhook attachment %s: %w", plan.webhook.name, err)
		}
	}
	// The primary lifecycle revalidates exact selections and performs its normal plan/apply behavior.
	if err := runUnifiedInitLifecycle(cmd, plan.mode, plan.primary); err != nil {
		// A committed webhook is intentionally retained; rerunning the same prompt discovers and reuses it.
		if plan.webhook != nil {
			return fmt.Errorf("webhook attachment %s was created, but %s initialization did not complete: %w; retry with fused-cli prompt %s", plan.webhook.name, strings.ToLower(promptModeLabel(plan.mode)), err, shellQuoteWorkspaceServiceArg(plan.goal))
		}
		return err
	}
	return nil
}

// init registers prompt as a root workflow because its primary output is not limited to SDKs.
func init() {
	RootCmd.AddCommand(newPromptInitCommand())
}
