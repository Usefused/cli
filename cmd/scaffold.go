package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	defaultScaffoldVersion  = "1.0.0"
	defaultScaffoldLanguage = "typescript"
)

var scaffoldKeyCleaner = regexp.MustCompile(`[^A-Za-z0-9]+`)

type scaffoldOptions struct {
	extend      bool
	services    []string
	operations  []string
	selectAll   []string
	version     string
	description string
	language    string
	bucket      string
}

type scaffoldRequest struct {
	kind           configfile.ConfigKind
	name           string
	path           string
	extend         bool
	services       []scaffoldService
	operations     []scaffoldOperation
	selectAll      []string
	version        string
	description    string
	language       string
	bucket         string
	generate       bool
	versionSet     bool
	descriptionSet bool
	languageSet    bool
	bucketSet      bool
	generateSet    bool
}

type scaffoldService struct {
	name    string
	version string
}

type scaffoldOperation struct {
	service   string
	operation string
}

type scaffoldResult struct {
	Action                string                `json:"action"`
	Kind                  configfile.ConfigKind `json:"kind"`
	Path                  string                `json:"path"`
	Changed               bool                  `json:"changed"`
	GeneratedBindingCount int                   `json:"generated_binding_count"`
}

type scaffoldRequirementsResolver func([]api.AppScaffoldSelection) ([]api.AppScaffoldRequirement, error)

type scaffoldBucketResolver func() (string, error)

type scaffoldWorkflow func(*cobra.Command, scaffoldRequest, scaffoldRequirementsResolver, scaffoldBucketResolver) error

// newScaffoldCommand wires production app scaffolds to Engine requirement and visible-bucket discovery.
func newScaffoldCommand(kind configfile.ConfigKind) *cobra.Command {
	// SDK init is the only scaffold that composes workspace activation and app lifecycle commits.
	if kind == configfile.KindSDK {
		return newScaffoldCommandWithWorkflow(kind, resolveScaffoldRequirements, resolveScaffoldBucket, runSDKInitWorkflow)
	}
	return newScaffoldCommandWithWorkflow(kind, resolveScaffoldRequirements, resolveScaffoldBucket, nil)
}

// newScaffoldCommandWithResolver preserves focused requirement tests with a deterministic existing-bucket candidate.
func newScaffoldCommandWithResolver(kind configfile.ConfigKind, resolver scaffoldRequirementsResolver) *cobra.Command {
	return newScaffoldCommandWithDependencies(kind, resolver, defaultTestScaffoldBucket)
}

// newScaffoldCommandWithDependencies keeps Engine-owned discovery replaceable without coupling local merge tests to remote state.
func newScaffoldCommandWithDependencies(kind configfile.ConfigKind, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) *cobra.Command {
	return newScaffoldCommandWithWorkflow(kind, resolver, bucketResolver, nil)
}

