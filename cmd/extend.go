package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

type unifiedExtendOptions struct {
	services    []string
	operations  []string
	selectAll   []string
	version     string
	description string
}

type unifiedExtendTarget struct {
	mode   unifiedInitMode
	path   string
	config *configfile.ParsedConfig
}

// newUnifiedExtendCommand creates the additive root workflow over an existing app declaration.
func newUnifiedExtendCommand() *cobra.Command {
	return newUnifiedExtendCommandWithRunner(runUnifiedInitLifecycle)
}

// newUnifiedExtendCommandWithRunner keeps target resolution testable while sharing the complete init lifecycle.
func newUnifiedExtendCommandWithRunner(runner unifiedInitRunner) *cobra.Command {
	opts := &unifiedExtendOptions{}
	command := &cobra.Command{
		Use:   "extend <app-name>",
		Short: "Add services or operations to an existing SDK, API app, or MCP server",
		Long: `Extend one existing Fused app through the same reviewed lifecycle as init.

The config determines whether the app is a generated SDK, direct API app, or
MCP server. A real change without --version advances a stable version to its
next minor release; pass --version to choose a different immutable successor.`,
		Args: cobra.ExactArgs(1),
		RunE: WithTelemetry("cli.extend", func(cmd *cobra.Command, args []string) error {
			target, err := resolveUnifiedExtendTarget(args[0])
			// Target resolution must prove one existing authored config before request construction.
			if err != nil {
				return err
			}
			request, err := buildUnifiedExtendRequest(cmd, target, opts)
			// Local selection errors must stop before the shared lifecycle can plan or mutate remote state.
			if err != nil {
				return err
			}
			return runner(cmd, target.mode, request)
		}),
	}

	command.Flags().StringSliceVar(&opts.services, "service", nil, "Registry service as <service>[@<version>]; comma-separated or repeatable")
	command.Flags().StringSliceVar(&opts.operations, "operation", nil, "Selected operation as <service>=<operationId>; repeatable")
	command.Flags().StringSliceVar(&opts.selectAll, "select-all", nil, "Service whose complete operation surface should be selected; repeatable")
	command.Flags().StringVar(&opts.version, "version", "", "Explicit immutable successor version")
	command.Flags().StringVar(&opts.description, "description", "", "Complete replacement for the MCP description on the successor")
	return command
}

// resolveUnifiedExtendTarget selects an exact -f file or discovers one unambiguous same-name app config.
func resolveUnifiedExtendTarget(name string) (unifiedExtendTarget, error) {
	name = strings.TrimSpace(name)
	// Empty identity can never be matched safely against authored app declarations.
	if name == "" {
		return unifiedExtendTarget{}, errors.New("app name must not be empty")
	}
	// An explicit config path is authoritative and bypasses workspace-wide name discovery.
	if strings.TrimSpace(ConfigFile) != "" {
		path, err := resolveUnifiedExtendExplicitPath(ConfigFile)
		if err != nil {
			return unifiedExtendTarget{}, err
		}
		return parseUnifiedExtendTarget(path, name)
	}
	paths, err := discoverUnifiedExtendPaths(name)
	if err != nil {
		return unifiedExtendTarget{}, err
	}
	// Extend never creates implicitly because a typo must not become a new app family.
	if len(paths) == 0 {
		return unifiedExtendTarget{}, fmt.Errorf("no SDK, API, or MCP config named %q exists; run 'fused-cli init %s' to create it or pass -f <exact-config-path>", name, name)
	}
	// Same-name declarations can represent distinct immutable app families, so directory order cannot choose for the user.
	if len(paths) > 1 {
		return unifiedExtendTarget{}, fmt.Errorf("multiple configs named %q exist (%s); pass -f <exact-config-path>", name, strings.Join(paths, ", "))
	}
	return parseUnifiedExtendTarget(paths[0], name)
}

// resolveUnifiedExtendExplicitPath verifies that -f names one regular existing config file.
func resolveUnifiedExtendExplicitPath(path string) (string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	info, err := os.Stat(path)
	// A missing or inaccessible explicit path is actionable without falling back to discovery.
	if err != nil {
		return "", fmt.Errorf("read extend target %q: %w", path, err)
	}
	// Directories are never valid config identities even if they contain a same-name YAML file.
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("extend target %q is not a regular file", path)
	}
	return path, nil
}

