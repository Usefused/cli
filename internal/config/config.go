// Package config manages the Fused CLI configuration file.
//
// Config follows the GitHub CLI pattern: values are set once via
// `fused-cli config set <key> <value>` and automatically resolved
// by all commands thereafter. The file lives at:
//
//	$XDG_CONFIG_HOME/fused/config.json   (Linux/macOS with XDG set)
//	~/.config/fused/config.json          (macOS/Linux fallback)
//	%APPDATA%\fused\config.json          (Windows)
//
// Resolution order per value: CLI flag > env var > config file > error.
// This keeps per-run overrides possible while making setup a one-time step.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Config holds the persisted CLI configuration.
// Fields map directly to `fused-cli config set <field-name> <value>`.
type Config struct {
	// EngineURL is the Fused Engine base URL. All CLI operations route here.
	// Set with: fused-cli config set engine-url http://localhost:8080
	EngineURL string `json:"engine_url,omitempty"`

	// APIKey is the Fused API key (FUSED_API_KEY).
	// Set with: fused-cli config set api-key sk-...
	APIKey string `json:"api_key,omitempty"`
}

// ErrNotConfigured is returned when a required value is absent from all
// resolution sources (flag, env, config file). Callers should surface the
// setup hint so users know how to fix it.
var ErrNotConfigured = errors.New("not configured")

// KnownKeys lists all settable config keys in the order shown by `config list`.
// This is the authoritative source of truth for key validation.
var KnownKeys = []string{"engine-url", "api-key"}

// Path returns the OS-appropriate path to the Fused config file.
// It honours $XDG_CONFIG_HOME, falling back to ~/.config on Unix.
func Path() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "fused", "config.json"), nil
}

// Load reads the config file. If the file does not exist it returns an empty
// Config without error — absence of the file is not an error (first run).
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return &Config{}, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// First run: no config file yet, return empty config.
		return &Config{}, nil
	}
	if err != nil {
		return &Config{}, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &Config{}, err
	}
	return &cfg, nil
}

// Save writes cfg atomically to the config file, creating the directory if needed.
func Save(cfg *Config) error {
	path, err := Path()
	if err != nil {
		return err
	}

	// Ensure parent directory exists before writing.
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	// Write to a temp file then rename for atomicity — avoids partial writes
	// corrupting the config on a crash or SIGKILL.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Set updates a single key in the config file.
// Returns an error if the key is unrecognised.
func Set(key, value string) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	switch key {
	case "engine-url":
		cfg.EngineURL = value
	case "api-key":
		cfg.APIKey = value
	default:
		return unknownKeyError(key)
	}
	return Save(cfg)
}

// Get returns a single config value by key.
// Returns ErrNotConfigured if the key is set but empty, or unknown key error.
func Get(key string) (string, error) {
	cfg, err := Load()
	if err != nil {
		return "", err
	}
	val, err := cfg.get(key)
	if err != nil {
		return "", err
	}
	if val == "" {
		return "", ErrNotConfigured
	}
	return val, nil
}

func (c *Config) get(key string) (string, error) {
	switch key {
	case "engine-url":
		return c.EngineURL, nil
	case "api-key":
		return c.APIKey, nil
	default:
		return "", unknownKeyError(key)
	}
}

// unknownKeyError formats a clear error listing valid keys.
func unknownKeyError(key string) error {
	return &UnknownKeyError{Key: key}
}

// UnknownKeyError is returned when an unrecognised config key is used.
type UnknownKeyError struct{ Key string }

func (e *UnknownKeyError) Error() string {
	return "unknown config key: " + e.Key + "\nValid keys: engine-url, api-key"
}
