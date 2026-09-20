package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

const managedAuthPath = "/workspace/managed-auth"

type ManagedAuthStatusResponse struct {
	Status            string `json:"status"`
	RevocationPending bool   `json:"revocation_pending"`
}

// ManagedAuthStatus reads saved enrollment state without changing it.
func (c *Client) ManagedAuthStatus() (*ManagedAuthStatusResponse, error) {
	return c.managedAuth(http.MethodGet)
}

// EnableManagedAuth persists opt-in and reconciles the installation credential.
func (c *Client) EnableManagedAuth() (*ManagedAuthStatusResponse, error) {
	return c.managedAuth(http.MethodPut)
}

// DisableManagedAuth persists opt-out even when remote revocation must be retried.
func (c *Client) DisableManagedAuth() (*ManagedAuthStatusResponse, error) {
	return c.managedAuth(http.MethodDelete)
}

// managedAuth keeps authentication and error handling identical across the enrollment controls.
func (c *Client) managedAuth(method string) (*ManagedAuthStatusResponse, error) {
	req, err := http.NewRequest(method, c.BaseURL+managedAuthPath, nil)
	// Malformed local configuration cannot produce a valid Engine request.
	if err != nil {
		return nil, err
	}
	// Attach the existing Engine control credential only when configured.
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}
	resp, err := c.doRequest(req)
	// Transport failure does not imply the preference was saved.
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Preserve the ordinary structured HTTP failure contract.
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("managed-auth request failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, readBoundedHTTPErrorBody(resp.Body)))
	}
	var out ManagedAuthStatusResponse
	// Only a complete status response can acknowledge a control action.
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}
