package api

type AppExecutionEvent struct {
	ID                  string            `json:"id"`
	TraceID             string            `json:"trace_id,omitempty"`
	SpanID              string            `json:"span_id,omitempty"`
	AppFamilyID         string            `json:"app_family_id"`
	AppID               string            `json:"app_id"`
	AppVersion          string            `json:"app_version"`
	AppKind             string            `json:"app_kind"`
	Transport           string            `json:"transport"`
	ProviderProtocol    string            `json:"provider_protocol,omitempty"`
	Direction           string            `json:"direction"`
	ServiceID           string            `json:"service_id,omitempty"`
	ServiceVersionID    string            `json:"service_version_id,omitempty"`
	OperationID         string            `json:"operation_id,omitempty"`
	Operation           string            `json:"operation,omitempty"`
	HTTPMethod          string            `json:"http_method,omitempty"`
	RequestPath         string            `json:"request_path,omitempty"`
	Environment         string            `json:"environment,omitempty"`
	ProviderHost        string            `json:"provider_host,omitempty"`
	ProviderHTTPStatus  *int              `json:"provider_http_status,omitempty"`
	ProviderStatusClass string            `json:"provider_status_class,omitempty"`
	Status              string            `json:"status"`
	FailureReason       string            `json:"failure_reason,omitempty"`
	FailureCategory     string            `json:"failure_category,omitempty"`
	FailureCode         string            `json:"failure_code,omitempty"`
	LatencyMS           int64             `json:"latency_ms"`
	ProviderLatencyMS   *int64            `json:"provider_latency_ms,omitempty"`
	AttemptCount        int               `json:"attempt_count"`
	AuthSchemeTypes     []string          `json:"auth_scheme_types"`
	AuthSchemeCount     int               `json:"auth_scheme_count"`
	PaginationType      string            `json:"pagination_type,omitempty"`
	PaginationPageCount int64             `json:"pagination_page_count,omitempty"`
	PaginationItemCount int64             `json:"pagination_item_count,omitempty"`
	PaginationByteCount int64             `json:"pagination_byte_count,omitempty"`
	RateLimitDecision   string            `json:"rate_limit_decision,omitempty"`
	RequestBytes        int64             `json:"request_bytes,omitempty"`
	ResponseBytes       int64             `json:"response_bytes,omitempty"`
	IdempotencyReplayed bool              `json:"idempotency_replayed,omitempty"`
	StartedAt           string            `json:"started_at"`
	EndedAt             string            `json:"ended_at"`
	Timings             []ExecutionTiming `json:"timings"`
}

type ExecutionTiming struct {
	Name       string  `json:"name"`
	DurationMS float64 `json:"duration_ms"`
}

type AppExecutionEventPage struct {
	Items []AppExecutionEvent `json:"items"`
	Total int                 `json:"total"`
}

type AppExecutionEventOptions struct {
	IncludeAllVersions bool
	Status             string
	StartDate          string
	EndDate            string
	PageOptions
}

// ListSDKExecutionEvents reads the canonical Engine activity page for SDK transport.
func (c *Client) ListSDKExecutionEvents(appID string, opts AppExecutionEventOptions) (*AppExecutionEventPage, error) {
	query := `query SDKExecutionActivity($appId: String!, $includeAllVersions: Boolean!, $status: String, $limit: Int!, $offset: Int!, $startDate: String, $endDate: String) {
		appExecutionEvents(app_id: $appId, include_all_versions: $includeAllVersions, transport: "sdk", status: $status, limit: $limit, offset: $offset, start_date: $startDate, end_date: $endDate) {
			total
			items {
				id trace_id span_id app_family_id app_id app_version app_kind
				transport provider_protocol direction service_id service_version_id
				operation_id operation http_method request_path environment provider_host
				provider_http_status provider_status_class status failure_reason failure_category failure_code
				latency_ms provider_latency_ms attempt_count auth_scheme_types auth_scheme_count
				pagination_type pagination_page_count pagination_item_count pagination_byte_count
				rate_limit_decision request_bytes response_bytes idempotency_replayed
				started_at ended_at timings { name duration_ms }
			}
		}
	}`
	var response struct {
		Page AppExecutionEventPage `json:"appExecutionEvents"`
	}
	variables := pageVars(opts.PageOptions)
	variables["appId"] = appID
	variables["includeAllVersions"] = opts.IncludeAllVersions
	variables["status"] = optionalGraphQLString(opts.Status)
	variables["startDate"] = optionalGraphQLString(opts.StartDate)
	variables["endDate"] = optionalGraphQLString(opts.EndDate)
	err := c.EngineGraphQL(query, variables, &response)
	return &response.Page, err
}

// optionalGraphQLString maps an omitted CLI filter to a GraphQL null variable.
func optionalGraphQLString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