// newScaffoldCommandWithWorkflow preserves the local scaffold seam while allowing production SDK init to orchestrate existing lifecycle functions.
func newScaffoldCommandWithWorkflow(kind configfile.ConfigKind, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver, workflow scaffoldWorkflow) *cobra.Command {
	opts := &scaffoldOptions{version: defaultScaffoldVersion, language: defaultScaffoldLanguage}
	use := "init [name]"
	args := cobra.RangeArgs(0, 1)
	selectionDescription := "services and operation selections"
	serviceFlagDescription := "Service key with an optional version as <key>[=<version>]; repeatable"
	operationFlagDescription := "Selected operation as <service>=<operationId>; repeatable"
	selectAllFlagDescription := "Service key whose operations should all be selected; repeatable"
	// SDK init can discover operation scope after service resolution, while other scaffold kinds retain explicit flag guidance.
	if kind == configfile.KindSDK {
		operationFlagDescription = "Selected operation as <service>=<operationId>; repeatable; omit to choose interactively"
		selectAllFlagDescription = "Service key whose operations should all be selected; repeatable; omit to choose interactively"
	}
	// MCP keeps exact version selection because only SDK init owns version-default orchestration.
	if kind == configfile.KindMCP {
		serviceFlagDescription = "Service key and version as <key>=<version>; repeatable"
	}
	if kind == configfile.KindWorkspace {
		use = "init"
		args = cobra.NoArgs
		selectionDescription = "services"
	}
	command := &cobra.Command{
		Use:   use,
		Short: "Create or extend a Fused config file",
		Long: fmt.Sprintf(`Create a %s config skeleton.

By default the command refuses to replace an existing file. Pass --extend to
merge %s into that file; an explicit --version retargets an app file to its
immutable successor.`, kind, selectionDescription),
		Args: args,
		RunE: WithTelemetry(fmt.Sprintf("cli.%s.init", kind), func(cmd *cobra.Command, args []string) error {
			request, err := buildScaffoldRequest(cmd, kind, args, opts)
			// Invalid local arguments must fail before either Engine discovery dependency is invoked.
			if err != nil {
				return err
			}
			// Production SDK init coordinates the same lifecycle helpers after local argument validation.
			if workflow != nil {
				return workflow(cmd, request, resolver, bucketResolver)
			}
			result, err := writeScaffold(request, resolver, bucketResolver)
			if err != nil {
				return err
			}
			recordGeneratedBindingCount(cmd.Context(), result.GeneratedBindingCount)
			recordAppliedChangeIf(cmd.Context(), cmd.CommandPath(), "config_file", result.Changed)
			return printScaffoldResult(cmd, result)
		}),
	}

	command.Flags().BoolVar(&opts.extend, "extend", false, "Merge into an existing config; use --version for an applied successor")
	command.Flags().StringSliceVar(&opts.services, "service", nil, serviceFlagDescription)
	if kind != configfile.KindWorkspace {
		command.Flags().StringSliceVar(&opts.operations, "operation", nil, operationFlagDescription)
		command.Flags().StringSliceVar(&opts.selectAll, "select-all", nil, selectAllFlagDescription)
		command.Flags().StringVar(&opts.version, "version", defaultScaffoldVersion, "Config version, or exact successor when extending an applied app")
		command.Flags().StringVar(&opts.bucket, "bucket", "", "Existing bucket to bind to this config")
	}
	if kind == configfile.KindSDK {
		command.Flags().StringVar(&opts.language, "language", defaultScaffoldLanguage, "SDK target language")
	}
	// MCP descriptions are authored by the calling agent and become server identity metadata, not tool documentation.
	if kind == configfile.KindMCP {
		command.Flags().StringVar(&opts.description, "description", "", "Human-readable summary of what this MCP server can do")
	}
	addJSONOutputFlag(command)
	// Resource-scoped app init remains invokable for scripts, but unified init is the only creation path advertised in help.
	if kind == configfile.KindSDK || kind == configfile.KindMCP {
		command.Hidden = true
		command.Long += fmt.Sprintf("\n\nFor new workflows, use 'fused-cli init <app-name> --%s'.", kind)
	}
	return command
}

// resolveScaffoldRequirements creates the authenticated Engine client lazily
// because empty app skeletons and workspace scaffolds have no routing targets.
func resolveScaffoldRequirements(selections []api.AppScaffoldSelection) ([]api.AppScaffoldRequirement, error) {
	// Service-bearing app scaffolds require Engine-owned immutable metadata.
	if len(selections) == 0 {
		return []api.AppScaffoldRequirement{}, nil
	}
	client, err := getAPIClient()
	if err != nil {
		return nil, err
	}
	return client.AppScaffoldRequirements(selections)
}

// resolveScaffoldBucket selects an existing visible bucket without creating or granting access as a side effect of init.
func resolveScaffoldBucket() (string, error) {
	client, err := getAPIClient()
	// Authentication and transport failures must stop init rather than masquerading as an empty workspace.
	if err != nil {
		return "", err
	}
	page, err := client.ListBucketSummariesPage(api.PageOptions{Limit: 100})
	// The ordinary bucket list is the authoritative read-visible candidate set for automatic selection.
	if err != nil {
		return "", err
	}
	return selectScaffoldBucket(page.Items)
}

// selectScaffoldBucket prefers the conventional default name and otherwise uses one existing visible candidate deterministically.
func selectScaffoldBucket(buckets []api.BucketSummaryResponse) (string, error) {
	for _, bucket := range buckets {
		// The visible bucket named default is the documented automatic candidate; plan still proves bucket.use.
		if strings.EqualFold(strings.TrimSpace(bucket.Name), "default") {
			return bucket.Name, nil
		}
	}
	// Falling back to the first visible bucket avoids silently creating workspace state when default is absent.
	if len(buckets) > 0 {
		return buckets[0].Name, nil
	}
	return "", errors.New("no visible bucket is available; create one or pass --bucket")
}

// defaultTestScaffoldBucket keeps focused command tests deterministic while production uses Engine-visible bucket selection.
func defaultTestScaffoldBucket() (string, error) {
	return "default", nil
}

