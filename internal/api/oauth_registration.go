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
