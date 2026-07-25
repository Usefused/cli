package configfile

// ConfigKind defines the type of Fused config file.
type ConfigKind string
type ConfigAPIVersion string

const (
	APIVersionV1  ConfigAPIVersion = "fused/v1"
	KindWorkspace ConfigKind       = "workspace"
	KindSDK       ConfigKind       = "sdk"
	KindMCP       ConfigKind       = "mcp"
)

// BaseConfig represents the fields common to all Fused configs.
type BaseConfig struct {
	APIVersion ConfigAPIVersion `yaml:"apiVersion" json:"apiVersion"`
	Kind       ConfigKind       `yaml:"kind" json:"kind"`
}

// WorkspaceConfig represents the desired state for a Fused workspace allowlist.
type WorkspaceConfig struct {
	BaseConfig   `yaml:",inline"`
	Services     map[string]WorkspaceService     `yaml:"services" json:"services"`
	Buckets      map[string]WorkspaceBucket      `yaml:"buckets,omitempty" json:"buckets,omitempty"`
	Deprecations []WorkspaceDeprecationDirective `yaml:"deprecations,omitempty" json:"deprecations,omitempty"`
}

// WorkspaceBucket owns runtime credential material keyed by service. Services
// declare what is enabled; buckets declare which credentials a selected
// artifact/runtime should use for those enabled services.
type WorkspaceBucket struct {
	ServiceConfig map[string]BucketServiceConfig `yaml:"service_config,omitempty" json:"service_config,omitempty"`
	// Secrets are generic, bucket-scoped named secrets -- not tied to any one
	// service, unlike ServiceConfig -- resolved at verification/dispatch time
	// via an explicit bucket.<bucket-name>.secret.<key-name> reference rather
	// than ambient SDK/artifact context (see
	// plans/plan-service-config-restructure.md item 4). Motivated by webhook
	// signing secrets needing to be shared across multiple SDKs/registrations,
	// but deliberately general-purpose. Values require $ENV references, same
	// discipline as Auth/Connect fields -- see validateWorkspaceBucketSecrets.
	Secrets map[string]string `yaml:"secrets,omitempty" json:"secrets,omitempty"`
}

type BucketServiceConfig struct {
	Auth    *AuthConfig    `yaml:"auth,omitempty" json:"auth,omitempty"`
	Connect *ConnectConfig `yaml:"connect,omitempty" json:"connect,omitempty"`
}

// WorkspaceService represents service versions enabled for a workspace.
type WorkspaceService struct {
	ServiceID string `yaml:"service_id,omitempty" json:"service_id,omitempty"`
	// Public controls Registry-level service visibility via updateServicePublic.
	// true  → service page is visible to all Registry consumers (owner only).
	// false → service is private to this workspace's account.
	// Omitted for third-party services (non-owners cannot set this field).
	Public   *bool    `yaml:"public,omitempty" json:"public,omitempty"`
	Versions []string `yaml:"versions,omitempty" json:"versions,omitempty"`
	// Engine apply needs immutable version IDs, not just display names; keeping
	// the resolved pair in config lets CLI/CI re-apply without a fresh registry
	// lookup that could drift under a reused version label.
	ResolvedVersions []WorkspaceResolvedVersion `yaml:"resolved_versions,omitempty" json:"resolved_versions,omitempty"`
	RuntimeConfig    *RuntimeConfig             `yaml:"runtime_config,omitempty" json:"runtime_config,omitempty"`
	ExecutionPolicy  *ExecutionPolicy           `yaml:"execution_policy,omitempty" json:"execution_policy,omitempty"`
	VersionPolicies  []WorkspaceVersionPolicy   `yaml:"version_policies,omitempty" json:"version_policies,omitempty"`
	// ConnectionProfiles is intentionally raw in the CLI: Engine owns full
	// validation, while CLI only resolves local env refs before apply.
	ConnectionProfiles []map[string]interface{} `yaml:"connection_profiles,omitempty" json:"connection_profiles,omitempty"`
}

type WorkspaceVersionPolicy struct {
	Version string `yaml:"version" json:"version"`
	// Public controls Registry-level visibility for just this version via
	// UpdateServiceVersionPublicStatus (owner only). Distinct from
	// ExecutionPolicy.Public, which controls whether this version's
	// rate_limit/retry are published, not whether the version itself is
	// visible. Omitted means "leave this version's visibility unchanged".
	Public          *bool            `yaml:"public,omitempty" json:"public,omitempty"`
	ExecutionPolicy *ExecutionPolicy `yaml:"execution_policy,omitempty" json:"execution_policy,omitempty"`
}

