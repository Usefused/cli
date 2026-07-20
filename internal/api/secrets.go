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

type SecretMetaResponse struct {
	ID             string     `json:"id"`
	ServiceID      string     `json:"service_id"`
	KeyName        string     `json:"key_name"`
	CredentialType string     `json:"credential_type"`
	BucketID       string     `json:"bucket_id"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (c *Client) UpsertSecret(serviceID, keyName, credentialType, value string, bucketID string, expiresAt *time.Time) error {
	reqBody := map[string]interface{}{
		"service_id":      serviceID,
		"key_name":        keyName,
		"credential_type": credentialType,
		"value":           value,
		"bucket_id":       bucketID,
		"expires_at":      expiresAt,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", c.BaseURL+"/workspace/secrets", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
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
		return fmt.Errorf("upsert secret failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(respBody))
	}

	return nil
}

func (c *Client) ListSecrets(bucketID string) ([]SecretMetaResponse, error) {
	u, err := url.Parse(c.BaseURL + "/workspace/secrets")
	if err != nil {
		return nil, err
	}
	if bucketID != "" {
		q := u.Query()
		q.Set("bucket_id", bucketID)
		u.RawQuery = q.Encode()
	}

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
		return nil, fmt.Errorf("list secrets failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(respBody))
	}

	var out []SecretMetaResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) DeleteSecret(serviceID, keyName string, bucketID string) error {
	u, err := url.Parse(c.BaseURL + "/workspace/secrets")
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("service_id", serviceID)
	q.Set("key_name", keyName)
	if bucketID != "" {
		q.Set("bucket_id", bucketID)
	}
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
		return fmt.Errorf("delete secret failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(respBody))
	}

	return nil
}
