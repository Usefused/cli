package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// Models

type Service struct {
	ID   string `json:"id"`
	Name string `json:"name"`
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
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	ServiceID   string `json:"service_id"`
	Version     string `json:"version"`
	ServiceName string `json:"service_name"`
	AddedBy     string `json:"added_by"`
	CreatedAt   string `json:"created_at"`
}

func (c *Client) ListWorkspaceServices(names ...string) ([]WorkspaceService, error) {
	url := c.BaseURL + "/workspace/services"
	if len(names) > 0 {
		url += "?names=" + strings.Join(names, ",")
	}
	req, err := http.NewRequest("GET", url, nil)
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

type ServiceApiVersion struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
}

func (c *Client) ServiceApiVersions(serviceID string) ([]ServiceApiVersion, error) {
	query := `
		query ServiceApiVersions($serviceId: String!) {
			serviceApiVersions(serviceId: $serviceId) {
				id
				name
				is_default
			}
		}
	`
	var resp struct {
		ServiceApiVersions []ServiceApiVersion `json:"serviceApiVersions"`
	}
	err := c.GraphQL(query, map[string]interface{}{"serviceId": serviceID}, &resp)
	return resp.ServiceApiVersions, err
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
		bodyStr := string(respBody)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200] + "..."
		}
		return nil, fmt.Errorf("failed to generate SDK (HTTP %d): %s", resp.StatusCode, bodyStr)
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
	close(eventChan)
	close(errChan)
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
		return nil, fmt.Errorf("plan SDK config failed (HTTP %d): %s", resp.StatusCode, string(respBody))
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
		return nil, fmt.Errorf("apply SDK config failed (HTTP %d): %s", resp.StatusCode, string(respBody))
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
