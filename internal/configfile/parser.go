package configfile

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadRun loads the config files required for a CLI run.
// If path is empty, it discovers from the .fused/ directory.
func LoadRun(path string) (*Run, error) {
	if path != "" {
		return loadSingleConfig(path)
	}
	return loadFusedDirectory()
}

func loadSingleConfig(path string) (*Run, error) {
	parsed, err := ParseFile(path)
	if err != nil {
		return nil, err
	}
	return &Run{Configs: []*ParsedConfig{parsed}}, nil
}

func loadFusedDirectory() (*Run, error) {
	fusedDir := ".fused"
	if err := ensureFusedDirectory(fusedDir); err != nil {
		return nil, err
	}
	run := &Run{}
	if err := appendWorkspaceConfig(run, fusedDir); err != nil {
		return nil, err
	}
	if err := appendSDKConfigs(run, filepath.Join(fusedDir, "sdks")); err != nil {
		return nil, err
	}
	return run, rejectDuplicateSDKNames(run.Configs)
}

func ensureFusedDirectory(fusedDir string) error {
	info, err := os.Stat(fusedDir)
	if os.IsNotExist(err) {
		return fmt.Errorf("no .fused directory found")
	}
	if err != nil {
		return fmt.Errorf("failed to access .fused directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf(".fused must be a directory")
	}
	return nil
}

func appendWorkspaceConfig(run *Run, fusedDir string) error {
	wsPath := filepath.Join(fusedDir, "workspace.yaml")
	if _, err := os.Stat(wsPath); err != nil {
		return nil
	}
	parsed, err := ParseFile(wsPath)
	if err != nil {
		return err
	}
	if parsed.Kind != KindWorkspace {
		return fmt.Errorf("expected workspace kind in %s", wsPath)
	}
	run.Configs = append(run.Configs, parsed)
	return nil
}

func appendSDKConfigs(run *Run, sdksDir string) error {
	if _, err := os.Stat(sdksDir); err != nil {
		return nil
	}
	return filepath.WalkDir(sdksDir, func(p string, d fs.DirEntry, err error) error {
		return appendSDKConfig(run, p, d, err)
	})
}

func appendSDKConfig(run *Run, p string, d fs.DirEntry, walkErr error) error {
	if walkErr != nil || d.IsDir() || !isYAMLFile(d.Name()) {
		return walkErr
	}
	parsed, err := ParseFile(p)
	if err != nil {
		return err
	}
	if parsed.Kind != KindSDK {
		return fmt.Errorf("expected sdk kind in %s, got %s", p, parsed.Kind)
	}
	run.Configs = append(run.Configs, parsed)
	return nil
}

func isYAMLFile(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

func rejectDuplicateSDKNames(configs []*ParsedConfig) error {
	seenNames := make(map[string]string)
	for _, cfg := range configs {
		if cfg.Kind == KindSDK {
			if existingPath, ok := seenNames[cfg.SDK.Name]; ok {
				return fmt.Errorf("duplicate sdk name %q found in %s and %s", cfg.SDK.Name, existingPath, cfg.Path)
			}
			seenNames[cfg.SDK.Name] = cfg.Path
		}
	}
	return nil
}

// ParseFile reads a file and parses it into the appropriate config struct based on its kind.
func ParseFile(path string) (*ParsedConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", path, err)
	}

	return Parse(data, path)
}

// Parse parses YAML data into the appropriate config struct.
func Parse(data []byte, sourcePath string) (*ParsedConfig, error) {
	var base BaseConfig
	if err := yaml.Unmarshal(data, &base); err != nil {
		return nil, fmt.Errorf("failed to parse base config: %w", err)
	}

	if base.Version != 1 {
		return nil, fmt.Errorf("unsupported config version: %d", base.Version)
	}

	hash := sha256.Sum256(data)
	parsed := &ParsedConfig{
		Kind:       base.Kind,
		Path:       sourcePath,
		SourceHash: fmt.Sprintf("sha256:%x", hash),
	}

	switch base.Kind {
	case KindWorkspace:
		var wsConfig WorkspaceConfig
		if err := yaml.Unmarshal(data, &wsConfig); err != nil {
			return nil, fmt.Errorf("failed to parse workspace config: %w", err)
		}
		parsed.Workspace = &wsConfig
		parsed.ConfigKey = "workspace"

	case KindSDK:
		var sdkConfig SDKConfig
		if err := yaml.Unmarshal(data, &sdkConfig); err != nil {
			return nil, fmt.Errorf("failed to parse sdk config: %w", err)
		}
		parsed.SDK = &sdkConfig
		parsed.ConfigKey = fmt.Sprintf("sdk:%s", sdkConfig.Name)

	default:
		return nil, fmt.Errorf("unknown config kind: %q", base.Kind)
	}

	if err := validateConfig(parsed); err != nil {
		return nil, err
	}

	return parsed, nil
}

// validateConfig performs basic semantic validation on the parsed config.
func validateConfig(parsed *ParsedConfig) error {
	switch parsed.Kind {
	case KindSDK:
		return validateSDKConfig(parsed.SDK)
	case KindWorkspace:
		return validateWorkspaceConfig(parsed.Workspace)
	}
	return nil
}

func validateSDKConfig(cfg *SDKConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("sdk config requires a name")
	}
	if cfg.SDKVersion == "" {
		return fmt.Errorf("sdk config requires an sdkVersion")
	}
	if err := validateSDKType(cfg.Language, cfg.Target); err != nil {
		return err
	}
	for svcName, svc := range cfg.Services {
		if err := validateSDKService(svcName, svc); err != nil {
			return err
		}
	}
	return nil
}

