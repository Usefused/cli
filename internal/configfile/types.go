package configfile

// ConfigKind defines the type of Fused config file.
type ConfigKind string

const (
	KindWorkspace ConfigKind = "workspace"
	KindSDK       ConfigKind = "sdk"
)

// BaseConfig represents the fields common to all Fused configs.
type BaseConfig struct {
	Kind    ConfigKind `yaml:"kind" json:"kind"`
	Version int        `yaml:"version" json:"version"`
}

// WorkspaceConfig represents the desired state for a Fused workspace allowlist.
type WorkspaceConfig struct {
	BaseConfig   `yaml:",inline"`
	Services     map[string]WorkspaceService     `yaml:"services" json:"services"`
	Deprecations []WorkspaceDeprecationDirective `yaml:"deprecations,omitempty" json:"deprecations,omitempty"`
}

// WorkspaceService represents service versions enabled for a workspace.
type WorkspaceService struct {
	ServiceID string   `yaml:"service_id,omitempty" json:"service_id,omitempty"`
	Public    *bool    `yaml:"public,omitempty" json:"public,omitempty"`
	Versions  []string `yaml:"versions,omitempty" json:"versions,omitempty"`
	// Engine apply needs immutable version IDs, not just display names; keeping
	// the resolved pair in config lets CLI/CI re-apply without a fresh registry
	// lookup that could drift under a reused version label.
	ResolvedVersions []WorkspaceResolvedVersion `yaml:"resolved_versions,omitempty" json:"resolved_versions,omitempty"`
	RuntimeConfig    *RuntimeConfig             `yaml:"runtime_config,omitempty" json:"runtime_config,omitempty"`
}

type WorkspaceResolvedVersion struct {
	Version          string `yaml:"version" json:"version"`
	ServiceVersionID string `yaml:"service_version_id" json:"service_version_id"`
}

type RuntimeConfig struct {
	BaseURL             string                       `yaml:"base_url,omitempty" json:"base_url,omitempty"`
	DefaultHeaders      map[string]string            `yaml:"default_headers,omitempty" json:"default_headers,omitempty"`
	Auth                *AuthConfig                  `yaml:"auth,omitempty" json:"auth,omitempty"`
	Connect             *ConnectConfig               `yaml:"connect,omitempty" json:"connect,omitempty"`
	Webhooks            map[string]WebhookConfig     `yaml:"webhooks,omitempty" json:"webhooks,omitempty"`
	Pagination          *PaginationConfig            `yaml:"pagination,omitempty" json:"pagination,omitempty"`
	PaginationOverrides map[string]*PaginationConfig `yaml:"pagination_overrides,omitempty" json:"pagination_overrides,omitempty"`
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
	Bucket          string `yaml:"bucket,omitempty" json:"bucket,omitempty"`
	AuthType        string `yaml:"auth_type" json:"auth_type"`
	Enabled         *bool  `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	ClientID        string `yaml:"client_id,omitempty" json:"client_id,omitempty"`
	ClientIDEnv     string `yaml:"client_id_env,omitempty" json:"client_id_env,omitempty"`
	ClientSecret    string `yaml:"client_secret,omitempty" json:"client_secret,omitempty"`
	ClientSecretEnv string `yaml:"client_secret_env,omitempty" json:"client_secret_env,omitempty"`
	RedirectURI     string `yaml:"redirect_uri" json:"redirect_uri"`
}

type ConnectMaterial struct {
	ClientID     string
	ClientSecret string
}

// WebhookConfig is one named webhook ingress registration for a service. The
// map key in RuntimeConfig.Webhooks (e.g. "repo-a", "staging") is the
// registration's identity across re-applies, not an event filter -- a team
// can register as many independent URLs for the same service as they want
// (however they've configured things on the provider's own dashboard), each
// getting its own generated slug and signing secret.
type WebhookConfig struct {
	SigningSecret string `yaml:"signing_secret,omitempty" json:"signing_secret,omitempty"`
}

type PaginationConfig struct {
	Type          string `yaml:"type" json:"type"`
	CursorField   string `yaml:"cursor_field,omitempty" json:"cursor_field,omitempty"`
	NextPageField string `yaml:"next_page_field,omitempty" json:"next_page_field,omitempty"`
}

// WorkspaceDeprecationDirective keeps deprecation as explicit config intent,
// rather than an apply-time decision hidden behind a removal flag.
type WorkspaceDeprecationDirective struct {
	ServiceID   string `yaml:"service_id" json:"service_id"`
	Version     string `yaml:"version,omitempty" json:"version,omitempty"`
	EffectiveAt string `yaml:"effective_at" json:"effective_at"`
	Reason      string `yaml:"reason,omitempty" json:"reason,omitempty"`
}

// SDKConfig represents the desired state for a generated SDK.
type SDKConfig struct {
	BaseConfig `yaml:",inline"`
	Name       string                `yaml:"name" json:"name"`
	SDKVersion string                `yaml:"sdkVersion" json:"sdkVersion"`
	Language   string                `yaml:"language" json:"language"`
	Target     string                `yaml:"target" json:"target"`
	Bucket     string                `yaml:"bucket,omitempty" json:"bucket,omitempty"`
	Services   map[string]SDKService `yaml:"services" json:"services"`
}

// SDKService represents the requested version and operations for a specific service in an SDK.
type SDKService struct {
	Version         string   `yaml:"version" json:"version"`
	Operations      []string `yaml:"operations" json:"operations"`
	Webhooks        []string `yaml:"webhooks,omitempty" json:"webhooks,omitempty"`
	LegacyEndpoints []string `yaml:"endpoints,omitempty" json:"-"`
}

// ParsedConfig is a container for the parsed configuration.
type ParsedConfig struct {
	Kind       ConfigKind
	Path       string
	ConfigKey  string
	SourceHash string
	Workspace  *WorkspaceConfig
	SDK        *SDKConfig
}

// Run represents a set of parsed configs loaded for a CLI execution.
type Run struct {
	Configs []*ParsedConfig
}
