package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// UpdateServiceDetails edits only supplied display fields through the owner-enforced Engine route.
func (c *Client) UpdateServiceDetails(id string, name, description *string) error {
	body, err := json.Marshal(struct {
		Name        *string `json:"name,omitempty"`
		Description *string `json:"description,omitempty"`
	}{name, description})
	// Never send a partial encoding after an unexpected serialization failure.
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPatch, c.BaseURL+"/integrations/"+url.PathEscape(id)+"/details", bytes.NewReader(body))
	// Invalid Engine configuration must fail before any network request.
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.APIKey)
	resp, err := c.doRequest(req)
	// Transport failures do not imply a successful update.
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Preserve the API's ownership or validation error for the caller.
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("update service details: %w", newHTTPError(resp.StatusCode, readBoundedHTTPErrorBody(resp.Body)))
	}
	return nil
}
