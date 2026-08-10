package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrConnectConfigNotFound lets a caller distinguish "nothing registered yet"
// from a genuine request failure -- connect.go's `get` command prints a
// friendly message for the former instead of surfacing a raw HTTP error.
var ErrConnectConfigNotFound = errors.New("connect config not found")

// ConnectConfigUpsertRequest mirrors the Engine admin endpoint's
// partial-update payload: pointer fields so the CLI sends only what the
// caller actually provided (e.g. a single field rotation) instead of
// resending a full app registration on every call -- see
// UpsertConnectConfigHandler on the Engine for the merge semantics this
// depends on.
type ConnectConfigUpsertRequest struct {
	AuthType     *string `json:"auth_type,omitempty"`
	AuthName     *string `json:"auth_name,omitempty"`
	Enabled      *bool   `json:"enabled,omitempty"`
	ClientID     *string `json:"client_id,omitempty"`
	ClientSecret *string `json:"client_secret,omitempty"`
	RedirectURI  *string `json:"redirect_uri,omitempty"`
}

// ConnectConfigResponse never carries decrypted client_id/client_secret --
// HasClientID/HasClientSecret only report presence, matching Engine's
// connectConfigResponse projection, so this type is safe to print directly.
type ConnectConfigResponse struct {
	ID              string    `json:"id"`
	BucketID        string    `json:"bucket_id"`
	ServiceID       string    `json:"service_id"`
	AuthType        string    `json:"auth_type"`
	AuthName        string    `json:"auth_name"`
	Enabled         bool      `json:"enabled"`
	RedirectURI     string    `json:"redirect_uri"`
	HasClientID     bool      `json:"has_client_id"`
	HasClientSecret bool      `json:"has_client_secret"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// UpsertConnectConfig registers or updates a bucket's OAuth/OIDC app
// registration for a service. This is an immediate admin action -- like
// UpsertSecret/UpsertSecrets, it takes effect on save with no plan/apply
// step, and unlike workspace.yaml's connect: block it is never written to
// or read back from a config file.
func (c *Client) UpsertConnectConfig(bucketID, serviceID string, payload ConnectConfigUpsertRequest) (*ConnectConfigResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	path := fmt.Sprintf("%s/workspace/buckets/%s/services/%s/connect-config", c.BaseURL, bucketID, serviceID)
	req, err := http.NewRequest("PUT", path, bytes.NewBuffer(body))
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
		return nil, fmt.Errorf("upsert connect config failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}
	var out ConnectConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode connect config response: %w", err)
	}
	return &out, nil
}

// GetConnectConfig reads back a bucket's registered OAuth/OIDC app --
// auth_type/enabled/redirect_uri plus has_client_id/has_client_secret, never
// the decrypted client_id/client_secret themselves, matching what `set`
// already returns. Returns ErrConnectConfigNotFound (not a generic error)
// when nothing has been registered yet, so a caller can tell "not set up"
// apart from "the request failed".
func (c *Client) GetConnectConfig(bucketID, serviceID string) (*ConnectConfigResponse, error) {
	path := fmt.Sprintf("%s/workspace/buckets/%s/services/%s/connect-config", c.BaseURL, bucketID, serviceID)
	req, err := http.NewRequest("GET", path, nil)
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

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrConnectConfigNotFound
	}
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get connect config failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}
	var out ConnectConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode connect config response: %w", err)
	}
	return &out, nil
}
