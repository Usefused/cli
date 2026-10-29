package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ServiceLifecycle remains readable by exact ID after the full definition is deleted.
type ServiceLifecycle struct {
	ServiceID string     `json:"service_id"`
	Name      string     `json:"name"`
	DeleteAt  *time.Time `json:"delete_at"`
	DeletedAt *time.Time `json:"deleted_at"`
}

// GetServiceLifecycle reads Registry state through the configured Engine's authorization boundary.
func (c *Client) GetServiceLifecycle(id string) (*ServiceLifecycle, error) {
	var state ServiceLifecycle
	// Failed reads must never be interpreted as an active or undeleted service.
	if err := c.serviceLifecycleRequest(http.MethodGet, id, "/lifecycle", nil, &state); err != nil {
		return nil, err
	}
	// A malformed success cannot identify another service as the requested target.
	if state.ServiceID != id {
		return nil, fmt.Errorf("service lifecycle response has an unexpected service ID")
	}
	return &state, nil
}

// ScheduleServiceDeletion deprecates every version with a real Registry deletion deadline.
func (c *Client) ScheduleServiceDeletion(id string, at time.Time) error {
	return c.serviceLifecycleRequest(http.MethodPut, id, "/lifecycle", map[string]time.Time{"delete_at": at.UTC()}, nil)
}

// CancelServiceDeletion cancels only the pending deadline; it cannot restore deleted definitions.
func (c *Client) CancelServiceDeletion(id string) error {
	return c.serviceLifecycleRequest(http.MethodDelete, id, "/lifecycle", nil, nil)
}

// DeleteRegistryService deletes the shared Registry definition rather than workspace membership alone.
func (c *Client) DeleteRegistryService(id string) error {
	return c.serviceLifecycleRequest(http.MethodDelete, id, "", nil, nil)
}

// serviceLifecycleRequest shares authentication and typed errors without retrying a potentially committed mutation.
func (c *Client) serviceLifecycleRequest(method, id, suffix string, body any, result any) error {
	var payload bytes.Buffer
	// Reads and cancellation intentionally carry no JSON payload.
	if body != nil {
		// Encoding failures must stop before a partial mutation reaches the Engine.
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequest(method, c.BaseURL+"/integrations/"+url.PathEscape(id)+suffix, &payload)
	// Invalid Engine configuration must not fall back to a different destination.
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", c.APIKey)
	// Content-Type describes only actual JSON, not empty DELETE/GET bodies.
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.doRequest(req)
	// A transport failure cannot establish whether a mutation committed.
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	want := http.StatusNoContent
	// Only lifecycle reads return a document; successful mutations return 204.
	if result != nil {
		want = http.StatusOK
	}
	// Preserve permission, deadline, missing-service and mixed-version failures for callers.
	if resp.StatusCode != want {
		return fmt.Errorf("Registry service lifecycle: %w", newHTTPError(resp.StatusCode, readBoundedHTTPErrorBody(resp.Body)))
	}
	// Never attempt to decode an empty successful mutation response.
	if result != nil {
		return json.NewDecoder(resp.Body).Decode(result)
	}
	return nil
}
