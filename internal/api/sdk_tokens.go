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

type SDKTokenGenerateResponse struct {
	ID        string    `json:"id"`
	SDKID     string    `json:"sdk_id"`
	Name      string    `json:"name"`
	Token     string    `json:"token,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type SDKTokenResponse struct {
	ID         string     `json:"id"`
	SDKID      string     `json:"sdk_id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

func (t *SDKTokenResponse) UnmarshalJSON(data []byte) error {
	type rawSDKTokenResponse struct {
		ID         string `json:"id"`
		SDKID      string `json:"sdk_id"`
		Name       string `json:"name"`
		CreatedAt  string `json:"created_at"`
		LastUsedAt string `json:"last_used_at"`
	}
	var raw rawSDKTokenResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	createdAt, err := parseGraphQLTime(raw.CreatedAt)
	if err != nil {
		return fmt.Errorf("parse sdk token created_at: %w", err)
	}
	lastUsedAt, err := parseOptionalGraphQLTime(raw.LastUsedAt)
	if err != nil {
		return fmt.Errorf("parse sdk token last_used_at: %w", err)
	}
	*t = SDKTokenResponse{
		ID:         raw.ID,
		SDKID:      raw.SDKID,
		Name:       raw.Name,
		CreatedAt:  createdAt,
		LastUsedAt: lastUsedAt,
	}
	return nil
}

func (c *Client) GenerateSDKToken(sdkID, name string) (*SDKTokenGenerateResponse, error) {
	reqBody := map[string]interface{}{
		"name": name,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	u, err := url.Parse(c.BaseURL + "/workspace/sdk-tokens")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("sdk_id", sdkID)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("POST", u.String(), bytes.NewBuffer(body))
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
		return nil, fmt.Errorf("generate sdk token failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(respBody))
	}

	var out SDKTokenGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListSDKTokens(sdkID string) ([]SDKTokenResponse, error) {
	query := `
		query SDKTokens($sdkId: String!) {
			sdkTokens(sdk_id: $sdkId) { id sdk_id name created_at last_used_at }
		}
	`
	var resp struct {
		Tokens []SDKTokenResponse `json:"sdkTokens"`
	}
	// Why: token listing is read-only metadata, while token generation and
	// revocation stay on REST because they mutate credential state.
	err := c.EngineGraphQL(query, map[string]interface{}{"sdkId": sdkID}, &resp)
	return resp.Tokens, err
}

func (c *Client) RevokeSDKToken(sdkID, name string) error {
	u, err := url.Parse(c.BaseURL + "/workspace/sdk-tokens")
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("sdk_id", sdkID)
	q.Set("name", name)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("DELETE", u.String(), nil)
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
		return fmt.Errorf("revoke sdk token failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(respBody))
	}

	return nil
}
