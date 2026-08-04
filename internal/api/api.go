package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/huh/spinner"
)

type Client struct {
	BaseURL      string
	APIKey       string
	HTTP         *http.Client
	showProgress bool
}

const DefaultTimeout = 30 * time.Second

var (
	errGraphQLResponseMalformed = errors.New("graphql_response_malformed: Engine returned a malformed GraphQL response; retry or check Engine logs")
	errGraphQLRequestRejected   = errors.New("graphql_request_rejected: Engine rejected the GraphQL request; check command inputs and workspace permissions")
	errGraphQLDataMalformed     = errors.New("graphql_data_malformed: Engine returned malformed GraphQL data; retry or check Engine logs")
	errGraphQLResourceNotFound  = errors.New("resource_not_found: resource was not found; use its name, slug, email, or full UUID")
	errGraphQLResourceAmbiguous = errors.New("resource_ambiguous: name exists as both an SDK and MCP server; use the full UUID")
)

const (
	graphQLCodeResourceNotFound  = "FUSED_RESOURCE_NOT_FOUND"
	graphQLCodeResourceAmbiguous = "FUSED_RESOURCE_AMBIGUOUS"
)

type ClientOptions struct {
	Context         context.Context
	Timeout         time.Duration
	RequestID       string
	DisableProgress bool
}

type requestTransport struct {
	base      http.RoundTripper
	ctx       context.Context
	requestID string
}

func (t *requestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cleanup := mergeRequestContext(req.Context(), t.ctx)
	request := req.Clone(ctx)
	if t.requestID != "" {
		request.Header.Set("X-Request-ID", t.requestID)
	}
	resp, err := t.base.RoundTrip(request)
	if err != nil {
		cleanup()
		return nil, err
	}
	// The request context must remain live while callers consume streaming
	// response bodies; closing the body is the HTTP lifecycle boundary.
	resp.Body = &cleanupReadCloser{ReadCloser: resp.Body, cleanup: cleanup}
	return resp, nil
}

type cleanupReadCloser struct {
	io.ReadCloser
	cleanup func()
}

func (c *cleanupReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cleanup()
	return err
}

func mergeRequestContext(requestCtx, executionCtx context.Context) (context.Context, func()) {
	if executionCtx == nil {
		return requestCtx, func() {}
	}
	ctx, cancel := context.WithCancel(requestCtx)
	stop := context.AfterFunc(executionCtx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

// doRequest executes an HTTP request while showing a spinner to the user on stderr.
func (c *Client) doRequest(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error

	action := func() {
		resp, err = c.HTTP.Do(req)
	}

	fi, _ := os.Stderr.Stat()
	isTTY := (fi.Mode() & os.ModeCharDevice) != 0

	if c.showProgress && isTTY {
		_ = spinner.New().Title("Working...").Output(os.Stderr).Action(action).Run()
	} else {
		action()
	}

	return resp, err
}

func NewClient(baseURL, apiKey string) *Client {
	return NewClientWithOptions(baseURL, apiKey, ClientOptions{})
}

func NewClientWithOptions(baseURL, apiKey string, opts ClientOptions) *Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		BaseURL:      baseURL,
		APIKey:       apiKey,
		showProgress: !opts.DisableProgress,
		HTTP: &http.Client{
			Timeout: timeout,
			Transport: &requestTransport{
				base:      http.DefaultTransport,
				ctx:       opts.Context,
				requestID: opts.RequestID,
			},
		},
	}
}

func (c *Client) GraphQL(query string, variables map[string]interface{}, out interface{}) error {
	return c.graphQLAt("/graphql", query, variables, out)
}

// EngineGraphQL calls the Engine-native GraphQL endpoint used by the UI for
// workspace reads. Keeping it separate from Registry GraphQL prevents CLI read
// commands from accidentally proxying Engine-owned bucket state through the
// catalogue surface.
func (c *Client) EngineGraphQL(query string, variables map[string]interface{}, out interface{}) error {
	return c.graphQLAt("/engine/graphql", query, variables, out)
}

func (c *Client) graphQLAt(path string, query string, variables map[string]interface{}, out interface{}) error {
	payload := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", c.BaseURL+path, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("graphql request failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}

	return decodeGraphQLData(respBody, out)
}

func decodeGraphQLData(respBody []byte, out interface{}) error {
	var graphqlResp struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &graphqlResp); err != nil {
		return errGraphQLResponseMalformed
	}
	if len(graphqlResp.Errors) > 0 {
		// GraphQL messages are remote input and can echo submitted credentials.
		// Only fixed extension codes select more useful, locally-authored errors.
		return safeGraphQLRequestError(graphqlResp.Errors[0].Extensions.Code)
	}
	if err := json.Unmarshal(graphqlResp.Data, out); err != nil {
		return errGraphQLDataMalformed
	}
	return nil
}

func safeGraphQLRequestError(code string) error {
	switch code {
	case graphQLCodeResourceNotFound:
		return errGraphQLResourceNotFound
	case graphQLCodeResourceAmbiguous:
		return errGraphQLResourceAmbiguous
	default:
		return errGraphQLRequestRejected
	}
}