type ExecutionPolicy struct {
	// Public, when true, publishes the rate_limit, retry, and pagination
	// settings to the Registry via UpdateServiceConfig so all SDK consumers
	// inherit these provider-declared limits. Only valid for services owned by
	// this account; non-owners will receive an error from the Engine during
	// apply.
	Public      *bool            `yaml:"public,omitempty" json:"public,omitempty"`
	RateLimit   *RateLimitConfig `yaml:"rate_limit,omitempty" json:"rate_limit,omitempty"`
	Retry       *RetryConfig     `yaml:"retry,omitempty" json:"retry,omitempty"`
	RetryConfig *RetryConfig     `yaml:"retry_config,omitempty" json:"retry_config,omitempty"`
	// Pagination moved under execution_policy from the now-deleted
	// runtime_config.pagination (see plans/plan-service-config-restructure.md
	// item 1) -- one value per service/version, sharing this same Public flag
	// rather than having its own.
	Pagination *PaginationConfig `yaml:"pagination,omitempty" json:"pagination,omitempty"`
	// EventExtractionPath and IncomingWebhookConfig are the provider's own
	// outbound webhook verification recipe
	// (plans/plan-service-config-restructure.md item 3) -- how *this service*
	// signs the webhooks it sends, not this workspace's own webhook
	// registrations (those stay under runtime_config.webhooks,
	// workspace-private). The json/yaml key matches the Registry's own
	// incoming_webhook_config field name (not a "webhook_config" alias) so
	// this struct round-trips unchanged through the Engine, which reuses it
	// verbatim as the Registry's request body. Safe to publish under this
	// same Public flag because IncomingWebhookConfig never carries a secret,
	// only the verification mechanism.
	EventExtractionPath   *string        `yaml:"event_extraction_path,omitempty" json:"event_extraction_path,omitempty"`
	IncomingWebhookConfig *WebhookVerify `yaml:"incoming_webhook_config,omitempty" json:"incoming_webhook_config,omitempty"`
	Reset                 bool           `yaml:"reset,omitempty" json:"reset,omitempty"`
}

// WebhookVerify is the provider-declared verification recipe: auth mechanism
// and where to find the signature. It intentionally has no secret field --
// see ExecutionPolicy.IncomingWebhookConfig's doc comment.
type WebhookVerify struct {
	AuthType            string   `yaml:"auth_type,omitempty" json:"auth_type,omitempty"`
	AuthLocation        string   `yaml:"auth_location,omitempty" json:"auth_location,omitempty"`
	AuthKeyName         string   `yaml:"auth_key_name,omitempty" json:"auth_key_name,omitempty"`
	SignatureHeader     string   `yaml:"signature_header,omitempty" json:"signature_header,omitempty"`
	VerificationHeaders []string `yaml:"verification_headers,omitempty" json:"verification_headers,omitempty"`
}

// PaginationConfig mirrors models.PaginationConfig on the Registry -- shape
// must match the real dispatch type (Type/RequestParam/ResponsePath), not the
// old dead workspace-config shape (Type/CursorField/NextPageField) that this
// replaces.
type PaginationConfig struct {
	Type         string `yaml:"type" json:"type"`
	RequestParam string `yaml:"request_param" json:"request_param"`
	ResponsePath string `yaml:"response_path" json:"response_path"`
}

type RateLimitConfig struct {
	Strategy          string `yaml:"strategy" json:"strategy"`
	RequestsPerSecond int    `yaml:"requests_per_second" json:"requests_per_second"`
	RequestsPerMinute int    `yaml:"requests_per_minute" json:"requests_per_minute"`
}

type RetryConfig struct {
	Strategy   string `yaml:"strategy" json:"strategy"`
	MaxRetries int    `yaml:"max_retries" json:"max_retries"`
	BackoffMs  int    `yaml:"backoff_ms" json:"backoff_ms"`
}

type WorkspaceResolvedVersion struct {
	Version          string `yaml:"version" json:"version"`
	ServiceVersionID string `yaml:"service_version_id" json:"service_version_id"`
}

// RuntimeConfig is now just webhook registrations -- base_url,
// default_headers, auth, connect, pagination, and pagination_overrides were
// all removed (see plans/plan-service-config-restructure.md): auth/connect
// already required buckets.<bucket>.service_config.<service> instead, and
// base_url/default_headers/pagination/pagination_overrides were confirmed
// dead -- parsed but never consumed by any apply/dispatch path. Webhooks
// stays here as the one still-functional field until its bucket-secret-
// reference replacement (plan items 2-4) lands.
type RuntimeConfig struct {
	Webhooks map[string]WebhookConfig `yaml:"webhooks,omitempty" json:"webhooks,omitempty"`
}

type AuthConfig struct {
	Bucket   string `yaml:"bucket,omitempty" json:"bucket,omitempty"`
	AuthType string `yaml:"auth_type" json:"auth_type"`
	Username string `yaml:"username,omitempty" json:"username,omitempty"`
	Password string `yaml:"password,omitempty" json:"password,omitempty"`
	Token    string `yaml:"token,omitempty" json:"token,omitempty"`
	APIKey   string `yaml:"api_key,omitempty" json:"api_key,omitempty"`
	Cert     string `yaml:"cert,omitempty" json:"cert,omitempty"`
	Key      string `yaml:"key,omitempty" json:"key,omitempty"`
}

