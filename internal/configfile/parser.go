package configfile

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	if err := appendDesiredConfigs(run, filepath.Join(fusedDir, "sdks"), KindSDK); err != nil {
		return nil, err
	}
	if err := appendDesiredConfigs(run, filepath.Join(fusedDir, "mcps"), KindMCP); err != nil {
		return nil, err
	}
	if err := appendDesiredConfigs(run, filepath.Join(fusedDir, "webhooks"), KindWebhook); err != nil {
		return nil, err
	}
	return run, rejectDuplicateConfigIdentities(run.Configs)
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

// appendDesiredConfigs discovers one kind-specific directory without letting
// a misplaced document silently run through a different command surface.
func appendDesiredConfigs(run *Run, configDir string, expectedKind ConfigKind) error {
	if _, err := os.Stat(configDir); err != nil {
		return nil
	}
	return filepath.WalkDir(configDir, func(p string, d fs.DirEntry, err error) error {
		return appendDesiredConfig(run, p, d, err, expectedKind)
	})
}

func appendDesiredConfig(run *Run, p string, d fs.DirEntry, walkErr error, expectedKind ConfigKind) error {
	if walkErr != nil || d.IsDir() || !isYAMLFile(d.Name()) {
		return walkErr
	}
	parsed, err := ParseFile(p)
	if err != nil {
		return err
	}
	if parsed.Kind != expectedKind {
		return fmt.Errorf("expected %s kind in %s, got %s", expectedKind, p, parsed.Kind)
	}
	run.Configs = append(run.Configs, parsed)
	return nil
}

