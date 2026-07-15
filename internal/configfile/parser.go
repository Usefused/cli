package configfile

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadRun loads the config files required for a CLI run.
// If path is empty, it discovers from the .fused/ directory.
func LoadRun(path string) (*Run, error) {
	if path != "" {
		return loadSingleConfig(path)
	}
	return loadFusedDirectory()
}

func loadSingleConfig(path string) (*Run, error) {
	parsed, err := ParseFile(path)
	if err != nil {
		return nil, err
	}
	return &Run{Configs: []*ParsedConfig{parsed}}, nil
}

func loadFusedDirectory() (*Run, error) {
	fusedDir := ".fused"
	if err := ensureFusedDirectory(fusedDir); err != nil {
		return nil, err
	}
	run := &Run{}
	if err := appendWorkspaceConfig(run, fusedDir); err != nil {
		return nil, err
	}
	if err := appendSDKConfigs(run, filepath.Join(fusedDir, "sdks")); err != nil {
		return nil, err
	}
	return run, rejectDuplicateSDKNames(run.Configs)
}

func ensureFusedDirectory(fusedDir string) error {
	info, err := os.Stat(fusedDir)
	if os.IsNotExist(err) {
		return fmt.Errorf("no .fused directory found")
	}
	if err != nil {
		return fmt.Errorf("failed to access .fused directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf(".fused must be a directory")
	}
	return nil
}

func appendWorkspaceConfig(run *Run, fusedDir string) error {
	wsPath := filepath.Join(fusedDir, "workspace.yaml")
	if _, err := os.Stat(wsPath); err != nil {
		return nil
	}
	parsed, err := ParseFile(wsPath)
	if err != nil {
		return err
	}
	if parsed.Kind != KindWorkspace {
		return fmt.Errorf("expected workspace kind in %s", wsPath)
	}
	run.Configs = append(run.Configs, parsed)
	return nil
}

func appendSDKConfigs(run *Run, sdksDir string) error {
	if _, err := os.Stat(sdksDir); err != nil {
		return nil
	}
	return filepath.WalkDir(sdksDir, func(p string, d fs.DirEntry, err error) error {
		return appendSDKConfig(run, p, d, err)
	})
}

func appendSDKConfig(run *Run, p string, d fs.DirEntry, walkErr error) error {
	if walkErr != nil || d.IsDir() || !isYAMLFile(d.Name()) {
		return walkErr
	}
	parsed, err := ParseFile(p)
	if err != nil {
		return err
	}
	if parsed.Kind != KindSDK {
		return fmt.Errorf("expected sdk kind in %s, got %s", p, parsed.Kind)
	}
	run.Configs = append(run.Configs, parsed)
	return nil
}

func isYAMLFile(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

func rejectDuplicateSDKNames(configs []*ParsedConfig) error {
	seenNames := make(map[string]string)
	for _, cfg := range configs {
		if cfg.Kind == KindSDK {
			if existingPath, ok := seenNames[cfg.SDK.Name]; ok {
				return fmt.Errorf("duplicate sdk name %q found in %s and %s", cfg.SDK.Name, existingPath, cfg.Path)
			}
			seenNames[cfg.SDK.Name] = cfg.Path
		}
	}
	return nil
}

// ParseFile reads a file and parses it into the appropriate config struct based on its kind.
func ParseFile(path string) (*ParsedConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", path, err)
	}

	return Parse(data, path)
}

// Parse parses YAML data into the appropriate config struct.
func Parse(data []byte, sourcePath string) (*ParsedConfig, error) {
	var base BaseConfig
	if err := yaml.Unmarshal(data, &base); err != nil {
		return nil, fmt.Errorf("failed to parse base config: %w", err)
	}

	if base.Version != 1 {
		return nil, fmt.Errorf("unsupported config version: %d", base.Version)
	}

	hash := sha256.Sum256(data)
	parsed := &ParsedConfig{
		Kind:       base.Kind,
		Path:       sourcePath,
		SourceHash: fmt.Sprintf("sha256:%x", hash),
	}

	switch base.Kind {
	case KindWorkspace:
		var wsConfig WorkspaceConfig
		if err := yaml.Unmarshal(data, &wsConfig); err != nil {
			return nil, fmt.Errorf("failed to parse workspace config: %w", err)
		}
		parsed.Workspace = &wsConfig
		parsed.ConfigKey = "workspace"

	case KindSDK:
		var sdkConfig SDKConfig
		if err := yaml.Unmarshal(data, &sdkConfig); err != nil {
			return nil, fmt.Errorf("failed to parse sdk config: %w", err)
		}
		parsed.SDK = &sdkConfig
		parsed.ConfigKey = fmt.Sprintf("sdk:%s", sdkConfig.Name)

	default:
		return nil, fmt.Errorf("unknown config kind: %q", base.Kind)
	}

	if err := validateConfig(parsed); err != nil {
		return nil, err
	}

	return parsed, nil
}

// validateConfig performs basic semantic validation on the parsed config.
func validateConfig(parsed *ParsedConfig) error {
	switch parsed.Kind {
	case KindSDK:
		return validateSDKConfig(parsed.SDK)
	case KindWorkspace:
		return validateWorkspaceConfig(parsed.Workspace)
	}
	return nil
}

func validateSDKConfig(cfg *SDKConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("sdk config requires a name")
	}
	if cfg.SDKVersion == "" {
		return fmt.Errorf("sdk config requires an sdkVersion")
	}
	if err := validateSDKType(cfg.Language, cfg.Target); err != nil {
		return err
	}
	for svcName, svc := range cfg.Services {
		if err := validateSDKService(svcName, svc); err != nil {
			return err
		}
	}
	return nil
}

func validateSDKType(language, target string) error {
	if language != "typescript" && language != "go" && language != "python" {
		return fmt.Errorf("invalid language %q", language)
	}
	if target != "sdk" && target != "mcp" {
		return fmt.Errorf("invalid target %q", target)
	}
	return nil
}

func validateSDKService(name string, svc SDKService) error {
	if svc.Version == "" {
		return fmt.Errorf("sdk service %q requires a version", name)
	}
	if len(svc.LegacyEndpoints) > 0 {
		return fmt.Errorf("sdk service %q uses legacy endpoints; use operations instead", name)
	}
	if len(svc.Operations) == 0 {
		return fmt.Errorf("sdk service %q requires at least one operation", name)
	}
	return nil
}

func validateWorkspaceConfig(cfg *WorkspaceConfig) error {
	for _, deprecation := range cfg.Deprecations {
		if deprecation.ServiceID == "" || deprecation.EffectiveAt == "" {
			return fmt.Errorf("workspace deprecations require service_id and effective_at")
		}
	}
	return nil
}