func validateSDKType(language, target string) error {
	if language != "typescript" && language != "go" && language != "python" {
		return fmt.Errorf("invalid language %q", language)
	}
	if target != "sdk" && target != "mcp" {
		return fmt.Errorf("invalid target %q", target)
	}
	return nil
}

func validateSDKService(name string, svc SDKService) error {
	if len(svc.LegacyEndpoints) > 0 {
		return fmt.Errorf("sdk service %q uses legacy endpoints; use operations instead", name)
	}
	if len(svc.Operations) == 0 {
		return fmt.Errorf("sdk service %q requires at least one operation", name)
	}
	return nil
}

func validateWorkspaceConfig(cfg *WorkspaceConfig) error {
	for name, svc := range cfg.Services {
		if err := validateWorkspaceService(name, svc); err != nil {
			return err
		}
	}
	for _, deprecation := range cfg.Deprecations {
		if deprecation.ServiceID == "" || deprecation.EffectiveAt == "" {
			return fmt.Errorf("workspace deprecations require service_id and effective_at")
		}
	}
	return nil
}

func validateWorkspaceService(name string, svc WorkspaceService) error {
	if svc.RuntimeConfig == nil {
		return nil
	}
	if err := validateWorkspaceAuthIntent(name, svc.RuntimeConfig.Auth); err != nil {
		return err
	}
	if svc.RuntimeConfig.Connect == nil {
		return nil
	}
	connect := svc.RuntimeConfig.Connect
	if strings.TrimSpace(connect.AuthType) == "" {
		return fmt.Errorf("workspace service %q connect requires auth_type", name)
	}
	if !isSupportedConnectAuthType(connect.AuthType) {
		return fmt.Errorf("workspace service %q connect has unsupported auth_type", name)
	}
	if strings.TrimSpace(connect.RedirectURI) == "" {
		return fmt.Errorf("workspace service %q connect requires redirect_uri", name)
	}
	return validateWorkspaceConnectClientMaterial(name, connect)
}

// validateWorkspaceAuthIntent keeps static auth material out of plan/state by
// requiring local env references for every credential field.
func validateWorkspaceAuthIntent(name string, auth *AuthConfig) error {
	if auth == nil {
		return nil
	}
	switch canonicalStaticAuthType(auth.AuthType) {
	case "basic":
		return validateAuthEnvRefs(name, "basic", auth.Username, auth.Password)
	case "api_key":
		return validateAuthEnvRefs(name, "api_key", auth.APIKey)
	case "mtls":
		return validateAuthEnvRefs(name, "mtls", auth.Cert, auth.Key)
	case "bearer", "oauth", "oidc":
		return validateAuthEnvRefs(name, canonicalStaticAuthType(auth.AuthType), auth.Token)
	default:
		return fmt.Errorf("workspace service %q auth has unsupported auth_type", name)
	}
}

