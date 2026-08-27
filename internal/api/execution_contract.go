package api

import (
	"encoding/json"

	"github.com/Usefused/cli/internal/catalogcontract"
	"github.com/Usefused/cli/internal/workflowcontract"
)

// ExecutionContractEnvelope is shared by service-version metadata and runtime
// snapshots. Keeping negotiation data beside the immutable version prevents a
// caller from accidentally applying capabilities from a different revision.
type ExecutionContractEnvelope struct {
	ContractVersion      int      `json:"contract_version"`
	RequiredCapabilities []string `json:"required_capabilities"`
}

// ServiceRuntimeContract mirrors the language-neutral Registry-to-Engine
// golden contract. These are transport types only: Registry and Engine remain
// responsible for normalizing and executing provider semantics.
type ServiceRuntimeContract struct {
	ExecutionContractEnvelope
	ServiceID         string                                  `json:"service_id"`
	ServiceVersionID  string                                  `json:"service_version_id"`
	Version           string                                  `json:"version"`
	Catalog           *catalogcontract.Composition            `json:"catalog,omitempty"`
	Service           ServiceRuntimeMetadata                  `json:"service"`
	Operations        []ServiceRuntimeOperation               `json:"operations"`
	Webhooks          []ServiceRuntimeWebhook                 `json:"webhooks"`
	SchemaDefinitions map[string]ServiceRuntimeSchemaContract `json:"schema_definitions,omitempty"`
}

type ServiceRuntimeMetadata struct {
	ID                    string                        `json:"id"`
	CurrentServiceVersion string                        `json:"current_service_version,omitempty"`
	Name                  string                        `json:"name"`
	Description           string                        `json:"description,omitempty"`
	BaseURL               string                        `json:"base_url"`
	Servers               *[]ServiceServer              `json:"servers,omitempty"`
	SourceID              string                        `json:"source_id,omitempty"`
	AuthConfigs           *[]AuthConfig                 `json:"auth_configs,omitempty"`
	Documentation         *ServiceDocumentation         `json:"documentation,omitempty"`
	RateLimit             *ServiceRateLimit             `json:"rate_limit,omitempty"`
	RetryConfig           *ServiceRetryConfig           `json:"retry_config,omitempty"`
	TimeoutMs             *int                          `json:"timeout_ms,omitempty"`
	Pagination            *ServicePagination            `json:"pagination,omitempty"`
	EventExtractionPath   *string                       `json:"event_extraction_path,omitempty"`
	IncomingWebhookConfig *ServiceIncomingWebhookConfig `json:"incoming_webhook_config,omitempty"`
	DefaultHeaders        map[string]string             `json:"default_headers,omitempty"`
	ConnectConfig         json.RawMessage               `json:"connect_config,omitempty"`
	IsPublic              *bool                         `json:"is_public,omitempty"`
	WatchForDrift         *bool                         `json:"watch_for_drift,omitempty"`
	AccountID             string                        `json:"account_id,omitempty"`
	IsOwner               *bool                         `json:"is_owner,omitempty"`
	CreatedAt             string                        `json:"created_at,omitempty"`
	UpdatedAt             string                        `json:"updated_at,omitempty"`
	OriginalFields        map[string]json.RawMessage    `json:"-"`
}

func (s *ServiceRuntimeMetadata) UnmarshalJSON(data []byte) error {
	type metadataAlias ServiceRuntimeMetadata
	var decoded metadataAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	fields, err := decodeRuntimeFields(data)
	if err != nil {
		return err
	}
	*s = ServiceRuntimeMetadata(decoded)
	s.OriginalFields = fields
	return nil
}

func (s ServiceRuntimeMetadata) MarshalJSON() ([]byte, error) {
	type metadataAlias ServiceRuntimeMetadata
	return marshalRuntimeObject(metadataAlias(s), s.OriginalFields)
}

type ServiceRuntimeOperation struct {
	ID                   string                                    `json:"id"`
	StableKey            string                                    `json:"stable_key,omitempty"`
	Name                 string                                    `json:"name"`
	Description          string                                    `json:"description,omitempty"`
	ResourceID           string                                    `json:"resource_id,omitempty"`
	ResourceName         string                                    `json:"resource_name,omitempty"`
	Version              string                                    `json:"version,omitempty"`
	Method               string                                    `json:"method"`
	Path                 string                                    `json:"path"`
	NormalizedPath       string                                    `json:"normalized_path,omitempty"`
	Deprecated           *bool                                     `json:"deprecated,omitempty"`
	OperationServers     []ServiceServer                           `json:"operation_servers,omitempty"`
	Parameters           *[]ServiceRuntimeParameter                `json:"parameters,omitempty"`
	RequestContent       *ServiceRuntimeRequestContent             `json:"request_content,omitempty"`
	Responses            map[string]ServiceRuntimeResponseContract `json:"responses,omitempty"`
	GraphQLQuery         *string                                   `json:"graphql_query,omitempty"`
	ProviderProtocol     string                                    `json:"provider_protocol,omitempty"`
	OperationKind        string                                    `json:"operation_kind,omitempty"`
	Pagination           *ServicePagination                        `json:"pagination,omitempty"`
	SecurityRequirements *SecurityRequirements                     `json:"security_requirements,omitempty"`
	Documentation        *OperationDocumentation                   `json:"documentation,omitempty"`
	AdditionalFields     map[string]json.RawMessage                `json:"-"`
	OriginalFields       map[string]json.RawMessage                `json:"-"`
}

