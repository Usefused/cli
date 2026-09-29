package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/google/uuid"
)

// UnifiedAppTraffic identifies the serving version without exposing credentials or source.
type UnifiedAppTraffic struct {
	AppFamilyID string `json:"app_family_id"`
	ActiveAppID string `json:"active_app_id"`
}

// ResolveUnifiedAppReference uses Engine's exact name/version lookup within the Unified App namespace.
func (c *Client) ResolveUnifiedAppReference(name, version string) (string, error) {
	id, err := c.resolveAppReference(name, version, "unified_app")
	// Denial or absence must not fall back to another app or a different version.
	if err != nil {
		return "", err
	}
	// An incomplete lookup response is not authority to send a promotion request.
	if _, err := uuid.Parse(id); err != nil {
		return "", errGraphQLDataMalformed
	}
	return id, nil
}

// GetUnifiedAppTraffic obtains current deployment state before a guarded promotion.
func (c *Client) GetUnifiedAppTraffic(appID string) (*UnifiedAppTraffic, error) {
	return c.unifiedAppTrafficRequest(http.MethodGet, appID, "traffic", nil)
}

// PromoteUnifiedApp selects retained immutable code using the same endpoint as the UI.
func (c *Client) PromoteUnifiedApp(appID, expected string) (*UnifiedAppTraffic, error) {
	body, _ := json.Marshal(map[string]string{"expected_active_app_id": expected})
	return c.unifiedAppTrafficRequest(http.MethodPost, appID, "promote", body)
}

// unifiedAppTrafficRequest validates exact path identity and never automatically retries a mutation.
func (c *Client) unifiedAppTrafficRequest(method, appID, action string, body []byte) (*UnifiedAppTraffic, error) {
	parsed, err := uuid.Parse(appID)
	// Reject aliases and malformed paths before accessing saved credentials or the network.
	if err != nil || parsed.String() != appID {
		return nil, errors.New("an exact App version ID is required")
	}
	request, err := http.NewRequest(method, c.BaseURL+"/apps/"+appID+"/"+action, bytes.NewReader(body))
	// Construction errors must not become an empty control request.
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	// Management credentials stay in headers, never in output or route parameters.
	if c.APIKey != "" {
		request.Header.Set("x-api-key", c.APIKey)
	}
	response, err := c.doRequest(request)
	// An uncertain mutation remains an error and is not silently repeated.
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	// Preserve conflict details so users can review a competing deployment.
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Unified App traffic request failed: %w", newHTTPError(response.StatusCode, readBoundedHTTPErrorBody(response.Body)))
	}
	var result UnifiedAppTraffic
	// Malformed success cannot be reported as a completed traffic switch.
	if err := json.NewDecoder(io.LimitReader(response.Body, 2048)).Decode(&result); err != nil {
		return nil, err
	}
	// Every receipt belongs to an exact family and promotion must acknowledge the requested target.
	if _, err := uuid.Parse(result.AppFamilyID); err != nil {
		return nil, errors.New("invalid traffic receipt")
	}
	if method == http.MethodPost && result.ActiveAppID != appID {
		return nil, errors.New("promotion receipt does not match requested version")
	}
	return &result, nil
}
