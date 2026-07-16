package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/Usefused/cli/internal/configfile"
)

func addWorkspaceService(path, serviceName, serviceID, version string) error {
	if path == "" {
		return errors.New("workspace config edit requires -f")
	}
	if serviceID == "" {
		if _, err := uuid.Parse(serviceName); err == nil {
			serviceID = serviceName
		}
	}
	cfg, err := loadWorkspaceConfigForEdit(path)
	if err != nil {
		return err
	}
	versions := []string(nil)
	if version != "" {
		versions = []string{version}
	}
	cfg.Services[serviceName] = configfile.WorkspaceService{
		ServiceID: serviceID,
		Versions:  versions,
	}
	return writeWorkspaceConfig(path, cfg)
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
	if !containsString(service.Versions, version) {
		service.Versions = append(service.Versions, version)
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
	service.Versions = removeString(service.Versions, version)
	cfg.Services[serviceName] = service
	return writeWorkspaceConfig(path, cfg)
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
			BaseConfig: configfile.BaseConfig{Kind: configfile.KindWorkspace, Version: 1},
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
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
