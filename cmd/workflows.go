package cmd

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// workflowTemplate is an input adapter; Engine retains the sole executable mapping compiler.
type workflowTemplate struct {
	SchemaVersion int      `yaml:"schema_version" json:"schema_version"`
	Slug          string   `yaml:"slug" json:"slug"`
	Version       string   `yaml:"version" json:"version"`
	Name          string   `yaml:"name" json:"name"`
	Description   string   `yaml:"description" json:"description"`
	Category      string   `yaml:"category" json:"category"`
	Requirements  []string `yaml:"requirements" json:"requirements"`
	Services      map[string]struct {
		ServiceID        string   `yaml:"service_id" json:"service_id"`
		ServiceVersionID string   `yaml:"service_version_id" json:"service_version_id"`
		Version          string   `yaml:"version" json:"version"`
		Operations       []string `yaml:"operations" json:"operations"`
	} `yaml:"services" json:"services"`
	Operations map[string]configfile.UnifiedOperation `yaml:"unified_operations" json:"unified_operations"`
}

// decodeWorkflowRelease checks content integrity before trusted scaffolding consumes published authoring.
func decodeWorkflowRelease(release api.WorkflowRelease) (workflowTemplate, error) {
	var template workflowTemplate
	// Corrupt or oversized remote content must fail before workspace activation.
	if len(release.Template) > 256<<10 || release.Hash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(release.Template))) {
		return template, fmt.Errorf("workflow content hash mismatch or oversized template")
	}
	decoder := yaml.NewDecoder(strings.NewReader(release.Template))
	decoder.KnownFields(true)
	// Unsupported template fields must fail before entering shared scaffolding.
	if err := decoder.Decode(&template); err != nil {
		return template, err
	}
	// Installed templates must have exact scope and supported authoring identity.
	if template.SchemaVersion != 1 || len(template.Services) == 0 || len(template.Operations) == 0 {
		return template, fmt.Errorf("invalid workflow template")
	}
	return template, nil
}

// addWorkflowSelections hydrates all selected releases before entering init's existing mutation boundary.
func addWorkflowSelections(request scaffoldRequest, ids []string) (scaffoldRequest, error) {
	// Ordinary init/extend retains its existing offline parsing and lifecycle behavior.
	if len(ids) == 0 {
		return request, nil
	}
	// Direct API and webhook resources do not expose this workflow installation contract.
	if request.kind != configfile.KindSDK && request.kind != configfile.KindMCP {
		return request, fmt.Errorf("workflows require an SDK or MCP app")
	}
	// Validate the entire exact selection before a network request.
	if err := validateWorkflowIDs(ids); err != nil {
		return request, err
	}
	client, err := getAPIClient()
	// Authentication or configuration failure cannot fall back to unauthenticated discovery.
	if err != nil {
		return request, err
	}
	page, err := client.ListWorkflows("", ids, 32, 0)
	// A partial authorized result must not silently omit one requested workflow.
	if err != nil {
		return request, err
	}
	// Equal counts alone do not prove the server returned the exact requested identities.
	if err := validateWorkflowResponse(ids, page.Items); err != nil {
		return request, err
	}
	for _, release := range page.Items {
		// All selected releases must compose before the lifecycle can mutate workspace state.
		if err := mergeWorkflowRelease(&request, release); err != nil {
			return request, err
		}
	}
	return request, nil
}

// validateWorkflowResponse rejects missing, duplicated, or substituted releases before any composition.
func validateWorkflowResponse(ids []string, releases []api.WorkflowRelease) error {
	pending := make(map[string]bool, len(ids))
	for _, id := range ids {
		pending[id] = true
	}
	for _, release := range releases {
		// Removing each identity once detects both duplicate results and unsolicited releases.
		if !pending[release.ID] {
			return fmt.Errorf("unexpected workflow release identity")
		}
		delete(pending, release.ID)
	}
	// A partial authorized response must not silently reduce the requested app scope.
	if len(pending) != 0 {
		return fmt.Errorf("one or more selected workflow releases are unavailable")
	}
	return nil
}

// mergeWorkflowRelease composes exact scopes and rejects conflicting pins before init can enable a service.
func mergeWorkflowRelease(request *scaffoldRequest, release api.WorkflowRelease) error {
	template, err := decodeWorkflowRelease(release)
	// Integrity and schema failures stop before merging any requested scope.
	if err != nil {
		return err
	}
	// Preserve immutable provider coordinates through workspace planning rather than reducing them to labels.
	if err := mergeWorkflowPins(request, template); err != nil {
		return err
	}
	config := &configfile.AppConfig{Services: map[string]configfile.AppService{}, UnifiedOperations: request.unifiedOperations}
	// Reuse the existing conflict-aware service and operation merger for the combined draft.
	if _, err := mergeAppServices(config, request.services); err != nil {
		return err
	}
	for key, service := range template.Services {
		// A workflow must not change a provider version already requested by another selection.
		if _, err := mergeAppServices(config, []scaffoldService{{name: key, version: service.Version}}); err != nil {
			return err
		}
		for _, operation := range service.Operations {
			request.operations = append(request.operations, scaffoldOperation{service: key, operation: operation})
		}
	}
	// Same-named methods cannot silently replace existing executable definitions.
	if _, err := mergePromptUnifiedOperations(config, template.Operations); err != nil {
		return err
	}
	request.unifiedOperations = config.UnifiedOperations
	request.services = nil
	for key, service := range config.Services {
		request.services = append(request.services, scaffoldService{name: key, version: service.Version})
	}
	// Stable ordering keeps repeated template selection from changing local desired-state bytes.
	sort.Slice(request.services, func(i, j int) bool { return request.services[i].name < request.services[j].name })
	request.workflowSources = append(request.workflowSources, configfile.WorkflowSource{ID: release.ID, Version: template.Version, Hash: release.Hash})
	return nil
}

