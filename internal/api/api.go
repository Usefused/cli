package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTP:    &http.Client{},
	}
}

func (c *Client) GraphQL(query string, variables map[string]interface{}, out interface{}) error {
	payload := map[string]interface{}{
		"query":     query,
		"variables": variables,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", c.BaseURL+"/graphql", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 400 {
		bodyStr := string(respBody)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200] + "..."
		}
		return fmt.Errorf("graphql request failed (HTTP %d): %s", resp.StatusCode, bodyStr)
	}

	return decodeGraphQLData(respBody, out)
}

func decodeGraphQLData(respBody []byte, out interface{}) error {
	var graphqlResp struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &graphqlResp); err != nil {
		return fmt.Errorf("failed to unmarshal graphql response: %v, body snippet: %s", err, truncateBody(respBody))
	}
	if len(graphqlResp.Errors) > 0 {
		return fmt.Errorf("graphql error: %s", graphqlResp.Errors[0].Message)
	}
	return json.Unmarshal(graphqlResp.Data, out)
}

func truncateBody(body []byte) string {
	bodyStr := string(body)
	if len(bodyStr) > 200 {
		return bodyStr[:200] + "..."
	}
	return bodyStr
}

func formatHTTPErrorBody(respBody []byte) string {
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(respBody, &payload); err == nil {
		if msg := strings.TrimSpace(payload.Error); msg != "" {
			return msg
		}
		if msg := strings.TrimSpace(payload.Message); msg != "" {
			return msg
		}
	}
	return strings.TrimSpace(truncateBody(respBody))
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

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("health check failed (HTTP %d)", resp.StatusCode)
	}

	var out HealthStatus
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Models

type Service struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ServiceVisibility struct {
	ServiceID string `json:"id"`
	IsOwner   bool   `json:"is_owner"`
	IsPublic  bool   `json:"is_public"`
	Slug      string `json:"slug"`
	Provider  string `json:"provider"`
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
	AddedBy          string                    `json:"added_by"`
	CreatedAt        string                    `json:"created_at"`
}

type WorkspaceServiceVersion struct {
	ID               string `json:"id"`
	ServiceVersionID string `json:"service_version_id"`
	Version          string `json:"version"`
	Status           string `json:"status"`
	CreatedAt        string `json:"created_at"`
	EnabledAt        string `json:"enabled_at"`
}

func (c *Client) ListWorkspaceServices(names ...string) ([]WorkspaceService, error) {
	reqURL := c.BaseURL + "/workspace/services"
	if len(names) > 0 {
		query := url.Values{}
		query.Set("names", strings.Join(names, ","))
		reqURL += "?" + query.Encode()
	}
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list workspace services failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var out []WorkspaceService
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
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
				provider
				is_owner
				is_public
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
}

type ServiceServer struct {
	URL         string `json:"url"`
	Description string `json:"description"`
}

type ServiceInfo struct {
	ID      string          `json:"id"`
	Name    string          `json:"name"`
	Slug    string          `json:"slug"`
	BaseURL string          `json:"base_url"`
	Servers []ServiceServer `json:"servers"`
}

