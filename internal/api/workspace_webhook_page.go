package api

import (
	"fmt"
	"strings"
)

// WorkspaceWebhookListing contains Engine-projected discovery metadata, never signing credentials.
type WorkspaceWebhookListing struct {
	WorkspaceWebhook
	ServiceID    string `json:"service_id"`
	ServiceName  string `json:"service_name"`
	ServiceRef   string `json:"service_ref"`
	CallbackURL  string `json:"callback_url"`
	DeliveryMode string `json:"delivery_mode"`
}

type WorkspaceWebhookPage struct {
	Items []WorkspaceWebhookListing `json:"items"`
	Total int                       `json:"total"`
}

// ListWorkspaceWebhookPage shares the UI's authorized, server-filtered registration query without catalogue fan-out.
func (c *Client) ListWorkspaceWebhookPage(serviceID, search string, opts PageOptions) (*WorkspaceWebhookPage, error) {
	// Reject unusable page boundaries instead of silently reading a different page.
	if opts.Limit < 0 || opts.Limit > 100 || opts.Offset < 0 {
		return nil, fmt.Errorf("webhook page requires a limit from 1 to 100 and a non-negative offset")
	}
	query := `query WorkspaceWebhookPage($limit: Int, $offset: Int, $serviceId: String, $search: String) {
		workspaceWebhookPage(limit: $limit, offset: $offset, service_id: $serviceId, search: $search) {
			total
			items { service_id service_name service_ref label slug callback_url delivery_mode signature created_at }
		}
	}`
	variables := pageVars(opts)
	variables["search"] = strings.TrimSpace(search)
	// An absent service filter means all authorized registrations, not an invalid empty UUID.
	if strings.TrimSpace(serviceID) != "" {
		variables["serviceId"] = strings.TrimSpace(serviceID)
	}
	var response struct {
		Page *struct {
			Items *[]WorkspaceWebhookListing `json:"items"`
			Total *int                       `json:"total"`
		} `json:"workspaceWebhookPage"`
	}
	// Preserve Engine transport authentication, request cancellation, and GraphQL errors.
	if err := c.EngineGraphQL(query, variables, &response); err != nil {
		return nil, err
	}
	// Missing page metadata must not masquerade as a successfully empty registration catalogue.
	if response.Page == nil || response.Page.Items == nil || response.Page.Total == nil || *response.Page.Total < 0 {
		return nil, fmt.Errorf("Fused returned an incomplete webhook page")
	}
	return &WorkspaceWebhookPage{Items: *response.Page.Items, Total: *response.Page.Total}, nil
}