type ServiceRuntimeParameter struct {
	Name          string                                    `json:"name"`
	In            string                                    `json:"in"`
	Required      bool                                      `json:"required"`
	Type          string                                    `json:"type"`
	Description   string                                    `json:"description"`
	PathEncoding  string                                    `json:"path_encoding,omitempty"`
	Serialization ServiceRuntimeParameterSerialization      `json:"serialization"`
	Schema        *ServiceRuntimeSchemaContract             `json:"schema,omitempty"`
	Content       map[string]ServiceRuntimeParameterContent `json:"content,omitempty"`
	Deprecated    *bool                                     `json:"deprecated,omitempty"`
	Example       any                                       `json:"example,omitempty"`
	Examples      map[string]any                            `json:"examples,omitempty"`
}

type ServiceRuntimeParameterSerialization struct {
	Style           string `json:"style"`
	Explode         *bool  `json:"explode"`
	AllowReserved   *bool  `json:"allow_reserved"`
	AllowEmptyValue *bool  `json:"allow_empty_value"`
}

// ServiceRuntimeParameterContent retains the media-specific parameter shape.
// It stays distinct from request content because OpenAPI permits only a schema
// plus examples here; request-body serialization has different semantics.
type ServiceRuntimeParameterContent struct {
	Schema         *ServiceRuntimeSchemaContract            `json:"schema,omitempty"`
	ItemSchema     *ServiceRuntimeSchemaContract            `json:"item_schema,omitempty"`
	Encoding       map[string]ServiceRuntimeRequestEncoding `json:"encoding,omitempty"`
	PrefixEncoding []ServiceRuntimeRequestEncoding          `json:"prefix_encoding,omitempty"`
	ItemEncoding   *ServiceRuntimeRequestEncoding           `json:"item_encoding,omitempty"`
	Example        any                                      `json:"example,omitempty"`
	Examples       map[string]any                           `json:"examples,omitempty"`
}

// ServiceRuntimeSchemaContract keeps source truth separate from its one
// Registry-owned runtime projection. CLI must never reinterpret Raw because a
// second projection would make generated SDK behavior depend on the reader.
type ServiceRuntimeSchemaContract struct {
	Dialect               string                                     `json:"dialect"`
	Raw                   json.RawMessage                            `json:"raw"`
	ContentHash           string                                     `json:"content_hash"`
	Projection            ServiceRuntimeSchema                       `json:"projection"`
	ProjectionDiagnostics []ServiceRuntimeSchemaProjectionDiagnostic `json:"projection_diagnostics,omitempty"`
	// The transport preserves scope; Registry and Engine remain the schema interpreters.
	SharedDefinitions bool `json:"shared_definitions,omitempty"`
}

type ServiceRuntimeSchemaProjectionDiagnostic struct {
	Code    string `json:"code"`
	Keyword string `json:"keyword"`
	Pointer string `json:"pointer"`
	Message string `json:"message"`
}

type ServiceRuntimeSchema struct {
	Ref                  string                          `json:"$ref,omitempty"`
	Type                 string                          `json:"type,omitempty"`
	Format               string                          `json:"format,omitempty"`
	Properties           map[string]ServiceRuntimeSchema `json:"properties,omitempty"`
	Items                *ServiceRuntimeSchema           `json:"items,omitempty"`
	AdditionalProperties *ServiceRuntimeSchema           `json:"additional_properties,omitempty"`
	Required             []string                        `json:"required,omitempty"`
	Example              any                             `json:"example,omitempty"`
}

type ServiceRuntimeRequestContent struct {
	Required         bool                                  `json:"required,omitempty"`
	PayloadParameter string                                `json:"payload_parameter,omitempty"`
	Representations  []ServiceRuntimeRequestRepresentation `json:"representations"`
	DefaultMediaType string                                `json:"default_media_type,omitempty"`
	UploadWorkflow   *workflowcontract.UploadWorkflow      `json:"upload_workflow,omitempty"`
}