func (c *Client) GetServiceInfo(serviceSlug string) (*ServiceInfo, error) {
	query := `
		query GetServiceInfo($id: String!, $provider: String) {
			service(id: $id, provider: $provider) {
				id
				name
				slug
				base_url
				servers {
					url
					description
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
	query := `
		query SearchEndpoints($serviceId: String!, $version: String, $q: String!) {
			searchEndpoints(serviceId: $serviceId, version: $version, q: $q) {
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
	err := c.GraphQL(query, map[string]interface{}{"serviceId": serviceID, "version": version, "q": q}, &resp)
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

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to generate SDK (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(respBody))
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

	resp, err := c.HTTP.Do(req)
	if err != nil {
		errChan <- err
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errChan <- fmt.Errorf("stream failed with status: %d", resp.StatusCode)
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

func (c *Client) GetSDK(sdkID string) (*SDKDetails, error) {
	query := `
		query GetSDK($id: String!) {
			sdk(id: $id) {
				sandbox_url
			}
		}
	`
	var resp GetSDKResponse
	err := c.GraphQL(query, map[string]interface{}{"id": sdkID}, &resp)
	if err != nil {
		return nil, err
	}
	return &resp.SDK, nil
}

type SDKBasicDetails struct {
	ID         string `json:"id"`
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
	query := `
		query GetSDKSelectionsByName($name: String!) {
			sdkByName(name: $name, version: "") {
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
	if err := c.GraphQL(query, map[string]interface{}{"name": name}, &resp); err != nil {
		return nil, err
	}
	return &resp.SDK, nil
}

// GetSDKSelectionResourceNames resolves a service's selection on a generated
// SDK -- whether select_all or an explicit endpoint/webhook ID list -- into
// the operation names `sdk sync` writes to sdk.yaml. select_all has no local
// yaml representation, so it must always be enumerated explicitly.
func (c *Client) GetSDKSelectionResourceNames(sdkID, serviceID string) ([]string, error) {
	query := `
		query GetSDKSelectionResourceNames($sdkId: String!, $serviceId: String!) {
			sdkSelectionResources(sdkId: $sdkId, serviceId: $serviceId) {
				name
			}
		}
	`
	var resp struct {
		Resources []struct {
			Name string `json:"name"`
		} `json:"sdkSelectionResources"`
	}
	if err := c.GraphQL(query, map[string]interface{}{"sdkId": sdkID, "serviceId": serviceID}, &resp); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(resp.Resources))
	for _, r := range resp.Resources {
		names = append(names, r.Name)
	}
	return names, nil
}

func (c *Client) DownloadSDK(sdkID string) ([]byte, error) {
	req, err := http.NewRequest("GET", c.BaseURL+"/sdks/"+sdkID+"/download", nil)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

type MCPActivateResult struct {
	MCPURL string `json:"mcp_url"`
}

// ActivateMCPServer posts to the Engine to deploy an MCP server.
func (c *Client) ActivateMCPServer(sdkID string) (*MCPActivateResult, error) {
	req, err := http.NewRequest("POST", c.BaseURL+"/engine/sdks/"+sdkID+"/activate", nil)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		bodyStr := string(respBody)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200] + "..."
		}
		return nil, fmt.Errorf("failed to activate MCP server (HTTP %d): %s", resp.StatusCode, bodyStr)
	}

	var out MCPActivateResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

type SDKConfigPlanResponse struct {
	PlanID        string                 `json:"plan_id"`
	ConfigKey     string                 `json:"config_key"`
	SourceHash    string                 `json:"source_hash"`
	Summary       map[string]interface{} `json:"summary"`
	Notifications NotificationInbox      `json:"notifications"`
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
	PlanID     string                 `json:"plan_id"`
	ConfigKey  string                 `json:"config_key"`
	SourceHash string                 `json:"source_hash"`
	Summary    map[string]interface{} `json:"summary"`
}

type ConfigApplyResponse struct {
	Status string `json:"status"`
	PlanID string `json:"plan_id"`
}

func (c *Client) PlanSDKConfig(sourceHash, configKey string, config json.RawMessage) (*SDKConfigPlanResponse, error) {
	reqBody := map[string]interface{}{
		"source_hash": sourceHash,
		"config_key":  configKey,
		"config":      config,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", c.BaseURL+"/sdk-config/plan", bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("plan SDK config failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(respBody))
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

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("plan workspace config failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var out ConfigPlanResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

type SDKConfigApplyResponse struct {
	Status string `json:"status"`
	PlanID string `json:"plan_id"`
	SDKID  string `json:"sdk_id"`
	JobID  string `json:"job_id"`
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

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("apply SDK config failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(respBody))
	}

	var out SDKConfigApplyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ApplyWorkspaceConfig(planID, sourceHash string) (*ConfigApplyResponse, error) {
	reqBody := map[string]interface{}{
		"plan_id":     planID,
		"source_hash": sourceHash,
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

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("apply workspace config failed (HTTP %d): %s", resp.StatusCode, string(respBody))
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

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update workspace plan action failed (HTTP %d): %s", resp.StatusCode, string(respBody))
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
	IsPublic      bool   `json:"is_public,omitempty"`
	TargetType    string `json:"target_type,omitempty"`
	Category      string `json:"category,omitempty"`
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
		return nil, fmt.Errorf("plan spec import failed (HTTP %d): %s", resp.StatusCode, string(respBody))
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

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("apply spec import failed (HTTP %d): %s", resp.StatusCode, string(respBody))
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

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("download generated SDK failed with status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}