type PermissionRequirement struct {
	Permission   string `json:"permission"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	DisplayName  string `json:"display_name,omitempty"`
}

type apiErrorPayload struct {
	Error   json.RawMessage         `json:"error"`
	Missing []PermissionRequirement `json:"missing"`
}

type engineAPIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Category  string `json:"category"`
	Retryable bool   `json:"retryable"`
	Details   struct {
		Missing []string `json:"missing"`
	} `json:"details,omitempty"`
	Remediation string `json:"remediation,omitempty"`
	TraceID     string `json:"trace_id,omitempty"`
}

func formatHTTPErrorBody(status int, respBody []byte) string {
	var payload apiErrorPayload
	if err := json.Unmarshal(respBody, &payload); err == nil {
		var structured engineAPIError
		if len(payload.Error) > 0 && json.Unmarshal(payload.Error, &structured) == nil && structured.Code != "" && structured.Message != "" {
			return formatEngineAPIError(structured)
		}

		var errorCode string
		_ = json.Unmarshal(payload.Error, &errorCode)
		if status == http.StatusUnauthorized && errorCode == "authentication_required" {
			return "authentication required; provide a valid Fused credential"
		}
		if status == http.StatusForbidden && errorCode == "permission_denied" {
			return formatPermissionDenied(payload.Missing)
		}
		if message := artifactOwnerHTTPError(errorCode); message != "" {
			return message
		}
	}
	// Response bodies are untrusted and may echo credentials. Callers already
	// include the operation and status, so a status-based message stays useful
	// without copying remote text into returned errors or telemetry.
	return genericHTTPError(status)
}

func formatEngineAPIError(engineError engineAPIError) string {
	message := engineError.Code + ": " + engineError.Message
	if len(engineError.Details.Missing) > 0 {
		message += " Missing: " + strings.Join(engineError.Details.Missing, ", ") + "."
	}
	if engineError.Remediation != "" {
		message += " " + engineError.Remediation
	}
	if engineError.TraceID != "" {
		message += " Trace: " + engineError.TraceID
	}
	return message
}

func artifactOwnerHTTPError(code string) string {
	switch code {
	case "artifact owner is immutable":
		return "artifact_owner_immutable: this artifact already has an owner; omit --owner-team or use its existing team slug"
	case "artifact owner is unavailable":
		return "artifact_owner_unavailable: ask a workspace administrator for help"
	case "owner team was not found or is archived":
		return "owner_team_unavailable: choose an active team slug"
	case "artifact owner authorization denied":
		return "owner_team_access_denied: you or the owning team no longer have the required access"
	default:
		return ""
	}
}

func genericHTTPError(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "request_rejected: Engine rejected the request; check command inputs"
	case http.StatusUnauthorized:
		return "authentication_failed: provide a valid Fused credential"
	case http.StatusForbidden:
		return "request_forbidden: check workspace permissions"
	case http.StatusNotFound:
		return "resource_not_found: the requested resource was not found"
	case http.StatusConflict:
		return "request_conflict: workspace state changed; refresh and retry"
	case http.StatusTooManyRequests:
		return "request_rate_limited: retry later"
	default:
		return "engine_request_failed: check Engine logs and retry"
	}
}

func formatPermissionDenied(missing []PermissionRequirement) string {
	if len(missing) == 0 {
		return "permission denied; ask a workspace administrator for access"
	}
	if containsPermission(missing, "access.manage") {
		return "permission denied; you are not a member of the owning team; join that team or ask an access administrator to perform this action"
	}
	items := make([]string, 0, len(missing))
	for _, requirement := range missing {
		if !requirement.valid() {
			continue
		}
		items = append(items, requirement.ProductDescription())
	}
	if len(items) == 0 {
		return "permission denied; ask a workspace administrator for access"
	}
	return "permission denied; ask a workspace administrator to allow you to " + strings.Join(items, "; ")
}

func containsPermission(requirements []PermissionRequirement, permission string) bool {
	for _, requirement := range requirements {
		if safeAuthorizationValue(requirement.Permission) == permission {
			return true
		}
	}
	return false
}

func (requirement PermissionRequirement) valid() bool {
	return safeAuthorizationValue(requirement.Permission) != "" &&
		safeAuthorizationValue(requirement.ResourceType) != "" &&
		safeAuthorizationValue(requirement.ResourceID) != ""
}

func (requirement PermissionRequirement) Description() string {
	resource := safeAuthorizationValue(requirement.ResourceType)
	if displayName := safeAuthorizationValue(requirement.DisplayName); displayName != "" {
		resource += fmt.Sprintf(" %q", displayName)
	}
	if resourceID := safeAuthorizationValue(requirement.ResourceID); resourceID != "" {
		resource += " (" + resourceID + ")"
	}
	return safeAuthorizationValue(requirement.Permission) + " on " + strings.TrimSpace(resource)
}

// ProductDescription is the normal CLI wording. Description retains the
// exact permission and resource IDs for JSON/advanced diagnostics only.
func (requirement PermissionRequirement) ProductDescription() string {
	return permissionAction(requirement.Permission) + " " + requirement.productResource()
}

func (requirement PermissionRequirement) productResource() string {
	resourceType := safeAuthorizationValue(requirement.ResourceType)
	displayName := safeAuthorizationValue(requirement.DisplayName)
	if displayName != "" {
		return resourceType + " " + fmt.Sprintf("%q", displayName)
	}
	switch resourceType {
	case "workspace":
		return "this workspace"
	case "service", "bucket", "artifact":
		return "the selected " + resourceType
	default:
		return "the requested resource"
	}
}

var productPermissionActions = map[string]string{
	"workspace.read":         "view",
	"workspace.update":       "change",
	"service.read":           "view",
	"service.consume":        "use",
	"service.manage":         "manage",
	"bucket.read":            "view",
	"bucket.values.read":     "view",
	"bucket.use":             "use",
	"bucket.manage":          "manage",
	"artifact.read":          "view",
	"artifact.create":        "create an SDK, MCP server, or webhook in",
	"artifact.manage":        "manage",
	"artifact.tokens.manage": "manage",
	"connection.read":        "view connections in",
	"connection.manage":      "manage connections in",
	"access.read":            "view access activity for",
	"audit.read":             "view access activity for",
	"access.manage":          "manage team access for",
}

func permissionAction(permission string) string {
	if action := productPermissionActions[safeAuthorizationValue(permission)]; action != "" {
		return action
	}
	return "complete this action for"
}

func safeAuthorizationValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || strings.Contains(strings.ToLower(value), "fsk_") {
		return ""
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return ""
		}
	}
	return value
}

// HealthStatus is the Engine's GET /health response shape.
type HealthStatus struct {
	Status      string `json:"status"`
	Plane       string `json:"plane"`
	Environment string `json:"environment"`
}

// Health calls the Engine's GET /health and returns its parsed status,
// including the --environment observability label (Task 8,
// engine_workspace_registration_plan.md) so callers like `workspace apply`'s
// production warning can react to it. Any error here (network failure,
// non-2xx, older Engine without the environment field decoding to "") is
// meant to be treated by callers as "couldn't determine environment" rather
// than a hard failure -- this is a plain health probe, not a critical path.
func (c *Client) Health() (*HealthStatus, error) {
	req, err := http.NewRequest("GET", c.BaseURL+"/health", nil)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("health check failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}

	var out HealthStatus
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Models

type Service struct {
	ID       string                   `json:"id"`
	Name     string                   `json:"name"`
	Slug     string                   `json:"slug"`
	Provider *ServiceProviderIdentity `json:"provider"`
	IsOwner  bool                     `json:"is_owner"`
	IsPublic bool                     `json:"is_public"`
}

// DisplaySlug returns the account-scoped reference accepted by subsequent
// CLI commands. A foreign public service must retain its provider qualifier;
// a service owned by the caller can use its bare slug.
func (s Service) DisplaySlug() string {
	if s.IsOwner || s.Provider == nil || s.Provider.Handle == "" {
		return s.Slug
	}
	return "@" + s.Provider.Handle + "/" + s.Slug
}

// ServiceProviderIdentity keeps the Registry's public ownership projection
// separate from its private account model. The CLI needs only the stable handle
// to construct an unambiguous service reference.
type ServiceProviderIdentity struct {
	Handle string `json:"handle"`
}

type ServiceVisibility struct {
	ServiceID string                   `json:"id"`
	IsOwner   bool                     `json:"is_owner"`
	IsPublic  bool                     `json:"is_public"`
	Slug      string                   `json:"slug"`
	Provider  *ServiceProviderIdentity `json:"provider"`
	// RateLimit/RetryConfig/Pagination/EventExtractionPath/IncomingWebhookConfig
	// are the provider-declared execution policy already published to the
	// Registry (via execution_policy.public: true), if any -- independent of
	// is_owner/is_public. Sync uses these together with IsOwner to round-trip
	// execution_policy.public back into workspace.yaml.
	RateLimit   *ServiceRateLimit   `json:"rate_limit"`
	RetryConfig *ServiceRetryConfig `json:"retry_config"`
	Pagination  *ServicePagination  `json:"pagination"`
	// BaseURLOverride is nil unless this service's execution_policy has
	// published a base_url override -- distinct from the general "base_url"
	// GraphQL field (not fetched here) which merges override-if-set-else-spec-derived
	// and can't tell sync whether the value came from execution_policy.
	BaseURLOverride       *string                       `json:"base_url_override"`
	EventExtractionPath   *string                       `json:"event_extraction_path"`
	IncomingWebhookConfig *ServiceIncomingWebhookConfig `json:"incoming_webhook_config"`
}

type ServiceRateLimit struct {
	Strategy          string `json:"strategy"`
	RequestsPerSecond int    `json:"requests_per_second"`
	RequestsPerMinute int    `json:"requests_per_minute"`
}

type ServiceRetryConfig struct {
	Strategy   string `json:"strategy"`
	MaxRetries int    `json:"max_retries"`
	BackoffMs  int    `json:"backoff_ms"`
}

type ServicePagination struct {
	Type         string `json:"type"`
	RequestParam string `json:"request_param"`
	ResponsePath string `json:"response_path"`
}

// ServiceIncomingWebhookConfig is the provider's webhook verification recipe
// -- auth mechanism + where to find the signature. It deliberately has no
// secret field: the Registry never publishes signing secrets (see
// plans/plan-service-config-restructure.md item 3), only the non-secret
// shape of how to verify a delivery.
type ServiceIncomingWebhookConfig struct {
	AuthType            string   `json:"auth_type"`
	AuthLocation        string   `json:"auth_location"`
	AuthKeyName         string   `json:"auth_key_name"`
	SignatureHeader     string   `json:"signature_header"`
	VerificationHeaders []string `json:"verification_headers"`
}

// ConnectionProfileRevision is the immutable Registry result printed by the
// provider publication command. It intentionally excludes the profile body.
type ConnectionProfileRevision struct {
	ProfileID        string `json:"profile_id"`
	ServiceID        string `json:"service_id"`
	ServiceVersionID string `json:"service_version_id"`
	Revision         int    `json:"revision"`
	ProfileHash      string `json:"profile_hash"`
	Provenance       string `json:"provenance"`
}

type Integration struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	Method      string `json:"method"`
	Description string `json:"description"`
	ServiceID   string `json:"service_id"`
}

type WorkspaceService struct {
	ID               string                    `json:"id"`
	WorkspaceID      string                    `json:"workspace_id"`
	ServiceID        string                    `json:"service_id"`
	ServiceVersionID string                    `json:"service_version_id"`
	Version          string                    `json:"version"`
	EnabledVersions  []WorkspaceServiceVersion `json:"enabled_versions"`
	ServiceName      string                    `json:"service_name"`
	// ServiceSlug is what `service <slug> show` / `workspace service <slug>
	// operations` actually expect -- the Engine resolves it fresh from the
	// Registry on every list call, so it may be empty if that lookup failed
	// (Registry unreachable), not just if the service genuinely has none.
	// Already provider-qualified ("@provider/slug") for services this
	// account doesn't own, since a bare slug is only unique per-account.
	ServiceSlug string `json:"service_slug"`
	AddedBy     string `json:"added_by"`
	CreatedAt   string `json:"created_at"`
}

