package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

	var graphqlResp struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(respBody, &graphqlResp); err != nil {
		bodyStr := string(respBody)
		if len(bodyStr) > 200 {
			bodyStr = bodyStr[:200] + "..."
		}
		return fmt.Errorf("failed to unmarshal graphql response: %v, body snippet: %s", err, bodyStr)
	}

	if len(graphqlResp.Errors) > 0 {
		return fmt.Errorf("graphql error: %s", graphqlResp.Errors[0].Message)
	}

	return json.Unmarshal(graphqlResp.Data, out)
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

func (c *Client) SearchEndpoints(serviceID, q string) ([]Integration, error) {
	query := `
		query SearchEndpoints($serviceId: String!, $q: String!) {
			searchEndpoints(serviceId: $serviceId, q: $q) {
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
	err := c.GraphQL(query, map[string]interface{}{"serviceId": serviceID, "q": q}, &resp)
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

	buf := make([]byte, 4096)
	var line []byte
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			for i := 0; i < n; i++ {
				if buf[i] == '\n' {
					if bytes.HasPrefix(line, []byte("data: ")) {
						data := bytes.TrimPrefix(line, []byte("data: "))
						var event SDKEvent
						if err := json.Unmarshal(data, &event); err == nil {
							eventChan <- event
						}
					}
					line = line[:0]
				} else {
					line = append(line, buf[i])
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			errChan <- err
			break
		}
	}
	close(eventChan)
	close(errChan)
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
