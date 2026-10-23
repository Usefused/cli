package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// secretExportRequest sends explicit policy/reveal actions without putting plaintext into URLs or errors.
func (c *Client) secretExportRequest(bucketID, secretID, action, method string, body []byte) (*http.Response, error) {
	target := c.BaseURL + "/workspace/buckets/" + url.PathEscape(bucketID) + "/secrets/" + url.PathEscape(secretID) + "/" + action
	req, err := http.NewRequest(method, target, bytes.NewReader(body))
	// Invalid configuration must fail before any request is sent.
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.APIKey)
	resp, err := c.doRequestWithoutRedirect(req)
	// Network failures retain the shared transport's bounded handling.
	if err != nil {
		return nil, err
	}
	// Error bodies are deliberately ignored; retrieval errors must never echo credential material.
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("secret %s failed (HTTP %d)", action, resp.StatusCode)
	}
	return resp, nil
}

// RevealBucketSecret retrieves only an opted-in secret and never places it into shared client caches.
func (c *Client) RevealBucketSecret(bucketID, secretID string) (string, error) {
	resp, err := c.secretExportRequest(bucketID, secretID, "reveal", http.MethodPost, nil)
	// Failed retrieval cannot be replaced with an empty successful credential.
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var result struct {
		Value *string `json:"value"`
	}
	// Match Engine's buffered audit response budget and suppress decoder details.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", fmt.Errorf("invalid secret retrieval response")
	}
	// An absent value must not masquerade as an empty successful credential.
	if result.Value == nil {
		return "", fmt.Errorf("invalid secret retrieval response")
	}
	return *result.Value, nil
}

// SetBucketSecretExportable changes policy without retrieving or replacing the value.
func (c *Client) SetBucketSecretExportable(bucketID, secretID string, exportable bool) error {
	body, _ := json.Marshal(map[string]bool{"exportable": exportable})
	resp, err := c.secretExportRequest(bucketID, secretID, "exportable", http.MethodPut, body)
	// Only an acknowledged policy update is success.
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
