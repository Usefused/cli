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

	"github.com/Usefused/cli/internal/pagination"
	"github.com/Usefused/cli/internal/ratelimitpolicy"
	"github.com/Usefused/cli/internal/retrypolicy"
	"github.com/Usefused/cli/internal/signaturepolicy"
	"github.com/charmbracelet/huh/spinner"
	"github.com/google/uuid"
)

type Client struct {
	BaseURL      string
	APIKey       string
	HTTP         *http.Client
	showProgress bool
}

const DefaultTimeout = time.Minute

var (
	errGraphQLResponseMalformed = &APIError{Code: "graphql_response_malformed", Message: "Engine returned a malformed GraphQL response", Category: "dependency", Retryable: true, Remediation: "Retry or check Engine logs."}
	errGraphQLRequestRejected   = &APIError{Code: "graphql_request_rejected", Message: "Engine rejected the GraphQL request", Category: "validation", Remediation: "Check command inputs and workspace permissions."}
	errGraphQLDataMalformed     = &APIError{Code: "graphql_data_malformed", Message: "Engine returned malformed GraphQL data", Category: "dependency", Retryable: true, Remediation: "Retry or check Engine logs."}
	errGraphQLResourceNotFound  = &APIError{Code: "resource_not_found", Message: "resource was not found", Category: "not_found", Remediation: "Use its name, slug, email, or full UUID."}
	errGraphQLResourceAmbiguous = &APIError{Code: "resource_ambiguous", Message: "name exists as both an SDK and MCP server", Category: "validation", Remediation: "use the full UUID."}
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

func (c *Client) GraphQL(query string, variables map[string]any, out any) error {
	return c.graphQLAt("/graphql", query, variables, out)
}

// EngineGraphQL calls the Engine-native GraphQL endpoint used by the UI for
// workspace reads. Keeping it separate from Registry GraphQL prevents CLI read
// commands from accidentally proxying Engine-owned bucket state through the
// catalogue surface.
func (c *Client) EngineGraphQL(query string, variables map[string]any, out any) error {
	return c.graphQLAt("/engine/graphql", query, variables, out)
}

func (c *Client) graphQLAt(path string, query string, variables map[string]any, out any) error {
	payload := map[string]any{
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
		return fmt.Errorf("graphql request failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, respBody))
	}

	return decodeGraphQLData(respBody, out)
}

func decodeGraphQLData(respBody []byte, out any) error {
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
	Message json.RawMessage         `json:"message"`
	Missing []PermissionRequirement `json:"missing"`
}

type APIError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Category  string `json:"category"`
	Retryable bool   `json:"retryable"`
	Details   struct {
		Bucket               *MissingCredentialBucket       `json:"bucket,omitempty"`
		MissingCredentials   []MissingCredentialRequirement `json:"missing_credentials,omitempty"`
		RequiredPermissions  []string                       `json:"required_permissions,omitempty"`
		ServerDetail         string                         `json:"server_detail,omitempty"`
		ApplyLeaseExpiresAt  string                         `json:"apply_lease_expires_at,omitempty"`
		RetryAfterSeconds    int                            `json:"retry_after_seconds,omitempty"`
		Stage                string                         `json:"stage,omitempty"`
		DependencyHTTPStatus int                            `json:"http_status,omitempty"`
	} `json:"details,omitempty"`
	Remediation string `json:"remediation,omitempty"`
	Phase       string `json:"phase,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
	CommitState string `json:"commit_state,omitempty"`
	Recovery    string `json:"recovery,omitempty"`
	TraceID     string `json:"trace_id,omitempty"`
	HTTPStatus  int    `json:"-"`
}

// MissingCredentialBucket is the exact Engine-owned target for interactive
// remediation. The ID is authoritative for writes while Name is safe display
// context resolved from the app's declarative bucket selection.
type MissingCredentialBucket struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MissingCredentialRequirement describes absent auth material without ever
// carrying a credential value. Static fields include their exact Engine secret
// key; OAuth/OIDC use the connection field and omit SecretKey.
type MissingCredentialRequirement struct {
	ServiceID         string                   `json:"service_id"`
	Service           string                   `json:"service,omitempty"`
	AuthType          string                   `json:"auth_type"`
	AuthName          string                   `json:"auth_name"`
	BasicPasswordMode BasicPasswordMode        `json:"basic_password_mode,omitempty"`
	RequiredFields    []MissingCredentialField `json:"required_fields"`
}

type MissingCredentialField struct {
	Name      string `json:"name"`
	SecretKey string `json:"secret_key,omitempty"`
}

func (e *APIError) Error() string {
	// The detail is intentionally separate from the stable message so scripts can
	// branch on the contract while an Engine owner still sees the parser decision.
	message := e.Code + ": " + e.Message
	if len(e.Details.RequiredPermissions) > 0 {
		message += " Required permissions: " + strings.Join(e.Details.RequiredPermissions, ", ") + "."
	}
	if len(e.Details.MissingCredentials) > 0 {
		message += fmt.Sprintf(" Missing credential requirements: %d.", len(e.Details.MissingCredentials))
	}
	if e.Details.ServerDetail != "" {
		message += " Server detail: " + e.Details.ServerDetail
	}
	if e.Details.ApplyLeaseExpiresAt != "" {
		message += " Apply lease expires: " + e.Details.ApplyLeaseExpiresAt + "."
	}
	if e.Details.RetryAfterSeconds > 0 {
		message += fmt.Sprintf(" Retry after %d seconds.", e.Details.RetryAfterSeconds)
	}
	if e.Remediation != "" {
		message += " " + e.Remediation
	}
	// Exact recovery commands are server-reviewed metadata, separate from
	// mutable remote detail that the shared parser deliberately suppresses.
	if e.Recovery != "" {
		message += " Recovery: `" + e.Recovery + "`."
	}
	if e.TraceID != "" {
		message += " Trace: " + e.TraceID
	}
	return message
}

func newHTTPError(status int, respBody []byte) error {
	var payload apiErrorPayload
	if err := json.Unmarshal(respBody, &payload); err == nil {
		if parsed := parsedHTTPError(status, payload); parsed != nil {
			return parsed
		}
		apiErr := genericHTTPError(status)
		apiErr.Details.ServerDetail = validationServerDetail(status, payload)
		return apiErr
	}
	// Non-JSON bodies have no contract boundary and can contain proxy pages or
	// credentials, so only structured Engine validation strings are displayable.
	return genericHTTPError(status)
}

// validationServerDetail exposes bounded validation context only where the
// caller can act on it; authentication and dependency responses stay opaque.
func validationServerDetail(status int, payload apiErrorPayload) string {
	if detail := decodedErrorString(payload.Error); detail != "" {
		return safeValidationDetail(status, detail)
	}
	return safeValidationDetail(status, decodedErrorString(payload.Message))
}

// safeValidationDetail centralizes the status gate so both legacy string and
// structured Engine errors receive the same local-display safety policy.
func safeValidationDetail(status int, value string) string {
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		return ""
	}
	return safeServerDetail(value)
}

// decodedErrorString rejects nested or mixed response shapes because those
// are not part of the Engine's user-facing validation-error contract.
func decodedErrorString(raw json.RawMessage) string {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

// safeServerDetail keeps terminal and JSON diagnostics useful without turning
// a remotely supplied error into an unbounded or credential-bearing payload.
func safeServerDetail(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" || containsCredentialMaterial(value) {
		return ""
	}
	const maxServerDetailRunes = 1024
	runes := []rune(value)
	if len(runes) > maxServerDetailRunes {
		return string(runes[:maxServerDetailRunes]) + "…"
	}
	return value
}

// containsCredentialMaterial fails closed on common serialized credential
// markers while allowing field names in parser errors to remain actionable.
func containsCredentialMaterial(value string) bool {
	lower := strings.ToLower(value)
	markers := []string{
		"fsk_", "-----begin ", "authorization:", "authorization=",
		"access_token=", `"access_token":`, "refresh_token=", `"refresh_token":`,
		"client_secret=", `"client_secret":`, "password=", `"password":`,
		"api_key=", `"api_key":`, "apikey=", `"apikey":`, "secret=", `"secret":`,
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func parsedHTTPError(status int, payload apiErrorPayload) error {
	// Structured errors remain the preferred stable contract, but their optional
	// owner detail still passes through the same validation-only safety boundary.
	var structured APIError
	if len(payload.Error) > 0 && json.Unmarshal(payload.Error, &structured) == nil && structured.Code != "" && structured.Message != "" {
		structured.HTTPStatus = status
		// Structured Engine errors are the dedicated instance's reviewed public
		// contract. Preserve their bounded detail across dependency/conflict
		// statuses while retaining the local credential-material fail-closed gate.
		structured.Details.ServerDetail = safeServerDetail(structured.Details.ServerDetail)
		return &structured
	}

	var errorCode string
	_ = json.Unmarshal(payload.Error, &errorCode)
	if status == http.StatusUnauthorized && errorCode == "authentication_required" {
		return &APIError{Code: "authentication_required", Message: "authentication required; provide a valid Fused credential", Category: "authentication", HTTPStatus: status}
	}
	if status == http.StatusForbidden && errorCode == "permission_denied" {
		return permissionDeniedError(status, payload.Missing)
	}
	return appOwnerHTTPError(status, errorCode)
}

func appOwnerHTTPError(status int, code string) error {
	switch code {
	case "app owner is immutable":
		return &APIError{Code: "app_owner_immutable", Message: "this app already has an owner", Category: "conflict", Remediation: "Omit --owner-team or use its existing team slug.", HTTPStatus: status}
	case "app owner is unavailable":
		return &APIError{Code: "app_owner_unavailable", Message: "the app owner is unavailable", Category: "authorization", Remediation: "Ask a workspace administrator for help.", HTTPStatus: status}
	case "owner team was not found or is archived":
		return &APIError{Code: "owner_team_unavailable", Message: "the owner team was not found or is archived", Category: "validation", Remediation: "Choose an active team slug.", HTTPStatus: status}
	case "app owner authorization denied":
		return &APIError{Code: "owner_team_access_denied", Message: "you or the owning team no longer have the required access", Category: "authorization", HTTPStatus: status}
	default:
		return nil
	}
}

// genericHTTPError keeps stable error codes independent of mutable server text.
func genericHTTPError(status int) *APIError {
	apiErr := &APIError{HTTPStatus: status}
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		apiErr.Code, apiErr.Message, apiErr.Category, apiErr.Remediation = "request_rejected", "Engine rejected the request", "validation", "Check command inputs."
	case http.StatusUnauthorized:
		apiErr.Code, apiErr.Message, apiErr.Category, apiErr.Remediation = "authentication_failed", "authentication failed", "authentication", "Provide a valid Fused credential."
	case http.StatusForbidden:
		apiErr.Code, apiErr.Message, apiErr.Category, apiErr.Remediation = "request_forbidden", "the request is forbidden", "authorization", "Check workspace permissions."
	case http.StatusNotFound:
		apiErr.Code, apiErr.Message, apiErr.Category = "resource_not_found", "the requested resource was not found", "not_found"
	case http.StatusConflict:
		apiErr.Code, apiErr.Message, apiErr.Category, apiErr.Retryable, apiErr.Remediation = "request_conflict", "workspace state changed", "conflict", true, "Refresh and retry."
	case http.StatusTooManyRequests:
		apiErr.Code, apiErr.Message, apiErr.Category, apiErr.Retryable, apiErr.Remediation = "request_rate_limited", "request rate limited", "rate_limit", true, "Retry later."
	default:
		apiErr.Code, apiErr.Message, apiErr.Category, apiErr.Retryable, apiErr.Remediation = "engine_request_failed", "Engine request failed", "dependency", status >= 500, "Check Engine logs and retry."
	}
	return apiErr
}

func permissionDeniedError(status int, missing []PermissionRequirement) error {
	message := formatPermissionDenied(missing)
	apiErr := &APIError{Code: "permission_denied", Message: message, Category: "authorization", HTTPStatus: status}
	apiErr.Details.RequiredPermissions = safePermissionDescriptions(missing)
	return apiErr
}

func formatPermissionDenied(missing []PermissionRequirement) string {
	if len(missing) == 0 {
		return "permission denied; ask a workspace administrator for access"
	}
	if containsPermission(missing, "access.manage") {
		return "permission denied; you are not a member of the owning team; join that team or ask an access administrator to perform this action"
	}
	items := safePermissionDescriptions(missing)
	if len(items) == 0 {
		return "permission denied; ask a workspace administrator for access"
	}
	return "permission denied; ask a workspace administrator to allow you to " + strings.Join(items, "; ")
}

func safePermissionDescriptions(requirements []PermissionRequirement) []string {
	items := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		if requirement.valid() {
			items = append(items, requirement.ProductDescription())
		}
	}
	return items
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
	case "service", "bucket", "app":
		return "the selected " + resourceType
	default:
		return "the requested resource"
	}
}

var productPermissionActions = map[string]string{
	"workspace.read":     "view",
	"workspace.update":   "change",
	"service.read":       "view",
	"service.consume":    "use",
	"service.manage":     "manage",
	"bucket.read":        "view",
	"bucket.values.read": "view",
	"bucket.use":         "use",
	"bucket.manage":      "manage",
	"app.read":           "view",
	"app.create":         "create an SDK, MCP server, or webhook in",
	"app.manage":         "manage",
	"app.tokens.manage":  "manage",
	"connection.read":    "view connections in",
	"connection.manage":  "manage connections in",
	"access.read":        "view access activity for",
	"audit.read":         "view access activity for",
	"access.manage":      "manage team access for",
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
		return nil, fmt.Errorf("health check failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, respBody))
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
	TimeoutMs   *int                `json:"timeout_ms"`
	Pagination  *ServicePagination  `json:"pagination"`
	// BaseURLOverride is nil unless this service's execution_policy has
	// published a base_url override -- distinct from the general "base_url"
	// GraphQL field (not fetched here) which merges override-if-set-else-spec-derived
	// and can't tell sync whether the value came from execution_policy.
	BaseURLOverride       *string                       `json:"base_url_override"`
	EventExtractionPath   *string                       `json:"event_extraction_path"`
	IncomingWebhookConfig *ServiceIncomingWebhookConfig `json:"incoming_webhook_config"`
}

type ServiceRateLimit = ratelimitpolicy.Config

const serviceRateLimitGraphQLFields = `
	version
	policies {
		name
		mode
		unit
		identity { inputs { kind binding name } }
		cost { default rules { operation cost } }
		algorithm
		fixed_window { limit duration_ms }
		rolling_window { limit duration_ms }
		token_bucket { capacity refill_units refill_interval_ms }
		concurrency { limit }
		response_signals {
			limit { source name path }
			remaining { source name path }
			reset { signal { source name path } format }
			cost { source name path }
		}
	}
	cooldown { statuses { min max } headers { name formats max_delay_ms } }
`

type ServiceRetryConfig = retrypolicy.Config

const serviceRetryGraphQLFields = `
	version
	rules {
		predicates {
			methods
			operation_kinds
			statuses { min max }
			errors
			body_replayability
			idempotency_key { requirement header }
			required_provider_headers
		}
		action {
			max_attempts
			max_elapsed_ms
			backoff { strategy base_delay_ms max_delay_ms jitter_ms }
			retry_after_headers { name formats max_delay_ms }
		}
	}
`

type ServicePagination = pagination.Config
type ServicePaginationRequestStep = pagination.RequestStep
type ServicePaginationResponsePlan = pagination.ResponsePlan
type ServicePaginationItemsSource = pagination.ItemsSource
type ServicePaginationResponseValue = pagination.ResponseValue
type ServicePaginationConditionalPath = pagination.ConditionalPath
type ServicePaginationRequestCondition = pagination.RequestCondition
type ServicePaginationItemSelector = pagination.ItemSelector
type ServicePaginationContinuationStep = pagination.ContinuationStep
type ServicePaginationOriginPolicy = pagination.OriginPolicy
type ServicePaginationTermination = pagination.Termination
type ServicePaginationShortPageTermination = pagination.ShortPageTermination
type ServicePaginationResponseCondition = pagination.ResponseCondition
type ServicePaginationGraphQLPlan = pagination.GraphQLPlan
type ServicePaginationGraphQLVariable = pagination.GraphQLVariable
type ServicePaginationGraphQLResultAlias = pagination.GraphQLResultAlias
type ServicePaginationIncrement = pagination.Increment
type ServicePaginationRequestTarget = pagination.RequestTarget
type ServicePaginationValueSource = pagination.ValueSource
type ServicePaginationScalar = pagination.Scalar
type ServicePaginationLimits = pagination.Limits

const servicePaginationGraphQLFields = `
	version
	request {
		state
		target { location name }
		value_type
		initial { type string integer boolean }
		constant { type string integer boolean }
		apply
	}
	response {
		items {
			path
			paths { path when { state operator value { type string integer boolean } } }
		}
		values {
			name
			source {
				location path name relation value_type
				paths { path when { state operator value { type string integer boolean } } }
				item { position path }
			}
		}
	}
	continuation {
		kind state response_value
		increment { mode value }
		origin { mode allowed_origins }
	}
	termination {
		stop_on_empty_items
		stop_on_short_page { request_state }
		stop_on_missing_values
		conditions { response_value state operator value { type string integer boolean } }
		repeated_value
	}
	graphql {
		variables { name state value_type }
		result_aliases { name alias }
		first_page_template
		subsequent_page_template
	}
	limits { max_pages max_items max_bytes max_duration_ms }
`

// ServiceIncomingWebhookConfig is the provider's webhook verification recipe
// -- auth mechanism + where to find the signature. It deliberately has no
// secret field: the Registry never publishes signing secrets (see
// plans/plan-service-config-restructure.md item 3), only the non-secret
// shape of how to verify a delivery.
type ServiceIncomingWebhookConfig struct {
	AuthType            string                  `json:"auth_type"`
	AuthLocation        string                  `json:"auth_location"`
	AuthKeyName         string                  `json:"auth_key_name"`
	SignatureHeader     string                  `json:"signature_header"`
	VerificationHeaders []string                `json:"verification_headers"`
	SignaturePolicy     *signaturepolicy.Config `json:"signature_policy,omitempty"`
}

const serviceSignaturePolicyGraphQLFields = `
	version
	rules {
		name kind
		predicates { source { location name path } operator value }
		verification {
			kind
			signature {
				secret_ref
				signature { location name path }
				components { kind names join algorithm encoding }
				algorithm encoding comparison prefix component_separator
			}
			jwt { secret_ref token { location name path } algorithms issuer audience clock_skew_ms }
			challenge { value { location name path } body_field status_code }
		}
	}
`

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
	ID                   string                  `json:"id"`
	Name                 string                  `json:"name"`
	Path                 string                  `json:"path"`
	Method               string                  `json:"method"`
	Description          string                  `json:"description"`
	ServiceID            string                  `json:"service_id"`
	SecurityRequirements SecurityRequirements    `json:"security_requirements"`
	Documentation        *OperationDocumentation `json:"documentation,omitempty"`
}

// SecurityRequirements preserves Registry ordering because alternatives are
// tried in declaration order by the runtime. The CLI only transports and
// displays this shape; Registry and Engine remain its semantic authorities.
type SecurityRequirements []SecurityAlternative

type SecurityAlternative struct {
	Schemes         []SecurityRequirement    `json:"schemes"`
	ServerSelection *SecurityServerSelection `json:"server_selection,omitempty"`
}

type SecurityServerSelection struct {
	Scheme    string `json:"scheme"`
	ServerURL string `json:"server_url"`
}

type SecurityRequirement struct {
	Scheme string   `json:"scheme"`
	Scopes []string `json:"scopes"`
}

const securityRequirementsGraphQLFields = `
	security_requirements {
		schemes { scheme scopes }
		server_selection
	}
`

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
	IsPublic bool           `json:"is_public"`
	Profile  map[string]any `json:"profile"`
}

// WorkspaceConnectConfig is the bucket-scoped, masked connect state consumed
// by workspace sync; encrypted OAuth app credentials are presence flags only.
type WorkspaceConnectConfig struct {
	BucketID        string                    `json:"bucket_id"`
	BucketName      string                    `json:"bucket_name"`
	ServiceID       string                    `json:"service_id"`
	AuthType        string                    `json:"auth_type"`
	AuthName        string                    `json:"auth_name"`
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
	err := c.EngineGraphQL(query, map[string]any{"names": names}, &resp)
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
				auth_name
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
	err := c.EngineGraphQL(query, map[string]any{"serviceId": serviceID}, &resp)
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
	err := c.EngineGraphQL(query, map[string]any{"connectionId": connectionID}, &response)
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
	err := c.EngineGraphQL(query, map[string]any{"connectionId": connectionID, "resourceId": resourceID}, &response)
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
	err := c.EngineGraphQL(query, map[string]any{"connectionId": connectionID}, &response)
	return response.Resources, err
}

// StartConnectSession calls the Engine's bucket-scoped connect route so CLI
// onboarding and scope reduction use the same policy as generated SDKs.
func (c *Client) StartConnectSession(bucketID, serviceID, endUserRef, createdByAppID string, resourceInput map[string]string, scopes []string) (*ConnectSessionStartResponse, error) {
	query := `
		mutation StartConnectSession($bucketId: String!, $serviceId: String!, $endUserRef: String!, $createdByAppId: String, $resourceInput: EngineJSON, $scopes: [String!]) {
			startConnectSession(bucket_id: $bucketId, service_id: $serviceId, end_user_ref: $endUserRef, created_by_app_id: $createdByAppId, resource_input: $resourceInput, scopes: $scopes) {
				authorize_url
				expires_at
			}
		}
	`
	vars := map[string]any{
		"bucketId":   bucketID,
		"serviceId":  serviceID,
		"endUserRef": endUserRef,
	}
	if strings.TrimSpace(createdByAppID) != "" {
		vars["createdByAppId"] = createdByAppID
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
				rate_limit {` + serviceRateLimitGraphQLFields + `}
				retry_config {` + serviceRetryGraphQLFields + `}
				timeout_ms
				pagination {` + servicePaginationGraphQLFields + `}
				event_extraction_path
					incoming_webhook_config { auth_type auth_location auth_key_name signature_header verification_headers signature_policy {` + serviceSignaturePolicyGraphQLFields + `} }
					base_url_override
			}
		}
	`
	var resp struct {
		ServicesByIDs []ServiceVisibility `json:"servicesByIds"`
	}
	if err := c.GraphQL(query, map[string]any{"serviceIds": serviceIDs}, &resp); err != nil {
		return nil, err
	}
	for _, svc := range resp.ServicesByIDs {
		out[svc.ServiceID] = svc
	}
	return out, nil
}

type ServiceVersion struct {
	ExecutionContractEnvelope
	ID            string                `json:"id"`
	ServiceID     string                `json:"service_id"`
	Name          string                `json:"name"`
	Status        string                `json:"status"`
	CreatedAt     string                `json:"created_at"`
	Documentation *ServiceDocumentation `json:"documentation,omitempty"`
	// IsPublic/RateLimit/RetryConfig mirror ServiceVisibility's identically
	// named fields, but scoped to this one version rather than the service as
	// a whole. Sync uses these to round-trip version_policies[*].public and
	// version_policies[*].execution_policy back into workspace.yaml.
	IsPublic    bool                `json:"is_public"`
	RateLimit   *ServiceRateLimit   `json:"rate_limit"`
	RetryConfig *ServiceRetryConfig `json:"retry_config"`
	TimeoutMs   *int                `json:"timeout_ms"`
	Pagination  *ServicePagination  `json:"pagination"`
	// BaseURLOverride mirrors ServiceVisibility.BaseURLOverride, scoped to
	// this version.
	BaseURLOverride       *string                       `json:"base_url_override"`
	EventExtractionPath   *string                       `json:"event_extraction_path"`
	IncomingWebhookConfig *ServiceIncomingWebhookConfig `json:"incoming_webhook_config"`
}

// ServiceVersionSummary is the bounded list projection for identity and
// execution compatibility. Keeping it separate from ServiceVersion prevents a
// routine list from fetching documentation and complete policy JSON.
type ServiceVersionSummary struct {
	ExecutionContractEnvelope
	ID        string `json:"id"`
	ServiceID string `json:"service_id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	IsPublic  bool   `json:"is_public"`
}

type ServiceServer struct {
	URL         string           `json:"url"`
	Name        string           `json:"name,omitempty"`
	Description string           `json:"description,omitempty"`
	Environment string           `json:"environment,omitempty"`
	IsDefault   bool             `json:"is_default,omitempty"`
	Variables   []ServerVariable `json:"variables,omitempty"`
}

type ServerVariable struct {
	Name     string   `json:"name"`
	Default  *string  `json:"default,omitempty"`
	Enum     []string `json:"enum,omitempty"`
	Required bool     `json:"required"`
}

type AuthConfig struct {
	Name                    string                        `json:"name,omitempty"`
	Type                    string                        `json:"type"`
	Scheme                  string                        `json:"scheme,omitempty"`
	BasicPasswordMode       BasicPasswordMode             `json:"basic_password_mode,omitempty"`
	Location                string                        `json:"location,omitempty"`
	KeyName                 string                        `json:"key_name,omitempty"`
	TokenEndpointAuthMethod TokenEndpointAuthMethod       `json:"token_endpoint_auth_method,omitempty"`
	OpenIDConnectURL        string                        `json:"open_id_connect_url,omitempty"`
	OAuth2MetadataURL       string                        `json:"oauth2_metadata_url,omitempty"`
	Deprecated              *bool                         `json:"deprecated,omitempty"`
	PKCERequired            bool                          `json:"pkce_required,omitempty"`
	ScopesDelimiter         string                        `json:"scopes_delimiter,omitempty"`
	ExtraAuthParams         map[string]string             `json:"extra_auth_params,omitempty"`
	ExtraTokenParams        map[string]string             `json:"extra_token_params,omitempty"`
	RefreshTokenRequired    bool                          `json:"refresh_token_required,omitempty"`
	RefreshTokenRotates     bool                          `json:"refresh_token_rotates,omitempty"`
	OAuth2Flows             map[string]OAuth2FlowContract `json:"oauth2_flows,omitempty"`
	Strategy                *AuthRuntimeStrategy          `json:"strategy,omitempty"`
	PolicyProvenance        map[string]string             `json:"policy_provenance,omitempty"`
}

type OAuth2FlowContract struct {
	AuthorizationURL       string            `json:"authorization_url,omitempty"`
	DeviceAuthorizationURL string            `json:"device_authorization_url,omitempty"`
	TokenURL               string            `json:"token_url,omitempty"`
	RefreshURL             string            `json:"refresh_url,omitempty"`
	Scopes                 map[string]string `json:"scopes"`
}

type AuthRuntimeStrategy struct {
	Kind      string                 `json:"kind"`
	OAuth1    *OAuth1Strategy        `json:"oauth1,omitempty"`
	Challenge *HTTPChallengeStrategy `json:"challenge,omitempty"`
}

type OAuth1Strategy struct {
	SignatureMethod   string `json:"signature_method"`
	ParameterLocation string `json:"parameter_location"`
}

type HTTPChallengeStrategy struct {
	Scheme string `json:"scheme"`
}

// BasicPasswordMode is intentionally an unconstrained transport string here;
// Registry validates the frozen vocabulary and Engine applies its behavior.
type BasicPasswordMode string

// TokenEndpointAuthMethod mirrors Registry's validated, credential-free OAuth
// transport contract. The CLI carries it verbatim and does not infer provider behavior.
type TokenEndpointAuthMethod string

const (
	TokenEndpointAuthMethodClientSecretBasic TokenEndpointAuthMethod = "client_secret_basic"
	TokenEndpointAuthMethodClientSecretPost  TokenEndpointAuthMethod = "client_secret_post"
)

const serviceServerGraphQLFields = `
	url
	name
	description
	environment
	is_default
	variables { name default enum required }
`

const serviceAuthConfigGraphQLFields = `
	name
	type
	scheme
	basic_password_mode
	location
	key_name
	token_endpoint_auth_method
	open_id_connect_url
	oauth2_metadata_url
	deprecated
	pkce_required
	scopes_delimiter
	extra_auth_params
	extra_token_params
	refresh_token_required
	refresh_token_rotates
	oauth2_flows
	strategy
	policy_provenance
`

type ServiceInfo struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
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
	vars := map[string]any{
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
				description
				slug
				base_url
					provider { handle }
				is_owner
				servers {
					` + serviceServerGraphQLFields + `
				}
				auth_configs {
					` + serviceAuthConfigGraphQLFields + `
				}
			}
		}
	`
	var resp struct {
		Service *ServiceInfo `json:"service"`
	}
	slug, provider := splitProviderQualifiedServiceRef(serviceSlug)
	err := c.GraphQL(query, map[string]any{"id": slug, "provider": provider}, &resp)
	return resp.Service, err
}

func (c *Client) ServiceVersions(serviceSlug string) ([]ServiceVersion, error) {
	query := `
		query ServiceVersions($serviceId: String!, $provider: String) {
			serviceVersions(serviceId: $serviceId, provider: $provider) {
				contract_version
				required_capabilities
				id
				service_id
				name
				status
				created_at
				documentation
				is_public
				rate_limit {` + serviceRateLimitGraphQLFields + `}
				retry_config {` + serviceRetryGraphQLFields + `}
				timeout_ms
				pagination {` + servicePaginationGraphQLFields + `}
				event_extraction_path
					incoming_webhook_config { auth_type auth_location auth_key_name signature_header verification_headers signature_policy {` + serviceSignaturePolicyGraphQLFields + `} }
					base_url_override
			}
		}
	`
	var resp struct {
		ServiceVersions []ServiceVersion `json:"serviceVersions"`
	}
	slug, provider := splitProviderQualifiedServiceRef(serviceSlug)
	err := c.GraphQL(query, map[string]any{"serviceId": slug, "provider": provider}, &resp)
	return resp.ServiceVersions, err
}

// ServiceVersionSummaries uses a deliberately lean GraphQL selection because
// version listing and target resolution never need documentation or policies.
func (c *Client) ServiceVersionSummaries(serviceSlug string) ([]ServiceVersionSummary, error) {
	query := `
		query ServiceVersionSummaries($serviceId: String!, $provider: String) {
			serviceVersions(serviceId: $serviceId, provider: $provider) {
				contract_version
				required_capabilities
				id
				service_id
				name
				status
				created_at
				is_public
			}
		}
	`
	var resp struct {
		ServiceVersions []ServiceVersionSummary `json:"serviceVersions"`
	}
	slug, provider := splitProviderQualifiedServiceRef(serviceSlug)
	err := c.GraphQL(query, map[string]any{"serviceId": slug, "provider": provider}, &resp)
	return resp.ServiceVersions, err
}

// SetConnectionProfile appends an owner-authorized immutable provider profile
// revision. Registry owns provenance, visibility, and stream identity.
func (c *Client) SetConnectionProfile(serviceID, serviceVersionID, name string, profile map[string]any) (*ConnectionProfileRevision, error) {
	query := `mutation SetConnectionProfile($serviceId: String!, $serviceVersionId: String!, $name: String!, $config: JSON!) {
		setConnectionProfile(service_id: $serviceId, service_version_id: $serviceVersionId, name: $name, config: $config) {
			profile_id service_id service_version_id revision profile_hash provenance
		}
	}`
	var response struct {
		Profile *ConnectionProfileRevision `json:"setConnectionProfile"`
	}
	if err := c.GraphQL(query, map[string]any{
		"serviceId": serviceID, "serviceVersionId": serviceVersionID, "name": name, "config": profile,
	}, &response); err != nil {
		return nil, err
	}
	return response.Profile, nil
}

// ServiceReference is the normalized provider-aware identity shared by Registry
// and Engine lookup callers. ProviderPrefixed lets identity-only callers reject
// malformed qualifiers while lexical search callers preserve them as text.
type ServiceReference struct {
	Raw              string
	Slug             string
	Provider         string
	ProviderPrefixed bool
	Qualified        bool
}

// ParseServiceReference recognizes only a complete @provider/slug identity so
// lexical-capable callers can retain incomplete qualifiers as ordinary text.
func ParseServiceReference(ref string) ServiceReference {
	raw := strings.TrimSpace(ref)
	rest, hasPrefix := strings.CutPrefix(raw, "@")
	// Ordinary search text never carries provider identity.
	if !hasPrefix {
		return ServiceReference{Raw: raw, Slug: raw}
	}
	provider, slug, hasSeparator := strings.Cut(rest, "/")
	// Empty or nested segments cannot be stable Registry identity, so preserve
	// the complete text for lexical search rather than partially interpreting it.
	if !hasSeparator || provider == "" || slug == "" || strings.Contains(slug, "/") ||
		strings.TrimSpace(provider) != provider || strings.TrimSpace(slug) != slug ||
		hasUnsafeServiceReferenceRune(provider+slug) {
		return ServiceReference{Raw: raw, Slug: raw, ProviderPrefixed: true}
	}
	return ServiceReference{Raw: raw, Slug: slug, Provider: provider, ProviderPrefixed: true, Qualified: true}
}

// hasUnsafeServiceReferenceRune rejects every whitespace or control rune so
// identity-only callers cannot pass terminal escapes hidden inside a qualifier.
func hasUnsafeServiceReferenceRune(value string) bool {
	return strings.IndexFunc(value, unsafeServiceReferenceRune) >= 0
}

// unsafeServiceReferenceRune defines the non-identity characters rejected by
// the shared provider-qualified parser.
func unsafeServiceReferenceRune(char rune) bool {
	return unicode.IsSpace(char) || unicode.IsControl(char)
}

// splitProviderQualifiedServiceRef retains the narrow tuple used by existing
// Registry queries while delegating all grammar decisions to the shared parser.
func splitProviderQualifiedServiceRef(ref string) (string, string) {
	parsed := ParseServiceReference(ref)
	return parsed.Slug, parsed.Provider
}

// ServiceLookupName returns the service segment used by Engine's bounded name
// lookup while keeping qualified-reference parsing owned by the API package.
func ServiceLookupName(ref string) string {
	return ParseServiceReference(ref).Slug
}

const serviceSearchGraphQLFields = `
		id
		name
		slug
		provider { handle }
		is_owner
		is_public
`

// SearchServices performs the existing one-query Registry catalogue search.
func (c *Client) SearchServices(q string) ([]Service, error) {
	query := `
		query SearchServices($q: String!) {
			searchServices(q: $q) {
				` + serviceSearchGraphQLFields + `
			}
		}
	`
	var resp struct {
		SearchServices []Service `json:"searchServices"`
	}
	err := c.GraphQL(query, map[string]any{"q": q}, &resp)
	return resp.SearchServices, err
}

// SearchServicesBatch resolves independent service references through the
// Registry's set-based field so composite CLI commands avoid N+1 DB searches.
func (c *Client) SearchServicesBatch(queries []string) (map[string][]Service, error) {
	// An empty composite has no Registry work and must not issue a malformed
	// zero-field GraphQL operation.
	if len(queries) == 0 {
		return map[string][]Service{}, nil
	}

	query := `
		query ServiceCandidatesByRefs($refs: [String!]!, $limitPerRef: Int!) {
			serviceCandidatesByRefs(refs: $refs, limitPerRef: $limitPerRef) {
				ref
				candidates { ` + serviceSearchGraphQLFields + ` }
			}
		}
	`
	var response struct {
		Results []struct {
			Ref        string    `json:"ref"`
			Candidates []Service `json:"candidates"`
		} `json:"serviceCandidatesByRefs"`
	}
	if err := c.GraphQL(query, map[string]any{"refs": queries, "limitPerRef": 20}, &response); err != nil {
		return nil, err
	}

	results := make(map[string][]Service, len(queries))
	for _, result := range response.Results {
		results[result.Ref] = result.Candidates
	}
	return results, nil
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
				documentation
				service_id
				` + securityRequirementsGraphQLFields + `
			}
		}
	`
	var resp struct {
		SearchEndpoints []Integration `json:"searchEndpoints"`
	}
	err := c.GraphQL(query, map[string]any{"serviceId": serviceID, "version": version, "q": q, "limit": normalLimit(opts.Limit), "offset": normalOffset(opts.Offset)}, &resp)
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
				documentation
				service_id
				` + securityRequirementsGraphQLFields + `
			}
		}
	`
	var resp struct {
		ServiceOperations []Integration `json:"serviceOperations"`
	}
	err := c.GraphQL(query, map[string]any{"serviceId": serviceID, "version": version}, &resp)
	return resp.ServiceOperations, err
}