type WorkspaceServiceVersion struct {
	ID               string `json:"id"`
	ServiceVersionID string `json:"service_version_id"`
	Version          string `json:"version"`
	Status           string `json:"status"`
	CreatedAt        string `json:"created_at"`
	EnabledAt        string `json:"enabled_at"`
}

// WorkspaceConnectProfile is the secret-free attachment snapshot returned by
// Engine GraphQL for declarative workspace reconstruction.
type WorkspaceConnectProfile struct {
	ServiceVersionID  string `json:"service_version_id"`
	AuthType          string `json:"auth_type"`
	RegistryProfileID string `json:"registry_profile_id"`
	Provenance        string `json:"provenance"`
	// IsPublic mirrors whether this profile was published to the Registry via
	// connection_profiles[*].public: true, so sync can round-trip that intent
	// back into workspace.yaml instead of dropping it on the next sync.
	IsPublic bool                   `json:"is_public"`
	Profile  map[string]interface{} `json:"profile"`
}

// WorkspaceConnectConfig is the bucket-scoped, masked connect state consumed
// by workspace sync; encrypted OAuth app credentials are presence flags only.
type WorkspaceConnectConfig struct {
	BucketID        string                    `json:"bucket_id"`
	BucketName      string                    `json:"bucket_name"`
	ServiceID       string                    `json:"service_id"`
	AuthType        string                    `json:"auth_type"`
	Enabled         bool                      `json:"enabled"`
	RedirectURI     string                    `json:"redirect_uri"`
	HasClientID     bool                      `json:"has_client_id"`
	HasClientSecret bool                      `json:"has_client_secret"`
	Profiles        []WorkspaceConnectProfile `json:"profiles"`
	Injections      []InjectionConfig         `json:"injections,omitempty"`
}

type InjectionConfig struct {
	Value    string `json:"value"`
	Location string `json:"location"`
	Name     string `json:"name"`
	Mode     string `json:"mode,omitempty"`
}

func (c *Client) ListWorkspaceServices(names ...string) ([]WorkspaceService, error) {
	query := `
		query WorkspaceServices($names: [String]) {
			workspaceServices(names: $names) {
				id
				workspace_id
				service_id
				service_version_id
				version
				service_name
				service_slug
				added_by
				created_at
				enabled_versions { id service_version_id version status created_at enabled_at }
			}
		}
	`
	var resp struct {
		Services []WorkspaceService `json:"workspaceServices"`
	}
	// Why: service listing is a read path, and Engine GraphQL now exposes the
	// same version/slug enrichment REST used to provide without client-side joins.
	err := c.EngineGraphQL(query, map[string]interface{}{"names": names}, &resp)
	return resp.Services, err
}

// ListWorkspaceConnectConfigs uses the Engine GraphQL read model so CLI sync
// never bypasses the product's authenticated GraphQL boundary.
func (c *Client) ListWorkspaceConnectConfigs() ([]WorkspaceConnectConfig, error) {
	query := `
		query WorkspaceConnectConfigs {
			workspaceConnectConfigs {
				bucket_id
				bucket_name
				service_id
				auth_type
				enabled
				redirect_uri
				has_client_id
				has_client_secret
				profiles { service_version_id auth_type registry_profile_id provenance is_public profile }
			}
		}
	`
	var resp struct {
		Configs []WorkspaceConnectConfig `json:"workspaceConnectConfigs"`
	}
	err := c.EngineGraphQL(query, nil, &resp)
	return resp.Configs, err
}

// WorkspaceWebhook is one registered webhook for a workspace service --
// visibility-only (Task 8 of engine_owned_webhooks_plan.md), so it
// deliberately has no signing-secret field to decode into: the server never
// sends it back.
type WorkspaceWebhook struct {
	Label     string `json:"label"`
	Slug      string `json:"slug"`
	CreatedAt string `json:"created_at"`
	// Signature is "set" or "none" -- the server never returns the secret_ref
	// itself here, only whether one is configured (see webhookSignatureStatus,
	// engine-release/internal/engine/api/connect_graphql.go).
	Signature string `json:"signature"`
}