// discoverUnifiedExtendPaths finds authored SDK and MCP YAML documents whose declared name matches exactly.
func discoverUnifiedExtendPaths(name string) ([]string, error) {
	roots := []string{filepath.Join(".fused", "sdks"), filepath.Join(".fused", "mcps")}
	paths := make([]string, 0)
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			// A disappeared or unreadable entry makes discovery incomplete and must fail closed.
			if walkErr != nil {
				return walkErr
			}
			// Only regular YAML documents can be app config candidates.
			if entry.IsDir() || !isUnifiedExtendYAML(path) {
				return nil
			}
			parsed, parseErr := configfile.ParseFile(path)
			// Unrelated invalid drafts should not prevent resolving a different exact app name.
			if parseErr != nil {
				return nil
			}
			candidate, nameErr := unifiedExtendConfigName(parsed)
			// Unsupported config kinds are outside the SDK/API/MCP discovery surface.
			if nameErr == nil && candidate == name {
				paths = append(paths, path)
			}
			return nil
		})
		// A conventional directory may be absent before the first app of that kind exists.
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("discover extend targets in %q: %w", root, err)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// isUnifiedExtendYAML limits discovery to the two supported YAML filename extensions.
func isUnifiedExtendYAML(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	return extension == ".yaml" || extension == ".yml"
}

// parseUnifiedExtendTarget parses one existing file and infers its public runtime outcome.
func parseUnifiedExtendTarget(path, expectedName string) (unifiedExtendTarget, error) {
	parsed, err := configfile.ParseFile(path)
	// Full config parsing ensures malformed authored state never enters the shared lifecycle.
	if err != nil {
		return unifiedExtendTarget{}, err
	}
	mode, err := inferUnifiedExtendMode(parsed)
	if err != nil {
		return unifiedExtendTarget{}, err
	}
	name, err := unifiedExtendConfigName(parsed)
	if err != nil {
		return unifiedExtendTarget{}, err
	}
	// An explicit path cannot silently retarget a differently named app.
	if name != strings.TrimSpace(expectedName) {
		return unifiedExtendTarget{}, fmt.Errorf("config %q declares app %q, not %q", path, name, expectedName)
	}
	return unifiedExtendTarget{mode: mode, path: path, config: parsed}, nil
}

// inferUnifiedExtendMode maps durable kind and SDK generation policy back to the public outcome.
func inferUnifiedExtendMode(parsed *configfile.ParsedConfig) (unifiedInitMode, error) {
	// MCP retains its distinct hosted runtime kind.
	if parsed != nil && parsed.MCP != nil {
		return unifiedInitModeMCP, nil
	}
	// An explicit generate:false SDK declaration is the direct REST API outcome.
	if parsed != nil && parsed.SDK != nil && parsed.SDK.Generate != nil && !*parsed.SDK.Generate {
		return unifiedInitModeAPI, nil
	}
	// SDK defaults generation on when the field is absent.
	if parsed != nil && parsed.SDK != nil {
		return unifiedInitModeSDK, nil
	}
	return "", errors.New("extend supports only existing kind: sdk or kind: mcp configs")
}

// unifiedExtendConfigName returns the app identity from either supported parsed config shape.
func unifiedExtendConfigName(parsed *configfile.ParsedConfig) (string, error) {
	// SDK and API declarations share the same durable app schema.
	if parsed != nil && parsed.SDK != nil {
		return strings.TrimSpace(parsed.SDK.Name), nil
	}
	// MCP supplies the same identity fields through its hosted-runtime schema.
	if parsed != nil && parsed.MCP != nil {
		return strings.TrimSpace(parsed.MCP.Name), nil
	}
	return "", errors.New("config is not an SDK, API, or MCP app")
}

// buildUnifiedExtendRequest converts additive flags and inferred identity into the shared scaffold contract.
func buildUnifiedExtendRequest(cmd *cobra.Command, target unifiedExtendTarget, opts *unifiedExtendOptions) (scaffoldRequest, error) {
	request, err := parseUnifiedExtendSelections(target, opts)
	// Reject invalid additive flags before reading or changing remote scope.
	if err != nil {
		return request, err
	}
	// Explicit identity and description overrides are checked before deciding whether to prompt for scope.
	if err := validateUnifiedExtendOverrides(cmd, target, opts); err != nil {
		return request, err
	}
	request.versionSet, request.descriptionSet = cmd.Flags().Changed("version"), cmd.Flags().Changed("description")
	request.description = strings.TrimSpace(opts.description)
	// Automation cannot infer an unspecified extension.
	if err := completeUnifiedExtendSelections(&request, target); err != nil {
		return request, err
	}
	request.name, request.version, request.kind, err = unifiedExtendIdentity(target.config)
	// An invalid existing identity cannot become a successor app.
	if err != nil {
		return request, err
	}
	// An explicit successor is authoritative over automatic additive version inference.
	if request.versionSet {
		request.version = strings.TrimSpace(opts.version)
	}
	request.path, request.extend = target.path, true
	if target.mode == unifiedInitModeSDK || target.mode == unifiedInitModeAPI {
		// Preserve the existing family's immutable package-generation mode.
		request.generate, request.generateSet = target.mode == unifiedInitModeSDK, true
	}
	return request, nil
}