// validateAuthEnvRefs treats static auth fields as secret material even when a
// provider calls one of them "username", keeping bucket writes apply-only.
func validateAuthEnvRefs(name, authType string, values ...string) error {
	for _, value := range values {
		if strings.TrimSpace(value) == "" || !isEnvRef(value) {
			// Static auth material is encrypted into Engine bucket secrets during
			// apply, so the shareable config must carry env refs rather than values.
			return fmt.Errorf("workspace service %q auth %s requires $ENV credential fields", name, authType)
		}
	}
	return nil
}

// validateWorkspaceConnectClientMaterial is isolated because this is the
// branch that decides whether config contains a safe env pointer or a secret.
func validateWorkspaceConnectClientMaterial(name string, connect *ConnectConfig) error {
	if strings.TrimSpace(connect.ClientIDEnv) != "" || strings.TrimSpace(connect.ClientSecretEnv) != "" {
		return fmt.Errorf("workspace service %q connect must use client_id/client_secret with $ENV references, not *_env fields", name)
	}
	if strings.TrimSpace(connect.ClientID) == "" {
		return fmt.Errorf("workspace service %q connect requires client_id", name)
	}
	if clientSecret := strings.TrimSpace(connect.ClientSecret); clientSecret != "" && !isEnvRef(clientSecret) {
		return fmt.Errorf("workspace service %q connect must use client_secret: $ENV, not inline client_secret", name)
	}
	if strings.TrimSpace(connect.ClientSecret) == "" {
		return fmt.Errorf("workspace service %q connect requires client_secret: $ENV", name)
	}
	return nil
}

// WorkspaceConnectMaterials returns apply-time OAuth/OIDC material keyed by the
// workspace service map key. Plan/state store env refs from the file; only apply
// sends resolved secrets so Engine can encrypt them into the bucket.
func (p *ParsedConfig) WorkspaceConnectMaterials() (map[string]ConnectMaterial, error) {
	if p.Workspace == nil {
		return nil, fmt.Errorf("parsed config is not a workspace")
	}
	materials := map[string]ConnectMaterial{}
	for key, svc := range p.Workspace.Services {
		material, ok, err := workspaceServiceConnectMaterial(key, svc)
		if err != nil {
			return nil, err
		}
		if ok {
			materials[key] = material
		}
	}
	return materials, nil
}

// WorkspaceAuthMaterials resolves static provider auth env refs only during
// apply so workspace plan/state files never contain plaintext bucket secrets.
func (p *ParsedConfig) WorkspaceAuthMaterials() (map[string]AuthMaterial, error) {
	if p.Workspace == nil {
		return nil, fmt.Errorf("parsed config is not a workspace")
	}
	materials := map[string]AuthMaterial{}
	for key, svc := range p.Workspace.Services {
		material, ok, err := workspaceServiceAuthMaterial(key, svc)
		if err != nil {
			return nil, err
		}
		if ok {
			materials[key] = material
		}
	}
	return materials, nil
}

func workspaceServiceAuthMaterial(name string, svc WorkspaceService) (AuthMaterial, bool, error) {
	if svc.RuntimeConfig == nil || svc.RuntimeConfig.Auth == nil {
		return AuthMaterial{}, false, nil
	}
	auth := *svc.RuntimeConfig.Auth
	if err := resolveAuthEnv(name, &auth); err != nil {
		return AuthMaterial{}, false, err
	}
	return AuthMaterial{Username: auth.Username, Password: auth.Password, Token: auth.Token, APIKey: auth.APIKey, Cert: auth.Cert, Key: auth.Key}, true, nil
}

// resolveAuthEnv resolves all possible fields; validation decides which fields
// are required for the selected auth type before this apply-time step.
func resolveAuthEnv(name string, auth *AuthConfig) error {
	if err := resolveAuthField(name, "username", &auth.Username); err != nil {
		return err
	}
	if err := resolveAuthField(name, "password", &auth.Password); err != nil {
		return err
	}
	if err := resolveAuthField(name, "token", &auth.Token); err != nil {
		return err
	}
	if err := resolveAuthField(name, "api_key", &auth.APIKey); err != nil {
		return err
	}
	if err := resolveAuthField(name, "cert", &auth.Cert); err != nil {
		return err
	}
	return resolveAuthField(name, "key", &auth.Key)
}

