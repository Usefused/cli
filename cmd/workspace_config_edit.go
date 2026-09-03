package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Usefused/cli/internal/configfile"
)

type workspaceServiceConfigAddition struct {
	serviceName       string
	expectedServiceID string
	identityKeys      []string
	persistServiceID  string
	version           string
}

// addWorkspaceServices authors every resolved addition in one atomic config
// write, preserving existing service policy and avoiding partial multi-add files.
func addWorkspaceServices(path string, additions []workspaceServiceConfigAddition) error {
	path = workspaceConfigEditPath(path)
	cfg, err := loadWorkspaceConfigForEdit(path)
	if err != nil {
		return err
	}
	if err := mergeWorkspaceServiceAdditions(cfg, additions); err != nil {
		return err
	}
	return writeWorkspaceConfig(path, cfg)
}

// mergeWorkspaceServiceAdditions applies the standalone command's identity checks and additive authoring rules to an in-memory draft.
func mergeWorkspaceServiceAdditions(cfg *configfile.WorkspaceConfig, additions []workspaceServiceConfigAddition) error {
	for _, addition := range additions {
		// Every requested and canonical key must agree before the in-memory draft
		// changes, preventing aliases from hiding a different persisted identity.
		if err := validateWorkspaceServiceConfigIdentity(cfg, addition); err != nil {
			return err
		}
		service := cfg.Services[addition.serviceName]
		// Only explicit identity additions persist an ID; discovered public
		// services retain canonical slug-based config-as-code behavior.
		if addition.persistServiceID != "" {
			service.ServiceID = addition.persistServiceID
			cfg.Services[addition.serviceName] = service
		}
		mergeWorkspaceServiceSelection(cfg, addition.serviceName, addition.version)
	}
	return nil
}

// validateWorkspaceServiceConfigIdentity ensures discovery cannot activate one
// Registry service while any requested YAML key remains pinned to another.
func validateWorkspaceServiceConfigIdentity(cfg *configfile.WorkspaceConfig, addition workspaceServiceConfigAddition) error {
	keys := addition.identityKeys
	// Direct internal callers still receive canonical-key protection even when
	// they do not carry discovery aliases.
	if len(keys) == 0 {
		keys = []string{addition.serviceName}
	}
	// Validate every alias before authoring the canonical resolved key.
	for _, key := range keys {
		service := cfg.Services[key]
		// An omitted ID remains slug-resolved intent, but an existing stable ID is
		// authoritative and cannot be silently repointed by discovery.
		if addition.expectedServiceID != "" && service.ServiceID != "" && service.ServiceID != addition.expectedServiceID {
			return fmt.Errorf("service %s already has service_id %s; refusing to resolve or activate it as %s", key, service.ServiceID, addition.expectedServiceID)
		}
	}
	return nil
}

// mergeWorkspaceServiceSelection is the one additive workspace-authoring rule
// shared by `workspace service add` and `workspace init --extend`. Reusing it
// prevents either command from erasing service policy or duplicating versions.
func mergeWorkspaceServiceSelection(config *configfile.WorkspaceConfig, serviceName, version string) bool {
	service, exists := config.Services[serviceName]
	changed := !exists
	if version != "" && !configWorkspaceServiceHasVersion(service, version) {
		service.Versions = append(service.Versions, configfile.WorkspaceServiceVersion{Version: version})
		changed = true
	}
	config.Services[serviceName] = service
	return changed
}

// removeWorkspaceService deletes one service while preserving the standard inferred workspace path.
func removeWorkspaceService(path, serviceName string) error {
	path = workspaceConfigEditPath(path)
	cfg, err := loadWorkspaceConfigForEdit(path)
	if err != nil {
		return err
	}
	delete(cfg.Services, serviceName)
	return writeWorkspaceConfig(path, cfg)
}

// addWorkspaceVersion adds one version without requiring callers to repeat the conventional workspace path.
func addWorkspaceVersion(path, serviceName, version string) error {
	path = workspaceConfigEditPath(path)
	cfg, err := loadWorkspaceConfigForEdit(path)
	if err != nil {
		return err
	}
	service, ok := cfg.Services[serviceName]
	if !ok {
		return fmt.Errorf("service %s is not in this workspace config", serviceName)
	}
	// Adding an already-enabled version is a no-op, not an error or a
	// duplicate entry -- `version add` is meant to be safely re-runnable.
	if !configWorkspaceServiceHasVersion(service, version) {
		service.Versions = append(service.Versions, configfile.WorkspaceServiceVersion{Version: version})
	}
	cfg.Services[serviceName] = service
	return writeWorkspaceConfig(path, cfg)
}

