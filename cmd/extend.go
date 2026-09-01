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
	services   []string
	operations []string
	selectAll  []string
	version    string
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

	command.Flags().StringSliceVar(&opts.services, "service", nil, "Registry service as <service>[=<version>]; repeatable")
	command.Flags().StringSliceVar(&opts.operations, "operation", nil, "Selected operation as <service>=<operationId>; repeatable")
	command.Flags().StringSliceVar(&opts.selectAll, "select-all", nil, "Service whose complete operation surface should be selected; repeatable")
	command.Flags().StringVar(&opts.version, "version", "", "Explicit immutable successor version")
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
	services, err := parseScaffoldServices(opts.services, false)
	// Service flag syntax must be valid before pinned versions are inherited from the file.
	if err != nil {
		return scaffoldRequest{}, err
	}
	services = inheritUnifiedExtendServiceVersions(services, target.config)
	operations, err := parseScaffoldOperations(opts.operations)
	// Explicit operation IDs retain the same parsing contract as unified init.
	if err != nil {
		return scaffoldRequest{}, err
	}
	selectAll, err := parseScaffoldNames("--select-all", opts.selectAll)
	// Complete-surface selections must also fail locally on malformed or duplicate names.
	if err != nil {
		return scaffoldRequest{}, err
	}
	versionSet := cmd.Flags().Changed("version")
	// An explicitly empty successor is different from omission and cannot be inferred safely.
	if versionSet && strings.TrimSpace(opts.version) == "" {
		return scaffoldRequest{}, errors.New("--version must not be empty")
	}
	selectionProvided := len(services) > 0 || len(operations) > 0 || len(selectAll) > 0 || versionSet
	// Automation cannot open the operation selector, so it must name one deterministic change.
	if !selectionProvided && nonInteractive() {
		return scaffoldRequest{}, errors.New("--no-input extend requires --service, --operation, --select-all, or --version")
	}
	// A bare terminal command searches operations across already selected services.
	if !selectionProvided {
		services = unifiedExtendSelectableServices(target.config)
		// A config with no scoped services has no operation catalogue to open, so request an explicit service.
		if len(services) == 0 {
			return scaffoldRequest{}, errors.New("extend requires --service because the existing app has no selected services")
		}
	}
	name, currentVersion, kind, err := unifiedExtendIdentity(target.config)
	if err != nil {
		return scaffoldRequest{}, err
	}
	version := currentVersion
	// Explicit successor intent is carried into pre-write version collision checks unchanged.
	if versionSet {
		version = strings.TrimSpace(opts.version)
	}
	request := scaffoldRequest{
		kind: kind, name: name, path: target.path, extend: true,
		services: services, operations: operations, selectAll: selectAll,
		version: version, versionSet: versionSet,
	}
	// Generated SDK and direct API declarations must preserve their distinct generation invariant during merge validation.
	if target.mode == unifiedInitModeSDK || target.mode == unifiedInitModeAPI {
		request.generate = target.mode == unifiedInitModeSDK
		request.generateSet = true
	}
	return request, nil
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
