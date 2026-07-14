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

// WorkspaceService represents the allowed and default versions for a service in a workspace.
type WorkspaceService struct {
	ServiceID string   `yaml:"service_id,omitempty" json:"service_id,omitempty"`
	Versions  []string `yaml:"versions" json:"versions"`
	Default   string   `yaml:"default" json:"default"`
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