// buildScaffoldRequest normalizes CLI fields while preserving which immutable identity values were explicitly supplied.
func buildScaffoldRequest(cmd *cobra.Command, kind configfile.ConfigKind, args []string, opts *scaffoldOptions) (scaffoldRequest, error) {
	name := ""
	if len(args) == 1 {
		name = strings.TrimSpace(args[0])
	}
	if err := validateScaffoldArgs(kind, name, opts.extend); err != nil {
		return scaffoldRequest{}, err
	}
	// SDK init may resolve an omitted version; MCP still requires exact immutable input.
	services, err := parseScaffoldServices(opts.services, kind == configfile.KindMCP)
	if err != nil {
		return scaffoldRequest{}, err
	}
	operations, err := parseScaffoldOperations(opts.operations)
	if err != nil {
		return scaffoldRequest{}, err
	}
	selectAll, err := parseScaffoldNames("--select-all", opts.selectAll)
	if err != nil {
		return scaffoldRequest{}, err
	}
	path, err := scaffoldTargetPath(kind, name, ConfigFile)
	if err != nil {
		return scaffoldRequest{}, err
	}
	descriptionSet := false
	// Workspace and SDK commands do not register this MCP-only flag.
	if kind == configfile.KindMCP {
		descriptionSet = cmd.Flags().Changed("description")
	}
	return scaffoldRequest{
		kind: kind, name: name, path: path, extend: opts.extend,
		services: services, operations: operations, selectAll: selectAll,
		version: opts.version, description: strings.TrimSpace(opts.description), language: opts.language, bucket: strings.TrimSpace(opts.bucket),
		versionSet: cmd.Flags().Changed("version"), languageSet: cmd.Flags().Changed("language"),
		descriptionSet: descriptionSet, bucketSet: cmd.Flags().Changed("bucket"),
	}, nil
}

func validateScaffoldArgs(kind configfile.ConfigKind, name string, extend bool) error {
	if kind == configfile.KindWorkspace && name != "" {
		return errors.New("workspace config does not take a name")
	}
	if kind != configfile.KindWorkspace && name == "" && !extend {
		return fmt.Errorf("%s config requires a name", kind)
	}
	return nil
}

func scaffoldTargetPath(kind configfile.ConfigKind, name, explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return explicit, nil
	}
	if kind == configfile.KindWorkspace {
		return filepath.Join(".fused", "workspace.yaml"), nil
	}
	fileName := safeConfigFileName(name)
	if fileName == "" {
		return "", fmt.Errorf("%s --extend without a name requires -f", kind)
	}
	directory := "sdks"
	if kind == configfile.KindMCP {
		directory = "mcps"
	}
	return filepath.Join(".fused", directory, fileName+".yaml"), nil
}

func parseScaffoldServices(values []string, requireVersion bool) ([]scaffoldService, error) {
	services := make([]scaffoldService, 0, len(values))
	for _, value := range values {
		name, version, _ := strings.Cut(strings.TrimSpace(value), "=")
		name, version = strings.TrimSpace(name), strings.TrimSpace(version)
		if name == "" {
			return nil, fmt.Errorf("--service requires <key>=<version>")
		}
		if requireVersion && version == "" {
			return nil, fmt.Errorf("--service %q requires a version as <key>=<version>", name)
		}
		services = append(services, scaffoldService{name: name, version: version})
	}
	return services, nil
}

func parseScaffoldOperations(values []string) ([]scaffoldOperation, error) {
	operations := make([]scaffoldOperation, 0, len(values))
	for _, value := range values {
		service, operation, found := strings.Cut(strings.TrimSpace(value), "=")
		service, operation = strings.TrimSpace(service), strings.TrimSpace(operation)
		if !found || service == "" || operation == "" {
			return nil, fmt.Errorf("--operation requires <service>=<operationId>")
		}
		operations = append(operations, scaffoldOperation{service: service, operation: operation})
	}
	return operations, nil
}

func parseScaffoldNames(flag string, values []string) ([]string, error) {
	names := make([]string, 0, len(values))
	for _, value := range values {
		name := strings.TrimSpace(value)
		if name == "" {
			return nil, fmt.Errorf("%s requires a service key", flag)
		}
		names = append(names, name)
	}
	return names, nil
}

// writeScaffold routes create and extend through their matching atomic-write workflows.
func writeScaffold(request scaffoldRequest, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) (scaffoldResult, error) {
	// Extend must inspect the existing document before deciding whether automatic bucket selection is necessary.
	if request.extend {
		return extendScaffold(request, resolver, bucketResolver)
	}
	data, generated, err := newScaffoldData(request, resolver, bucketResolver)
	if err != nil {
		return scaffoldResult{}, err
	}
	if err := atomicCreateFile(request.path, data, 0o644, scaffoldValidator(request)); err != nil {
		return scaffoldResult{}, err
	}
	return scaffoldResult{Action: "created", Kind: request.kind, Path: request.path, Changed: true, GeneratedBindingCount: generated}, nil
}

