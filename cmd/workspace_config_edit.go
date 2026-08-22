package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/Usefused/cli/internal/configfile"
)

func addWorkspaceService(path, serviceName, serviceID, version string) error {
	if path == "" {
		return errors.New("workspace config edit requires -f")
	}
	cfg, err := loadWorkspaceConfigForEdit(path)
	if err != nil {
		return err
	}
	service := cfg.Services[serviceName]
	if serviceID != "" {
		if service.ServiceID != "" && service.ServiceID != serviceID {
			return fmt.Errorf("service %s already has service_id %s; refusing to replace it with %s", serviceName, service.ServiceID, serviceID)
		}
		service.ServiceID = serviceID
		cfg.Services[serviceName] = service
	}
	mergeWorkspaceServiceSelection(cfg, serviceName, version)
	return writeWorkspaceConfig(path, cfg)
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
