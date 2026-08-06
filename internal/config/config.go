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
// Engine URLs resolve as CLI flag > env var > config file. Credentials resolve
// as CLI flag > saved config/login > API-key env > license env. This keeps
// per-run overrides possible while preserving an explicit user login.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Config holds the persisted CLI configuration.
// Fields map directly to `fused-cli config set <field-name> <value>`.
type Config struct {
	// EngineURL is the Fused Engine base URL. All CLI operations route here.
	// Set with: fused-cli config set engine-url http://localhost:8080
	EngineURL string `json:"engine_url,omitempty"`

	// APIKey is the saved Engine credential created by login or config set.
	// Set manually with: fused-cli config set api-key fsk_...
	APIKey string `json:"api_key,omitempty"`

	// APIKeyExpiresAt is set only for Engine-issued managed CLI credentials.
	// Static and bootstrap keys intentionally remain valid without this field.
	APIKeyExpiresAt string `json:"api_key_expires_at,omitempty"`

	// CredentialID and CredentialSource are server-issued login provenance.
	// They are intentionally not exposed through `config set`.
	CredentialID     string `json:"credential_id,omitempty"`
	CredentialSource string `json:"credential_source,omitempty"`
}

const ManagedCLILoginSource = "managed_cli_login"

// ErrNotConfigured is returned when a required value is absent from all
// resolution sources (flag, env, config file). Callers should surface the
// setup hint so users know how to fix it.
var ErrNotConfigured = errors.New("not configured")

// KnownKeys lists all settable config keys in the order shown by `config list`.
// This is the authoritative source of truth for key validation.
var KnownKeys = []string{"engine-url", "api-key", "api-key-expires-at"}

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
		// A manually supplied key must not inherit managed-login provenance from
		// the credential it replaces, or logout could revoke the wrong identity.
		cfg.APIKeyExpiresAt = ""
		cfg.CredentialID = ""
		cfg.CredentialSource = ""
	case "api-key-expires-at":
		cfg.APIKeyExpiresAt = value
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
	case "api-key-expires-at":
		return c.APIKeyExpiresAt, nil
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
	return "unknown config key: " + e.Key + "\nValid keys: engine-url, api-key, api-key-expires-at"
}

// SaveLogin writes all login state in one atomic rename so a crash cannot pair
// a new credential with an old Engine URL or omit its expiry metadata.
func SaveLogin(engineURL, apiKey, credentialID string, expiresAt time.Time) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	cfg.EngineURL = engineURL
	cfg.APIKey = apiKey
	cfg.APIKeyExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	cfg.CredentialID = credentialID
	cfg.CredentialSource = ManagedCLILoginSource
	return Save(cfg)
}

// ClearCredential removes only the locally saved credential state. The Engine
// URL remains because signing out should not make the selected Engine unknown.
func ClearCredential() (bool, error) {
	cfg, err := Load()
	if err != nil {
		return false, err
	}
	changed := cfg.APIKey != "" || cfg.APIKeyExpiresAt != ""
	if !changed {
		return false, nil
	}
	cfg.APIKey = ""
	cfg.APIKeyExpiresAt = ""
	cfg.CredentialID = ""
	cfg.CredentialSource = ""
	return true, Save(cfg)
}