// extendScaffold preserves the existing draft until the complete merged document validates and can be replaced atomically.
func extendScaffold(request scaffoldRequest, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) (scaffoldResult, error) {
	data, err := os.ReadFile(request.path)
	if os.IsNotExist(err) {
		return scaffoldResult{}, fmt.Errorf("cannot extend %s: file does not exist", request.path)
	}
	if err != nil {
		return scaffoldResult{}, fmt.Errorf("read config %s: %w", request.path, err)
	}
	updated, changed, generated, err := extendScaffoldData(request, data, resolver, bucketResolver)
	if err != nil {
		return scaffoldResult{}, err
	}
	result := scaffoldResult{Action: "unchanged", Kind: request.kind, Path: request.path, Changed: changed, GeneratedBindingCount: generated}
	if !changed {
		return result, nil
	}
	if err := atomicWriteFile(request.path, updated, 0o644, scaffoldValidator(request)); err != nil {
		return scaffoldResult{}, err
	}
	result.Action = "extended"
	return result, nil
}

// newScaffoldData builds one complete create document before any filesystem mutation occurs.
func newScaffoldData(request scaffoldRequest, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) ([]byte, int, error) {
	// Workspace scaffolds own bucket declarations separately and must not acquire app-only defaults.
	if request.kind == configfile.KindWorkspace {
		config := &configfile.WorkspaceConfig{
			BaseConfig: configfile.BaseConfig{APIVersion: configfile.APIVersionV1, Kind: request.kind},
			Services:   map[string]configfile.WorkspaceService{},
		}
		mergeWorkspaceServices(config, request.services)
		data, err := yaml.Marshal(config)
		return data, 0, err
	}
	config := &configfile.AppConfig{
		BaseConfig: configfile.BaseConfig{APIVersion: configfile.APIVersionV1, Kind: request.kind},
		Name:       request.name,
		Version:    request.version,
		Services:   map[string]configfile.AppService{},
	}
	// Only MCP uses the authored summary as protocol-level server identity.
	if request.kind == configfile.KindMCP {
		config.Description = request.description
	}
	if request.kind == configfile.KindSDK {
		config.Language = request.language
		// Direct API mode declares no-codegen before serialization so config creation remains one validated atomic write.
		if request.generateSet {
			generate := request.generate
			config.Generate = &generate
		}
	}
	// An explicit flag remains authoritative and is verified later by plan's exact bucket.use check.
	if request.bucketSet {
		config.Bucket = request.bucket
	}
	if _, err := mergeAppSelections(config, request); err != nil {
		return nil, 0, err
	}
	if _, err := ensureScaffoldBucket(config, bucketResolver); err != nil {
		return nil, 0, err
	}
	generated, err := enrichAppScaffold(config, resolver)
	if err != nil {
		return nil, 0, err
	}
	data, err := yaml.Marshal(config)
	return data, generated, err
}

// extendScaffoldData keeps workspace-local and Engine-backed app merge policies separated at one boundary.
func extendScaffoldData(request scaffoldRequest, data []byte, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) ([]byte, bool, int, error) {
	// Workspace drafts have no Engine-backed app routing requirements.
	if request.kind == configfile.KindWorkspace {
		return extendWorkspaceScaffoldData(request, data)
	}
	return extendAppScaffoldData(request, data, resolver, bucketResolver)
}

// extendWorkspaceScaffoldData keeps the local-only workspace merge separate
// from Engine-backed SDK and MCP enrichment.
func extendWorkspaceScaffoldData(request scaffoldRequest, data []byte) ([]byte, bool, int, error) {
	config := &configfile.WorkspaceConfig{}
	if err := decodeScaffoldDraft(data, request.path, request.kind, config); err != nil {
		return nil, false, 0, err
	}
	// Older editable skeletons may omit the map until their first service is added.
	if config.Services == nil {
		config.Services = map[string]configfile.WorkspaceService{}
	}
	changed := mergeWorkspaceServices(config, request.services)
	updated, err := yaml.Marshal(config)
	return updated, changed, 0, err
}

