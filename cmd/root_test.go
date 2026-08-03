package cmd_test

import (
	"strings"
	"testing"

	"github.com/Usefused/cli/cmd"
	"github.com/Usefused/cli/internal/config"
)

func TestGetEngineURL_FromFlag(t *testing.T) {
	cmd.EngineURL = ""
	cmd.APIKey = ""
	t.Setenv("FUSED_ENGINE_URL", "http://env")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_ = config.Set("engine-url", "http://config")

	// Run a dummy command so cobra binds the flag
	cmd.RootCmd.SetArgs([]string{"--engine-url", "http://flag", "config", "list"})
	cmd.RootCmd.Execute()

	url, err := cmd.GetEngineURL()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://flag" {
		t.Errorf("expected http://flag, got %s", url)
	}
}

func TestGetEngineURL_FromEnv(t *testing.T) {
	cmd.EngineURL = ""
	cmd.APIKey = ""
	t.Setenv("FUSED_ENGINE_URL", "http://env")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_ = config.Set("engine-url", "http://config")

	// Flag is empty
	url, err := cmd.GetEngineURL()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://env" {
		t.Errorf("expected http://env, got %s", url)
	}
}

func TestGetEngineURL_FromConfig(t *testing.T) {
	cmd.EngineURL = ""
	cmd.APIKey = ""
	t.Setenv("FUSED_ENGINE_URL", "") // clear env
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_ = config.Set("engine-url", "http://config")

	url, err := cmd.GetEngineURL()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "http://config" {
		t.Errorf("expected http://config, got %s", url)
	}
}

func TestGetEngineURL_UnsetReturnsError(t *testing.T) {
	cmd.EngineURL = ""
	cmd.APIKey = ""
	t.Setenv("FUSED_ENGINE_URL", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // empty config

	_, err := cmd.GetEngineURL()
	if err == nil {
		t.Fatal("expected error when Engine URL is entirely unset")
	}
	// Error should contain the setup hint
	if !strings.Contains(err.Error(), "fused-cli config set engine-url") {
		t.Errorf("expected error to contain setup hint, got: %v", err)
	}
}

func TestGetAPIKey_ResolutionOrder(t *testing.T) {
	cmd.EngineURL = ""
	cmd.APIKey = ""
	// 1. Config fallback
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_ = config.Set("api-key", "test-config-key")
	t.Setenv("FUSED_API_KEY", "")
	t.Setenv("FUSED_LICENSE_KEY", "")

	if key := cmd.GetAPIKey(); key != "test-config-key" {
		t.Errorf("expected test-config-key, got %s", key)
	}

	// 2. The bootstrap Owner license overrides a persisted key.
	t.Setenv("FUSED_LICENSE_KEY", "test-license-key")
	if key := cmd.GetAPIKey(); key != "test-license-key" {
		t.Errorf("expected test-license-key, got %s", key)
	}

	// 3. A personal/service credential overrides the bootstrap license.
	t.Setenv("FUSED_API_KEY", "test-personal-key")
	if key := cmd.GetAPIKey(); key != "test-personal-key" {
		t.Errorf("expected test-personal-key, got %s", key)
	}

	// 4. An explicit flag overrides every environment source.
	cmd.RootCmd.SetArgs([]string{"--key", "test-flag-key", "config", "list"})
	cmd.RootCmd.Execute()
	if key := cmd.GetAPIKey(); key != "test-flag-key" {
		t.Errorf("expected test-flag-key, got %s", key)
	}
}
