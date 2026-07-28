package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type IntegrationExtractionStartRequest struct {
	Name               string `json:"name"`
	ServiceSlug        string `json:"service_slug"`
	Version            string `json:"version"`
	SourceURL          string `json:"source_url"`
	SourceContent      string `json:"source_content,omitempty"`
	ImportMethod       string `json:"import_method"`
	TargetResourceName string `json:"target_resource_name,omitempty"`
	TargetType         string `json:"target_type,omitempty"`
}

type IntegrationExtractionStartResponse struct {
	SessionID string `json:"session_id"`
	ServiceID string `json:"service_id,omitempty"`
}

type IntegrationEndpointIdentifier struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Name   string `json:"name,omitempty"`
}

type IntegrationExtractionQuestion struct {
	ID           string        `json:"id"`
	Text         string        `json:"text"`
	Options      []string      `json:"options,omitempty"`
	Endpoints    []Integration `json:"endpoints,omitempty"`
	DefaultValue string        `json:"default_value,omitempty"`
}

type IntegrationExtractionEvent struct {
	Type          string                          `json:"type"`
	Message       string                          `json:"message,omitempty"`
	Questions     []IntegrationExtractionQuestion `json:"questions,omitempty"`
	ToolCallID    string                          `json:"tool_call_id,omitempty"`
	IntegrationID string                          `json:"integration_id,omitempty"`
	Version       string                          `json:"version,omitempty"`
	Payload       *Integration                    `json:"payload,omitempty"`
}

type WorkspaceServiceAddRequest struct {
	ServiceID        string `json:"service_id"`
	ServiceName      string `json:"service_name"`
	VersionTag       string `json:"version_tag"`
	ServiceVersionID string `json:"service_version_id,omitempty"`
}

func (c *Client) StartIntegrationExtraction(ctx context.Context, req IntegrationExtractionStartRequest) (*IntegrationExtractionStartResponse, error) {
	var out IntegrationExtractionStartResponse
	if err := c.postJSON(ctx, "/integrations/start", req, http.StatusAccepted, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) RespondIntegrationExtraction(ctx context.Context, sessionID, answer string) error {
	req := struct {
		SessionID string `json:"session_id"`
		Answer    string `json:"answer"`
	}{SessionID: sessionID, Answer: answer}
	return c.postJSON(ctx, "/integrations/respond", req, http.StatusOK, nil)
}

func (c *Client) AddWorkspaceService(ctx context.Context, req WorkspaceServiceAddRequest) error {
	return c.postJSON(ctx, "/workspace/services", req, http.StatusOK, nil)
}

func (c *Client) postJSON(ctx context.Context, path string, reqBody any, successStatus int, out any) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewBuffer(body))
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
	if resp.StatusCode != successStatus {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s failed (HTTP %d): %s", strings.TrimPrefix(path, "/"), resp.StatusCode, formatHTTPErrorBody(respBody))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) StreamIntegrationExtraction(ctx context.Context, sessionID string, onEvent func(IntegrationExtractionEvent) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/integrations/session/"+url.PathEscape(sessionID)+"/stream", nil)
	if err != nil {
		return err
	}
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
		return fmt.Errorf("integration extraction stream failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(respBody))
	}
	return readSSEEvents(resp.Body, onEvent)
}

func readSSEEvents(reader io.Reader, onEvent func(IntegrationExtractionEvent) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var data []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := dispatchSSEData(data, onEvent); err != nil {
				return err
			}
			data = nil
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if payload, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimSpace(payload))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return dispatchSSEData(data, onEvent)
}

func dispatchSSEData(data []string, onEvent func(IntegrationExtractionEvent) error) error {
	raw := strings.TrimSpace(strings.Join(data, "\n"))
	if raw == "" {
		return nil
	}
	var event IntegrationExtractionEvent
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return fmt.Errorf("failed to parse integration extraction stream event: %w", err)
	}
	if onEvent == nil {
		return errors.New("integration extraction stream callback is required")
	}
	return onEvent(event)
}
