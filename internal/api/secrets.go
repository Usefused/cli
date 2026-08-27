package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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

func (s *SecretMetaResponse) UnmarshalJSON(data []byte) error {
	type rawSecretMetaResponse struct {
		ID             string `json:"id"`
		ServiceID      string `json:"service_id"`
		KeyName        string `json:"key_name"`
		CredentialType string `json:"credential_type"`
		BucketID       string `json:"bucket_id"`
		ExpiresAt      string `json:"expires_at"`
		CreatedAt      string `json:"created_at"`
		UpdatedAt      string `json:"updated_at"`
	}
	var raw rawSecretMetaResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	expiresAt, err := parseOptionalGraphQLTime(raw.ExpiresAt)
	if err != nil {
		return fmt.Errorf("parse secret expires_at: %w", err)
	}
	createdAt, err := parseGraphQLTime(raw.CreatedAt)
	if err != nil {
		return fmt.Errorf("parse secret created_at: %w", err)
	}
	updatedAt, err := parseGraphQLTime(raw.UpdatedAt)
	if err != nil {
		return fmt.Errorf("parse secret updated_at: %w", err)
	}

	*s = SecretMetaResponse{
		ID:             raw.ID,
		ServiceID:      raw.ServiceID,
		KeyName:        raw.KeyName,
		CredentialType: raw.CredentialType,
		BucketID:       raw.BucketID,
		ExpiresAt:      expiresAt,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}
	return nil
}

func parseOptionalGraphQLTime(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := parseGraphQLTime(value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseGraphQLTime(value string) (time.Time, error) {
	// Engine GraphQL serializes optional timestamps as empty strings, so
	// CLI decoding normalizes that API detail at the boundary instead of
	// leaking string checks through command rendering.
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, value)
}

type SecretMetaPageResponse struct {
	Items []SecretMetaResponse `json:"items"`
	Total int                  `json:"total"`
}

// UpsertSecret stores one credential value and safely bounds Engine failures.
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

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody := readBoundedHTTPErrorBody(resp.Body)
		return fmt.Errorf("upsert secret failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, respBody))
	}

	return nil
}

type SecretUpsertRequest struct {
	ServiceID      string     `json:"service_id"`
	KeyName        string     `json:"key_name"`
	CredentialType string     `json:"credential_type"`
	Value          string     `json:"value"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}

// CredentialFamilyUpsertRequest keeps semantic paired fields free of Engine storage key details.
type CredentialFamilyUpsertRequest struct {
	ServiceID      string            `json:"service_id"`
	CredentialType string            `json:"credential_type"`
	AuthName       string            `json:"auth_name"`
	Values         map[string]string `json:"values"`
	ExpiresAt      *time.Time        `json:"expires_at,omitempty"`
}

// UpsertSecrets sends paired credential material as one admin action so Engine
// can validate and persist the group atomically at the store boundary.
func (c *Client) UpsertSecrets(bucketID string, secrets []SecretUpsertRequest) error {
	reqBody := map[string]interface{}{
		"bucket_id": bucketID,
		"secrets":   secrets,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", c.BaseURL+"/workspace/secrets/bulk", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody := readBoundedHTTPErrorBody(resp.Body)
		return fmt.Errorf("upsert secrets failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, respBody))
	}
	return nil
}

// UpsertCredentialFamily sends one semantic family for atomic Engine-owned naming and persistence.
func (c *Client) UpsertCredentialFamily(bucketID string, family CredentialFamilyUpsertRequest) error {
	reqBody := map[string]interface{}{
		"bucket_id":         bucketID,
		"credential_family": family,
	}
	body, err := json.Marshal(reqBody)
	// Serialization must finish before any credential-bearing request is created.
	if err != nil {
		return err
	}
	req, err := http.NewRequest("PUT", c.BaseURL+"/workspace/secrets/bulk", bytes.NewBuffer(body))
	// Request construction errors cannot be retried with an incomplete body.
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// Control authentication is optional only for explicitly unauthenticated test clients.
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}
	resp, err := c.doRequest(req)
	// Transport errors are already bounded by the shared client.
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Engine errors are bounded before they become CLI output.
	if resp.StatusCode >= 400 {
		respBody := readBoundedHTTPErrorBody(resp.Body)
		return fmt.Errorf("upsert credential family failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, respBody))
	}
	return nil
}

// DeleteSecret removes one credential value and safely bounds Engine failures.
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

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody := readBoundedHTTPErrorBody(resp.Body)
		return fmt.Errorf("delete secret failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, respBody))
	}

	return nil
}