type AuthMaterial struct {
	Username string
	Password string
	Token    string
	APIKey   string
	Cert     string
	Key      string
}

type ConnectConfig struct {
	AuthType        string `yaml:"auth_type" json:"auth_type"`
	Enabled         *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	ClientID        string `yaml:"client_id,omitempty" json:"client_id,omitempty"`
	ClientIDEnv     string `yaml:"client_id_env,omitempty" json:"client_id_env,omitempty"`
	ClientSecret    string `yaml:"client_secret,omitempty" json:"client_secret,omitempty"`
	ClientSecretEnv string `yaml:"client_secret_env,omitempty" json:"client_secret_env,omitempty"`
	RedirectURI     string `yaml:"redirect_uri" json:"redirect_uri"`
}

type ConnectMaterial struct {
	ClientID      string
	ClientSecret  string
	BindingValues map[string]string
}

// WebhookConfig is one named webhook ingress registration for a service. The
// map key in RuntimeConfig.Webhooks (e.g. "repo-a", "staging") is the
// registration's identity across re-applies, not an event filter -- a team
// can register as many independent URLs for the same service as they want
// (however they've configured things on the provider's own dashboard), each
// getting its own generated slug and signing secret.
//
// Secret replaces the old literal SigningSecret field
// (plans/plan-service-config-restructure.md item 4): it is a
// "bucket.<name>.secret.<key>" reference into a bucket's generic named-secret
// declaration (WorkspaceBucket.Secrets), never a value that lands in this
// file. The bucket name may be omitted for the shorthand
// "bucket.secret.<key>" form, which resolves against the "default" bucket.
type WebhookConfig struct {
	Secret string `yaml:"secret,omitempty" json:"secret,omitempty"`
}

// WorkspaceDeprecationDirective keeps deprecation as explicit config intent,
// rather than an apply-time decision hidden behind a removal flag.
type WorkspaceDeprecationDirective struct {
	ServiceID   string `yaml:"service_id" json:"service_id"`
	Version     string `yaml:"version,omitempty" json:"version,omitempty"`
	EffectiveAt string `yaml:"effective_at" json:"effective_at"`
	Reason      string `yaml:"reason,omitempty" json:"reason,omitempty"`
}

// ArtifactConfig carries the shared, versioned declaration for generated SDKs
// and Engine-projected MCP servers. Keeping selection shape shared prevents
// their plan results from drifting while their executors remain distinct.
type ArtifactConfig struct {
	BaseConfig `yaml:",inline"`
	Name       string                     `yaml:"name" json:"name"`
	Version    string                     `yaml:"version" json:"version"`
	Language   string                     `yaml:"language,omitempty" json:"language,omitempty"`
	Bucket     string                     `yaml:"bucket,omitempty" json:"bucket,omitempty"`
	Services   map[string]ArtifactService `yaml:"services" json:"services"`
}

type SDKConfig = ArtifactConfig
type MCPConfig = ArtifactConfig

// ArtifactService represents the requested immutable provider version and
// selected surface shared by SDK and MCP artifact declarations.
type ArtifactService struct {
	Version    string           `yaml:"version" json:"version"`
	Operations []string         `yaml:"operations" json:"operations"`
	Webhooks   []string         `yaml:"webhooks,omitempty" json:"webhooks,omitempty"`
	SelectAll  bool             `yaml:"select_all,omitempty" json:"select_all,omitempty"`
	Auth       *ArtifactAuth    `yaml:"auth,omitempty" json:"auth,omitempty"`
	Connect    *ArtifactConnect `yaml:"connect,omitempty" json:"connect,omitempty"`
}

// ArtifactAuth selects a Registry-declared scheme; credential material stays
// in the bucket or user connection and is never accepted in artifact config.
type ArtifactAuth struct {
	Type string `yaml:"type" json:"type"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
}

// ArtifactConnect narrows OAuth/OIDC consent without carrying tokens or
// provider application secrets in source control.
type ArtifactConnect struct {
	Scopes []string `yaml:"scopes,omitempty" json:"scopes,omitempty"`
}

type SDKService = ArtifactService

// ParsedConfig is a container for the parsed configuration.
type ParsedConfig struct {
	Kind       ConfigKind
	Path       string
	ConfigKey  string
	SourceHash string
	Workspace  *WorkspaceConfig
	SDK        *SDKConfig
	MCP        *MCPConfig
}

// Run represents a set of parsed configs loaded for a CLI execution.
type Run struct {
	Configs []*ParsedConfig
}
