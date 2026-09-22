package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

// promptExplicitUpdateTarget allows an explicit name or existing -f document to anchor intent interpretation.
func promptExplicitUpdateTarget(opts *promptInitOptions) (*unifiedExtendTarget, error) {
	name := strings.TrimSpace(opts.update)
	// An existing explicit file is an update target; a missing file remains a creation destination.
	if name == "" && ConfigFile != "" {
		_, err := os.Stat(ConfigFile)
		if err == nil {
			parsed, parseErr := configfile.ParseFile(ConfigFile)
			// Malformed authored state cannot supply identity hints for a model request.
			if parseErr != nil {
				return nil, parseErr
			}
			name, err = unifiedExtendConfigName(parsed)
			if err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(err) {
			// Permission errors must not be interpreted as an absent creation target.
			return nil, err
		}
	}
	// No explicit baseline allows the intent parser to classify create versus update.
	if name == "" {
		return nil, nil
	}
	target, err := resolveUnifiedExtendTarget(name)
	return &target, err
}

// promptUpdateContext projects identity hints without sending bucket references, auth, or authored mappings to the model.
func promptUpdateContext(target *unifiedExtendTarget) string {
	// Creation has no existing app context to disclose.
	if target == nil {
		return ""
	}
	name, _ := unifiedExtendConfigName(target.config)
	services := make([]string, 0, len(unifiedExtendServices(target.config)))
	for service := range unifiedExtendServices(target.config) {
		services = append(services, service)
	}
	sort.Strings(services)
	data, _ := json.Marshal(map[string]any{"name": name, "kind": promptModeLabel(target.mode), "services": services})
	return string(data)
}

// promptIntentUpdateTarget fails closed on unknown actions and resolves model-named updates through deterministic local discovery.
func promptIntentUpdateTarget(explicit *unifiedExtendTarget, intent *api.IntentPayload) (*unifiedExtendTarget, error) {
	// Unsupported edits and unresolved business intent must stop instead of becoming an additive approximation.
	if strings.TrimSpace(intent.Clarification) != "" {
		return nil, fmt.Errorf("clarify your app goal: %s", intent.Clarification)
	}
	// Unknown actions cannot safely select a mutation lifecycle.
	if intent.Action != "" && intent.Action != "create" && intent.Action != "update" {
		return nil, fmt.Errorf("unsupported prompt action %q", intent.Action)
	}
	// An explicit target is authoritative, but a conflicting named target needs a corrected goal.
	if explicit != nil {
		name, _ := unifiedExtendConfigName(explicit.config)
		if intent.Target != "" && intent.Target != name {
			return nil, fmt.Errorf("prompt targets %q but the selected app is %q", intent.Target, name)
		}
		return explicit, nil
	}
	// Older parser responses remain compatible for creation, never implicit updates.
	if intent.Action != "update" && intent.Target == "" {
		return nil, nil
	}
	// Conflicting model fields cannot turn a creation request into an update of a named family.
	if intent.Action != "update" {
		return nil, fmt.Errorf("prompt returned an existing target without update intent; specify --update <app-name>")
	}
	// An unnamed update needs user input rather than selecting an arbitrary local SDK.
	if strings.TrimSpace(intent.Target) == "" {
		return nil, fmt.Errorf("name the existing app in your goal or pass --update <app-name>")
	}
	target, err := resolveUnifiedExtendTarget(intent.Target)
	return &target, err
}

// promptUpdateMode preserves authored app kind while detecting contradictory explicit flags.
func promptUpdateMode(target *unifiedExtendTarget, override string) (unifiedInitMode, error) {
	// A requested kind is a constraint, not permission to convert an existing app family.
	if override != "" {
		mode, err := resolvePromptInitMode(override, "")
		if err != nil || mode != target.mode {
			return "", fmt.Errorf("--kind conflicts with the existing %s app", promptModeLabel(target.mode))
		}
	}
	return target.mode, nil
}

// promptUpdateRequest reuses additive extension semantics and leaves omitted identity settings unchanged.
func promptUpdateRequest(cmd *cobra.Command, request scaffoldRequest, target *unifiedExtendTarget, opts *promptInitOptions) (scaffoldRequest, error) {
	name, version, kind, err := unifiedExtendIdentity(target.config)
	// A complete authored identity is required before any service additions can be proposed.
	if err != nil {
		return scaffoldRequest{}, err
	}
	if opts.name != "" && opts.name != name {
		// Renaming a family is outside additive prompt updates.
		return scaffoldRequest{}, fmt.Errorf("--name cannot rename existing app %q", name)
	}
	request.name, request.path, request.kind, request.extend = name, target.path, kind, true
	request.version, request.versionSet = version, cmd.Flags().Changed("version")
	// Explicit versions retain extend's collision and immutable-content checks.
	if request.versionSet {
		request.version = strings.TrimSpace(opts.version)
		if request.version == "" {
			return scaffoldRequest{}, fmt.Errorf("--version must not be empty")
		}
	}
	request.services = inheritUnifiedExtendServiceVersions(request.services, target.config)
	current := target.config.MCP
	// SDK and API share the same authored shape, including language and receiver attachment.
	if target.config.SDK != nil {
		current = target.config.SDK
	}
	// Updates add capabilities but cannot silently change the generated language.
	if opts.language != "" && opts.language != current.Language {
		return scaffoldRequest{}, fmt.Errorf("--language conflicts with existing app language %q", current.Language)
	}
	request.language = current.Language
	request.description = current.Description
	request.webhookAttachment = current.WebhookAttachment
	request.descriptionSet, request.languageSet = false, false
	return request, nil
}

// pinPromptUpdateServices preserves exact provider versions even when intent used a Registry alias.
func pinPromptUpdateServices(request scaffoldRequest, resolved []sdkInitResolvedService, target *unifiedExtendTarget) (scaffoldRequest, []sdkInitResolvedService) {
	// Creation uses normal workspace-first version defaults.
	if target == nil {
		return request, resolved
	}
	configured := unifiedExtendServices(target.config)
	for i := range resolved {
		// Existing app pins take precedence over discovery defaults for newly mentioned aliases.
		if service, exists := configured[resolved[i].target.slug]; exists {
			resolved[i].version = service.Version
			request.services[i].version = service.Version
		}
	}
	return request, resolved
}

// finalizePromptPlan validates a complete in-memory draft and its successor before interactive approval.
func finalizePromptPlan(client *api.Client, plan promptInitPlan) (promptInitPlan, error) {
	// Existing wildcard policies already cover classified calls and must retain their authored encoding.
	if plan.primary.extend {
		parsed, err := configfile.ParseFile(plan.primary.path)
		if err != nil {
			return promptInitPlan{}, err
		}
		configured := unifiedExtendServices(parsed)
		operations := make([]scaffoldOperation, 0, len(plan.primary.operations))
		for _, operation := range plan.primary.operations {
			// Classification remains exact during drafting; only the additive config merge reuses complete scope.
			if configured[operation.service].SelectAll {
				plan.primary.selectAll = appendUniquePromptString(plan.primary.selectAll, operation.service)
				continue
			}
			operations = append(operations, operation)
		}
		plan.primary.operations = operations
	}
	request, err := completeSDKInitCreateBucket(plan.primary, resolveScaffoldBucket)
	// Bucket selection belongs in the proposal rather than after its approval.
	if err != nil {
		return promptInitPlan{}, err
	}
	request, err = completeSDKInitVersionExtension(client, request, resolveScaffoldBucket)
	// Version collisions and composition conflicts must be reported before the user approves a successor.
	if err != nil {
		return promptInitPlan{}, err
	}
	_, _, _, err = prepareUnifiedInitPlanInput(plan.mode, request, deferScaffoldRequirements, resolveScaffoldBucket)
	// Structural validation does not execute operations or replace the final Engine plan checks.
	if err != nil {
		return promptInitPlan{}, err
	}
	plan.primary = request
	return plan, nil
}

// validatePromptPlanBaseline prevents applying a proposal to a file that changed during model generation or review.
func validatePromptPlanBaseline(plan promptInitPlan) error {
	// New app publication retains the existing create-only collision behavior.
	if !plan.primary.extend {
		return ensureUnifiedInitTargetAbsent(plan.primary.path)
	}
	current, err := configfile.ParseFile(plan.primary.path)
	// A missing or invalid baseline requires a fresh proposal rather than a best-effort merge.
	if err != nil {
		return err
	}
	if plan.baseHash == "" || current.SourceHash != plan.baseHash {
		return fmt.Errorf("existing app config changed during prompt review; rerun prompt to review a fresh proposal")
	}
	return nil
}