func isYAMLFile(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

// rejectDuplicateConfigIdentities allows independently versioned apps while
// ensuring one desired-state file owns each immutable app or webhook identity.
func rejectDuplicateConfigIdentities(configs []*ParsedConfig) error {
	seenKeys := make(map[string]string)
	for _, cfg := range configs {
		if cfg.Kind != KindSDK && cfg.Kind != KindMCP && cfg.Kind != KindWebhook {
			continue
		}
		if existingPath, ok := seenKeys[cfg.ConfigKey]; ok {
			return fmt.Errorf("duplicate config identity %q found in %s and %s", cfg.ConfigKey, existingPath, cfg.Path)
		}
		seenKeys[cfg.ConfigKey] = cfg.Path
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

	if base.APIVersion == "" {
		return nil, fmt.Errorf("config apiVersion is required; use %q", APIVersionV1)
	}
	if base.APIVersion != APIVersionV1 {
		return nil, fmt.Errorf("unsupported config apiVersion %q; use %q", base.APIVersion, APIVersionV1)
	}

	hash := sha256.Sum256(data)
	parsed := &ParsedConfig{
		Kind:       base.Kind,
		Path:       sourcePath,
		SourceHash: fmt.Sprintf("sha256:%x", hash),
	}

	if err := parseTypedConfig(data, base.Kind, parsed); err != nil {
		return nil, err
	}

	if err := validateConfig(parsed); err != nil {
		return nil, err
	}

	return parsed, nil
}

// parseTypedConfig keeps kind-specific decoding explicit so retired fields
// cannot leak between workspace, SDK, and MCP documents.
func parseTypedConfig(data []byte, kind ConfigKind, parsed *ParsedConfig) error {
	switch kind {
	case KindWorkspace:
		var wsConfig WorkspaceConfig
		if err := strictUnmarshal(data, &wsConfig); err != nil {
			return fmt.Errorf("failed to parse workspace config: %w", err)
		}
		parsed.Workspace = &wsConfig
		parsed.ConfigKey = "workspace"
	case KindSDK:
		// App config keys include version so plan/apply receipts cannot cross
		// immutable releases that share a human-readable family name.
		var sdkConfig SDKConfig
		if err := strictUnmarshal(data, &sdkConfig); err != nil {
			return fmt.Errorf("failed to parse sdk config: %w", err)
		}
		parsed.SDK = &sdkConfig
		parsed.ConfigKey = appConfigKey(KindSDK, sdkConfig.Name, sdkConfig.Version)
	case KindMCP:
		// MCP and SDK share the app shape but keep distinct key prefixes so
		// Engine can route each desired state to its own executor without legacy targets.
		var mcpConfig MCPConfig
		if err := strictUnmarshal(data, &mcpConfig); err != nil {
			return fmt.Errorf("failed to parse mcp config: %w", err)
		}
		parsed.MCP = &mcpConfig
		parsed.ConfigKey = appConfigKey(KindMCP, mcpConfig.Name, mcpConfig.Version)
	case KindWebhook:
		// kind: webhook has no version -- unlike SDK/MCP it's not an
		// immutable release, it's a continuously-reconciled registration
		// bundle (like kind: workspace), so its config key is just its name.
		var webhookConfig WebhookConfig
		if err := strictUnmarshal(data, &webhookConfig); err != nil {
			return fmt.Errorf("failed to parse webhook config: %w", err)
		}
		parsed.Webhook = &webhookConfig
		parsed.ConfigKey = fmt.Sprintf("webhook:%s", webhookConfig.Name)
	default:
		return fmt.Errorf("unknown config kind: %q", kind)
	}
	return nil
}

// validateConfig performs basic semantic validation on the parsed config.
func validateConfig(parsed *ParsedConfig) error {
	switch parsed.Kind {
	case KindSDK:
		return validateSDKConfig(parsed.SDK)
	case KindMCP:
		return validateMCPConfig(parsed.MCP)
	case KindWorkspace:
		return validateWorkspaceConfig(parsed.Workspace)
	case KindWebhook:
		return validateWebhookConfig(parsed.Webhook)
	}
	return nil
}

// validateWebhookConfig applies the same secret-ref grammar check
// validateWorkspaceRuntimeConfig already uses for the (now deprecated)
// RuntimeConfig.Webhooks path -- kind: webhook's Secret field is the exact
// same "${bucket.<name>.secret.<key>}" reference, just relocated.
func validateWebhookConfig(cfg *WebhookConfig) error {
	if strings.TrimSpace(cfg.Name) == "" {
		return fmt.Errorf("webhook config requires a name")
	}
	if len(cfg.Services) == 0 {
		return fmt.Errorf("webhook config %q requires at least one service", cfg.Name)
	}
	for svcName, svc := range cfg.Services {
		if strings.TrimSpace(svc.Secret) == "" {
			continue
		}
		if !isBucketSecretRef(svc.Secret) {
			return fmt.Errorf(`webhook config %q service %q secret must be "${bucket.<name>.secret.<key>}" or "${bucket.secret.<key>}"`, cfg.Name, svcName)
		}
	}
	return nil
}

func validateSDKConfig(cfg *SDKConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("sdk config requires a name")
	}
	if err := validateAppConfig(cfg, KindSDK); err != nil {
		return err
	}
	return nil
}

// validateMCPConfig reuses the app rules while preserving MCP-specific
// language and webhook restrictions.
func validateMCPConfig(cfg *MCPConfig) error { return validateAppConfig(cfg, KindMCP) }

// validateAppConfig centralizes shared identity, selection, and auth
// policy checks so SDK and MCP files cannot drift.
func validateAppConfig(cfg *AppConfig, kind ConfigKind) error {
	if cfg.Name == "" {
		return fmt.Errorf("%s config requires a name", kind)
	}
	if !appVersionPattern.MatchString(cfg.Version) {
		return fmt.Errorf("%s config requires a SemVer-compatible version", kind)
	}
	if kind == KindSDK && !isSDKLanguage(cfg.Language) {
		return fmt.Errorf("invalid language %q", cfg.Language)
	}
	if kind == KindMCP && strings.TrimSpace(cfg.Language) != "" {
		return fmt.Errorf("mcp config must not set language")
	}
	// generate: only describes whether a downloadable package is built, which
	// has no meaning for an Engine-hosted MCP server. AppConfig is shared
	// between the two kinds, so reject it here rather than letting it decode
	// silently into a field nothing reads.
	if kind == KindMCP && cfg.Generate != nil {
		return fmt.Errorf("mcp config must not set generate")
	}
	if len(cfg.Services) == 0 {
		return fmt.Errorf("%s config requires at least one service", kind)
	}
	if err := validateAppServices(cfg.Services, kind, cfg.WebhookAttachment); err != nil {
		return err
	}
	return validateUnifiedOperations(cfg, kind)
}

// validateAppServices keeps per-service rules separate from app
// identity rules because MCP and SDK share services but diverge on webhooks.
//
// The existing "mcp service cannot select webhooks" restriction predates
// webhook_attachment/kind: webhook and its rationale isn't recorded here --
// preserved as-is rather than assumed lifted, since MCP's generated surface
// (Engine-hosted tools, not callback-based typed SDK methods) may not have
// an equivalent place to deliver webhook events at all. Confirm with the
// original author's intent before allowing kind: mcp to set
// webhook_attachment or per-service Webhooks/WebhooksSelectAll.
func validateAppServices(services map[string]SDKService, kind ConfigKind, webhookAttachment string) error {
	for svcName, svc := range services {
		if err := validateSDKService(svcName, svc); err != nil {
			return err
		}
		if kind == KindMCP && (len(svc.Webhooks) > 0 || svc.WebhooksSelectAll) {
			return fmt.Errorf("mcp service %q cannot select webhooks", svcName)
		}
		if (len(svc.Webhooks) > 0 || svc.WebhooksSelectAll) && strings.TrimSpace(webhookAttachment) == "" {
			return fmt.Errorf("%s service %q selects webhook events but the app has no webhook_attachment", kind, svcName)
		}
	}
	return nil
}

var appVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// isSDKLanguage limits package generation to emitters the Registry owns.
func isSDKLanguage(language string) bool {
	return language == "typescript" || language == "go" || language == "python"
}

// appConfigKey includes version because app releases are immutable
// desired-state identities rather than revisions of one mutable row.
func appConfigKey(kind ConfigKind, name, version string) string {
	return fmt.Sprintf("%s:%s:%s", kind, name, version)
}

// strictUnmarshal makes misspelled config fields actionable instead of
// silently dropping security or selection policy.
func strictUnmarshal(data []byte, target any) error {
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	return decoder.Decode(target)
}

func validateSDKService(name string, svc SDKService) error {
	if len(svc.Operations) == 0 && !svc.SelectAll {
		return fmt.Errorf("sdk service %q requires at least one operation", name)
	}
	if svc.Auth != nil && strings.TrimSpace(svc.Auth.Type) == "" {
		return fmt.Errorf("sdk service %q auth requires type", name)
	}
	if svc.Auth != nil && !isAppAuthType(svc.Auth.Type) {
		return fmt.Errorf("sdk service %q auth type must be one of basic, bearer, api_key, oauth, oidc, or mtls", name)
	}
	return nil
}

// isAppAuthType mirrors Engine validation using the public Fused auth
// vocabulary rather than raw OpenAPI scheme shapes.
func isAppAuthType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "basic", "bearer", "api_key", "oauth", "oidc", "mtls":
		return true
	default:
		return false
	}
}

