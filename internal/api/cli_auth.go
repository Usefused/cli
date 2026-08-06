package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

var ErrCLILogoutAlreadyInactive = errors.New("saved CLI credential is already inactive")

type WhoAmIResponse struct {
	Authenticated        bool       `json:"authenticated"`
	AccountID            string     `json:"account_id"`
	WorkspaceID          string     `json:"workspace_id"`
	SubjectID            string     `json:"subject_id"`
	SubjectKind          string     `json:"subject_kind"`
	DisplayName          string     `json:"display_name"`
	Email                string     `json:"email"`
	CredentialID         string     `json:"credential_id"`
	CredentialSource     string     `json:"credential_source"`
	AuthenticationMethod string     `json:"authentication_method"`
	ExpiresAt            *time.Time `json:"expires_at"`
}

func (c *Client) WhoAmI() (*WhoAmIResponse, error) {
	req, err := c.authRequest(http.MethodGet, "/auth/whoami")
	if err != nil {
		return nil, err
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
		return nil, fmt.Errorf("whoami failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, body))
	}
	var identity WhoAmIResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&identity); err != nil {
		return nil, errors.New("Engine returned an invalid whoami response")
	}
	if !identity.Authenticated || identity.SubjectID == "" || identity.WorkspaceID == "" || identity.CredentialID == "" {
		return nil, errors.New("Engine returned an incomplete whoami response")
	}
	return &identity, nil
}

func (c *Client) LogoutCLI() error {
	req, err := c.authRequest(http.MethodPost, "/auth/cli/logout")
	if err != nil {
		return err
	}
	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<10))
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrCLILogoutAlreadyInactive
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("CLI logout failed (HTTP %d): %s", resp.StatusCode, genericHTTPError(resp.StatusCode))
	}
	return nil
}

func (c *Client) authRequest(method, path string) (*http.Request, error) {
	req, err := http.NewRequest(method, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}
	return req, nil
}