// resolveAuthField gives each env lookup a field-specific error so operators
// know exactly which local variable is missing.
func resolveAuthField(name, field string, value *string) error {
	resolved, err := resolveMaybeEnv(*value)
	if err != nil {
		return fmt.Errorf("workspace service %q auth %s: %w", name, field, err)
	}
	*value = resolved
	return nil
}

func workspaceServiceConnectMaterial(name string, svc WorkspaceService) (ConnectMaterial, bool, error) {
	if svc.RuntimeConfig == nil || svc.RuntimeConfig.Connect == nil {
		return ConnectMaterial{}, false, nil
	}
	connect := *svc.RuntimeConfig.Connect
	if err := resolveConnectEnv(name, &connect); err != nil {
		return ConnectMaterial{}, false, err
	}
	return ConnectMaterial{ClientID: connect.ClientID, ClientSecret: connect.ClientSecret}, true, nil
}

func resolveConnectEnv(name string, connect *ConnectConfig) error {
	clientID, err := resolveMaybeEnv(connect.ClientID)
	if err != nil {
		return fmt.Errorf("workspace service %q connect client_id: %w", name, err)
	}
	clientSecret, err := resolveMaybeEnv(connect.ClientSecret)
	if err != nil {
		return fmt.Errorf("workspace service %q connect client_secret: %w", name, err)
	}
	connect.ClientID = clientID
	connect.ClientSecret = clientSecret
	connect.ClientIDEnv = ""
	connect.ClientSecretEnv = ""
	return nil
}

func resolveMaybeEnv(value string) (string, error) {
	if value = strings.TrimSpace(value); value != "" {
		envRef := envRefName(value)
		if envRef == "" {
			return value, nil
		}
		// `$VAR` in config is a pointer to operator-local secret material; only
		// resolve it during apply so plan/state can remain shareable.
		return lookupRequiredEnv(envRef)
	}
	return "", nil
}

// canonicalStaticAuthType accepts only the public config vocabulary; provider
// import spellings are normalized inside the Engine, not in user config.
func canonicalStaticAuthType(authType string) string {
	normalized := strings.ToLower(strings.TrimSpace(authType))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case "api_key", "oauth", "oidc", "basic", "bearer", "mtls":
		return normalized
	default:
		return normalized
	}
}

// isSupportedConnectAuthType limits connect config to browser/user flows; API
// key, bearer, and basic credentials belong in bucket secrets instead.
func isSupportedConnectAuthType(authType string) bool {
	switch canonicalStaticAuthType(authType) {
	case "oauth", "oidc":
		return true
	default:
		return false
	}
}

// lookupRequiredEnv fails closed so an unset local secret cannot silently apply
// an empty OAuth client credential into Engine's encrypted connect config.
func lookupRequiredEnv(envName string) (string, error) {
	resolved := os.Getenv(envName)
	if strings.TrimSpace(resolved) == "" {
		return "", fmt.Errorf("%s is not set", envName)
	}
	return resolved, nil
}

// isEnvRef keeps validation aligned with apply-time resolution: client_secret
// may be present in config only when it is a local-env pointer, never a secret.
func isEnvRef(value string) bool {
	return envRefName(value) != ""
}

// envRefName accepts only whole-value env refs, not interpolation, so plan
// artifacts keep a clear "this value comes from local env" shape.
func envRefName(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
		return validEnvName(strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")))
	}
	if strings.HasPrefix(value, "$") {
		return validEnvName(strings.TrimPrefix(value, "$"))
	}
	return ""
}

// validEnvName mirrors shell-style env identifiers closely enough for config
// validation without expanding arbitrary shell syntax.
func validEnvName(name string) string {
	if name == "" {
		return ""
	}
	for i, r := range name {
		if validEnvNameRune(i, r) {
			continue
		}
		return ""
	}
	return name
}

// validEnvNameRune deliberately avoids shell expansion semantics; config may
// name an env var, but it should not execute or interpolate shell syntax.
func validEnvNameRune(index int, r rune) bool {
	return r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || index > 0 && r >= '0' && r <= '9'
}
