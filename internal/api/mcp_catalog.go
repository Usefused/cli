package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"io"
	"net/http"
)

// MCPCatalog retains original definition schemas while never accepting provider credentials from the CLI.
type MCPCatalog struct {
	ProtocolVersion   string            `json:"protocol_version"`
	Server            json.RawMessage   `json:"server"`
	Supported         map[string]bool   `json:"supported"`
	Tools             []json.RawMessage `json:"tools"`
	Prompts           []json.RawMessage `json:"prompts"`
	Resources         []json.RawMessage `json:"resources"`
	ResourceTemplates []json.RawMessage `json:"resource_templates"`
}

type MCPCatalogSnapshot struct {
	ID         string     `json:"id"`
	URL        string     `json:"url"`
	BucketName string     `json:"bucket_name,omitempty"`
	SecretName string     `json:"secret_name,omitempty"`
	CreatedAt  string     `json:"created_at"`
	Catalog    MCPCatalog `json:"catalog"`
}

type MCPCatalogDraft struct {
	MCPCatalogSnapshot
	ExpiresAt string `json:"expires_at"`
	Changes   struct {
		Added   int `json:"added"`
		Changed int `json:"changed"`
		Removed int `json:"removed"`
	} `json:"changes"`
}

type MCPDiscoveryInput struct {
	URL        string `json:"url"`
	BucketName string `json:"bucket_name,omitempty"`
	SecretName string `json:"secret_name,omitempty"`
}

// GetMCPCatalog reads the actor's approved snapshot without contacting the provider.
func (c *Client) GetMCPCatalog(serviceID, versionID string) (*MCPCatalogSnapshot, error) {
	var result struct {
		Catalog *MCPCatalogSnapshot `json:"catalog"`
	}
	err := c.mcpCatalogRequest(serviceID, versionID, "", http.MethodGet, nil, &result)
	// Empty catalogs are legitimate; malformed nonempty snapshots must never look approved.
	if err == nil && result.Catalog != nil {
		err = validateMCPCatalogIdentity(result.Catalog.ID)
	}
	return result.Catalog, err
}

// DiscoverMCPCatalog asks Engine to store a reviewable preview; only Engine resolves credentials and performs egress.
func (c *Client) DiscoverMCPCatalog(serviceID, versionID string, input MCPDiscoveryInput) (*MCPCatalogDraft, error) {
	var result MCPCatalogDraft
	err := c.mcpCatalogRequest(serviceID, versionID, "/discover", http.MethodPost, input, &result)
	// Only a concrete preview identity can be offered for review and apply.
	if err == nil {
		err = validateMCPCatalogIdentity(result.ID)
	}
	return &result, err
}

// ApplyMCPCatalog promotes only an exact Engine-held draft, retaining Engine ownership and conflict checks.
func (c *Client) ApplyMCPCatalog(serviceID, versionID, draftID string) (*MCPCatalogSnapshot, error) {
	id, err := uuid.Parse(draftID)
	// A nonzero UUID prevents accidental promotion without a reviewed preview.
	if err != nil || id == uuid.Nil {
		return nil, fmt.Errorf("draft-id must be a nonzero UUID")
	}
	var result MCPCatalogSnapshot
	err = c.mcpCatalogRequest(serviceID, versionID, "/apply", http.MethodPost, map[string]string{"draft_id": draftID}, &result)
	// Promotion must return the reviewed identity rather than a different catalog head.
	if err == nil && result.ID != draftID {
		err = fmt.Errorf("Fused returned a different MCP catalog revision")
	}
	return &result, err
}

// mcpCatalogPath admits only exact UUID path segments before attaching the user's Engine authentication.
func (c *Client) mcpCatalogPath(serviceID, versionID, action string) (string, error) {
	for _, value := range []string{serviceID, versionID} {
		id, err := uuid.Parse(value)
		// Invalid path identity must never turn the control-plane request into another route.
		if err != nil || id == uuid.Nil {
			return "", fmt.Errorf("service and service version IDs must be nonzero UUIDs")
		}
	}
	return fmt.Sprintf("%s/workspace/services/%s/versions/%s/mcp-catalog%s", c.BaseURL, serviceID, versionID, action), nil
}

// mcpCatalogRequest shares the authenticated, cancellable Engine transport and bounds catalog response allocation.
func (c *Client) mcpCatalogRequest(serviceID, versionID, action, method string, input, output any) error {
	endpoint, err := c.mcpCatalogPath(serviceID, versionID, action)
	// Invalid scope is rejected before encoding or dispatch.
	if err != nil {
		return err
	}
	body, err := json.Marshal(input)
	// Encoding failure cannot be represented as an empty mutation.
	if err != nil {
		return err
	}
	request, err := http.NewRequest(method, endpoint, bytes.NewReader(body))
	// Configuration errors are local failures, not evidence of a successful import.
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-api-key", c.APIKey)
	response, err := c.doRequest(request)
	// The shared client sanitizes transport errors and never auto-retries this mutation.
	if err != nil {
		return err
	}
	defer response.Body.Close()
	// Keep typed Engine authorization and stale-preview errors available to automation.
	if response.StatusCode >= http.StatusBadRequest {
		return newHTTPError(response.StatusCode, readBoundedHTTPErrorBody(response.Body))
	}
	const maxBytes = 9 << 20
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	// Catalog definitions are bounded independently from generic GraphQL responses.
	if err != nil || len(raw) > maxBytes {
		return fmt.Errorf("invalid or oversized MCP catalog response")
	}
	// Complete JSON decoding rejects truncated or multiple success envelopes.
	if err := json.Unmarshal(raw, output); err != nil {
		return fmt.Errorf("invalid MCP catalog response")
	}
	return nil
}

// validateMCPCatalogIdentity rejects malformed success envelopes before they reach user review.
func validateMCPCatalogIdentity(value string) error {
	id, err := uuid.Parse(value)
	// A nil UUID is not an imported or reviewable catalog revision.
	if err != nil || id == uuid.Nil {
		return fmt.Errorf("Fused returned an invalid MCP catalog revision")
	}
	return nil
}
