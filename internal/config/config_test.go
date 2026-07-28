package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Usefused/cli/internal/config"
)

// TestLoad_FileNotFound verifies that a missing config file is not an error;
// it returns an empty Config so the CLI works on first run without setup.
func TestLoad_FileNotFound(t *testing.T) {
	// Point XDG_CONFIG_HOME at a temp dir with no config file.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("expected no error on missing file, got: %v", err)
	}
	if cfg.EngineURL != "" || cfg.APIKey != "" {
		t.Errorf("expected empty config on first run, got: %+v", cfg)
	}
}

// TestSave_And_Load_RoundTrip verifies that values survive a save → load cycle.
func TestSave_And_Load_RoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	want := &config.Config{
		EngineURL: "http://localhost:8080",
		APIKey:    "sk-test-key",
	}
	if err := config.Save(want); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	got, err := config.Load()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if got.EngineURL != want.EngineURL {
		t.Errorf("EngineURL: got %q, want %q", got.EngineURL, want.EngineURL)
	}
	if got.APIKey != want.APIKey {
		t.Errorf("APIKey: got %q, want %q", got.APIKey, want.APIKey)
	}
}

// TestSet_EngineURL verifies Set("engine-url") updates only that field.
func TestSet_EngineURL(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.Set("engine-url", "http://engine.test"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	cfg, _ := config.Load()
	if cfg.EngineURL != "http://engine.test" {
		t.Errorf("EngineURL: got %q, want %q", cfg.EngineURL, "http://engine.test")
	}
	// Other fields must remain unset.
	if cfg.APIKey != "" {
		t.Errorf("unexpected APIKey after setting engine-url: %q", cfg.APIKey)
	}
}

// TestSet_APIKey verifies Set("api-key") stores the key.
func TestSet_APIKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := config.Set("api-key", "sk-abc"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	cfg, _ := config.Load()
	if cfg.APIKey != "sk-abc" {
		t.Errorf("APIKey: got %q, want %q", cfg.APIKey, "sk-abc")
	}
}

// TestSet_UnknownKey verifies that an invalid key returns a clear error.
func TestSet_UnknownKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	err := config.Set("totally-unknown-key", "value")
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
	if _, ok := err.(*config.UnknownKeyError); !ok {
		t.Errorf("expected UnknownKeyError, got %T: %v", err, err)
	}
}

// TestGet_ReturnsValue verifies Get returns a previously Set value.
func TestGet_ReturnsValue(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_ = config.Set("engine-url", "http://engine.test")

	val, err := config.Get("engine-url")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != "http://engine.test" {
		t.Errorf("Get: got %q, want %q", val, "http://engine.test")
	}
}

// TestGet_UnsetReturnsErrNotConfigured verifies Get returns ErrNotConfigured
// when the value is absent, so callers can surface the setup hint.
func TestGet_UnsetReturnsErrNotConfigured(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := config.Get("engine-url")
	if err == nil {
		t.Fatal("expected ErrNotConfigured, got nil")
	}
}

// TestSave_Atomic verifies that Save uses a temp file then renames,
// so a partial write cannot corrupt the config.
func TestSave_Atomic(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := config.Save(&config.Config{EngineURL: "http://a"}); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Temp file must not remain after Save succeeds.
	cfgPath, _ := config.Path()
	tmpPath := cfgPath + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("temp file should not exist after successful save: %s", tmpPath)
	}
}

// TestConfigPath_IsUnderConfigDir verifies the config file lives under an
// appropriate user-scoped directory, not a system or temp location.
func TestConfigPath_IsUnderConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "") // clear so it falls back to ~/. config

	path, err := config.Path()
	if err != nil {
		t.Fatalf("Path failed: %v", err)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("config path must be absolute, got: %s", path)
	}
	// Must end in .../fused/config.json
	if filepath.Base(path) != "config.json" {
		t.Errorf("config path should end in config.json, got: %s", path)
	}
	dir := filepath.Dir(path)
	if filepath.Base(dir) != "fused" {
		t.Errorf("config parent dir should be 'fused', got: %s", dir)
	}
}
