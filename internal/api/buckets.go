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

type BucketMetaResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	IsDefault bool      `json:"is_default"`
	CreatedAt time.Time `json:"created_at"`
}

type BucketValueResponse struct {
	ID        string `json:"id"`
	ServiceID string `json:"service_id"`
	KeyName   string `json:"key_name"`
	Location  string `json:"location"`
	Value     string `json:"value"`
}

func (c *Client) CreateBucket(name string) error {
	reqBody := map[string]interface{}{
		"name": name,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", c.BaseURL+"/workspace/buckets", bytes.NewBuffer(body))
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
		return fmt.Errorf("create bucket failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(respBody))
	}

	return nil
}

func (c *Client) ListBuckets() ([]BucketMetaResponse, error) {
	req, err := http.NewRequest("GET", c.BaseURL+"/workspace/buckets", nil)
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
		return nil, fmt.Errorf("list buckets failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(respBody))
	}

	var out []BucketMetaResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) UpsertBucketValue(bucketID, serviceID, keyName, location, value string) error {
	reqBody := map[string]interface{}{
		"service_id": serviceID,
		"key_name":   keyName,
		"location":   location,
		"value":      value,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", fmt.Sprintf("%s/workspace/buckets/%s/values", c.BaseURL, bucketID), bytes.NewBuffer(body))
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
		return fmt.Errorf("upsert bucket value failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(respBody))
	}

	return nil
}

func (c *Client) ListBucketValues(bucketID string) ([]BucketValueResponse, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/workspace/buckets/%s/values", c.BaseURL, bucketID), nil)
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
		return nil, fmt.Errorf("list bucket values failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(respBody))
	}

	var values []BucketValueResponse
	if err := json.NewDecoder(resp.Body).Decode(&values); err != nil {
		return nil, err
	}
	return values, nil
}

func (c *Client) DeleteBucketValue(bucketID, serviceID, keyName string) error {
	req, err := http.NewRequest("DELETE", fmt.Sprintf("%s/workspace/buckets/%s/values?service_id=%s&key_name=%s", c.BaseURL, bucketID, url.QueryEscape(serviceID), url.QueryEscape(keyName)), nil)
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
		return fmt.Errorf("delete bucket value failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(respBody))
	}

	return nil
}

func (c *Client) DeleteBucket(name string) error {
	req, err := http.NewRequest("DELETE", c.BaseURL+"/workspace/buckets/"+name, nil)
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
		return fmt.Errorf("delete bucket failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(respBody))
	}

	return nil
}