type Webhook struct {
	ID          string                    `json:"id"`
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	Contract    *InboundOperationContract `json:"contract,omitempty"`
}

func (c *Client) FetchWebhooks(serviceID, version string) ([]Webhook, error) {
	query := `
		query FetchWebhooks($id: String!) {
			service(id: $id) {
				webhooks {
					id
					name
					description
					contract
				}
			}
		}
	`
	var resp struct {
		Service struct {
			Webhooks []Webhook `json:"webhooks"`
		} `json:"service"`
	}
	err := c.GraphQL(query, map[string]any{"id": serviceID}, &resp)
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
	err := c.GraphQL(query, map[string]any{"q": q}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp.ParseSDKIntent, nil
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
		errChan <- fmt.Errorf("stream failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, respBody))
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

func (c *Client) DownloadSDK(appID string) ([]byte, error) {
	req, err := http.NewRequest("GET", c.BaseURL+"/sdks/"+appID+"/download", nil)
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
		return nil, fmt.Errorf("download failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, respBody))
	}

	return io.ReadAll(resp.Body)
}

type SDKConfigPlanResponse struct {
	PlanID              string                  `json:"plan_id"`
	OwnerType           string                  `json:"owner_type"`
	ConfigKey           string                  `json:"config_key"`
	SourceHash          string                  `json:"source_hash"`
	Summary             map[string]any          `json:"summary"`
	Notifications       NotificationInbox       `json:"notifications"`
	RequiredPermissions []PermissionRequirement `json:"required_permissions"`
}

type NotificationInbox struct {
	Items    []NotificationItem `json:"items"`
	Warnings []string           `json:"warnings"`
}

type NotificationItem struct {
	ID                  string `json:"id"`
	Source              string `json:"source"`
	Type                string `json:"type"`
	Severity            string `json:"severity"`
	Status              string `json:"status"`
	ServiceID           string `json:"service_id"`
	Version             string `json:"version"`
	ConfigKey           string `json:"config_key"`
	Message             string `json:"message"`
	IntegrationObjectID string `json:"integration_object_id"`
	WebhookObjectID     string `json:"webhook_object_id"`
	DetectedAt          string `json:"detected_at"`
	Diff                []any  `json:"diff"`
}

type ConfigPlanResponse struct {
	PlanID              string                  `json:"plan_id"`
	ConfigKey           string                  `json:"config_key"`
	SourceHash          string                  `json:"source_hash"`
	Summary             map[string]any          `json:"summary"`
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

type DesiredConfigPlanIntent struct {
	SourceHash    string
	ConfigKey     string
	OwnerTeamSlug string
	Config        json.RawMessage
}

// PlanSDKConfig sends native SDK desired state through the shared app
// plan client while preserving the SDK-specific Engine route.
func (c *Client) PlanSDKConfig(intent DesiredConfigPlanIntent) (*SDKConfigPlanResponse, error) {
	return c.planDesiredConfig("sdk", intent)
}

// PlanMCPConfig plans an Engine runtime without invoking Registry generation.
func (c *Client) PlanMCPConfig(intent DesiredConfigPlanIntent) (*SDKConfigPlanResponse, error) {
	return c.planDesiredConfig("mcp", intent)
}

// PlanWebhookConfig plans a kind: webhook config through the same shared
// route pattern SDK/MCP use ("/webhook-config/plan"). The response has no
// notifications field (only workspace/SDK/MCP applies can affect other
// apps) -- SDKConfigPlanResponse.Notifications just decodes to its zero
// value, same as reusing this helper already does for any response shape
// that omits a field the shared struct declares.
func (c *Client) PlanWebhookConfig(intent DesiredConfigPlanIntent) (*SDKConfigPlanResponse, error) {
	return c.planDesiredConfig("webhook", intent)
}

// planDesiredConfig keeps SDK and MCP command behavior identical while the
// Engine routes each kind to its distinct executor.
func (c *Client) planDesiredConfig(kind string, intent DesiredConfigPlanIntent) (*SDKConfigPlanResponse, error) {
	reqBody := map[string]any{
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
		return nil, fmt.Errorf("plan %s config failed (HTTP %d): %w", kind, resp.StatusCode, newHTTPError(resp.StatusCode, respBody))
	}

	var out SDKConfigPlanResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) PlanWorkspaceConfig(sourceHash, configKey string, config json.RawMessage) (*ConfigPlanResponse, error) {
	reqBody := map[string]any{
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
		return nil, fmt.Errorf("plan workspace config failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, respBody))
	}

	var out ConfigPlanResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

type SDKConfigApplyResponse struct {
	Status         string `json:"status"`
	PlanID         string `json:"plan_id"`
	AppFamilyID    string `json:"app_family_id"`
	AppID          string `json:"app_id"`
	JobID          string `json:"job_id"`
	ExecutionToken string `json:"execution_token"`
}

type MCPConfigApplyResponse struct {
	Status           string           `json:"status"`
	PlanID           string           `json:"plan_id"`
	ConfigKey        string           `json:"config_key"`
	AppFamilyID      string           `json:"app_family_id"`
	AppID            string           `json:"app_id"`
	DefaultTransport string           `json:"default_transport"`
	TransportURLs    MCPTransportURLs `json:"transport_urls"`
	ExecutionToken   string           `json:"execution_token"`
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

// ApplySDKConfig may return a plaintext execution token only when Engine first
// creates the SDK. Callers must surface it without retaining it.
func (c *Client) ApplySDKConfig(planID, sourceHash string) (*SDKConfigApplyResponse, error) {
	reqBody := map[string]any{
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
		return nil, fmt.Errorf("apply SDK config failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, respBody))
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
	reqBody := map[string]any{"plan_id": planID, "source_hash": sourceHash}
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
		return nil, fmt.Errorf("apply mcp config failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, respBody))
	}
	var out MCPConfigApplyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ApplyWebhookConfig reconciles a kind: webhook config's registrations.
// Unlike SDK/MCP there is no generated package or token -- the response is
// just the set of (service, slug) rows the Engine just wrote.
func (c *Client) ApplyWebhookConfig(planID, sourceHash string) (*WebhookConfigApplyResponse, error) {
	reqBody := map[string]any{"plan_id": planID, "source_hash": sourceHash}
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
		return nil, fmt.Errorf("apply webhook config failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, respBody))
	}
	var out WebhookConfigApplyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ApplyWorkspaceConfig(planID, sourceHash string, authMaterials map[string]AuthMaterial, profileMaterials map[string]ConnectMaterial, bucketSecretMaterials map[string]string) (*ConfigApplyResponse, error) {
	reqBody := map[string]any{
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
		return nil, fmt.Errorf("apply workspace config failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, respBody))
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
		return fmt.Errorf("update workspace plan action failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, respBody))
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
	Name           string  `json:"name"`
	Slug           string  `json:"slug"`
	Version        string  `json:"version,omitempty"`
	SourceURL      string  `json:"source_url,omitempty"`
	SourceContent  string  `json:"source_content,omitempty"`
	OverlayContent *string `json:"overlay_content,omitempty"`
	// IsPublic is omitted from the request entirely when --public was not
	// passed, so the Registry can default it differently depending on
	// whether this targets a new service or a new version of an existing
	// one -- see resolveImportPlanIsPublic on the Registry side.
	IsPublic   *bool  `json:"is_public,omitempty"`
	TargetType string `json:"target_type,omitempty"`
	Category   string `json:"category,omitempty"`
	Strict     bool   `json:"strict,omitempty"`
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

type SpecImportDiagnostic struct {
	Severity           string   `json:"severity"`
	Code               string   `json:"code"`
	Scope              string   `json:"scope"`
	Method             string   `json:"method,omitempty"`
	Path               string   `json:"path,omitempty"`
	OperationID        string   `json:"operation_id,omitempty"`
	Message            string   `json:"message"`
	Recommendation     string   `json:"recommendation,omitempty"`
	Source             string   `json:"source,omitempty"`
	SourceFormat       string   `json:"source_format,omitempty"`
	SourceVersion      string   `json:"source_version,omitempty"`
	Pointer            string   `json:"pointer,omitempty"`
	Service            string   `json:"service,omitempty"`
	Disposition        string   `json:"disposition,omitempty"`
	RequiredCapability string   `json:"required_capability,omitempty"`
	Provenance         string   `json:"provenance,omitempty"`
	Confidence         *float64 `json:"confidence,omitempty"`
}

type SpecImportStrictError struct {
	HTTPStatus  int
	Code        string
	Message     string
	Diagnostics []SpecImportDiagnostic
}

func (e *SpecImportStrictError) Error() string {
	message := strings.Join(strings.Fields(e.Message), " ")
	if message == "" {
		message = "strict import rejected provider contract diagnostics"
	}
	return fmt.Sprintf("plan spec import failed (HTTP %d): %s: %s", e.HTTPStatus, e.Code, message)
}

type SpecImportPlanResponse struct {
	PlanID           string                 `json:"plan_id"`
	SourceHash       string                 `json:"source_hash"`
	OverlayPresent   bool                   `json:"overlay_present"`
	OverlayHash      string                 `json:"overlay_hash,omitempty"`
	SourceBundleHash string                 `json:"source_bundle_hash"`
	ReviewHash       string                 `json:"review_hash"`
	SourceFormat     string                 `json:"source_format"`
	AdapterVersion   string                 `json:"adapter_version"`
	ServiceID        string                 `json:"service_id"`
	Slug             string                 `json:"slug"`
	Name             string                 `json:"name"`
	IsNewService     bool                   `json:"is_new_service"`
	TargetVersion    string                 `json:"target_version"`
	TargetType       string                 `json:"target_type"`
	Action           string                 `json:"action"`
	Diff             SpecImportDiff         `json:"diff"`
	Usage            *SpecImportUsage       `json:"usage,omitempty"`
	Diagnostics      []SpecImportDiagnostic `json:"diagnostics,omitempty"`
}

type SpecImportApplyRequest struct {
	PlanID     string `json:"plan_id"`
	ReviewHash string `json:"review_hash"`
}

type SpecImportApplyResponse struct {
	Status           string `json:"status"`
	PlanID           string `json:"plan_id"`
	OperationID      string `json:"operation_id"`
	Phase            string `json:"phase"`
	CommitState      string `json:"commit_state"`
	ServiceID        string `json:"service_id"`
	ServiceVersionID string `json:"service_version_id"`
	Slug             string `json:"slug"`
	IsNewService     bool   `json:"is_new_service"`
	Action           string `json:"action"`
	Version          string `json:"version"`
	Revision         int    `json:"revision"`
}

// SpecImportApplyOutcomeUnknownError marks a POST whose transport or response
// cannot prove either failure or the exact committed Registry result.
type SpecImportApplyOutcomeUnknownError struct {
	cause error
}

// Error stays bounded because transport errors can contain private Engine URLs.
func (e *SpecImportApplyOutcomeUnknownError) Error() string {
	return "import apply response did not prove the mutation outcome"
}

// Unwrap retains the local cause for timeout/reset classification without rendering it.
func (e *SpecImportApplyOutcomeUnknownError) Unwrap() error {
	return e.cause
}

// SpecImportStatusResponse is the durable, read-only recovery view for one
// reviewed import operation.
type SpecImportStatusResponse struct {
	Status           string `json:"status"`
	OperationID      string `json:"operation_id"`
	Phase            string `json:"phase"`
	CommitState      string `json:"commit_state"`
	ServiceID        string `json:"service_id,omitempty"`
	ServiceVersionID string `json:"service_version_id,omitempty"`
	Slug             string `json:"slug,omitempty"`
	Version          string `json:"version,omitempty"`
	Revision         int    `json:"revision,omitempty"`
	Code             string `json:"code,omitempty"`
	Recovery         string `json:"recovery,omitempty"`
	Guidance         string `json:"guidance,omitempty"`
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
		if strictError := decodeSpecImportStrictError(resp.StatusCode, respBody); strictError != nil {
			return nil, strictError
		}
		return nil, fmt.Errorf("plan spec import failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, respBody))
	}

	var out SpecImportPlanResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func decodeSpecImportStrictError(status int, body []byte) *SpecImportStrictError {
	if status != http.StatusUnprocessableEntity {
		return nil
	}
	var response struct {
		Error       string                 `json:"error"`
		Message     string                 `json:"message"`
		Diagnostics []SpecImportDiagnostic `json:"diagnostics"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Error != "strict_import_rejected" {
		return nil
	}
	return &SpecImportStrictError{
		HTTPStatus: status, Code: response.Error, Message: response.Message,
		Diagnostics: append([]SpecImportDiagnostic(nil), response.Diagnostics...),
	}
}

// ApplySpecImport commits the exact source and overlay reviewed by Registry.
// reviewHash is opaque to the CLI so canonicalization has one owner.
func (c *Client) ApplySpecImport(planID, reviewHash string) (*SpecImportApplyResponse, error) {
	reqBody := SpecImportApplyRequest{PlanID: planID, ReviewHash: reviewHash}
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
	// Once the POST is handed to HTTP, transport failure cannot prove whether Registry committed.
	if err != nil {
		return nil, newSpecImportApplyOutcomeUnknownError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, truncated, readErr := readBoundedCLIHTTPBody(resp.Body)
		// Only a complete structured safe envelope authoritatively classifies POST failure.
		if readErr != nil || truncated {
			return nil, newSpecImportApplyOutcomeUnknownError(readErr)
		}
		return nil, classifySpecImportApplyHTTPError(resp.StatusCode, respBody)
	}
	return decodeSpecImportApplyProof(resp.Body, planID)
}

// newSpecImportApplyOutcomeUnknownError centralizes the bounded uncertainty marker.
func newSpecImportApplyOutcomeUnknownError(cause error) error {
	// A missing read error still needs a local cause for errors.Is/errors.As traversal.
	if cause == nil {
		cause = errors.New("import apply response was incomplete")
	}
	return &SpecImportApplyOutcomeUnknownError{cause: cause}
}

// classifySpecImportApplyHTTPError accepts only the slim server-owned error
// contract; proxy pages and generic status bodies cannot prove mutation state.
func classifySpecImportApplyHTTPError(status int, body []byte) error {
	err := newHTTPError(status, body)
	var apiErr *APIError
	// Stable commit knowledge and recovery distinguish authoritative import errors from generic HTTP categories.
	if errors.As(err, &apiErr) && apiErr.Phase != "" && validImportCommitState(apiErr.CommitState) && apiErr.Recovery != "" {
		return fmt.Errorf("apply spec import failed (HTTP %d): %w", status, apiErr)
	}
	return newSpecImportApplyOutcomeUnknownError(err)
}

// decodeSpecImportApplyProof admits one bounded JSON object and validates every
// identity/result field before the CLI records a successful mutation.
func decodeSpecImportApplyProof(body io.Reader, planID string) (*SpecImportApplyResponse, error) {
	payload, truncated, err := readBoundedCLIHTTPBody(body)
	// Read failure or size truncation leaves the POST outcome unknown.
	if err != nil || truncated {
		return nil, newSpecImportApplyOutcomeUnknownError(err)
	}
	var result SpecImportApplyResponse
	// json.Unmarshal rejects empty, malformed, truncated, and trailing non-whitespace bodies.
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, newSpecImportApplyOutcomeUnknownError(err)
	}
	// A syntactically valid body is still non-authoritative until its durable proof matches the receipt.
	if err := validateSpecImportApplyProof(result, planID); err != nil {
		return nil, newSpecImportApplyOutcomeUnknownError(err)
	}
	return &result, nil
}

// validateSpecImportApplyProof binds success to the requested operation and the
// complete result invariant persisted atomically by Registry.
func validateSpecImportApplyProof(result SpecImportApplyResponse, planID string) error {
	// Identity, terminal state, and durable result are independent proof obligations.
	if err := validateSpecImportApplyIdentity(result, planID); err != nil {
		return err
	}
	// A matching operation is not successful until Registry reports committed completion.
	if result.Status != "applied" || result.Phase != "complete" || result.CommitState != "committed" {
		return errors.New("import apply response was not a committed completion")
	}
	return validateSpecImportApplyResult(result)
}

// validateSpecImportApplyIdentity proves both response aliases name the exact receipt operation.
func validateSpecImportApplyIdentity(result SpecImportApplyResponse, planID string) error {
	requestedID, requestedErr := uuid.Parse(strings.TrimSpace(planID))
	planProofID, planErr := uuid.Parse(strings.TrimSpace(result.PlanID))
	operationID, operationErr := uuid.Parse(strings.TrimSpace(result.OperationID))
	// Both response identities must be canonical aliases of the requested receipt operation.
	if requestedErr != nil || planErr != nil || operationErr != nil || requestedID != planProofID || requestedID != operationID {
		return errors.New("import apply operation proof did not match the receipt")
	}
	return nil
}

// validateSpecImportApplyResult enforces the same complete result invariant Registry persists atomically.
func validateSpecImportApplyResult(result SpecImportApplyResponse) error {
	_, serviceErr := uuid.Parse(result.ServiceID)
	_, versionErr := uuid.Parse(result.ServiceVersionID)
	// Every atomically stored result field is required to reproduce the committed success exactly.
	if serviceErr != nil || versionErr != nil || strings.TrimSpace(result.Slug) == "" || strings.TrimSpace(result.Version) == "" || result.Revision <= 0 {
		return errors.New("import apply response omitted committed result proof")
	}
	return nil
}

// validImportCommitState admits the complete stable safe-error vocabulary.
func validImportCommitState(value string) bool {
	switch value {
	case "not_committed", "committed", "unknown":
		return true
	default:
		return false
	}
}

// GetSpecImportStatus reads the existing Registry operation ledger without
// retrying or otherwise mutating the reviewed import.
func (c *Client) GetSpecImportStatus(operationID string) (*SpecImportStatusResponse, error) {
	operationID = strings.TrimSpace(operationID)
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+"/integrations/import/operations/"+operationID, nil)
	// Request construction failures happen before any remote status read.
	if err != nil {
		return nil, err
	}
	// Authentication matches plan/apply because status is account-scoped.
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}
	resp, err := c.doRequest(req)
	// Transport failures retain the shared client's safe error semantics.
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Structured Registry errors flow through the existing bounded parser.
	if resp.StatusCode >= 400 {
		body := readBoundedHTTPErrorBody(resp.Body)
		return nil, fmt.Errorf("get import status failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, body))
	}
	var result SpecImportStatusResponse
	// Malformed success bodies cannot be treated as a known operation outcome.
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) DeactivateApp(appID string) error {
	req, err := http.NewRequest(http.MethodDelete, c.BaseURL+"/apps/"+appID+"/", nil)
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
		return fmt.Errorf("deactivate failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, b))
	}
	return nil
}