// extendAppScaffoldData merges identity and selections before bounded Engine discovery and one atomic serialization.
func extendAppScaffoldData(request scaffoldRequest, data []byte, resolver scaffoldRequirementsResolver, bucketResolver scaffoldBucketResolver) ([]byte, bool, int, error) {
	config := &configfile.AppConfig{}
	if err := decodeScaffoldDraft(data, request.path, request.kind, config); err != nil {
		return nil, false, 0, err
	}
	// Editable empty app skeletons need a map before additive service merge.
	if config.Services == nil {
		config.Services = map[string]configfile.AppService{}
	}
	changed, err := mergeAppIdentity(config, request)
	if err != nil {
		return nil, false, 0, err
	}
	selectionChanged, err := mergeAppSelections(config, request)
	if err != nil {
		return nil, false, 0, err
	}
	bucketChanged, err := ensureScaffoldBucket(config, bucketResolver)
	if err != nil {
		return nil, false, 0, err
	}
	generated, err := enrichAppScaffold(config, resolver)
	if err != nil {
		return nil, false, 0, err
	}
	updated, err := yaml.Marshal(config)
	return updated, changed || selectionChanged || bucketChanged || generated > 0, generated, err
}

// ensureScaffoldBucket fills only a missing service-bearing app bucket so empty editable skeletons stay local-only.
func ensureScaffoldBucket(config *configfile.AppConfig, resolver scaffoldBucketResolver) (bool, error) {
	// Existing or explicitly authored buckets must never be replaced by automatic selection.
	if strings.TrimSpace(config.Bucket) != "" {
		return false, nil
	}
	// Empty skeletons remain editable offline because they cannot be planned until a service is selected anyway.
	if len(config.Services) == 0 {
		return false, nil
	}
	bucket, err := resolver()
	// Bucket discovery failures stop the write so init cannot emit another locally valid but unplannable app config.
	if err != nil {
		return false, err
	}
	config.Bucket = bucket
	return true, nil
}

func mergeWorkspaceServices(config *configfile.WorkspaceConfig, services []scaffoldService) bool {
	changed := false
	for _, requested := range services {
		changed = mergeWorkspaceServiceSelection(config, requested.name, requested.version) || changed
	}
	return changed
}

// mergeAppIdentity fills missing immutable app metadata without rewriting an existing authored value.
func mergeAppIdentity(config *configfile.AppConfig, request scaffoldRequest) (bool, error) {
	changed, err := mergeScaffoldField(&config.Name, request.name, request.name != "", "name")
	// Identity conflicts must abort before any later field can be partially merged.
	if err != nil {
		return false, err
	}
	versionChanged, err := mergeAppVersion(config, request)
	// Version transitions are allowed only as an explicit in-place extension, preserving accidental-retarget protection.
	if err != nil {
		return false, err
	}
	changed = changed || versionChanged
	languageChanged, err := mergeAppLanguage(config, request)
	// Kind-specific language rejection must happen before routing fields are touched.
	if err != nil {
		return false, err
	}
	descriptionChanged, err := mergeMCPDescription(config, request)
	// Authored server prose is immutable, so a conflict stops the additive extension.
	if err != nil {
		return false, err
	}
	generateChanged, err := mergeSDKGenerate(config, request)
	// Package-generation intent is immutable within one app version, so extension must reject a conflicting mode.
	if err != nil {
		return false, err
	}
	bucketChanged, err := mergeScaffoldField(&config.Bucket, request.bucket, request.bucketSet, "bucket")
	return changed || languageChanged || descriptionChanged || generateChanged || bucketChanged, err
}

// mergeAppVersion treats an explicit version on --extend as a deliberate new immutable app version in the same file.
func mergeAppVersion(config *configfile.AppConfig, request scaffoldRequest) (bool, error) {
	// Omitted version flags preserve the file's authored version instead of applying the command default.
	if !request.versionSet {
		return false, nil
	}
	// Repeating the current version is idempotent and does not rewrite the document.
	if config.Version == request.version {
		return false, nil
	}
	// Empty skeleton identity may be completed without being considered a version transition.
	if strings.TrimSpace(config.Version) == "" {
		config.Version = request.version
		return true, nil
	}
	// Only additive extension explicitly authorizes retargeting the same file to a new immutable version.
	if !request.extend {
		return false, fmt.Errorf("cannot extend config: existing version %q conflicts with %q", config.Version, request.version)
	}
	config.Version = request.version
	return true, nil
}

// mergeSDKGenerate adds an explicit package-generation policy without treating an omitted historical default as false.
func mergeSDKGenerate(config *configfile.AppConfig, request scaffoldRequest) (bool, error) {
	// MCP has no package generator and must never receive the shared SDK-only field.
	if request.kind != configfile.KindSDK {
		return false, nil
	}
	// Ordinary SDK scaffolds preserve the absent-means-generate compatibility contract.
	if !request.generateSet {
		return false, nil
	}
	// An omitted field historically means package generation, so changing it to false would mutate immutable version behavior.
	if config.Generate == nil {
		// Explicit SDK mode agrees with the historical true default and needs no document rewrite.
		if request.generate {
			return false, nil
		}
		return false, errors.New("generate conflicts with existing config")
	}
	// Repeating the same explicit policy is idempotent.
	if *config.Generate == request.generate {
		return false, nil
	}
	return false, errors.New("generate conflicts with existing config")
}

