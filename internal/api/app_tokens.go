package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type AppTokenGenerateRequest struct {
	Name      string   `json:"name"`
	Allow     []string `json:"allow,omitempty"`
	ExpiresIn *int64   `json:"expires_in,omitempty"`
}

type AppTokenGenerateResponse struct {
	ID          string     `json:"id"`
	AppFamilyID string     `json:"app_family_id"`
	Name        string     `json:"name"`
	Allow       []string   `json:"allow"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	Token       string     `json:"token,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type AppTokenResponse struct {
	ID          string     `json:"id"`
	AppFamilyID string     `json:"app_family_id"`
	Name        string     `json:"name"`
	Allow       []string   `json:"allow"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
}

func (t *AppTokenResponse) UnmarshalJSON(data []byte) error {
	type rawAppTokenResponse struct {
		ID          string   `json:"id"`
		AppFamilyID string   `json:"app_family_id"`
		Name        string   `json:"name"`
		Allow       []string `json:"allow"`
		ExpiresAt   string   `json:"expires_at"`
		CreatedAt   string   `json:"created_at"`
		LastUsedAt  string   `json:"last_used_at"`
	}
	var raw rawAppTokenResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	createdAt, err := parseGraphQLTime(raw.CreatedAt)
	if err != nil {
		return fmt.Errorf("parse app token created_at: %w", err)
	}
	lastUsedAt, err := parseOptionalGraphQLTime(raw.LastUsedAt)
	if err != nil {
		return fmt.Errorf("parse app token last_used_at: %w", err)
	}
	expiresAt, err := parseOptionalGraphQLTime(raw.ExpiresAt)
	if err != nil {
		return fmt.Errorf("parse app token expires_at: %w", err)
	}
	*t = AppTokenResponse{
		ID:          raw.ID,
		AppFamilyID: raw.AppFamilyID,
		Name:        raw.Name,
		Allow:       raw.Allow,
		ExpiresAt:   expiresAt,
		CreatedAt:   createdAt,
		LastUsedAt:  lastUsedAt,
	}
	return nil
}

func (c *Client) GenerateAppToken(appFamilyID string, input AppTokenGenerateRequest) (*AppTokenGenerateResponse, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(c.BaseURL + "/workspace/app-tokens")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("app_family_id", appFamilyID)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("POST", u.String(), bytes.NewBuffer(body))
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
		return nil, fmt.Errorf("generate app token failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}

	var out AppTokenGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListAppTokens(appFamilyID string) ([]AppTokenResponse, error) {
	query := `
		query AppTokens($appFamilyId: String!) {
			appTokens(app_family_id: $appFamilyId) { id app_family_id name allow expires_at created_at last_used_at }
		}
	`
	var resp struct {
		Tokens []AppTokenResponse `json:"appTokens"`
	}
	// Why: token listing is read-only metadata, while token generation and
	// revocation stay on REST because they mutate credential state.
	err := c.EngineGraphQL(query, map[string]interface{}{"appFamilyId": appFamilyID}, &resp)
	return resp.Tokens, err
}

func (c *Client) RevokeAppToken(appFamilyID, name string) error {
	u, err := url.Parse(c.BaseURL + "/workspace/app-tokens")
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("app_family_id", appFamilyID)
	q.Set("name", name)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("DELETE", u.String(), nil)
	if err != nil {
		return err
	}
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("revoke app token failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}

	return nil
}
