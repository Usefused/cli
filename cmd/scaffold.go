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
	extend     bool
	services   []string
	operations []string
	selectAll  []string
	version    string
	language   string
	bucket     string
}

type scaffoldRequest struct {
	kind        configfile.ConfigKind
	name        string
	path        string
	extend      bool
	services    []scaffoldService
	operations  []scaffoldOperation
	selectAll   []string
	version     string
	language    string
	bucket      string
	versionSet  bool
	languageSet bool
	bucketSet   bool
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

// newScaffoldCommand wires production app scaffolds to Engine requirement
// discovery while leaving the dependency replaceable in focused offline tests.
func newScaffoldCommand(kind configfile.ConfigKind) *cobra.Command {
	return newScaffoldCommandWithResolver(kind, resolveScaffoldRequirements)
}

// newScaffoldCommandWithResolver injects the only remote scaffold dependency
// so parsing, merging, and atomic writes remain independently testable.
func newScaffoldCommandWithResolver(kind configfile.ConfigKind, resolver scaffoldRequirementsResolver) *cobra.Command {
	opts := &scaffoldOptions{version: defaultScaffoldVersion, language: defaultScaffoldLanguage}
	use := "init [name]"
	args := cobra.RangeArgs(0, 1)
	selectionDescription := "services and operation selections"
	serviceFlagDescription := "Service key and version as <key>=<version>; repeatable"
	if kind == configfile.KindWorkspace {
		use = "init"
		args = cobra.NoArgs
		selectionDescription = "services"
		serviceFlagDescription = "Service key with an optional version as <key>[=<version>]; repeatable"
	}
	command := &cobra.Command{
		Use:   use,
		Short: "Create or extend a Fused config file",
		Long: fmt.Sprintf(`Create a %s config skeleton.

By default the command refuses to replace an existing file. Pass --extend to
merge %s into an existing config instead.`, kind, selectionDescription),
		Args: args,
		RunE: WithTelemetry(fmt.Sprintf("cli.%s.init", kind), func(cmd *cobra.Command, args []string) error {
			request, err := buildScaffoldRequest(cmd, kind, args, opts)
			if err != nil {
				return err
			}
			result, err := writeScaffold(request, resolver)
			if err != nil {
				return err
			}
			recordGeneratedBindingCount(cmd.Context(), result.GeneratedBindingCount)
			recordAppliedChangeIf(cmd.Context(), cmd.CommandPath(), "config_file", result.Changed)
			return printScaffoldResult(cmd, result)
		}),
	}

	command.Flags().BoolVar(&opts.extend, "extend", false, "Merge into an existing config instead of creating a new file")
	command.Flags().StringSliceVar(&opts.services, "service", nil, serviceFlagDescription)
	if kind != configfile.KindWorkspace {
		command.Flags().StringSliceVar(&opts.operations, "operation", nil, "Selected operation as <service>=<operationId>; repeatable")
		command.Flags().StringSliceVar(&opts.selectAll, "select-all", nil, "Service key whose operations should all be selected; repeatable")
		command.Flags().StringVar(&opts.version, "version", defaultScaffoldVersion, "Config version")
		command.Flags().StringVar(&opts.bucket, "bucket", "", "Existing bucket to bind to this config")
	}
	if kind == configfile.KindSDK {
		command.Flags().StringVar(&opts.language, "language", defaultScaffoldLanguage, "SDK target language")
	}
	addJSONOutputFlag(command)
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

func buildScaffoldRequest(cmd *cobra.Command, kind configfile.ConfigKind, args []string, opts *scaffoldOptions) (scaffoldRequest, error) {
	name := ""
	if len(args) == 1 {
		name = strings.TrimSpace(args[0])
	}
	if err := validateScaffoldArgs(kind, name, opts.extend); err != nil {
		return scaffoldRequest{}, err
	}
	services, err := parseScaffoldServices(opts.services, kind != configfile.KindWorkspace)
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
	return scaffoldRequest{
		kind: kind, name: name, path: path, extend: opts.extend,
		services: services, operations: operations, selectAll: selectAll,
		version: opts.version, language: opts.language, bucket: strings.TrimSpace(opts.bucket),
		versionSet: cmd.Flags().Changed("version"), languageSet: cmd.Flags().Changed("language"),
		bucketSet: cmd.Flags().Changed("bucket"),
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

func writeScaffold(request scaffoldRequest, resolver scaffoldRequirementsResolver) (scaffoldResult, error) {
	if request.extend {
		return extendScaffold(request, resolver)
	}
	data, generated, err := newScaffoldData(request, resolver)
	if err != nil {
		return scaffoldResult{}, err
	}
	if err := atomicCreateFile(request.path, data, 0o644, scaffoldValidator(request.path, request.kind)); err != nil {
		return scaffoldResult{}, err
	}
	return scaffoldResult{Action: "created", Kind: request.kind, Path: request.path, Changed: true, GeneratedBindingCount: generated}, nil
}

func extendScaffold(request scaffoldRequest, resolver scaffoldRequirementsResolver) (scaffoldResult, error) {
	data, err := os.ReadFile(request.path)
	if os.IsNotExist(err) {
		return scaffoldResult{}, fmt.Errorf("cannot extend %s: file does not exist", request.path)
	}
	if err != nil {
		return scaffoldResult{}, fmt.Errorf("read config %s: %w", request.path, err)
	}
	updated, changed, generated, err := extendScaffoldData(request, data, resolver)
	if err != nil {
		return scaffoldResult{}, err
	}
	result := scaffoldResult{Action: "unchanged", Kind: request.kind, Path: request.path, Changed: changed, GeneratedBindingCount: generated}
	if !changed {
		return result, nil
	}
	if err := atomicWriteFile(request.path, updated, 0o644, scaffoldValidator(request.path, request.kind)); err != nil {
		return scaffoldResult{}, err
	}
	result.Action = "extended"
	return result, nil
}

func newScaffoldData(request scaffoldRequest, resolver scaffoldRequirementsResolver) ([]byte, int, error) {
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
	if request.kind == configfile.KindSDK {
		config.Language = request.language
	}
	// Why: omission delegates to Engine's existing default-bucket selection;
	// scaffolding must not invent a bucket or imply that one was created.
	if request.bucketSet {
		config.Bucket = request.bucket
	}
	if _, err := mergeAppSelections(config, request); err != nil {
		return nil, 0, err
	}
	generated, err := enrichAppScaffold(config, resolver)
	if err != nil {
		return nil, 0, err
	}
	data, err := yaml.Marshal(config)
	return data, generated, err
}

func extendScaffoldData(request scaffoldRequest, data []byte, resolver scaffoldRequirementsResolver) ([]byte, bool, int, error) {
	// Workspace drafts have no Engine-backed app routing requirements.
	if request.kind == configfile.KindWorkspace {
		return extendWorkspaceScaffoldData(request, data)
	}
	return extendAppScaffoldData(request, data, resolver)
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

// extendAppScaffoldData merges identity and selections before performing one
// requirement lookup and one atomic serialization.
func extendAppScaffoldData(request scaffoldRequest, data []byte, resolver scaffoldRequirementsResolver) ([]byte, bool, int, error) {
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
	generated, err := enrichAppScaffold(config, resolver)
	if err != nil {
		return nil, false, 0, err
	}
	updated, err := yaml.Marshal(config)
	return updated, changed || selectionChanged || generated > 0, generated, err
}

func mergeWorkspaceServices(config *configfile.WorkspaceConfig, services []scaffoldService) bool {
	changed := false
	for _, requested := range services {
		changed = mergeWorkspaceServiceSelection(config, requested.name, requested.version) || changed
	}
	return changed
}

func mergeAppIdentity(config *configfile.AppConfig, request scaffoldRequest) (bool, error) {
	changed, err := mergeScaffoldField(&config.Name, request.name, request.name != "", "name")
	if err != nil {
		return false, err
	}
	versionChanged, err := mergeScaffoldField(&config.Version, request.version, request.versionSet, "version")
	if err != nil {
		return false, err
	}
	changed = changed || versionChanged
	if request.kind == configfile.KindSDK {
		languageChanged, err := mergeScaffoldField(&config.Language, request.language, request.languageSet, "language")
		if err != nil {
			return false, err
		}
		changed = changed || languageChanged
	} else if request.languageSet {
		return false, errors.New("mcp config cannot set --language")
	}
	bucketChanged, err := mergeScaffoldField(&config.Bucket, request.bucket, request.bucketSet, "bucket")
	return changed || bucketChanged, err
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
	// Why: --extend is additive. Refusing conflicting identity/routing fields
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

func scaffoldValidator(path string, kind configfile.ConfigKind) contentValidator {
	return func(data []byte) error {
		if kind == configfile.KindWorkspace {
			return decodeScaffoldDraft(data, path, kind, &configfile.WorkspaceConfig{})
		}
		return decodeScaffoldDraft(data, path, kind, &configfile.AppConfig{})
	}
}

func decodeScaffoldDraft(data []byte, path string, expectedKind configfile.ConfigKind, target any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	// Why: the CLI loader owns one typed config per file. Rejecting a second
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