// mergeAppLanguage keeps package-language handling out of hosted MCP identity merging.
func mergeAppLanguage(config *configfile.AppConfig, request scaffoldRequest) (bool, error) {
	// Only generated SDK packages have a language field that can be extended.
	if request.kind == configfile.KindSDK {
		return mergeScaffoldField(&config.Language, request.language, request.languageSet, "language")
	}
	// Rejecting an explicit MCP language prevents an apparently accepted flag from having no runtime effect.
	if request.languageSet {
		return false, errors.New("mcp config cannot set --language")
	}
	return false, nil
}

// mergeMCPDescription fills missing hosted-server prose without rewriting authored identity.
func mergeMCPDescription(config *configfile.AppConfig, request scaffoldRequest) (bool, error) {
	// SDK configs have no server metadata consumer, so their shared struct field remains untouched.
	if request.kind != configfile.KindMCP {
		return false, nil
	}
	return mergeScaffoldField(&config.Description, request.description, request.descriptionSet, "description")
}

func mergeScaffoldField(current *string, requested string, provided bool, field string) (bool, error) {
	if !provided {
		return false, nil
	}
	if *current == requested {
		return false, nil
	}
	if *current == "" {
		*current = requested
		return true, nil
	}
	// --extend is additive. Refusing conflicting identity/routing fields
	// prevents an innocent extension from silently retargeting a config.
	return false, fmt.Errorf("cannot extend config: existing %s %q conflicts with %q", field, *current, requested)
}

func mergeAppSelections(config *configfile.AppConfig, request scaffoldRequest) (bool, error) {
	changed, err := mergeAppServices(config, request.services)
	if err != nil {
		return false, err
	}
	operationsChanged, err := mergeAppOperations(config, request.operations)
	if err != nil {
		return false, err
	}
	selectAllChanged, err := mergeAppSelectAll(config, request.selectAll)
	return changed || operationsChanged || selectAllChanged, err
}

// enrichAppScaffold adds only missing routing bindings after all create or
// extend selections have been merged into one authoritative app draft.
func enrichAppScaffold(config *configfile.AppConfig, resolver scaffoldRequirementsResolver) (int, error) {
	selections := appScaffoldSelections(config)
	requirements, err := resolver(selections)
	if err != nil {
		return 0, fmt.Errorf("resolve app scaffold requirements: %w", err)
	}
	generated := 0
	// A single normalized pass preserves user entries and keeps generated order stable.
	canonical, err := canonicalScaffoldRequirements(requirements)
	if err != nil {
		return 0, err
	}
	if err := validateScaffoldRequirementKeys(canonical); err != nil {
		return 0, err
	}
	for _, requirement := range canonical {
		added, addErr := addScaffoldRequirement(config, requirement)
		if addErr != nil {
			return 0, addErr
		}
		// Counts describe generated metadata without exposing its name or value.
		if added {
			generated++
		}
	}
	return generated, nil
}

// appScaffoldSelections converts the complete app service map into a stable
// batch input without changing user-authored operation ordering in the file.
func appScaffoldSelections(config *configfile.AppConfig) []api.AppScaffoldSelection {
	names := make([]string, 0, len(config.Services))
	// Maps are unordered, so service names are collected before the Engine request.
	for name := range config.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	selections := make([]api.AppScaffoldSelection, 0, len(names))
	// Each service contributes once, preventing per-service GraphQL calls.
	for _, name := range names {
		service := config.Services[name]
		operations := append([]string(nil), service.Operations...)
		// Operation order is not semantic, so the wire representation can be canonical.
		sort.Strings(operations)
		selections = append(selections, api.AppScaffoldSelection{
			Service: name, Version: service.Version, Operations: operations, SelectAll: service.SelectAll,
		})
	}
	return selections
}