func validateWorkspaceConfig(cfg *WorkspaceConfig) error {
	for name, svc := range cfg.Services {
		if err := validateWorkspaceService(name, svc); err != nil {
			return err
		}
	}
	for bucketName, bucket := range cfg.Buckets {
		if err := validateWorkspaceBucket(bucketName, bucket, cfg.Services); err != nil {
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
	// Validate the service-level default policy first, independent of any
	// per-version entries below -- it's legal for a service to set only this
	// and have every version inherit it with no override of its own.
	if err := validateWorkspaceExecutionPolicy(name, "", svc.ExecutionPolicy); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, version := range svc.Versions {
		v := strings.TrimSpace(version.Version)
		if v == "" {
			// An entry with no version string can't be matched against
			// anything (not Registry identity, not a later add/remove), so
			// reject it here rather than letting it silently no-op downstream.
			return fmt.Errorf("workspace service %q versions entry requires a version", name)
		}
		if seen[v] {
			// Two entries for the same version would make "which override
			// applies" ambiguous -- nesting means there's no longer a
			// separate list where this could accidentally happen per field,
			// so catch it once here for the whole entry.
			return fmt.Errorf("workspace service %q has duplicate version %s", name, v)
		}
		seen[v] = true
		// A version's own ExecutionPolicy is optional -- only validate it when
		// actually set, since nil just means "inherit the service default".
		if version.ExecutionPolicy != nil {
			if err := validateWorkspaceExecutionPolicy(name, v, version.ExecutionPolicy); err != nil {
				return err
			}
		}
		if err := validateWorkspaceConnectionProfiles(name, version.ConnectionProfiles); err != nil {
			return err
		}
	}
	return nil
}

// workspaceVersionNames projects a service's nested Versions into the bare
// string list callers that only care about identity (not overrides/profiles)
// still need, e.g. deprecation/version-existence checks.
func workspaceVersionNames(versions []WorkspaceServiceVersion) []string {
	names := make([]string, 0, len(versions))
	for _, version := range versions {
		names = append(names, version.Version)
	}
	return names
}

// isBucketSecretRef matches exactly the forms internal/shared/secretref
// accepts on the Engine: an explicit "${bucket.<name>.secret.<key>}" or the
// default-bucket shorthand "${bucket.secret.<key>}" (the CLI never needs to
// author the "env" counterpart here -- a webhook secret ref is always meant
// to be a secret -- but syntax validation still accepts it so a config that
// is merely the wrong kind fails at apply time with Engine's clearer error
// instead of a generic CLI parse rejection). This module can't import Engine
// internals directly (separate Go module), so it's kept in sync by hand --
// see secretref.Parse/SingleTag for the source of truth.
func isBucketSecretRef(value string) bool {
	inner, ok := bucketRefTagContents(value)
	if !ok {
		return false
	}
	return isBucketRefPath(inner)
}

// bucketRefTagContents accepts value only when it is exactly one ${...} tag,
// matching secretref.SingleTag's whole-value-only rule.
func bucketRefTagContents(value string) (string, bool) {
	if !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") {
		return "", false
	}
	inner := value[2 : len(value)-1]
	return inner, inner != ""
}

// isBucketRefPath classifies the already-extracted tag contents.
func isBucketRefPath(path string) bool {
	parts := strings.Split(path, ".")
	switch {
	case len(parts) == 4 && parts[0] == "bucket" && isBucketRefKindWord(parts[2]):
		return parts[1] != "" && parts[3] != ""
	case len(parts) == 3 && parts[0] == "bucket" && isBucketRefKindWord(parts[1]):
		return parts[2] != ""
	default:
		return false
	}
}

// isBucketRefKindWord accepts the same two kind words secretref.Kind does.
func isBucketRefKindWord(word string) bool {
	return word == "env" || word == "secret"
}

func validateWorkspaceExecutionPolicy(name, version string, policy *ExecutionPolicy) error {
	if policy == nil {
		return nil
	}
	retry, err := workspaceExecutionPolicyRetry(policy)
	if err != nil {
		return workspaceExecutionPolicyValidationError(name, version, err.Error())
	}
	if err := validateWorkspaceExecutionPolicyTimeout(policy.TimeoutMs); err != nil {
		return workspaceExecutionPolicyValidationError(name, version, err.Error())
	}
	if err := validateWorkspaceServerVariables(policy.ServerVariables); err != nil {
		return workspaceExecutionPolicyValidationError(name, version, err.Error())
	}
	if policy.Reset {
		if err := validateWorkspaceExecutionPolicyReset(policy, retry); err != nil {
			return workspaceExecutionPolicyValidationError(name, version, err.Error())
		}
		return nil
	}
	// rate_limit/retry are no longer individually mandatory -- base_url,
	// pagination, event_extraction_path, and incoming_webhook_config are all
	// legitimate to set on their own (workspaceExecutionPolicyFromRemote/
	// workspaceVersionExecutionPolicyFromRemote in cmd/workspace_sync.go
	// already round-trip pagination-only or base_url-only policies from the
	// Registry, so requiring rate_limit/retry here rejected the CLI's own
	// sync output). Still reject a genuinely empty block -- one with none of
	// the configurable fields set -- since that's never a meaningful policy.
	if workspaceExecutionPolicyEmpty(policy, retry) {
		return workspaceExecutionPolicyValidationError(name, version,
			"requires at least one of rate_limit, retry, timeout_ms, pagination, base_url, server_variables, event_extraction_path, or incoming_webhook_config")
	}
	return nil
}

func workspaceExecutionPolicyEmpty(policy *ExecutionPolicy, retry *RetryConfig) bool {
	// A small presence scan keeps validation maintainable as independent policy
	// fields are added without turning the caller into a high-complexity guard.
	present := []bool{
		policy.RateLimit != nil, retry != nil, policy.TimeoutMs != nil, policy.Pagination != nil,
		policy.BaseURL != nil, len(policy.ServerVariables) > 0, policy.EventExtractionPath != nil,
		policy.IncomingWebhookConfig != nil,
	}
	for _, configured := range present {
		if configured {
			return false
		}
	}
	return true
}

var (
	workspaceServerVariableNamePattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)
	workspaceServerVariableValuePattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,512}$`)
)

func validateWorkspaceServerVariables(variables map[string]string) error {
	if len(variables) > 128 {
		return fmt.Errorf("server_variables may contain at most 128 entries")
	}
	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !workspaceServerVariableNamePattern.MatchString(name) {
			return fmt.Errorf("server_variables name %q is invalid", name)
		}
		if !workspaceServerVariableValuePattern.MatchString(variables[name]) {
			return fmt.Errorf("server_variables value for %q is invalid", name)
		}
	}
	return nil
}

func workspaceExecutionPolicyRetry(policy *ExecutionPolicy) (*RetryConfig, error) {
	if policy.Retry != nil && policy.RetryConfig != nil {
		return nil, fmt.Errorf("retry and retry_config cannot both be set")
	}
	if policy.Retry != nil {
		return policy.Retry, nil
	}
	return policy.RetryConfig, nil
}

func validateWorkspaceExecutionPolicyReset(policy *ExecutionPolicy, retry *RetryConfig) error {
	if policy.Public != nil || !workspaceExecutionPolicyEmpty(policy, retry) {
		return fmt.Errorf("reset cannot include other execution policy fields")
	}
	return nil
}

const maxWorkspaceExecutionTimeoutMs = 24 * 60 * 60 * 1000

func validateWorkspaceExecutionPolicyTimeout(timeoutMs *int) error {
	if timeoutMs == nil || (*timeoutMs >= 1 && *timeoutMs <= maxWorkspaceExecutionTimeoutMs) {
		return nil
	}
	return fmt.Errorf("timeout_ms must be between 1 and %d", maxWorkspaceExecutionTimeoutMs)
}

func workspaceExecutionPolicyValidationError(name, version, message string) error {
	if strings.TrimSpace(version) == "" {
		return fmt.Errorf("workspace service %q execution_policy %s", name, message)
	}
	return fmt.Errorf("workspace service %q versions entry %s execution_policy %s", name, version, message)
}

// validateWorkspaceConnectionProfiles catches semantic contradictions the raw
// CLI profile shape cannot express in types while leaving full schema/contract
// validation to the Engine.
func validateWorkspaceConnectionProfiles(name string, profiles []map[string]interface{}) error {
	for _, profile := range profiles {
		if err := validateWorkspaceOAuth2Flow(name, profile); err != nil {
			return err
		}
		reset, _ := profile["reset"].(bool)
		if !reset {
			continue
		}
		profileID, _ := profile["profile_id"].(string)
		if profile["profile"] != nil || strings.TrimSpace(profileID) != "" {
			return fmt.Errorf("workspace service %q connection_profiles reset cannot include profile or profile_id", name)
		}
	}
	return nil
}

func validateWorkspaceOAuth2Flow(name string, entry map[string]interface{}) error {
	profile, _ := entry["profile"].(map[string]interface{})
	if profile == nil || profile["oauth2_flow"] == nil {
		return nil
	}
	flow, ok := profile["oauth2_flow"].(string)
	if !ok || strings.TrimSpace(flow) == "" {
		return fmt.Errorf("workspace service %q connection profile oauth2_flow must be a string", name)
	}
	authType, _ := entry["auth_type"].(string)
	if authType != "oauth" && authType != "oauth2" {
		return fmt.Errorf("workspace service %q connection profile oauth2_flow requires auth_type oauth", name)
	}
	if !isOAuth2FlowName(flow) {
		return fmt.Errorf("workspace service %q connection profile oauth2_flow is not supported", name)
	}
	return nil
}

func isOAuth2FlowName(flow string) bool {
	switch strings.TrimSpace(flow) {
	case "implicit", "password", "clientCredentials", "authorizationCode":
		return true
	default:
		return false
	}
}

// validateWorkspaceBucket keeps bucket material scoped to services declared in
// the same file, preventing credentials from silently targeting an unapproved
// service key.
func validateWorkspaceBucket(bucketName string, bucket WorkspaceBucket, services map[string]WorkspaceService) error {
	if strings.TrimSpace(bucketName) == "" {
		return fmt.Errorf("workspace bucket name is required")
	}
	for serviceName, serviceConfig := range bucket.ServiceConfig {
		if _, ok := services[serviceName]; !ok {
			return fmt.Errorf("workspace bucket %q references unknown service %q", bucketName, serviceName)
		}
		if err := validateWorkspaceAuthIntent(serviceName, serviceConfig.Auth); err != nil {
			return err
		}
	}
	return validateWorkspaceBucketSecrets(bucketName, bucket.Secrets)
}

// validateWorkspaceBucketSecrets enforces the same $ENV-only discipline as
// Auth/Connect fields on the newer, generic bucket.<name>.secrets.<key>
// declarative field -- static values are secret material and must never be
// committed to workspace.yaml.
func validateWorkspaceBucketSecrets(bucketName string, secrets map[string]string) error {
	for key, value := range secrets {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("workspace bucket %q has a secret with an empty key name", bucketName)
		}
		if strings.TrimSpace(value) == "" || !isEnvRef(value) {
			return fmt.Errorf("workspace bucket %q secret %q requires a $ENV reference", bucketName, key)
		}
	}
	return nil
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

// WorkspaceProfileMaterials resolves only profile binding env refs -- a
// connection profile is service-level routing policy (keyed by service, not
// by bucket), which is why it uses its own resolution pass rather than
// riding along with anything bucket-scoped. Reuses ConnectMaterial purely
// for its BindingValues field; ClientID/ClientSecret are never populated
// here (see ConnectMaterial's doc comment).
func (p *ParsedConfig) WorkspaceProfileMaterials() (map[string]ConnectMaterial, error) {
	if p.Workspace == nil {
		return nil, fmt.Errorf("parsed config is not a workspace")
	}
	materials := map[string]ConnectMaterial{}
	for key, svc := range p.Workspace.Services {
		material, ok, err := workspaceServiceProfileMaterial(key, svc)
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
	for bucketName, bucket := range p.Workspace.Buckets {
		for key, serviceConfig := range bucket.ServiceConfig {
			material, ok, err := workspaceServiceAuthMaterial(key, serviceConfig)
			if err != nil {
				return nil, err
			}
			if ok {
				materials[workspaceBucketMaterialKey(bucketName, key)] = material
			}
		}
	}
	return materials, nil
}

// WorkspaceBucketSecretMaterials resolves buckets.<name>.secrets.<key> $ENV
// refs only during apply, mirroring WorkspaceAuthMaterials -- generic bucket
// secrets (plans/plan-service-config-restructure.md item 4) get the same
// out-of-band resolution treatment as bucket-owned Auth/Connect material, so
// plan/state files never carry a resolved secret value.
func (p *ParsedConfig) WorkspaceBucketSecretMaterials() (map[string]string, error) {
	if p.Workspace == nil {
		return nil, fmt.Errorf("parsed config is not a workspace")
	}
	materials := map[string]string{}
	for bucketName, bucket := range p.Workspace.Buckets {
		for key, ref := range bucket.Secrets {
			value, err := resolveMaybeEnv(ref)
			if err != nil {
				return nil, fmt.Errorf("workspace bucket %q secret %q: %w", workspaceConnectBucketName(bucketName), key, err)
			}
			materials[workspaceBucketMaterialKey(bucketName, key)] = value
		}
	}
	return materials, nil
}

// workspaceServiceAuthMaterial resolves only bucket-scoped static auth so one
// service's secrets cannot be accidentally reused by another bucket entry.
func workspaceServiceAuthMaterial(name string, serviceConfig BucketServiceConfig) (AuthMaterial, bool, error) {
	if serviceConfig.Auth == nil {
		return AuthMaterial{}, false, nil
	}
	auth := *serviceConfig.Auth
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

// workspaceServiceProfileMaterial resolves dynamic binding refs separately so
// service-level profile policy can be reviewed without exposing local values.
func workspaceServiceProfileMaterial(name string, svc WorkspaceService) (ConnectMaterial, bool, error) {
	for _, profile := range workspaceConnectionProfileMaps(svc) {
		bindingValues, err := workspaceProfileBindingValues(name, profile)
		if err != nil {
			return ConnectMaterial{}, false, err
		}
		if len(bindingValues) > 0 {
			return ConnectMaterial{BindingValues: bindingValues}, true, nil
		}
	}
	return ConnectMaterial{}, false, nil
}

// workspaceConnectionProfileMaps reads inline profiles without re-marshalling
// so unknown-but-valid profile fields survive CLI pass-through validation.
func workspaceConnectionProfileMaps(svc WorkspaceService) []map[string]interface{} {
	var out []map[string]interface{}
	for _, version := range svc.Versions {
		for _, item := range version.ConnectionProfiles {
			profile, _ := item["profile"].(map[string]interface{})
			if profile != nil {
				out = append(out, profile)
			}
		}
	}
	return out
}

// workspaceBucketMaterialKey prevents two buckets configuring the same service
// from overwriting each other's apply-time credential material.
func workspaceBucketMaterialKey(bucketName, serviceKey string) string {
	return workspaceConnectBucketName(bucketName) + "\x00" + serviceKey
}

// workspaceConnectBucketName mirrors Engine's default bucket fallback so local
// material keys line up with apply validation.
func workspaceConnectBucketName(bucketName string) string {
	name := strings.TrimSpace(bucketName)
	if name == "" {
		return "default"
	}
	return name
}

// workspaceProfileBindingValues resolves only declared binding references so
// unrelated process environment values can never cross the apply boundary.
func workspaceProfileBindingValues(serviceName string, profile map[string]interface{}) (map[string]string, error) {
	bindings, _ := profile["bindings"].([]interface{})
	values := map[string]string{}
	for _, raw := range bindings {
		binding, _ := raw.(map[string]interface{})
		name := envRefName(fmt.Sprint(binding["value"]))
		if name == "" {
			continue
		}
		value, ok := os.LookupEnv(name)
		if !ok {
			return nil, fmt.Errorf("workspace service %q profile binding environment variable %s is not set", serviceName, name)
		}
		values[name] = value
	}
	return values, nil
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
	return IsEnvironmentReference(value)
}

// IsEnvironmentReference exposes the parser's canonical whole-value $ENV
// check so sync can preserve safe references without duplicating its grammar.
func IsEnvironmentReference(value string) bool {
	return envRefName(value) != ""
}

// envRefName accepts only whole-value env refs, not interpolation, so plan
// documents keep a clear "this value comes from local env" shape.
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