// parseUnifiedExtendSelections reuses physical flag parsing and inherits omitted provider pins from the existing app.
func parseUnifiedExtendSelections(target unifiedExtendTarget, opts *unifiedExtendOptions) (scaffoldRequest, error) {
	request := scaffoldRequest{}
	var err error
	request.services, err = parseScaffoldServices(opts.services, false)
	// Malformed service references must not be inherited into the existing config.
	if err != nil {
		return request, err
	}
	request.services = inheritUnifiedExtendServiceVersions(request.services, target.config)
	request.operations, err = parseScaffoldOperations(opts.operations)
	// Operation syntax must be valid before merging any additions.
	if err != nil {
		return request, err
	}
	request.selectAll, err = parseScaffoldNames("--select-all", opts.selectAll)
	return request, err
}

// validateUnifiedExtendOverrides prevents empty or mode-incompatible replacements from changing app identity.
func validateUnifiedExtendOverrides(cmd *cobra.Command, target unifiedExtendTarget, opts *unifiedExtendOptions) error {
	// Empty explicit values differ from omission and cannot be inferred safely.
	if cmd.Flags().Changed("version") && strings.TrimSpace(opts.version) == "" {
		return errors.New("--version must not be empty")
	}
	// Only an explicit description override changes existing authored metadata.
	if cmd.Flags().Changed("description") {
		// Only hosted MCP servers expose an authored server description.
		if target.mode != unifiedInitModeMCP {
			return errors.New("--description can only be used when extending an MCP server")
		}
		// An explicit blank description cannot erase required MCP metadata.
		if strings.TrimSpace(opts.description) == "" {
			return errors.New("--description must not be empty")
		}
	}
	return nil
}

// completeUnifiedExtendSelections opens the existing selector only when no deterministic extension was supplied.
func completeUnifiedExtendSelections(request *scaffoldRequest, target unifiedExtendTarget) error {
	hasIntent := len(request.services)+len(request.operations)+len(request.selectAll) > 0
	// Version-only or description-only updates are also explicit changes.
	if hasIntent || request.versionSet || request.descriptionSet {
		return nil
	}
	// Noninteractive extension requires deterministic user intent.
	if nonInteractive() {
		return errors.New("--no-input extend requires --service, --operation, --select-all, --version, or an MCP --description")
	}
	request.services = unifiedExtendSelectableServices(target.config)
	// An empty existing app offers no safe provider selection to infer.
	if len(request.services) == 0 {
		return errors.New("extend requires --service because the existing app has no selected services")
	}
	return nil
}

// inheritUnifiedExtendServiceVersions keeps an existing provider pin when --service omits its version.
func inheritUnifiedExtendServiceVersions(services []scaffoldService, parsed *configfile.ParsedConfig) []scaffoldService {
	configured := unifiedExtendServices(parsed)
	for index := range services {
		// A caller-supplied version remains authoritative over the existing pin.
		if strings.TrimSpace(services[index].version) != "" {
			continue
		}
		// Exact configured keys are stable and avoid accidentally inheriting through an ambiguous Registry alias.
		if existing, ok := configured[services[index].name]; ok {
			services[index].version = existing.Version
		}
	}
	return services
}

// unifiedExtendIdentity returns the immutable identity fields shared by SDK/API and MCP configs.
func unifiedExtendIdentity(parsed *configfile.ParsedConfig) (string, string, configfile.ConfigKind, error) {
	// SDK covers both generated-package and direct-REST outcomes.
	if parsed != nil && parsed.SDK != nil {
		return strings.TrimSpace(parsed.SDK.Name), strings.TrimSpace(parsed.SDK.Version), configfile.KindSDK, nil
	}
	// MCP uses the same version extension contract under its own kind selector.
	if parsed != nil && parsed.MCP != nil {
		return strings.TrimSpace(parsed.MCP.Name), strings.TrimSpace(parsed.MCP.Version), configfile.KindMCP, nil
	}
	return "", "", "", errors.New("config is not an SDK, API, or MCP app")
}

// unifiedExtendSelectableServices returns deterministic existing pins for the shared interactive operation selector.
func unifiedExtendSelectableServices(parsed *configfile.ParsedConfig) []scaffoldService {
	configured := unifiedExtendServices(parsed)
	names := make([]string, 0, len(configured))
	for name := range configured {
		names = append(names, name)
	}
	sort.Strings(names)
	services := make([]scaffoldService, 0, len(names))
	for _, name := range names {
		// Services already scoped to every operation need no additional operation selection.
		if configured[name].SelectAll {
			continue
		}
		services = append(services, scaffoldService{name: name, version: configured[name].Version})
	}
	return services
}

// unifiedExtendServices exposes the selected service map without duplicating kind branches throughout request construction.
func unifiedExtendServices(parsed *configfile.ParsedConfig) map[string]configfile.AppService {
	// SDK and API store selections on the SDK schema.
	if parsed != nil && parsed.SDK != nil {
		return parsed.SDK.Services
	}
	// MCP stores the same selection shape on its hosted app schema.
	if parsed != nil && parsed.MCP != nil {
		return parsed.MCP.Services
	}
	return nil
}

// init registers the additive app workflow beside unified creation.
func init() {
	RootCmd.AddCommand(newUnifiedExtendCommand())
}
