package api

type WorkspaceShare struct {
	ID                  string `json:"id"`
	RoleSlug            string `json:"role_slug"`
	RoleDisplayName     string `json:"role_display_name"`
	ResourceType        string `json:"resource_type"`
	ResourceID          string `json:"resource_id"`
	ResourceDisplayName string `json:"resource_display_name"`
	CreatedAt           string `json:"created_at"`
}

type WorkspaceSharePage struct {
	Items []WorkspaceShare `json:"items"`
	Total int              `json:"total"`
}

type WorkspaceShareMutationPayload struct {
	Share                 *WorkspaceShare `json:"share"`
	AuthorizationRevision int64           `json:"authorization_revision"`
	Changed               bool            `json:"changed"`
}

const workspaceShareFields = `
		id role_slug role_display_name resource_type resource_id resource_display_name created_at
`

func (c *Client) ListWorkspaceShares(resourceType string, opts PageOptions) (*WorkspaceSharePage, error) {
	query := `query WorkspaceShares($resourceType: WorkspaceShareResource, $limit: Int!, $offset: Int!) {
		workspaceShares(resource_type: $resourceType, limit: $limit, offset: $offset) {
			total items { ` + workspaceShareFields + ` }
		}
	}`
	var response struct {
		WorkspaceShares WorkspaceSharePage `json:"workspaceShares"`
	}
	variables := pageVars(opts)
	if resourceType == "" {
		variables["resourceType"] = nil
	} else {
		variables["resourceType"] = resourceType
	}
	err := c.EngineGraphQL(query, variables, &response)
	return &response.WorkspaceShares, err
}

func (c *Client) GrantWorkspaceBucketAccess(bucketID string) (*WorkspaceShareMutationPayload, error) {
	return c.mutateWorkspaceShare("grantWorkspaceBucketAccess", "bucket_id", bucketID)
}

func (c *Client) RevokeWorkspaceBucketAccess(bucketID string) (*WorkspaceShareMutationPayload, error) {
	return c.mutateWorkspaceShare("revokeWorkspaceBucketAccess", "bucket_id", bucketID)
}

func (c *Client) GrantWorkspaceArtifactAccess(artifactID string) (*WorkspaceShareMutationPayload, error) {
	return c.mutateWorkspaceShare("grantWorkspaceArtifactAccess", "artifact_id", artifactID)
}

func (c *Client) RevokeWorkspaceArtifactAccess(artifactID string) (*WorkspaceShareMutationPayload, error) {
	return c.mutateWorkspaceShare("revokeWorkspaceArtifactAccess", "artifact_id", artifactID)
}

func (c *Client) mutateWorkspaceShare(field, argument, resourceID string) (*WorkspaceShareMutationPayload, error) {
	query := `mutation WorkspaceShare($resourceId: ID!) {
		` + field + `(` + argument + `: $resourceId) {
			share { ` + workspaceShareFields + ` }
			authorization_revision changed
		}
	}`
	var response map[string]WorkspaceShareMutationPayload
	err := c.EngineGraphQL(query, map[string]interface{}{"resourceId": resourceID}, &response)
	payload := response[field]
	return &payload, err
}
