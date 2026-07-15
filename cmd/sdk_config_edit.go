package cmd

import (
	"errors"
	"fmt"
	"os"

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
