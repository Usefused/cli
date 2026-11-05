package api

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"net/http"
)

// BridgeRequest accesses only the sanitized catalogue or explicit revocation control routes.
func (c *Client) BridgeRequest(action, id string) (json.RawMessage, error) {
	path, method := "/bridges", http.MethodGet
	// Only finite actions can attach saved management credentials to a request.
	switch action {
	case "list":
	case "manifests", "revoke":
		// Exact UUIDs prevent path injection or accidental alias selection.
		if _, err := uuid.Parse(id); err != nil {
			return nil, errors.New("an exact Bridge pairing ID is required")
		}
		path += "/" + id
		// Revocation is explicit; catalogue inspection remains read-only.
		if action == "manifests" {
			path += "/manifests"
		} else {
			method = http.MethodDelete
		}
	default:
		return nil, errors.New("unsupported Bridge action")
	}
	request, err := http.NewRequest(method, c.BaseURL+path, nil)
	// Failed request construction must not fall back to another endpoint.
	if err != nil {
		return nil, err
	}
	request.Header.Set("x-api-key", c.APIKey)
	response, err := c.doRequest(request)
	// Mutations are never automatically replayed after a transport failure.
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	// Preserve the Engine's structured access and validation errors.
	if response.StatusCode != 200 {
		return nil, newHTTPError(response.StatusCode, readBoundedHTTPErrorBody(response.Body))
	}
	var result json.RawMessage
	err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&result)
	return result, err
}
