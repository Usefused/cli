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
	ServiceID     string         `yaml:"service_id,omitempty" json:"service_id,omitempty"`
	Public        *bool          `yaml:"public,omitempty" json:"public,omitempty"`
	Versions      []string       `yaml:"versions,omitempty" json:"versions,omitempty"`
	RuntimeConfig *RuntimeConfig `yaml:"runtime_config,omitempty" json:"runtime_config,omitempty"`
}

type RuntimeConfig struct {
	BaseURL             string                       `yaml:"base_url,omitempty" json:"base_url,omitempty"`
	DefaultHeaders      map[string]string            `yaml:"default_headers,omitempty" json:"default_headers,omitempty"`
	Webhooks            map[string]WebhookConfig     `yaml:"webhooks,omitempty" json:"webhooks,omitempty"`
	Pagination          *PaginationConfig            `yaml:"pagination,omitempty" json:"pagination,omitempty"`
	PaginationOverrides map[string]*PaginationConfig `yaml:"pagination_overrides,omitempty" json:"pagination_overrides,omitempty"`
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