// ListWorkspaceWebhooks looks up every webhook registration for one workspace
// service, so a user can find a registration's URL again without re-running
// workspace apply just to see the printout it produced once at apply time.
func (c *Client) ListWorkspaceWebhooks(serviceID string) ([]WorkspaceWebhook, error) {
	query := `
		query WorkspaceWebhooks($serviceId: String!) {
			workspaceWebhooks(service_id: $serviceId) { label slug created_at signature }
		}
	`
	var resp struct {
		Webhooks []WorkspaceWebhook `json:"workspaceWebhooks"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"serviceId": serviceID}, &resp)
	return resp.Webhooks, err
}

type ConnectSessionStartResponse struct {
	AuthorizeURL string `json:"authorize_url"`
	ExpiresAt    string `json:"expires_at"`
}

type ConnectionResource struct {
	ID                 string   `json:"id"`
	ConnectionID       string   `json:"connection_id"`
	ServiceID          string   `json:"service_id"`
	ProviderResourceID string   `json:"provider_resource_id"`
	ResourceType       string   `json:"resource_type"`
	DisplayName        string   `json:"display_name"`
	BaseURL            string   `json:"base_url"`
	Scopes             []string `json:"scopes"`
	IsDefault          bool     `json:"is_default"`
	CreatedAt          string   `json:"created_at"`
	UpdatedAt          string   `json:"updated_at"`
}

// ListConnectionResources reads the already-filtered Engine resource list;
// provider discovery bodies and token material never reach the CLI.
func (c *Client) ListConnectionResources(connectionID string) ([]ConnectionResource, error) {
	query := `query ConnectionResources($connectionId: String!) {
		connectionResources(connection_id: $connectionId) {
			id connection_id service_id provider_resource_id resource_type display_name base_url scopes is_default created_at updated_at
		}
	}`
	var response struct {
		Resources []ConnectionResource `json:"connectionResources"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"connectionId": connectionID}, &response)
	return response.Resources, err
}

// SetDefaultConnectionResource uses the audited GraphQL mutation shared with
// UI so all user-triggered default changes follow one ownership path.
func (c *Client) SetDefaultConnectionResource(connectionID, resourceID string) (*ConnectionResource, error) {
	query := `mutation SetDefaultConnectionResource($connectionId: String!, $resourceId: String!) {
		setDefaultConnectionResource(connection_id: $connectionId, resource_id: $resourceId) {
			id connection_id service_id provider_resource_id resource_type display_name base_url scopes is_default created_at updated_at
		}
	}`
	var response struct {
		Resource ConnectionResource `json:"setDefaultConnectionResource"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"connectionId": connectionID, "resourceId": resourceID}, &response)
	if err != nil {
		return nil, err
	}
	return &response.Resource, nil
}

// RediscoverConnectionResources invokes the audited Engine lifecycle action and
// returns only the reconciled non-secret resource projection.
func (c *Client) RediscoverConnectionResources(connectionID string) ([]ConnectionResource, error) {
	query := `mutation RediscoverConnectionResources($connectionId: String!) {
		rediscoverConnectionResources(connection_id: $connectionId) {
			id connection_id service_id provider_resource_id resource_type display_name base_url scopes is_default created_at updated_at
		}
	}`
	var response struct {
		Resources []ConnectionResource `json:"rediscoverConnectionResources"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"connectionId": connectionID}, &response)
	return response.Resources, err
}

// StartConnectSession calls the Engine's bucket-scoped connect route so CLI
// onboarding and scope reduction use the same policy as generated SDKs.
func (c *Client) StartConnectSession(bucketID, serviceID, endUserRef, createdByArtifactID string, resourceInput map[string]string, scopes []string) (*ConnectSessionStartResponse, error) {
	query := `
		mutation StartConnectSession($bucketId: String!, $serviceId: String!, $endUserRef: String!, $createdByArtifactId: String, $resourceInput: EngineJSON, $scopes: [String!]) {
			startConnectSession(bucket_id: $bucketId, service_id: $serviceId, end_user_ref: $endUserRef, created_by_artifact_id: $createdByArtifactId, resource_input: $resourceInput, scopes: $scopes) {
				authorize_url
				expires_at
			}
		}
	`
	vars := map[string]interface{}{
		"bucketId":   bucketID,
		"serviceId":  serviceID,
		"endUserRef": endUserRef,
	}
	if strings.TrimSpace(createdByArtifactID) != "" {
		vars["createdByArtifactId"] = createdByArtifactID
	}
	if len(resourceInput) > 0 {
		vars["resourceInput"] = resourceInput
	}
	if len(scopes) > 0 {
		vars["scopes"] = scopes
	}

	var resp struct {
		StartConnectSession ConnectSessionStartResponse `json:"startConnectSession"`
	}

	err := c.EngineGraphQL(query, vars, &resp)
	if err != nil {
		return nil, err
	}

	return &resp.StartConnectSession, nil
}

func (c *Client) ServiceVisibilities(serviceIDs []string) (map[string]ServiceVisibility, error) {
	out := map[string]ServiceVisibility{}
	if len(serviceIDs) == 0 {
		return out, nil
	}
	query := `
		query ServiceVisibilities($serviceIds: [String!]!) {
			servicesByIds(serviceIds: $serviceIds) {
				id
				slug
					provider { handle }
				is_owner
				is_public
				rate_limit { strategy requests_per_second requests_per_minute }
				retry_config { strategy max_retries backoff_ms }
				pagination { type request_param response_path }
				event_extraction_path
				incoming_webhook_config { auth_type auth_location auth_key_name signature_header verification_headers }
				base_url_override
			}
		}
	`
	var resp struct {
		ServicesByIDs []ServiceVisibility `json:"servicesByIds"`
	}
	if err := c.GraphQL(query, map[string]interface{}{"serviceIds": serviceIDs}, &resp); err != nil {
		return nil, err
	}
	for _, svc := range resp.ServicesByIDs {
		out[svc.ServiceID] = svc
	}
	return out, nil
}