type ServiceRuntimeRequestRepresentation struct {
	MediaType      string                                   `json:"media_type"`
	Serialization  string                                   `json:"serialization"`
	Schema         *ServiceRuntimeSchemaContract            `json:"schema,omitempty"`
	ItemSchema     *ServiceRuntimeSchemaContract            `json:"item_schema,omitempty"`
	Encoding       map[string]ServiceRuntimeRequestEncoding `json:"encoding,omitempty"`
	PrefixEncoding []ServiceRuntimeRequestEncoding          `json:"prefix_encoding,omitempty"`
	ItemEncoding   *ServiceRuntimeRequestEncoding           `json:"item_encoding,omitempty"`
	Example        any                                      `json:"example,omitempty"`
	Examples       map[string]any                           `json:"examples,omitempty"`
}

type ServiceRuntimeRequestEncoding struct {
	ContentType    string                                   `json:"content_type,omitempty"`
	Headers        map[string]ServiceRuntimeHeaderContract  `json:"headers,omitempty"`
	Style          string                                   `json:"style,omitempty"`
	Explode        *bool                                    `json:"explode,omitempty"`
	AllowReserved  *bool                                    `json:"allow_reserved,omitempty"`
	BinaryEncoding string                                   `json:"binary_encoding,omitempty"`
	Encoding       map[string]ServiceRuntimeRequestEncoding `json:"encoding,omitempty"`
	PrefixEncoding []ServiceRuntimeRequestEncoding          `json:"prefix_encoding,omitempty"`
	ItemEncoding   *ServiceRuntimeRequestEncoding           `json:"item_encoding,omitempty"`
}

type ServiceRuntimeHeaderContract struct {
	Description   string                                    `json:"description,omitempty"`
	Required      *bool                                     `json:"required,omitempty"`
	Deprecated    *bool                                     `json:"deprecated,omitempty"`
	Serialization ServiceRuntimeParameterSerialization      `json:"serialization"`
	Schema        *ServiceRuntimeSchemaContract             `json:"schema,omitempty"`
	Content       map[string]ServiceRuntimeParameterContent `json:"content,omitempty"`
	Example       any                                       `json:"example,omitempty"`
	Examples      map[string]any                            `json:"examples,omitempty"`
}

type ServiceRuntimeResponseContract struct {
	Summary         string                                  `json:"summary,omitempty"`
	Description     string                                  `json:"description"`
	Headers         map[string]ServiceRuntimeHeaderContract `json:"headers,omitempty"`
	Representations []ServiceRuntimeResponseRepresentation  `json:"representations"`
	Links           map[string]ServiceRuntimeLinkContract   `json:"links,omitempty"`
}

type ServiceRuntimeResponseRepresentation struct {
	MediaType      string                             `json:"media_type"`
	Schema         *ServiceRuntimeSchemaContract      `json:"schema,omitempty"`
	ItemSchema     *ServiceRuntimeSchemaContract      `json:"item_schema,omitempty"`
	SSE            *ServiceRuntimeSSEResponseContract `json:"sse,omitempty"`
	PrefixEncoding []ServiceRuntimeRequestEncoding    `json:"prefix_encoding,omitempty"`
	ItemEncoding   *ServiceRuntimeRequestEncoding     `json:"item_encoding,omitempty"`
	Example        any                                `json:"example,omitempty"`
	Examples       map[string]any                     `json:"examples,omitempty"`
}

type ServiceRuntimeSSEResponseContract struct {
	ItemMode     string  `json:"item_mode"`
	DoneSentinel *string `json:"done_sentinel,omitempty"`
}

type ServiceRuntimeLinkContract struct {
	OperationRef string               `json:"operation_ref,omitempty"`
	OperationID  string               `json:"operation_id,omitempty"`
	Description  string               `json:"description,omitempty"`
	Parameters   map[string]any       `json:"parameters,omitempty"`
	RequestBody  any                  `json:"request_body,omitempty"`
	Server       *ServiceServer       `json:"server,omitempty"`
	Extensions   NamespacedExtensions `json:"extensions,omitempty"`
}

type ServiceRuntimeWebhook struct {
	ID          string                    `json:"id"`
	ServiceID   string                    `json:"service_id,omitempty"`
	Name        string                    `json:"name"`
	Method      string                    `json:"method"`
	Description string                    `json:"description,omitempty"`
	RequestBody *ServiceRuntimeSchema     `json:"request_body,omitempty"`
	Contract    *InboundOperationContract `json:"contract,omitempty"`
	CreatedAt   string                    `json:"created_at,omitempty"`
	UpdatedAt   string                    `json:"updated_at,omitempty"`
}

