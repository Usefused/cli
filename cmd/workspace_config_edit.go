package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
	// Workspace authoring always requires an explicit config destination so a
	// composite never writes into an inferred or unrelated config file.
	if path == "" {
		return errors.New("workspace config edit requires -f")
	}
	cfg, err := loadWorkspaceConfigForEdit(path)
	if err != nil {
		return err
	}
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
	return writeWorkspaceConfig(path, cfg)
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

func removeWorkspaceService(path, serviceName string) error {
	cfg, err := loadWorkspaceConfigForEdit(path)
	if err != nil {
		return err
	}
	delete(cfg.Services, serviceName)
	return writeWorkspaceConfig(path, cfg)
}

func addWorkspaceVersion(path, serviceName, version string) error {
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

func removeWorkspaceVersion(path, serviceName, version string) error {
	cfg, err := loadWorkspaceConfigForEdit(path)
	if err != nil {
		return err
	}
	service, ok := cfg.Services[serviceName]
	if !ok {
		return fmt.Errorf("service %s is not in this workspace config", serviceName)
	}
	service.Versions = removeWorkspaceServiceVersion(service.Versions, version)
	cfg.Services[serviceName] = service
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

func addWorkspaceDeprecation(path, serviceName, version, effectiveAt, reason string) error {
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

func loadWorkspaceConfigForEdit(path string) (*configfile.WorkspaceConfig, error) {
	if path == "" {
		return nil, errors.New("workspace config edit requires -f")
	}
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