// newWorkflowCommand adds discovery and deliberate file publication without creating another install lifecycle.
func newWorkflowCommand() *cobra.Command {
	command := &cobra.Command{Use: "workflow", Short: "Browse and publish reusable Unified Operation templates", Args: cobra.NoArgs, RunE: requireSubcommand}
	var search string
	var limit, offset int
	list := &cobra.Command{Use: "list", Short: "List available workflow releases", Args: cobra.NoArgs, RunE: WithTelemetry("cli.workflow.list", func(cmd *cobra.Command, _ []string) error {
		client, err := getAPIClient()
		// Discovery requires the configured authenticated Engine transport.
		if err != nil {
			return err
		}
		page, err := client.ListWorkflows(search, nil, limit, offset)
		// Failed discovery must not print a successful empty result.
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(page)
	})}
	list.Flags().StringVar(&search, "search", "", "Search workflow names, descriptions, and categories")
	list.Flags().IntVar(&limit, "limit", 20, "Page size (1..50)")
	list.Flags().IntVar(&offset, "offset", 0, "Page offset")
	var public bool
	publish := &cobra.Command{Use: "publish <template.json>", Short: "Publish an immutable workflow release from an explicit JSON file", Args: cobra.ExactArgs(1), RunE: WithTelemetry("cli.workflow.publish", func(cmd *cobra.Command, args []string) error {
		return publishWorkflowFile(cmd, args[0], public)
	})}
	publish.Flags().BoolVar(&public, "public", false, "Make this release available to other Registry accounts")
	command.AddCommand(list, publish)
	return command
}

// publishWorkflowFile bounds the selected file and leaves authoritative publication validation to Registry.
func publishWorkflowFile(cmd *cobra.Command, path string, public bool) error {
	file, err := os.Open(path)
	// Publication reads only the explicit file, with no fallback to installed app source.
	if err != nil {
		return err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (256<<10)+1))
	// Only one bounded JSON manifest may cross the intentional publication boundary.
	if err != nil {
		return err
	}
	// Reject malformed or oversized publication before contacting Registry.
	if len(raw) > 256<<10 || !json.Valid(raw) {
		return fmt.Errorf("workflow must be a JSON document of at most 256 KiB")
	}
	client, err := getAPIClient()
	// A missing authenticated transport must not be mistaken for successful publication.
	if err != nil {
		return err
	}
	release, err := client.PublishWorkflow(string(raw), public)
	// Only an acknowledged immutable release may be printed as published.
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(release)
}

// mergeWorkflowSources preserves existing provenance and rejects an attempted in-place release replacement.
func mergeWorkflowSources(config *configfile.AppConfig, sources []configfile.WorkflowSource) (bool, error) {
	byID := map[string]configfile.WorkflowSource{}
	for _, source := range config.WorkflowSources {
		byID[source.ID] = source
	}
	changed := false
	for _, source := range sources {
		existing, exists := byID[source.ID]
		// One immutable release identity cannot acquire different content through extension.
		if exists && existing != source {
			return false, fmt.Errorf("workflow release provenance conflict")
		}
		// Repeated installations preserve the current immutable app version.
		if !exists {
			byID[source.ID] = source
			changed = true
		}
	}
	config.WorkflowSources = nil
	for _, source := range byID {
		config.WorkflowSources = append(config.WorkflowSources, source)
	}
	// Canonical source ordering prevents selection order from forcing a version change.
	sort.Slice(config.WorkflowSources, func(i, j int) bool { return config.WorkflowSources[i].ID < config.WorkflowSources[j].ID })
	return changed, nil
}

// init registers workflow discovery beside the existing init/extend installation commands.
func init() { RootCmd.AddCommand(newWorkflowCommand()) }

// validateWorkflowIDs rejects floating names and duplicate selections before resolving the catalogue batch.
func validateWorkflowIDs(ids []string) error {
	unique := map[string]bool{}
	for _, id := range ids {
		// UUID identity pins a release rather than allowing latest-version drift.
		if _, err := uuid.Parse(id); err != nil || unique[id] {
			return fmt.Errorf("--workflow requires unique release UUIDs")
		}
		unique[id] = true
	}
	return nil
}
