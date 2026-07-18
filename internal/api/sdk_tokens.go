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
	u, err := url.Parse(c.BaseURL + "/workspace/sdk-tokens")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("sdk_id", sdkID)
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
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
		return nil, fmt.Errorf("list sdk tokens failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(respBody))
	}

	var out []SDKTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
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
