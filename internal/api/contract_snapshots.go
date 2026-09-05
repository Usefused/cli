package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

const refreshMissingServiceContractsMutation = `mutation RefreshMissingServiceContracts($limit: Int!) {
	refreshMissingServiceContracts(limit: $limit) {
		status
		missing
		refreshed
		failed
		results {
			service_id
			service_version_id
			version
			contract_hash
			error
			error_message
		}
	}
}`

type RefreshMissingServiceContractsResult struct {
	Status    string                         `json:"status"`
	Missing   int                            `json:"missing"`
	Refreshed int                            `json:"refreshed"`
	Failed    int                            `json:"failed"`
	Results   []RefreshMissingContractResult `json:"results"`
}

type RefreshMissingContractResult struct {
	ServiceID        string `json:"service_id"`
	ServiceVersionID string `json:"service_version_id"`
	Version          string `json:"version"`
	ContractHash     string `json:"contract_hash"`
	Error            string `json:"error"`
	ErrorMessage     string `json:"error_message"`
}

// RefreshServiceContractResult identifies the exact immutable workspace snapshot refreshed through Engine.
type RefreshServiceContractResult struct {
	Status           string `json:"status"`
	ServiceID        string `json:"service_id"`
	ServiceVersionID string `json:"service_version_id"`
	Version          string `json:"version"`
	ContractHash     string `json:"contract_hash"`
}

// RefreshServiceContract refreshes only the exact active service version selected by stable UUIDs.
func (c *Client) RefreshServiceContract(serviceID, serviceVersionID string) (*RefreshServiceContractResult, error) {
	serviceID = strings.TrimSpace(serviceID)
	serviceVersionID = strings.TrimSpace(serviceVersionID)
	// UUID validation prevents path-shape ambiguity before an authenticated mutation is attempted.
	if _, err := uuid.Parse(serviceID); err != nil {
		return nil, fmt.Errorf("service id must be a valid UUID: %w", err)
	}
	// The version UUID is independently validated because both path segments authorize one exact snapshot.
	if _, err := uuid.Parse(serviceVersionID); err != nil {
		return nil, fmt.Errorf("service version id must be a valid UUID: %w", err)
	}
	endpoint := fmt.Sprintf("%s/workspace/services/%s/versions/%s/refresh", c.BaseURL, serviceID, serviceVersionID)
	request, err := http.NewRequest(http.MethodPost, endpoint, nil)
	// Local request-construction failures occur before any Engine mutation and retain their standard Go cause.
	if err != nil {
		return nil, err
	}
	// Authentication is omitted only for test clients or Engines with another transport-level credential.
	if c.APIKey != "" {
		request.Header.Set("x-api-key", c.APIKey)
	}
	response, err := c.doRequest(request)
	// Transport projection is already sanitized by the shared control-plane client.
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	// Structured Engine errors remain discoverable through errors.As while hostile response bodies stay bounded.
	if response.StatusCode >= http.StatusBadRequest {
		body := readBoundedHTTPErrorBody(response.Body)
		return nil, fmt.Errorf("refresh workspace service contract failed (HTTP %d): %w", response.StatusCode, newHTTPError(response.StatusCode, body))
	}
	var result RefreshServiceContractResult
	// Malformed success bodies cannot prove which immutable snapshot changed and therefore fail closed.
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode refreshed workspace service contract: %w", err)
	}
	// A successful response must prove that Engine refreshed the same immutable identity the client requested.
	if result.Status != "refreshed" || result.ServiceID != serviceID || result.ServiceVersionID != serviceVersionID || strings.TrimSpace(result.ContractHash) == "" {
		return nil, fmt.Errorf("Engine returned an invalid refreshed workspace service contract identity")
	}
	return &result, nil
}

func (c *Client) RefreshMissingServiceContracts(limit int) (*RefreshMissingServiceContractsResult, error) {
	var out struct {
		Result RefreshMissingServiceContractsResult `json:"refreshMissingServiceContracts"`
	}
	variables := map[string]interface{}{"limit": normalizeRefreshMissingContractsLimit(limit)}
	if err := c.EngineGraphQL(refreshMissingServiceContractsMutation, variables, &out); err != nil {
		return nil, err
	}
	return &out.Result, nil
}

func normalizeRefreshMissingContractsLimit(limit int) int {
	// Why clamp client-side too: a typo should not ask the Engine for an
	// unexpectedly large migration batch, even though the resolver also bounds it.
	if limit <= 0 || limit > 100 {
		return 100
	}
	return limit
}