// canonicalScaffoldRequirements trims, sorts, and removes exact duplicate
// metadata so a repeated Engine row cannot duplicate a generated injection.
func canonicalScaffoldRequirements(requirements []api.AppScaffoldRequirement) ([]api.AppScaffoldRequirement, error) {
	sorted := append([]api.AppScaffoldRequirement(nil), requirements...)
	// Trimming aligns response identifiers with the canonical config boundary.
	for index := range sorted {
		sorted[index].Service = strings.TrimSpace(sorted[index].Service)
		sorted[index].Variable = strings.TrimSpace(sorted[index].Variable)
		// Missing correlation metadata would otherwise silently omit a required binding.
		if sorted[index].Service == "" || sorted[index].Variable == "" {
			return nil, errors.New("Engine returned an invalid scaffold requirement")
		}
	}
	sort.Slice(sorted, func(left, right int) bool {
		// Service-first ordering keeps each config service's injections contiguous.
		if sorted[left].Service == sorted[right].Service {
			return sorted[left].Variable < sorted[right].Variable
		}
		return sorted[left].Service < sorted[right].Service
	})
	result := make([]api.AppScaffoldRequirement, 0, len(sorted))
	last := ""
	// Adjacent equality is sufficient after the deterministic sort.
	for _, requirement := range sorted {
		key := requirement.Service + "\x00" + requirement.Variable
		// Repeated metadata represents one logical routing target.
		if key == last {
			continue
		}
		result = append(result, requirement)
		last = key
	}
	return result, nil
}

// validateScaffoldRequirementKeys fails closed when distinct provider
// variables would otherwise share one normalized bucket value within a service.
func validateScaffoldRequirementKeys(requirements []api.AppScaffoldRequirement) error {
	variablesByKey := make(map[string]string, len(requirements))
	// Bucket values are service-scoped, so collisions matter only inside one service.
	for _, requirement := range requirements {
		key := requirement.Service + "\x00" + scaffoldBucketValueKey(requirement.Service, requirement.Variable)
		previous, exists := variablesByKey[key]
		// Distinct targets must never silently share one generated provider value.
		if exists && previous != requirement.Variable {
			return errors.New("Engine returned scaffold requirements with colliding generated keys")
		}
		variablesByKey[key] = requirement.Variable
	}
	return nil
}

// addScaffoldRequirement mutates one selected service only when the user has
// not already supplied the same server-variable target.
func addScaffoldRequirement(config *configfile.AppConfig, requirement api.AppScaffoldRequirement) (bool, error) {
	service, exists := config.Services[requirement.Service]
	// Engine responses must remain correlated to the submitted selection batch.
	if !exists {
		return false, errors.New("Engine returned scaffold requirements for an unselected service")
	}
	// User-authored routing policy remains authoritative during additive init.
	if hasServerVariableInjection(service.Injections, requirement.Variable) {
		return false, nil
	}
	key := scaffoldBucketValueKey(requirement.Service, requirement.Variable)
	// Canonical Registry variable names should always yield one safe ASCII key.
	if key == "" {
		return false, errors.New("Engine returned an invalid scaffold requirement")
	}
	service.Injections = append(service.Injections, configfile.InjectionConfig{
		Location: "server_variable", Name: requirement.Variable,
		Value: "${bucket.env." + key + "}", Mode: "force",
	})
	config.Services[requirement.Service] = service
	return true, nil
}

// hasServerVariableInjection recognizes the Engine's canonicalized target
// shape while retaining the user's original spelling and value unchanged.
func hasServerVariableInjection(injections []configfile.InjectionConfig, variable string) bool {
	// Only location and target define duplication; mode and value remain user policy.
	for _, injection := range injections {
		if strings.EqualFold(strings.TrimSpace(injection.Location), "server_variable") && strings.TrimSpace(injection.Name) == variable {
			return true
		}
	}
	return false
}

// scaffoldBucketValueKey derives one portable non-secret bucket key while
// avoiding a repeated service prefix already present in the variable name.
func scaffoldBucketValueKey(service, variable string) string {
	// Provider-qualified keys use their slug tail so @sendbird/sendbird and
	// sendbird share the same portable bucket-key prefix.
	if separator := strings.LastIndex(service, "/"); separator >= 0 {
		service = service[separator+1:]
	}
	serviceKey := strings.ToUpper(scaffoldKeyCleaner.ReplaceAllString(service, ""))
	variableKey := strings.Trim(strings.ToUpper(scaffoldKeyCleaner.ReplaceAllString(variable, "_")), "_")
	// A missing variable component cannot form a usable dynamic reference.
	if variableKey == "" {
		return ""
	}
	// Service-less fallbacks and already-prefixed names avoid duplicated text.
	if serviceKey == "" || variableKey == serviceKey || strings.HasPrefix(variableKey, serviceKey+"_") {
		return variableKey
	}
	return serviceKey + "_" + variableKey
}

