package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const maxCLIHTTPErrorBytes int64 = 64 << 10

// readBoundedHTTPErrorBody limits untrusted control-plane failures before the
// shared parser projects their safe structured fields.
func readBoundedHTTPErrorBody(body io.Reader) []byte {
	payload, _ := io.ReadAll(io.LimitReader(body, maxCLIHTTPErrorBytes))
	return payload
}

// AddWorkspaceServiceRequest mirrors Engine's scoped additive activation
// boundary; an omitted version lets Engine resolve the current Registry version.
type AddWorkspaceServiceRequest struct {
	ServiceID        string `json:"service_id"`
	ServiceName      string `json:"service_name"`
	VersionTag       string `json:"version_tag"`
	ServiceVersionID string `json:"service_version_id,omitempty"`
}

// AddWorkspaceService activates exactly one service through Engine's existing
// additive endpoint and never invokes full desired-state workspace mirroring.
func (c *Client) AddWorkspaceService(payload AddWorkspaceServiceRequest) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+"/workspace/services", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// Authentication is optional only for test clients and Engines configured
	// with another transport; never send an empty credential header.
	if c.APIKey != "" {
		req.Header.Set("x-api-key", c.APIKey)
	}

	resp, err := c.doRequest(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Reuse the CLI's safe Engine error projection so remote response text does
	// not become an unbounded terminal or automation payload.
	if resp.StatusCode >= http.StatusBadRequest {
		respBody := readBoundedHTTPErrorBody(resp.Body)
		return fmt.Errorf("add workspace service failed (HTTP %d): %w", resp.StatusCode, newHTTPError(resp.StatusCode, respBody))
	}
	return nil
}
