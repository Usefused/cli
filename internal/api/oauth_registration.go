package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

// TemporaryOAuthClientRequest contains only the metadata accepted by Engine's anonymous local-client registration endpoint.
type TemporaryOAuthClientRequest struct {
	Name        string   `json:"name,omitempty"`
	RedirectURI string   `json:"redirect_uri"`
	Scopes      []string `json:"scopes"`
}

// TemporaryOAuthClientRegistration carries the one-time client secret and its fixed expiry.
type TemporaryOAuthClientRegistration struct {
	ClientID          string    `json:"client_id"`
	ClientSecret      string    `json:"client_secret"`
	ClientIDExpiresAt time.Time `json:"client_id_expires_at"`
}

// OAuthRegistrationScopes reads Engine's public issuer metadata so an anonymous CLI can offer current permission names.
func (c *Client) OAuthRegistrationScopes() ([]string, error) {
	management, err := c.oauthIssuerScopes("/.well-known/oauth-authorization-server", false)
	if err != nil {
		return nil, err
	}
	// The MCP issuer advertises a separate scope that the UI also lists for client registration.
	mcp, err := c.oauthIssuerScopes("/.well-known/oauth-authorization-server/mcp/oauth", true)
	if err != nil {
		return nil, err
	}
	// Stable unique values keep terminal selection independent of issuer ordering.
	seen := make(map[string]struct{}, len(management)+len(mcp))
	values := make([]string, 0, len(management)+len(mcp))
	for _, scope := range append(management, mcp...) {
		// Duplicate issuer entries must not appear as duplicate checkboxes.
		if _, duplicate := seen[scope]; duplicate {
			continue
		}
		seen[scope] = struct{}{}
		values = append(values, scope)
	}
	sort.Strings(values)
	return values, nil
}

// oauthIssuerScopes reads one bounded public discovery document and tolerates an absent optional MCP issuer.
func (c *Client) oauthIssuerScopes(path string, optional bool) ([]string, error) {
	request, err := http.NewRequest(http.MethodGet, c.BaseURL+path, nil)
	// Invalid Engine URLs cannot be used to infer a local scope catalog.
	if err != nil {
		return nil, err
	}
	// Public discovery must not follow a redirect into another origin.
	response, err := c.doRequestWithoutRedirect(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	// Engines without an MCP issuer have no MCP scope to add to the picker.
	if optional && response.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	// Failed metadata reads retain the shared bounded error projection.
	if response.StatusCode != http.StatusOK {
		errorBody, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return nil, fmt.Errorf("read OAuth scopes (HTTP %d): %w", response.StatusCode, newHTTPError(response.StatusCode, errorBody))
	}
	metadataBody, err := io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
	// An incomplete or oversized catalog must not produce a misleading partial picker.
	if err != nil || len(metadataBody) > 16<<10 {
		return nil, errors.New("Fused returned an invalid OAuth scope catalog")
	}
	var metadata struct {
		Scopes []string `json:"scopes_supported"`
	}
	// A missing scope list cannot support informed terminal review.
	if err := json.Unmarshal(metadataBody, &metadata); err != nil || len(metadata.Scopes) == 0 {
		return nil, errors.New("Fused returned an empty OAuth scope catalog")
	}
	for _, scope := range metadata.Scopes {
		// Empty values are not usable permission identities.
		if scope == "" {
			return nil, errors.New("Fused returned an invalid OAuth scope catalog")
		}
	}
	return metadata.Scopes, nil
}

// RegisterTemporaryOAuthClient requests an ephemeral pair without sending a saved CLI control credential.
func (c *Client) RegisterTemporaryOAuthClient(input TemporaryOAuthClientRequest) (TemporaryOAuthClientRegistration, error) {
	body, err := json.Marshal(input)
	// Only a complete request may reach the public registration endpoint.
	if err != nil {
		return TemporaryOAuthClientRegistration{}, err
	}
	request, err := http.NewRequest(http.MethodPost, c.BaseURL+"/oauth/register", bytes.NewReader(body))
	// An invalid Engine origin must fail before the anonymous HTTP mutation.
	if err != nil {
		return TemporaryOAuthClientRegistration{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	// Registration metadata must not be forwarded to a redirect target selected by Engine.
	response, err := c.doRequestWithoutRedirect(request)
	// A transport failure cannot establish whether Engine registered a client.
	if err != nil {
		return TemporaryOAuthClientRegistration{}, err
	}
	defer response.Body.Close()
	// Registration has one success status; bounded errors use the shared safe Engine decoder.
	if response.StatusCode != http.StatusCreated {
		errorBody, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return TemporaryOAuthClientRegistration{}, fmt.Errorf("OAuth client registration failed (HTTP %d): %w", response.StatusCode, newHTTPError(response.StatusCode, errorBody))
	}
	// Bound the entire response, including trailing data, before decoding a one-time secret.
	successBody, err := io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
	if err != nil || len(successBody) > 16<<10 {
		return TemporaryOAuthClientRegistration{}, errors.New("Fused returned an oversized OAuth client registration")
	}
	decoder := json.NewDecoder(bytes.NewReader(successBody))
	decoder.DisallowUnknownFields()
	var registration TemporaryOAuthClientRegistration
	// Missing or trailing response data must not be printed as a usable credential pair.
	if err := decoder.Decode(&registration); err != nil {
		return TemporaryOAuthClientRegistration{}, errors.New("Fused returned an invalid OAuth client registration")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF || registration.ClientID == "" || registration.ClientSecret == "" || !registration.ClientIDExpiresAt.After(time.Now()) {
		return TemporaryOAuthClientRegistration{}, errors.New("Fused returned an incomplete OAuth client registration")
	}
	return registration, nil
}
