package configfile

import (
	"github.com/Usefused/cli/internal/pagination"
	"github.com/Usefused/cli/internal/ratelimitpolicy"
	"github.com/Usefused/cli/internal/retrypolicy"
	"github.com/Usefused/cli/internal/signaturepolicy"
)

// ConfigKind defines the type of Fused config file.
type ConfigKind string
type ConfigAPIVersion string

const (
	APIVersionV1  ConfigAPIVersion = "fused/v1"
	KindWorkspace ConfigKind       = "workspace"
	KindSDK       ConfigKind       = "sdk"
	KindMCP       ConfigKind       = "mcp"
	KindWebhook   ConfigKind       = "webhook"
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
// app/runtime should use for those enabled services.
type WorkspaceBucket struct {
	ServiceConfig map[string]BucketServiceConfig `yaml:"service_config,omitempty" json:"service_config,omitempty"`
	// Secrets are generic, bucket-scoped named secrets -- not tied to any one
	// service, unlike ServiceConfig -- resolved at verification/dispatch time
	// via an explicit bucket.<bucket-name>.secret.<key-name> reference rather
	// than ambient SDK/app context (see
	// plans/plan-service-config-restructure.md item 4). Motivated by webhook
	// signing secrets needing to be shared across multiple SDKs/registrations,
	// but deliberately general-purpose. Values require $ENV references, same
	// discipline as Auth/Connect fields -- see validateWorkspaceBucketSecrets.
	Secrets map[string]string `yaml:"secrets,omitempty" json:"secrets,omitempty"`
}

// Connect (the bucket's OAuth/OIDC app registration -- client_id/
// client_secret/redirect_uri) was removed from here deliberately: it is now
// an immediate admin action only, `fused-cli connect <slug> set` (see
// fused-bucket), never a workspace.yaml field. Two ways to register the same
// app registration -- one declarative-and-required-in-full, one imperative-
// and-partial-update -- was the exact kind of duplicated decision-making
// this config is meant to avoid; connect set's partial-update support also
// made the declarative form strictly worse (it always required every
// field). Auth is unaffected -- it is a fully static credential, not an
// interactive flow, so it has no equivalent imperative-vs-declarative split.
type BucketServiceConfig struct {
	Auth       *AuthConfig       `yaml:"auth,omitempty" json:"auth,omitempty"`
	Injections []InjectionConfig `yaml:"injections,omitempty" json:"injections,omitempty"`
}

// WorkspaceService represents service versions enabled for a workspace.
type WorkspaceService struct {
	ServiceID string `yaml:"service_id,omitempty" json:"service_id,omitempty"`
	// Public controls Registry-level service visibility via updateServicePublic.
	// true  → service page is visible to all Registry consumers (owner only).
	// false → service is private to this workspace's account.
	// Omitted for third-party services (non-owners cannot set this field).
	Public *bool `yaml:"public,omitempty" json:"public,omitempty"`
	// Versions is one entry per enabled version: its identity (Version, plus
	// the Engine-resolved ServiceVersionID once known), any per-version
	// override of Public/ExecutionPolicy, and the connection profiles scoped
	// to it. These used to be three separate service-level lists
	// (resolved_versions, version_policies, connection_profiles), each keyed
	// by a repeated `version` string; nesting them here means a version's
	// identity is declared exactly once instead of once per list.
	Versions []WorkspaceServiceVersion `yaml:"versions,omitempty" json:"versions,omitempty"`
	// RuntimeConfig/runtime_config.webhooks (the workspace's own webhook
	// registrations) was removed with no backward compatibility once
	// kind: webhook shipped -- see plans/plan-webhook-kind.md. Registration
	// now lives entirely in kind: webhook config files.
	// ExecutionPolicy is the default applied to every version in Versions
	// unless that version sets its own ExecutionPolicy override.
	ExecutionPolicy *ExecutionPolicy `yaml:"execution_policy,omitempty" json:"execution_policy,omitempty"`
}

// WorkspaceServiceVersion is one version a service enables, along with
// everything scoped to just that version.
type WorkspaceServiceVersion struct {
	Version string `yaml:"version" json:"version"`
	// ServiceVersionID is the Engine-resolved immutable ID for Version, kept
	// alongside it so CLI/CI can re-apply without a fresh registry lookup
	// that could drift under a reused version label.
	ServiceVersionID string `yaml:"service_version_id,omitempty" json:"service_version_id,omitempty"`
	// Public controls Registry-level visibility for just this version via
	// UpdateServiceVersionPublicStatus (owner only). Distinct from
	// ExecutionPolicy.Public, which controls whether this version's
	// rate_limit/retry are published, not whether the version itself is
	// visible. Omitted means "leave this version's visibility unchanged".
	Public *bool `yaml:"public,omitempty" json:"public,omitempty"`
	// ExecutionPolicy overrides the service-level default for just this
	// version. Nil means "use the service-level default unchanged".
	ExecutionPolicy *ExecutionPolicy `yaml:"execution_policy,omitempty" json:"execution_policy,omitempty"`
	// ConnectionProfiles is intentionally raw in the CLI: Engine owns full
	// validation, while CLI only resolves local env refs before apply. Each
	// Version is already implied by nesting here. auth_name remains part of an
	// entry when a provider declares several named schemes in the same family;
	// Engine needs both values to select the reviewed scheme deterministically.
	ConnectionProfiles []map[string]interface{} `yaml:"connection_profiles,omitempty" json:"connection_profiles,omitempty"`
}

type ExecutionPolicy struct {
	// Public, when true, publishes the rate_limit, retry, and pagination
	// settings through the Registry publish API so downstream consumers inherit
	// these provider-declared limits. Only valid for services owned by this
	// account; non-owners will receive an error from the Engine during apply.
	Public      *bool            `yaml:"public,omitempty" json:"public,omitempty"`
	RateLimit   *RateLimitConfig `yaml:"rate_limit,omitempty" json:"rate_limit,omitempty"`
	Retry       *RetryConfig     `yaml:"retry,omitempty" json:"retry,omitempty"`
	RetryConfig *RetryConfig     `yaml:"retry_config,omitempty" json:"retry_config,omitempty"`
	TimeoutMs   *int             `yaml:"timeout_ms,omitempty" json:"timeout_ms,omitempty"`
	// Pagination moved under execution_policy from the now-deleted
	// runtime_config.pagination (see plans/plan-service-config-restructure.md
	// item 1) -- one value per service/version, sharing this same Public flag
	// rather than having its own.
	Pagination *PaginationConfig `yaml:"pagination,omitempty" json:"pagination,omitempty"`
	// BaseURL overrides a wrong or missing spec-derived base_url for this
	// service (or, on one WorkspaceServiceVersion's own ExecutionPolicy
	// override, just that version). Takes effect
	// locally in this workspace on every apply regardless of Public; Public
	// additionally publishes it to the provider contract so every other
	// consumer's effective base_url inherits it too.
	BaseURL *string `yaml:"base_url,omitempty" json:"base_url,omitempty"`
	// ServerVariables binds reviewed OpenAPI server-template variables without
	// requiring an authentication profile. Values remain ordinary workspace
	// configuration; Engine owns final enum, host, and URL safety validation.
	ServerVariables map[string]string `yaml:"server_variables,omitempty" json:"server_variables,omitempty"`
	// EventExtractionPath and IncomingWebhookConfig are the provider's own
	// outbound webhook verification recipe
	// (plans/plan-service-config-restructure.md item 3) -- how *this service*
	// signs the webhooks it sends, not this workspace's own webhook
	// registrations (those stay under runtime_config.webhooks,
	// workspace-private). The json/yaml key keeps the established
	// incoming_webhook_config wire name (not a "webhook_config" alias) so this
	// struct round-trips unchanged through the Engine and Registry publish API.
	// Safe to publish under this same Public flag because
	// IncomingWebhookConfig never carries a secret, only the verification
	// mechanism.
	EventExtractionPath   *string        `yaml:"event_extraction_path,omitempty" json:"event_extraction_path,omitempty"`
	IncomingWebhookConfig *WebhookVerify `yaml:"incoming_webhook_config,omitempty" json:"incoming_webhook_config,omitempty"`
	Reset                 bool           `yaml:"reset,omitempty" json:"reset,omitempty"`
}

// WebhookVerify is the provider-declared verification recipe: auth mechanism
// and where to find the signature. It intentionally has no secret field --
// see ExecutionPolicy.IncomingWebhookConfig's doc comment.
type WebhookVerify struct {
	AuthType            string                  `yaml:"auth_type,omitempty" json:"auth_type,omitempty"`
	AuthLocation        string                  `yaml:"auth_location,omitempty" json:"auth_location,omitempty"`
	AuthKeyName         string                  `yaml:"auth_key_name,omitempty" json:"auth_key_name,omitempty"`
	SignatureHeader     string                  `yaml:"signature_header,omitempty" json:"signature_header,omitempty"`
	VerificationHeaders []string                `yaml:"verification_headers,omitempty" json:"verification_headers,omitempty"`
	SignaturePolicy     *signaturepolicy.Config `yaml:"signature_policy,omitempty" json:"signature_policy,omitempty"`
}

// PaginationConfig is an alias so workspace files and Registry projections
// transport the exact same v3 shape without duplicated field mappings.
type PaginationConfig = pagination.Config

// RateLimitConfig aliases the canonical transport contract. The CLI must
// preserve it exactly while the Engine owns semantic validation and runtime
// enforcement.
type RateLimitConfig = ratelimitpolicy.Config

type RetryConfig = retrypolicy.Config

// RuntimeConfig (workspace-level runtime_config.webhooks) was removed
// outright with no backward compatibility once kind: webhook shipped -- see
// plans/plan-webhook-kind.md and WorkspaceService's doc comment.
type AuthConfig struct {
	Bucket   string `yaml:"bucket,omitempty" json:"bucket,omitempty"`
	AuthType string `yaml:"auth_type" json:"auth_type"`
	AuthName string `yaml:"auth_name,omitempty" json:"auth_name,omitempty"`
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

// ConnectMaterial carries apply-time-resolved values Engine never re-derives
// itself. Only BindingValues is populated today -- a connection profile's
// bindings referencing an $ENV placeholder (see WorkspaceProfileMaterials).
// ClientID/ClientSecret remain on this type because it is also the value
// api.ConnectMaterial's wire shape mirrors for that same profile-binding
// path (see cmd/config_runner.go); they are not set by anything since the
// bucket-level connect: block was removed in favor of `fused-cli connect
// <slug> set` -- registering that app is now an immediate admin action, not
// resolved as part of a workspace apply.
type ConnectMaterial struct {
	ClientID      string
	ClientSecret  string
	BindingValues map[string]string
}

// WebhookConfig is kind: webhook -- a named, team-owned bundle of
// webhook ingress registrations that can span multiple services, with its
// own independent plan/apply lifecycle (see plans/plan-webhook-kind.md).
// Name is the registration identity: the (service, Name) pair is what
// fused_workspace_webhooks is keyed on now, replacing the old free-form
// per-service label RuntimeConfig.Webhooks used. Name must be globally
// unique per (service) across every kind: webhook config in the account.
// A second config cannot silently claim a registration another config owns.
type WebhookConfig struct {
	BaseConfig `yaml:",inline"`
	Name       string                    `yaml:"name" json:"name"`
	Services   map[string]WebhookService `yaml:"services" json:"services"`
}

// WebhookService is one service's registration within a kind:
// webhook config -- just the signing secret reference, since the config's
// own Name (not a per-service label) is the registration's
// identity now.
type WebhookService struct {
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

// AppConfig carries the shared, versioned declaration for generated SDKs
// and Engine-projected MCP servers. Keeping selection shape shared prevents
// their plan results from drifting while their executors remain distinct.
type AppConfig struct {
	BaseConfig `yaml:",inline"`
	Name       string `yaml:"name" json:"name"`
	Version    string `yaml:"version" json:"version"`
	Language   string `yaml:"language,omitempty" json:"language,omitempty"`
	Bucket     string `yaml:"bucket,omitempty" json:"bucket,omitempty"`
	// WebhookAttachment names one kind: webhook config (its own top-level
	// `name:`) this SDK/MCP wants webhook delivery from. Deliberately a
	// single scalar, not a list, and hoisted here at the app's top
	// level rather than nested per-service -- one webhook config can
	// itself span multiple services (see WebhookConfig), so pinning
	// the attachment here once covers every service below that the attached
	// webhook config also registers, instead of repeating the same name under
	// each service. Only one attachment is supported per SDK/MCP app
	// until multiple bucket attachments exist (plans/deferred-work.md).
	// Per-service AppService.Webhooks/WebhooksSelectAll then scope
	// which events, from this one attached webhook config, actually get
	// delivered/generated for each service.
	WebhookAttachment string                `yaml:"webhook_attachment,omitempty" json:"webhook_attachment,omitempty"`
	Services          map[string]AppService `yaml:"services" json:"services"`
	// UnifiedOperations is SDK-only declarative composition. It stays beside
	// Services because bindings refer to opaque configured service keys in this
	// exact immutable app version; Engine resolves those keys during plan.
	UnifiedOperations map[string]UnifiedOperation `yaml:"unified_operations,omitempty" json:"unified_operations,omitempty"`
}

type SDKConfig = AppConfig
type MCPConfig = AppConfig

// AppService represents the requested immutable provider version and
// selected surface shared by SDK and MCP app declarations.
//
// Webhooks/WebhooksSelectAll are the delivery/codegen surface for whatever
// kind: webhook config this service's owning AppConfig attaches via
// WebhookAttachment (see AppConfig.WebhookAttachment's doc comment) --
// they select *which events* to receive/generate typed methods for, never
// which webhook config. An empty/omitted Webhooks list means no events at
// all (explicit opt-in), mirroring how Operations requires SelectAll rather
// than an implicit "all" default; WebhooksSelectAll is the equivalent
// explicit "give me every event" escape hatch for Webhooks, kept as its own
// field rather than overloading SelectAll since Operations/Webhooks are
// independent selections.
type AppService struct {
	Version           string            `yaml:"version" json:"version"`
	Operations        []string          `yaml:"operations" json:"operations"`
	Webhooks          []string          `yaml:"webhooks,omitempty" json:"webhooks,omitempty"`
	WebhooksSelectAll bool              `yaml:"webhooks_select_all,omitempty" json:"webhooks_select_all,omitempty"`
	SelectAll         bool              `yaml:"select_all,omitempty" json:"select_all,omitempty"`
	Auth              *AppAuth          `yaml:"auth,omitempty" json:"auth,omitempty"`
	Connect           *AppConnect       `yaml:"connect,omitempty" json:"connect,omitempty"`
	Injections        []InjectionConfig `yaml:"injections,omitempty" json:"injections,omitempty"`
}

// InjectionConfig injects a value into a specific location of a request at
// runtime. The Value field supports dynamic interpolation via ${...} tags
// (e.g., ${bucket.env.FROM_EMAIL}, ${bucket.secrets.API_KEY}).
type InjectionConfig struct {
	Value    string `yaml:"value" json:"value"`
	Location string `yaml:"location" json:"location"`
	Name     string `yaml:"name" json:"name"`
	Mode     string `yaml:"mode,omitempty" json:"mode,omitempty"`
}

// AppAuth selects a Registry-declared scheme; credential material stays
// in the bucket or user connection and is never accepted in app config.
type AppAuth struct {
	Type string `yaml:"type" json:"type"`
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
}

// AppConnect narrows OAuth/OIDC consent without carrying tokens or
// provider application secrets in source control.
type AppConnect struct {
	Scopes []string `yaml:"scopes,omitempty" json:"scopes,omitempty"`
}

type SDKService = AppService

// UnifiedOperation declares one typed SDK wrapper over selected provider
// operations. Input and output schemas remain ordinary JSON-compatible values;
// semantic schema and expression validation belongs to Engine plan.
type UnifiedOperation struct {
	Description string                             `yaml:"description,omitempty" json:"description,omitempty"`
	Input       map[string]DynamicValue            `yaml:"input" json:"input"`
	Bindings    map[string]UnifiedOperationBinding `yaml:"bindings" json:"bindings"`
	Output      *UnifiedOperationOutput            `yaml:"output,omitempty" json:"output,omitempty"`
}

// UnifiedOperationBinding supports either the compact operationId scalar or
// an expanded alias that can select a service independently of the binding key.
type UnifiedOperationBinding struct {
	Service   string                    `yaml:"service,omitempty" json:"service,omitempty"`
	Operation string                    `yaml:"operation" json:"operation"`
	Input     map[string]DynamicValue   `yaml:"input,omitempty" json:"input,omitempty"`
	DependsOn []string                  `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
	Rollback  *UnifiedOperationRollback `yaml:"rollback,omitempty" json:"rollback,omitempty"`
	Output    *UnifiedOperationOutput   `yaml:"output,omitempty" json:"output,omitempty"`
	compact   bool
}

// UnifiedOperationRollback declares the same-service operation used to
// compensate a successful binding when a direct dependent fails.
type UnifiedOperationRollback struct {
	Operation string                  `yaml:"operation" json:"operation"`
	Input     map[string]DynamicValue `yaml:"input,omitempty" json:"input,omitempty"`
}

// UnifiedOperationOutput maps a successful provider response into a declared
// JSON schema. Root and binding-level outputs are mutually exclusive.
type UnifiedOperationOutput struct {
	Schema  map[string]DynamicValue `yaml:"schema" json:"schema"`
	Mapping map[string]DynamicValue `yaml:"mapping" json:"mapping"`
}

// ParsedConfig is a container for the parsed configuration.
type ParsedConfig struct {
	Kind       ConfigKind
	Path       string
	ConfigKey  string
	SourceHash string
	Workspace  *WorkspaceConfig
	SDK        *SDKConfig
	MCP        *MCPConfig
	Webhook    *WebhookConfig
}

// Run represents a set of parsed configs loaded for a CLI execution.
type Run struct {
	Configs []*ParsedConfig
}
