package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Usefused/cli/internal/configfile"
)

func addSDKService(path, serviceName, version string) error {
	if path == "" {
		return errors.New("sdk config edit requires -f")
	}
	if version == "" {
		return errors.New("sdk service add requires --version")
	}
	cfg, err := loadSDKConfigForEdit(path)
	if err != nil {
		return err
	}
	service := cfg.Services[serviceName]
	service.Version = version
	if service.Operations == nil {
		service.Operations = []string{}
	}
	cfg.Services[serviceName] = service
	return writeSDKConfig(path, cfg)
}

func removeSDKService(path, serviceName string) error {
	if path == "" {
		return errors.New("sdk config edit requires -f")
	}
	cfg, err := loadSDKConfigForEdit(path)
	if err != nil {
		return err
	}
	if _, ok := cfg.Services[serviceName]; !ok {
		return fmt.Errorf("service %s is not in this SDK config", serviceName)
	}
	delete(cfg.Services, serviceName)
	return writeSDKConfig(path, cfg)
}

func addSDKOperations(path, serviceName string, operations []string) error {
	if path == "" {
		return errors.New("sdk config edit requires -f")
	}
	cfg, err := loadSDKConfigForEdit(path)
	if err != nil {
		return err
	}
	service, ok := cfg.Services[serviceName]
	if !ok {
		return fmt.Errorf("service %s is not in this SDK config", serviceName)
	}
	for _, operation := range operations {
		if !containsString(service.Operations, operation) {
			service.Operations = append(service.Operations, operation)
		}
	}
	cfg.Services[serviceName] = service
	return writeSDKConfig(path, cfg)
}

func removeSDKOperations(path, serviceName string, operations []string) error {
	if path == "" {
		return errors.New("sdk config edit requires -f")
	}
	cfg, err := loadSDKConfigForEdit(path)
	if err != nil {
		return err
	}
	service, ok := cfg.Services[serviceName]
	if !ok {
		return fmt.Errorf("service %s is not in this SDK config", serviceName)
	}
	for _, operation := range operations {
		service.Operations = removeString(service.Operations, operation)
	}
	cfg.Services[serviceName] = service
	return writeSDKConfig(path, cfg)
}

func loadSDKConfigForEdit(path string) (*configfile.SDKConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseSDKConfig(path, data)
}

func loadSDKConfigForSync(path, sdkName string) (string, *configfile.SDKConfig, error) {
	target, err := sdkConfigSyncPath(path, sdkName)
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return target, &configfile.SDKConfig{
			BaseConfig: configfile.BaseConfig{Kind: configfile.KindSDK, Version: 1},
			Name:       sdkName,
			Language:   "typescript",
			Target:     "sdk",
			Services:   map[string]configfile.SDKService{},
		}, nil
	}
	if err != nil {
		return "", nil, err
	}
	cfg, err := parseSDKConfig(target, data)
	if err == nil && cfg.Name == "" {
		cfg.Name = sdkName
	}
	return target, cfg, err
}

func sdkConfigSyncPath(path, sdkName string) (string, error) {
	if path != "" {
		return path, nil
	}
	fileName := safeConfigFileName(sdkName)
	if fileName == "" {
		return "", errors.New("sdk sync requires an sdk name")
	}
	return filepath.Join(".fused", "sdks", fileName+".yaml"), nil
}

func safeConfigFileName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(value)
	return strings.Trim(value, ".")
}

func parseSDKConfig(path string, data []byte) (*configfile.SDKConfig, error) {
	var cfg configfile.SDKConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Kind != configfile.KindSDK {
		return nil, fmt.Errorf("expected sdk config in %s", path)
	}
	if cfg.Services == nil {
		cfg.Services = map[string]configfile.SDKService{}
	}
	return &cfg, nil
}

func writeSDKConfig(path string, cfg *configfile.SDKConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func containsString(items []string, wanted string) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}

func removeString(items []string, unwanted string) []string {
	out := items[:0]
	for _, item := range items {
		if item != unwanted {
			out = append(out, item)
		}
	}
	return out
}

func addSDKWebhooks(path, serviceName string, webhooks []string) error {
	if path == "" {
		return errors.New("sdk config edit requires -f")
	}
	cfg, err := loadSDKConfigForEdit(path)
	if err != nil {
		return err
	}
	service, ok := cfg.Services[serviceName]
	if !ok {
		return fmt.Errorf("service %s is not in this SDK config", serviceName)
	}
	if service.Webhooks == nil {
		service.Webhooks = []string{}
	}
	for _, webhook := range webhooks {
		if !containsString(service.Webhooks, webhook) {
			service.Webhooks = append(service.Webhooks, webhook)
		}
	}
	cfg.Services[serviceName] = service
	return writeSDKConfig(path, cfg)
}

func removeSDKWebhooks(path, serviceName string, webhooks []string) error {
	if path == "" {
		return errors.New("sdk config edit requires -f")
	}
	cfg, err := loadSDKConfigForEdit(path)
	if err != nil {
		return err
	}
	service, ok := cfg.Services[serviceName]
	if !ok {
		return fmt.Errorf("service %s is not in this SDK config", serviceName)
	}
	if service.Webhooks == nil {
		service.Webhooks = []string{}
	}
	for _, webhook := range webhooks {
		service.Webhooks = removeString(service.Webhooks, webhook)
	}
	cfg.Services[serviceName] = service
	return writeSDKConfig(path, cfg)
}