// InboundOperationContract preserves source callback and webhook fidelity for
// review. The CLI transports this metadata but never treats it as an outbound
// operation or invents execution behavior from runtime expressions.
type InboundOperationContract struct {
	Kind                 string                                    `json:"kind"`
	RuntimeExpression    string                                    `json:"runtime_expression,omitempty"`
	Parent               *CallbackParent                           `json:"parent,omitempty"`
	Path                 string                                    `json:"path"`
	Summary              string                                    `json:"summary,omitempty"`
	Description          string                                    `json:"description,omitempty"`
	Tags                 []string                                  `json:"tags"`
	ExternalDocs         *ExternalDocumentation                    `json:"external_docs,omitempty"`
	Deprecated           bool                                      `json:"deprecated"`
	OperationServers     []ServiceServer                           `json:"operation_servers,omitempty"`
	Parameters           []ServiceRuntimeParameter                 `json:"parameters"`
	RequestContent       *ServiceRuntimeRequestContent             `json:"request_content,omitempty"`
	Responses            map[string]ServiceRuntimeResponseContract `json:"responses"`
	SecurityRequirements SecurityRequirements                      `json:"security_requirements"`
	Extensions           NamespacedExtensions                      `json:"extensions,omitempty"`
}

type CallbackParent struct {
	OperationID  string `json:"operation_id"`
	Method       string `json:"method"`
	Path         string `json:"path"`
	CallbackName string `json:"callback_name"`
}

type NamespacedExtensions map[string]NamespacedExtension

type NamespacedExtension struct {
	Value      json.RawMessage `json:"value"`
	Provenance string          `json:"provenance"`
}

type ExternalDocumentation struct {
	Description string `json:"description,omitempty"`
	URL         string `json:"url"`
}

type OperationDocumentation struct {
	Summary      string                 `json:"summary,omitempty"`
	Description  string                 `json:"description,omitempty"`
	Tags         []string               `json:"tags"`
	ExternalDocs *ExternalDocumentation `json:"external_docs,omitempty"`
	Extensions   NamespacedExtensions   `json:"extensions,omitempty"`
}

type ServiceDocumentation struct {
	TermsOfService string                 `json:"terms_of_service,omitempty"`
	Contact        *ContactDocumentation  `json:"contact,omitempty"`
	License        *LicenseDocumentation  `json:"license,omitempty"`
	Tags           []TagDocumentation     `json:"tags"`
	ExternalDocs   *ExternalDocumentation `json:"external_docs,omitempty"`
	Extensions     NamespacedExtensions   `json:"extensions,omitempty"`
}

type ContactDocumentation struct {
	Name  string `json:"name,omitempty"`
	URL   string `json:"url,omitempty"`
	Email string `json:"email,omitempty"`
}

type LicenseDocumentation struct {
	Name       string `json:"name,omitempty"`
	Identifier string `json:"identifier,omitempty"`
	URL        string `json:"url,omitempty"`
}

type TagDocumentation struct {
	Name         string                 `json:"name"`
	Summary      string                 `json:"summary,omitempty"`
	Parent       string                 `json:"parent,omitempty"`
	Kind         string                 `json:"kind,omitempty"`
	Description  string                 `json:"description,omitempty"`
	ExternalDocs *ExternalDocumentation `json:"external_docs,omitempty"`
}

var serviceRuntimeOperationKnownFields = []string{
	"id", "stable_key", "name", "description", "resource_id", "resource_name", "version",
	"method", "path", "normalized_path", "deprecated", "operation_servers", "parameters",
	"request_content", "responses", "graphql_query", "provider_protocol",
	"operation_kind", "pagination", "security_requirements", "documentation",
}

func (o *ServiceRuntimeOperation) UnmarshalJSON(data []byte) error {
	type operationAlias ServiceRuntimeOperation
	var decoded operationAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, field := range serviceRuntimeOperationKnownFields {
		delete(fields, field)
	}
	*o = ServiceRuntimeOperation(decoded)
	o.AdditionalFields = fields
	original, err := decodeRuntimeFields(data)
	if err != nil {
		return err
	}
	o.OriginalFields = original
	return nil
}

func (o ServiceRuntimeOperation) MarshalJSON() ([]byte, error) {
	type operationAlias ServiceRuntimeOperation
	return marshalRuntimeObject(operationAlias(o), o.AdditionalFields, o.OriginalFields)
}

func decodeRuntimeFields(data []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	err := json.Unmarshal(data, &fields)
	return fields, err
}

func marshalRuntimeObject(value any, passthrough ...map[string]json.RawMessage) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	fields, err := decodeRuntimeFields(encoded)
	if err != nil {
		return nil, err
	}
	for _, source := range passthrough {
		for field, original := range source {
			// Re-emit explicit zero/null values from Registry while keeping typed
			// values authoritative when callers mutate the decoded DTO.
			if _, encodedField := fields[field]; !encodedField {
				fields[field] = original
			}
		}
	}
	return json.Marshal(fields)
}
