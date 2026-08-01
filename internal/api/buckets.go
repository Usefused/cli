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

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create bucket failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}

	return nil
}

// ListBuckets returns every bucket in the workspace, for callers (name/ID
// resolution, shell completion) that need to search the full set rather than
// show one page. Engine has no "get bucket by name" query, so this is the
// only way to resolve a `--bucket <name>` flag to an ID.
//
// This used to send its own `query Buckets { buckets {...} }` request, but
// Engine's schema dropped the bare `buckets` field when bucket listing moved
// to the paginated bucketSummaryPage surface `bucket list` already uses --
// that migration missed this helper, which kept sending the removed query
// until every `--bucket <name>` command started failing. Paging through
// ListBucketSummariesPage instead of inventing a second query means this
// reuses the one query already proven correct against the real schema.
func (c *Client) ListBuckets() ([]BucketMetaResponse, error) {
	const pageSize = 100
	var all []BucketMetaResponse
	offset := 0
	for {
		page, err := c.ListBucketSummariesPage(PageOptions{Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			createdAt, err := parseGraphQLTime(item.CreatedAt)
			if err != nil {
				return nil, fmt.Errorf("parse bucket created_at: %w", err)
			}
			all = append(all, BucketMetaResponse{
				ID:        item.ID,
				Name:      item.Name,
				IsDefault: item.IsDefault,
				CreatedAt: createdAt,
			})
		}
		offset += len(page.Items)
		if len(page.Items) == 0 || offset >= page.Total {
			return all, nil
		}
	}
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

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upsert bucket value failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}

	return nil
}

func (c *Client) ListBucketValues(bucketID string) ([]BucketValueResponse, error) {
	query := `
		query BucketValues($bucketId: String!) {
			bucketValues(bucket_id: $bucketId) { id service_id key_name location value }
		}
	`
	var resp struct {
		Values []BucketValueResponse `json:"bucketValues"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"bucketId": bucketID}, &resp)
	return resp.Values, err
}

func (c *Client) DeleteBucketValue(bucketID, serviceID, keyName string) error {
	req, err := http.NewRequest("DELETE", fmt.Sprintf("%s/workspace/buckets/%s/values?service_id=%s&key_name=%s", c.BaseURL, bucketID, url.QueryEscape(serviceID), url.QueryEscape(keyName)), nil)
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
		return fmt.Errorf("delete bucket value failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
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

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete bucket failed (HTTP %d): %s", resp.StatusCode, formatHTTPErrorBody(resp.StatusCode, respBody))
	}

	return nil
}