type ServiceVersion struct {
	ID        string `json:"id"`
	ServiceID string `json:"service_id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	// IsPublic/RateLimit/RetryConfig mirror ServiceVisibility's identically
	// named fields, but scoped to this one version rather than the service as
	// a whole. Sync uses these to round-trip version_policies[*].public and
	// version_policies[*].execution_policy back into workspace.yaml.
	IsPublic    bool                `json:"is_public"`
	RateLimit   *ServiceRateLimit   `json:"rate_limit"`
	RetryConfig *ServiceRetryConfig `json:"retry_config"`
	Pagination  *ServicePagination  `json:"pagination"`
	// BaseURLOverride mirrors ServiceVisibility.BaseURLOverride, scoped to
	// this version.
	BaseURLOverride       *string                       `json:"base_url_override"`
	EventExtractionPath   *string                       `json:"event_extraction_path"`
	IncomingWebhookConfig *ServiceIncomingWebhookConfig `json:"incoming_webhook_config"`
}

type ServiceServer struct {
	URL         string `json:"url"`
	Description string `json:"description"`
}

type AuthConfig struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Scheme   string `json:"scheme"`
	Location string `json:"location"`
	KeyName  string `json:"key_name"`
}

type ServiceInfo struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Slug        string          `json:"slug"`
	BaseURL     string          `json:"base_url"`
	Servers     []ServiceServer `json:"servers"`
	AuthConfigs []AuthConfig    `json:"auth_configs"`
	// Provider/IsOwner mirror ServiceVisibility -- Provider is only ever set
	// (by the Registry) for a service this account doesn't own. Slug is bare
	// regardless of ownership (slugs are only unique per-account), so
	// DisplaySlug is what callers should print back to a user, not Slug
	// directly.
	Provider *ServiceProviderIdentity `json:"provider"`
	IsOwner  bool                     `json:"is_owner"`
}

// DisplaySlug returns what a user should be shown/type back for this
// service: the bare slug if they own it, "@provider/slug" otherwise. Safe to
// call unconditionally here (unlike the Engine's workspace-service listing,
// which needs an explicit is_public check) because `service <slug> show`
// only ever returns a non-owned service when it's public in the first place
// -- the Registry's own resolveAccountScopedService already gates on that
// before this struct is ever populated.
func (s *ServiceInfo) DisplaySlug() string {
	if s.IsOwner || s.Provider == nil || s.Provider.Handle == "" {
		return s.Slug
	}
	return "@" + s.Provider.Handle + "/" + s.Slug
}

func (c *Client) GetServiceLatestVersion(serviceSlug string) (string, error) {
	query := `
		query GetServiceLatestVersion($id: String!, $provider: String) {
			service(id: $id, provider: $provider) {
				service_versions(limit: 1) {
					name
				}
			}
		}
	`
	var resp struct {
		Service *struct {
			ServiceVersions []struct {
				Name string `json:"name"`
			} `json:"service_versions"`
		} `json:"service"`
	}
	slug, provider := splitProviderQualifiedServiceRef(serviceSlug)
	vars := map[string]interface{}{
		"id":       slug,
		"provider": provider,
	}
	if err := c.GraphQL(query, vars, &resp); err != nil {
		return "", err
	}
	if resp.Service == nil {
		return "", fmt.Errorf("service not found: %s", serviceSlug)
	}
	if len(resp.Service.ServiceVersions) == 0 {
		return "", fmt.Errorf("no versions found for service %s", serviceSlug)
	}
	return resp.Service.ServiceVersions[0].Name, nil
}

func (c *Client) GetServiceInfo(serviceSlug string) (*ServiceInfo, error) {
	query := `
		query GetServiceInfo($id: String!, $provider: String) {
			service(id: $id, provider: $provider) {
				id
				name
				slug
				base_url
					provider { handle }
				is_owner
				servers {
					url
					description
				}
				auth_configs {
					name
					type
					scheme
					location
					key_name
				}
			}
		}
	`
	var resp struct {
		Service *ServiceInfo `json:"service"`
	}
	slug, provider := splitProviderQualifiedServiceRef(serviceSlug)
	err := c.GraphQL(query, map[string]interface{}{"id": slug, "provider": provider}, &resp)
	return resp.Service, err
}

func (c *Client) ServiceVersions(serviceSlug string) ([]ServiceVersion, error) {
	query := `
		query ServiceVersions($serviceId: String!, $provider: String) {
			serviceVersions(serviceId: $serviceId, provider: $provider) {
				id
				service_id
				name
				status
				created_at
				is_public
				rate_limit { strategy requests_per_second requests_per_minute }
				retry_config { strategy max_retries backoff_ms }
				pagination { type request_param response_path }
				event_extraction_path
				incoming_webhook_config { auth_type auth_location auth_key_name signature_header verification_headers }
				base_url_override
			}
		}
	`
	var resp struct {
		ServiceVersions []ServiceVersion `json:"serviceVersions"`
	}
	slug, provider := splitProviderQualifiedServiceRef(serviceSlug)
	err := c.GraphQL(query, map[string]interface{}{"serviceId": slug, "provider": provider}, &resp)
	return resp.ServiceVersions, err
}

// SetConnectionProfile appends an owner-authorized immutable provider profile
// revision. Registry owns provenance, visibility, and stream identity.
func (c *Client) SetConnectionProfile(serviceID, serviceVersionID, name string, profile map[string]interface{}) (*ConnectionProfileRevision, error) {
	query := `mutation SetConnectionProfile($serviceId: String!, $serviceVersionId: String!, $name: String!, $config: JSON!) {
		setConnectionProfile(service_id: $serviceId, service_version_id: $serviceVersionId, name: $name, config: $config) {
			profile_id service_id service_version_id revision profile_hash provenance
		}
	}`
	var response struct {
		Profile *ConnectionProfileRevision `json:"setConnectionProfile"`
	}
	if err := c.GraphQL(query, map[string]interface{}{
		"serviceId": serviceID, "serviceVersionId": serviceVersionID, "name": name, "config": profile,
	}, &response); err != nil {
		return nil, err
	}
	return response.Profile, nil
}

func splitProviderQualifiedServiceRef(ref string) (string, string) {
	if !strings.HasPrefix(ref, "@") {
		return ref, ""
	}
	rest := ref[1:]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[i+1:], rest[:i]
	}
	return ref, ""
}

func (c *Client) SearchServices(q string) ([]Service, error) {
	query := `
		query SearchServices($q: String!) {
			searchServices(q: $q) {
				id
				name
				slug
				provider { handle }
				is_owner
				is_public
			}
		}
	`
	var resp struct {
		SearchServices []Service `json:"searchServices"`
	}
	err := c.GraphQL(query, map[string]interface{}{"q": q}, &resp)
	return resp.SearchServices, err
}

func (c *Client) SearchEndpoints(serviceID, version, q string) ([]Integration, error) {
	return c.SearchEndpointsPage(serviceID, version, q, PageOptions{})
}

func (c *Client) SearchEndpointsPage(serviceID, version, q string, opts PageOptions) ([]Integration, error) {
	query := `
		query SearchEndpoints($serviceId: String!, $version: String, $q: String!, $limit: Int, $offset: Int) {
			searchEndpoints(serviceId: $serviceId, version: $version, q: $q, limit: $limit, offset: $offset) {
				id
				name
				path
				method
				description
				service_id
			}
		}
	`
	var resp struct {
		SearchEndpoints []Integration `json:"searchEndpoints"`
	}
	err := c.GraphQL(query, map[string]interface{}{"serviceId": serviceID, "version": version, "q": q, "limit": normalLimit(opts.Limit), "offset": normalOffset(opts.Offset)}, &resp)
	return resp.SearchEndpoints, err
}

func (c *Client) ServiceOperations(serviceID, version string) ([]Integration, error) {
	query := `
		query ServiceOperations($serviceId: String!, $version: String) {
			serviceOperations(serviceId: $serviceId, version: $version) {
				id
				name
				path
				method
				description
				service_id
			}
		}
	`
	var resp struct {
		ServiceOperations []Integration `json:"serviceOperations"`
	}
	err := c.GraphQL(query, map[string]interface{}{"serviceId": serviceID, "version": version}, &resp)
	return resp.ServiceOperations, err
}

type Webhook struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (c *Client) FetchWebhooks(serviceID, version string) ([]Webhook, error) {
	query := `
		query FetchWebhooks($id: String!) {
			service(id: $id) {
				webhooks {
					id
					name
					description
				}
			}
		}
	`
	var resp struct {
		Service struct {
			Webhooks []Webhook `json:"webhooks"`
		} `json:"service"`
	}
	err := c.GraphQL(query, map[string]interface{}{"id": serviceID}, &resp)
	return resp.Service.Webhooks, err
}

type IntentService struct {
	Name          string `json:"name"`
	EndpointQuery string `json:"endpoint_query"`
}

type IntentPayload struct {
	Services []IntentService `json:"services"`
}

func (c *Client) ParseSDKIntent(q string) (*IntentPayload, error) {
	query := `
		query ParseSDKIntent($q: String!) {
			parseSDKIntent(q: $q) {
				services {
					name
					endpoint_query
				}
			}
		}
	`
	var resp struct {
		ParseSDKIntent IntentPayload `json:"parseSDKIntent"`
	}
	err := c.GraphQL(query, map[string]interface{}{"q": q}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp.ParseSDKIntent, nil
}

type SDKSelection struct {
	ServiceID   string   `json:"service_id"`
	EndpointIDs []string `json:"endpoint_ids"`
}

type GenerateSDKRequest struct {
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Version        string         `json:"version"`
	TargetType     string         `json:"target_type"`
	TargetLanguage string         `json:"target_language,omitempty"`
	Selections     []SDKSelection `json:"selections"`
	SkipSandbox    bool           `json:"skip_sandbox"`
	UpgradeFrom    string         `json:"upgrade_from,omitempty"`
}

type GenerateSDKResponse struct {
	JobID string `json:"job_id"`
}

func (c *Client) GenerateSDK(reqBody GenerateSDKRequest) (*GenerateSDKResponse, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", c.BaseURL+"/sdks/generate", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to generate SDK (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}

	var out GenerateSDKResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

type SDKEvent struct {
	Type          string `json:"type"`
	Message       string `json:"message"`
	IntegrationID string `json:"integration_id,omitempty"`
}

func (c *Client) StreamSDKGenerationEvents(jobID string, eventChan chan<- SDKEvent, errChan chan<- error) {
	defer close(eventChan)
	defer close(errChan)
	req, err := http.NewRequest("GET", c.BaseURL+"/sdks/job/"+jobID+"/stream", nil)
	if err != nil {
		errChan <- err
		return
	}
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.doRequest(req)
	if err != nil {
		errChan <- err
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		errChan <- fmt.Errorf("stream failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
		return
	}

	if err := streamSDKEvents(resp.Body, eventChan); err != nil {
		errChan <- err
	}
}

func streamSDKEvents(reader io.Reader, eventChan chan<- SDKEvent) error {
	buf := make([]byte, 4096)
	var line []byte
	for {
		n, err := reader.Read(buf)
		line = processSDKEventBytes(buf[:n], line, eventChan)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func processSDKEventBytes(chunk []byte, line []byte, eventChan chan<- SDKEvent) []byte {
	for _, b := range chunk {
		if b == '\n' {
			emitSDKEvent(line, eventChan)
			line = line[:0]
			continue
		}
		line = append(line, b)
	}
	return line
}

func emitSDKEvent(line []byte, eventChan chan<- SDKEvent) {
	if !bytes.HasPrefix(line, []byte("data: ")) {
		return
	}
	data := bytes.TrimPrefix(line, []byte("data: "))
	var event SDKEvent
	if err := json.Unmarshal(data, &event); err == nil {
		eventChan <- event
	}
}

type SDKDetails struct {
	SandboxURL string `json:"sandbox_url"`
}

type GetSDKResponse struct {
	SDK SDKDetails `json:"sdk"`
}

func (c *Client) GetSDK(artifactID string) (*SDKDetails, error) {
	query := `
		query GetSDK($id: String!) {
			sdk(id: $id) {
				sandbox_url
			}
		}
	`
	var resp GetSDKResponse
	err := c.GraphQL(query, map[string]interface{}{"id": artifactID}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp.SDK, nil
}

type SDKBasicDetails struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Version    string `json:"version"`
	SandboxURL string `json:"sandbox_url"`
}

type GetSDKByNameResponse struct {
	SDK SDKBasicDetails `json:"sdkByName"`
}

func (c *Client) GetSDKByName(name string, version string) (*SDKBasicDetails, error) {
	query := `
		query GetSDKByName($name: String!, $version: String) {
			sdkByName(name: $name, version: $version) {
				id
				name
				version
				sandbox_url
			}
		}
	`
	var resp GetSDKByNameResponse
	err := c.GraphQL(query, map[string]interface{}{"name": name, "version": version}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp.SDK, nil
}

type SDKSelectionDetail struct {
	ServiceID          string   `json:"service_id"`
	ServiceName        string   `json:"service_name"`
	ServiceSlug        string   `json:"service_slug"`
	ServiceProvider    string   `json:"service_provider"`
	EndpointIDs        []string `json:"endpoint_ids"`
	WebhookIDs         []string `json:"webhook_ids"`
	SelectAll          bool     `json:"select_all"`
	ServiceVersionID   string   `json:"service_version_id"`
	ServiceVersionName string   `json:"service_version_name"`
}

type SDKWithSelections struct {
	ID                 string               `json:"id"`
	Version            string               `json:"version"`
	DetailedSelections []SDKSelectionDetail `json:"detailed_selections"`
}

// GetSDKSelectionsByName fetches the most recently generated SDK with the
// given name, including its full per-service selection detail. Used by
// `sdk sync` to reconstruct local sdk.yaml service entries. Passing an empty
// version to sdkByName resolves to the latest generated SDK -- there's no
// separate "latest" query; this is the documented way to get it.
func (c *Client) GetSDKSelectionsByName(name string) (*SDKWithSelections, error) {
	return c.GetSDKSelectionsByNameVersion(name, "")
}

func (c *Client) GetSDKSelectionsByNameVersion(name string, version string) (*SDKWithSelections, error) {
	query := `
		query GetSDKSelectionsByName($name: String!, $version: String) {
			sdkByName(name: $name, version: $version) {
				id
				version
				detailed_selections {
					service_id
					service_name
					service_slug
					service_provider
					endpoint_ids
					webhook_ids
					select_all
					service_version_id
					service_version_name
				}
			}
		}
	`
	var resp struct {
		SDK SDKWithSelections `json:"sdkByName"`
	}
	if err := c.GraphQL(query, map[string]interface{}{"name": name, "version": version}, &resp); err != nil {
		return nil, err
	}
	return &resp.SDK, nil
}

// GetSDKSelectionResourceNames resolves a service's selection on a generated
// SDK -- whether select_all or an explicit endpoint/webhook ID list -- into
// the operation names `sdk sync` writes to sdk.yaml. select_all has no local
// yaml representation, so it must always be enumerated explicitly.
func (c *Client) GetSDKSelectionResourceNames(artifactID, serviceID string) ([]string, error) {
	query := `
		query GetSDKSelectionResourceNames($artifactId: String!, $serviceId: String!) {
			sdkSelectionResources(artifactId: $artifactId, serviceId: $serviceId) {
				name
			}
		}
	`
	var resp struct {
		Resources []struct {
			Name string `json:"name"`
		} `json:"sdkSelectionResources"`
	}
	if err := c.GraphQL(query, map[string]interface{}{"artifactId": artifactID, "serviceId": serviceID}, &resp); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(resp.Resources))
	for _, r := range resp.Resources {
		names = append(names, r.Name)
	}
	return names, nil
}

func (c *Client) DownloadSDK(artifactID string) ([]byte, error) {
	req, err := http.NewRequest("GET", c.BaseURL+"/sdks/"+artifactID+"/download", nil)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}

	return io.ReadAll(resp.Body)
}

type SDKConfigPlanResponse struct {
	PlanID              string                  `json:"plan_id"`
	OwnerType           string                  `json:"owner_type"`
	ConfigKey           string                  `json:"config_key"`
	SourceHash          string                  `json:"source_hash"`
	Summary             map[string]interface{}  `json:"summary"`
	Notifications       NotificationInbox       `json:"notifications"`
	RequiredPermissions []PermissionRequirement `json:"required_permissions"`
}

type NotificationInbox struct {
	Items    []NotificationItem `json:"items"`
	Warnings []string           `json:"warnings"`
}

type NotificationItem struct {
	ID                  string        `json:"id"`
	Source              string        `json:"source"`
	Type                string        `json:"type"`
	Severity            string        `json:"severity"`
	Status              string        `json:"status"`
	ServiceID           string        `json:"service_id"`
	Version             string        `json:"version"`
	ConfigKey           string        `json:"config_key"`
	Message             string        `json:"message"`
	IntegrationObjectID string        `json:"integration_object_id"`
	WebhookObjectID     string        `json:"webhook_object_id"`
	DetectedAt          string        `json:"detected_at"`
	Diff                []interface{} `json:"diff"`
}

type ConfigPlanResponse struct {
	PlanID              string                  `json:"plan_id"`
	ConfigKey           string                  `json:"config_key"`
	SourceHash          string                  `json:"source_hash"`
	Summary             map[string]interface{}  `json:"summary"`
	RequiredPermissions []PermissionRequirement `json:"required_permissions"`
	// Notifications was previously absent from this struct -- kind: workspace
	// was the one plan response missing it entirely, unlike SDKConfigPlanResponse
	// (sdk/mcp) above. WorkspaceConfigPlanHandler now returns the same
	// "notifications" key, filtered to the services/versions this workspace
	// config declares (see plans/plan-service-changelog.md's "## Phase 4").
	Notifications NotificationInbox `json:"notifications"`
}

type ConfigApplyResponse struct {
	Status   string                 `json:"status"`
	PlanID   string                 `json:"plan_id"`
	Webhooks []AppliedWebhookConfig `json:"webhooks,omitempty"`
}

type ConnectMaterial struct {
	ClientID      string            `json:"client_id"`
	ClientSecret  string            `json:"client_secret"`
	BindingValues map[string]string `json:"binding_values,omitempty"`
}

type AuthMaterial struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
	Cert     string `json:"cert,omitempty"`
	Key      string `json:"key,omitempty"`
}

// AppliedWebhookConfig is one webhook registration the apply just created or
// refreshed. Slug is the opaque path segment only -- the CLI builds the full
// display URL itself (base URL + "/webhook/" + Slug + "-" + ServiceKey)
// since it already knows which Engine host it just called.
type AppliedWebhookConfig struct {
	ServiceKey string `json:"service_key"`
	Label      string `json:"label"`
	Slug       string `json:"slug"`
}

type ArtifactPlanIntent struct {
	SourceHash    string
	ConfigKey     string
	OwnerTeamSlug string
	Config        json.RawMessage
}

// PlanSDKConfig sends native SDK desired state through the shared artifact
// plan client while preserving the SDK-specific Engine route.
func (c *Client) PlanSDKConfig(intent ArtifactPlanIntent) (*SDKConfigPlanResponse, error) {
	return c.planArtifactConfig("sdk", intent)
}

// PlanMCPConfig plans an Engine runtime without invoking Registry generation.
func (c *Client) PlanMCPConfig(intent ArtifactPlanIntent) (*SDKConfigPlanResponse, error) {
	return c.planArtifactConfig("mcp", intent)
}

// PlanWebhookConfig plans a kind: webhook artifact through the same shared
// route pattern SDK/MCP use ("/webhook-config/plan"). The response has no
// notifications field (only workspace/SDK/MCP applies can affect other
// artifacts) -- SDKConfigPlanResponse.Notifications just decodes to its zero
// value, same as reusing this helper already does for any response shape
// that omits a field the shared struct declares.
func (c *Client) PlanWebhookConfig(intent ArtifactPlanIntent) (*SDKConfigPlanResponse, error) {
	return c.planArtifactConfig("webhook", intent)
}

// planArtifactConfig keeps SDK and MCP command behavior identical while the
// Engine routes each kind to its distinct executor.
func (c *Client) planArtifactConfig(kind string, intent ArtifactPlanIntent) (*SDKConfigPlanResponse, error) {
	reqBody := map[string]interface{}{
		"source_hash": intent.SourceHash,
		"config_key":  intent.ConfigKey,
		"config":      intent.Config,
	}
	// Omission makes the authenticated subject the owner. A team is only
	// selected explicitly, by stable slug, and apply never accepts an override.
	if intent.OwnerTeamSlug != "" {
		reqBody["owner_team"] = intent.OwnerTeamSlug
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", c.BaseURL+"/"+kind+"-config/plan", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("plan %s config failed (HTTP %d): %s", kind, resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}

	var out SDKConfigPlanResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) PlanWorkspaceConfig(sourceHash, configKey string, config json.RawMessage) (*ConfigPlanResponse, error) {
	reqBody := map[string]interface{}{
		"source_hash": sourceHash,
		"config_key":  configKey,
		"config":      config,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", c.BaseURL+"/workspace/config/plan", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("plan workspace config failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}

	var out ConfigPlanResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

type SDKConfigApplyResponse struct {
	Status     string `json:"status"`
	PlanID     string `json:"plan_id"`
	ArtifactID string `json:"artifact_id"`
	JobID      string `json:"job_id"`
}

type MCPConfigApplyResponse struct {
	Status         string `json:"status"`
	PlanID         string `json:"plan_id"`
	ConfigKey      string `json:"config_key"`
	MCPID          string `json:"artifact_id"`
	MCPURL         string `json:"mcp_url"`
	ExecutionToken string `json:"execution_token"`
}

// WebhookConfigApplyResponse mirrors webhookConfigApplyResponse
// (engine-release/internal/engine/api/webhook_config_handlers.go) -- kind: webhook
// produces no runtime/package/token, just the set of registrations it
// reconciled.
type WebhookConfigApplyResponse struct {
	Status        string                      `json:"status"`
	PlanID        string                      `json:"plan_id"`
	ConfigKey     string                      `json:"config_key"`
	Name          string                      `json:"name"`
	Registrations []WebhookConfigRegistration `json:"registrations"`
}

type WebhookConfigRegistration struct {
	Service string `json:"service"`
	Slug    string `json:"slug"`
}

func (c *Client) ApplySDKConfig(planID, sourceHash string) (*SDKConfigApplyResponse, error) {
	reqBody := map[string]interface{}{
		"plan_id":     planID,
		"source_hash": sourceHash,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", c.BaseURL+"/sdk-config/apply", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("apply SDK config failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}

	var out SDKConfigApplyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ApplyMCPConfig activates the resolved Engine scope and returns its plaintext
// execution token only when the runtime is first created.
func (c *Client) ApplyMCPConfig(planID, sourceHash string) (*MCPConfigApplyResponse, error) {
	reqBody := map[string]interface{}{"plan_id": planID, "source_hash": sourceHash}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", c.BaseURL+"/mcp-config/apply", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("apply mcp config failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}
	var out MCPConfigApplyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ApplyWebhookConfig reconciles a kind: webhook artifact's registrations.
// Unlike SDK/MCP there is no generated package or token -- the response is
// just the set of (service, slug) rows the Engine just wrote.
func (c *Client) ApplyWebhookConfig(planID, sourceHash string) (*WebhookConfigApplyResponse, error) {
	reqBody := map[string]interface{}{"plan_id": planID, "source_hash": sourceHash}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", c.BaseURL+"/webhook-config/apply", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("apply webhook config failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}
	var out WebhookConfigApplyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ApplyWorkspaceConfig(planID, sourceHash string, authMaterials map[string]AuthMaterial, profileMaterials map[string]ConnectMaterial, bucketSecretMaterials map[string]string) (*ConfigApplyResponse, error) {
	reqBody := map[string]interface{}{
		"plan_id":     planID,
		"source_hash": sourceHash,
	}
	if len(authMaterials) > 0 {
		reqBody["auth_materials"] = authMaterials
	}
	if len(profileMaterials) > 0 {
		reqBody["profile_materials"] = profileMaterials
	}
	if len(bucketSecretMaterials) > 0 {
		reqBody["bucket_secret_materials"] = bucketSecretMaterials
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", c.BaseURL+"/workspace/config/apply", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("apply workspace config failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}

	var out ConfigApplyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateWorkspacePlanAction(planID string, actions []map[string]any, actionID, decision string) error {
	updated := false
	for i := range actions {
		if actions[i]["id"] == actionID {
			actions[i]["decision"] = decision
			updated = true
			break
		}
	}
	if !updated {
		return fmt.Errorf("plan action %s not found", actionID)
	}
	reqBody := map[string]any{"actions": actions}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPatch, fmt.Sprintf("%s/config/plans/%s/actions", c.BaseURL, planID), bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update workspace plan action failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}

	return nil
}

// ─── Non-interactive spec import (fused import plan/apply) ─────────────────
//
// Unlike the sdk-config/workspace-config plan/apply pair above, these two
// hit the Registry's /integrations/import/* endpoints directly rather than
// an Engine-owned config endpoint -- but they still go through the same
// BaseURL (the Engine), since the Engine already proxies every
// /integrations/* path straight through to the Registry unchanged
// (RESTProxyMountPaths), so the CLI needs no separate Registry URL concept.

type SpecImportPlanRequest struct {
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	Version       string `json:"version,omitempty"`
	SourceURL     string `json:"source_url,omitempty"`
	SourceContent string `json:"source_content,omitempty"`
	// IsPublic is omitted from the request entirely when --public was not
	// passed, so the Registry can default it differently depending on
	// whether this targets a new service or a new version of an existing
	// one -- see resolveImportPlanIsPublic on the Registry side.
	IsPublic   *bool  `json:"is_public,omitempty"`
	TargetType string `json:"target_type,omitempty"`
	Category   string `json:"category,omitempty"`
}

type SpecImportDiff struct {
	Added        int      `json:"added"`
	Changed      int      `json:"changed"`
	Removed      int      `json:"removed"`
	ChangedNames []string `json:"changed_names"`
	RemovedNames []string `json:"removed_names"`
}

type SpecImportSDKUsage struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Version             string `json:"version"`
	UsesChangedEndpoint bool   `json:"uses_changed_endpoint"`
}

type SpecImportWorkspaceUsage struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SpecImportUsage struct {
	SDKs       []SpecImportSDKUsage       `json:"sdks"`
	Workspaces []SpecImportWorkspaceUsage `json:"workspaces"`
}

type SpecImportPlanResponse struct {
	PlanID        string           `json:"plan_id"`
	SourceHash    string           `json:"source_hash"`
	ServiceID     string           `json:"service_id"`
	Slug          string           `json:"slug"`
	Name          string           `json:"name"`
	IsNewService  bool             `json:"is_new_service"`
	TargetVersion string           `json:"target_version"`
	Action        string           `json:"action"`
	Diff          SpecImportDiff   `json:"diff"`
	Usage         *SpecImportUsage `json:"usage,omitempty"`
}

type SpecImportApplyRequest struct {
	PlanID     string `json:"plan_id"`
	SourceHash string `json:"source_hash"`
}

type SpecImportApplyResponse struct {
	Status       string `json:"status"`
	PlanID       string `json:"plan_id"`
	ServiceID    string `json:"service_id"`
	Slug         string `json:"slug"`
	IsNewService bool   `json:"is_new_service"`
	Action       string `json:"action"`
	Version      string `json:"version"`
	Revision     int    `json:"revision"`
}

// PlanSpecImport computes (but does not commit) a non-interactive spec
// import -- see Registry's importPlanHandler for what it parses/diffs/
// resolves.
func (c *Client) PlanSpecImport(req SpecImportPlanRequest) (*SpecImportPlanResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest("POST", c.BaseURL+"/integrations/import/plan", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("plan spec import failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}

	var out SpecImportPlanResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ApplySpecImport commits a plan produced by PlanSpecImport. sourceHash must
// match the plan's own recorded hash or the Registry rejects the call --
// same concurrency guard ApplySDKConfig/ApplyWorkspaceConfig already use.
func (c *Client) ApplySpecImport(planID, sourceHash string) (*SpecImportApplyResponse, error) {
	reqBody := SpecImportApplyRequest{PlanID: planID, SourceHash: sourceHash}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", c.BaseURL+"/integrations/import/apply", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("apply spec import failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}

	var out SpecImportApplyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DownloadGeneratedSDK(configKey string) ([]byte, error) {
	req, err := http.NewRequest("GET", c.BaseURL+"/sdk-config/"+configKey+"/download", nil)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download generated SDK failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}

	return io.ReadAll(resp.Body)
}

func (c *Client) DeactivateSDK(artifactID string) error {
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+"/sdk-config/"+artifactID+"/deactivate", nil)
	if err != nil {
		return err
	}
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("deactivate failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, b))
	}
	return nil
}