func mergeAppServices(config *configfile.AppConfig, services []scaffoldService) (bool, error) {
	changed := false
	for _, requested := range services {
		service, exists := config.Services[requested.name]
		if exists && service.Version != "" && service.Version != requested.version {
			return false, fmt.Errorf("service %q already uses version %q", requested.name, service.Version)
		}
		if !exists || service.Version == "" {
			service.Version = requested.version
			if service.Operations == nil {
				service.Operations = []string{}
			}
			config.Services[requested.name] = service
			changed = true
		}
	}
	return changed, nil
}

func mergeAppOperations(config *configfile.AppConfig, operations []scaffoldOperation) (bool, error) {
	changed := false
	for _, requested := range operations {
		service, exists := config.Services[requested.service]
		if !exists {
			return false, fmt.Errorf("operation service %q is not declared; add --service %s=<version>", requested.service, requested.service)
		}
		if service.SelectAll {
			return false, fmt.Errorf("service %q already uses select_all and cannot list operations", requested.service)
		}
		if !containsString(service.Operations, requested.operation) {
			service.Operations = append(service.Operations, requested.operation)
			config.Services[requested.service] = service
			changed = true
		}
	}
	return changed, nil
}

func mergeAppSelectAll(config *configfile.AppConfig, services []string) (bool, error) {
	changed := false
	for _, serviceName := range services {
		service, exists := config.Services[serviceName]
		if !exists {
			return false, fmt.Errorf("select-all service %q is not declared; add --service %s=<version>", serviceName, serviceName)
		}
		if len(service.Operations) > 0 {
			return false, fmt.Errorf("service %q lists operations and cannot use select_all", serviceName)
		}
		if !service.SelectAll {
			service.SelectAll = true
			config.Services[serviceName] = service
			changed = true
		}
	}
	return changed, nil
}

// scaffoldValidator applies full semantic validation to unified app outcomes and every existing app extension.
func scaffoldValidator(request scaffoldRequest) contentValidator {
	return func(data []byte) error {
		// Compatibility scaffolds retain structural validation, while unified modes signal their complete outcome through immutable mode fields.
		if request.kind == configfile.KindWorkspace || (!request.extend && !request.generateSet && !request.descriptionSet) {
			if request.kind == configfile.KindWorkspace {
				return decodeScaffoldDraft(data, request.path, request.kind, &configfile.WorkspaceConfig{})
			}
			return decodeScaffoldDraft(data, request.path, request.kind, &configfile.AppConfig{})
		}
		_, err := configfile.Parse(data, request.path)
		return err
	}
}

func decodeScaffoldDraft(data []byte, path string, expectedKind configfile.ConfigKind, target any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	// The CLI loader owns one typed config per file. Rejecting a second
	// document avoids appearing to extend content the loader would ignore.
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("config %s must contain exactly one YAML document", path)
		}
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	base, err := scaffoldBaseConfig(target)
	if err != nil {
		return err
	}
	if base.APIVersion != configfile.APIVersionV1 {
		return fmt.Errorf("config %s must use apiVersion %q", path, configfile.APIVersionV1)
	}
	if base.Kind != expectedKind {
		return fmt.Errorf("expected %s config in %s, got %s", expectedKind, path, base.Kind)
	}
	return nil
}

func scaffoldBaseConfig(target any) (configfile.BaseConfig, error) {
	switch config := target.(type) {
	case *configfile.WorkspaceConfig:
		return config.BaseConfig, nil
	case *configfile.AppConfig:
		return config.BaseConfig, nil
	default:
		return configfile.BaseConfig{}, fmt.Errorf("unsupported scaffold target %T", target)
	}
}

func printScaffoldResult(cmd *cobra.Command, result scaffoldResult) error {
	// JSON consumers receive one bounded count and never generated references or values.
	if wantsJSON(cmd) {
		return writeJSON(cmd, result)
	}
	switch result.Action {
	case "created":
		fmt.Fprintf(cmd.OutOrStdout(), "Created %s config skeleton at %s.\n", result.Kind, result.Path)
	case "extended":
		fmt.Fprintf(cmd.OutOrStdout(), "Extended %s config at %s.\n", result.Kind, result.Path)
	default:
		fmt.Fprintf(cmd.OutOrStdout(), "No changes needed in %s.\n", result.Path)
	}
	// Human output reports setup size without exposing variable or bucket key names.
	if result.GeneratedBindingCount > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Generated server-variable bindings: %d.\n", result.GeneratedBindingCount)
	}
	return nil
}

func init() {
	workspaceCmd.AddCommand(newScaffoldCommand(configfile.KindWorkspace))
	sdkCmd.AddCommand(newScaffoldCommand(configfile.KindSDK))
	mcpCmd.AddCommand(newScaffoldCommand(configfile.KindMCP))
}