// removeWorkspaceVersion removes an exact enabled version and drops an empty service entry so Engine can plan its removal.
func removeWorkspaceVersion(path, serviceName, version string) error {
	path = workspaceConfigEditPath(path)
	cfg, err := loadWorkspaceConfigForEdit(path)
	if err != nil {
		return err
	}
	service, ok := cfg.Services[serviceName]
	if !ok {
		return fmt.Errorf("service %s is not in this workspace config", serviceName)
	}
	// Reject a stale or mistyped version before rewriting the user's workspace file.
	if !configWorkspaceServiceHasVersion(service, version) {
		return fmt.Errorf("version %s of service %s is not in this workspace config", version, serviceName)
	}
	service.Versions = removeWorkspaceServiceVersion(service.Versions, version)
	// A workspace service without a selected version is invalid, so removing its
	// final version is represented as removal of the service itself.
	if len(service.Versions) == 0 {
		delete(cfg.Services, serviceName)
	} else {
		cfg.Services[serviceName] = service
	}
	return writeWorkspaceConfig(path, cfg)
}

// workspaceServiceHasVersion checks by version identity only -- resolved ID
// or per-version overrides don't affect whether a version is already enabled.
func configWorkspaceServiceHasVersion(service configfile.WorkspaceService, version string) bool {
	for _, v := range service.Versions {
		// Match on the version string alone -- ServiceVersionID/overrides are
		// irrelevant to "is this version already enabled".
		if v.Version == version {
			return true
		}
	}
	return false
}

// removeWorkspaceServiceVersion drops the one matching entry (identity,
// resolved ID, overrides, and connection profiles together) rather than just
// the bare version string, since those now travel with it.
func removeWorkspaceServiceVersion(versions []configfile.WorkspaceServiceVersion, version string) []configfile.WorkspaceServiceVersion {
	// versions[:0:0] forces a fresh backing array (zero length, zero
	// capacity) instead of reusing the caller's slice in place, so a partial
	// write failure downstream can't leave the caller's original slice
	// mutated out from under it.
	out := versions[:0:0]
	for _, v := range versions {
		// Drop the one entry matching the version being removed; everything
		// else (identity, overrides, connection profiles) for every other
		// version carries over untouched.
		if v.Version == version {
			continue
		}
		out = append(out, v)
	}
	return out
}

// addWorkspaceDeprecation appends a lifecycle directive to the conventional or explicit workspace file.
func addWorkspaceDeprecation(path, serviceName, version, effectiveAt, reason string) error {
	path = workspaceConfigEditPath(path)
	cfg, err := loadWorkspaceConfigForEdit(path)
	if err != nil {
		return err
	}
	service, ok := cfg.Services[serviceName]
	if !ok {
		return fmt.Errorf("service %s is not in this workspace config", serviceName)
	}
	cfg.Deprecations = append(cfg.Deprecations, configfile.WorkspaceDeprecationDirective{
		ServiceID:   service.ServiceID,
		Version:     version,
		EffectiveAt: effectiveAt,
		Reason:      reason,
	})
	return writeWorkspaceConfig(path, cfg)
}

// workspaceConfigEditPath makes the scaffolded workspace file the default for standalone edit commands.
func workspaceConfigEditPath(path string) string {
	// An explicit path always wins; only blank input opts into project-local discovery.
	if strings.TrimSpace(path) != "" {
		return path
	}
	return filepath.Join(".fused", "workspace.yaml")
}

// loadWorkspaceConfigForEdit reads an existing workspace config without creating an implicit empty file.
func loadWorkspaceConfigForEdit(path string) (*configfile.WorkspaceConfig, error) {
	path = workspaceConfigEditPath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseWorkspaceConfig(path, data)
}

func loadWorkspaceConfigForSync(path string) (string, *configfile.WorkspaceConfig, error) {
	target := path
	if target == "" {
		target = filepath.Join(".fused", "workspace.yaml")
	}
	data, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return target, &configfile.WorkspaceConfig{
			BaseConfig: configfile.BaseConfig{APIVersion: configfile.APIVersionV1, Kind: configfile.KindWorkspace},
			Services:   map[string]configfile.WorkspaceService{},
		}, nil
	}
	if err != nil {
		return "", nil, err
	}
	cfg, err := parseWorkspaceConfig(target, data)
	return target, cfg, err
}

func parseWorkspaceConfig(path string, data []byte) (*configfile.WorkspaceConfig, error) {
	var cfg configfile.WorkspaceConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Kind != configfile.KindWorkspace {
		return nil, fmt.Errorf("expected workspace config in %s", path)
	}
	if cfg.Services == nil {
		cfg.Services = map[string]configfile.WorkspaceService{}
	}
	return &cfg, nil
}

func writeWorkspaceConfig(path string, cfg *configfile.WorkspaceConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return atomicWriteFile(path, data, 0644, func(candidate []byte) error {
		_, err := parseWorkspaceConfig(path, candidate)
		return err
	})
}
