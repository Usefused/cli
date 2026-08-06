package api

type AppOwningTeam struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type AppOwningTeamPage struct {
	Items []AppOwningTeam `json:"items"`
	Total int             `json:"total"`
}

type AppBuildSelector struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	DisplayName  string `json:"display_name"`
}

type AppBuildSelectorPage struct {
	Items []AppBuildSelector `json:"items"`
	Total int                `json:"total"`
}

func (c *Client) ListAppOwningTeams(search string, opts PageOptions) (*AppOwningTeamPage, error) {
	query := `
		query AppOwningTeams($search: String, $limit: Int!, $offset: Int!) {
			appOwningTeams(search: $search, limit: $limit, offset: $offset) {
				total
				items { id name slug }
			}
		}
	`
	var response struct {
		Page AppOwningTeamPage `json:"appOwningTeams"`
	}
	variables := pageVars(opts)
	variables["search"] = search
	err := c.EngineGraphQL(query, variables, &response)
	return &response.Page, err
}

func (c *Client) ListAppBuildSelectors(ownerTeamID, resourceType, search string, opts PageOptions) (*AppBuildSelectorPage, error) {
	query := `
		query AppBuildSelectors($ownerTeamId: ID!, $resourceType: AppSelectorResourceType!, $search: String, $limit: Int!, $offset: Int!) {
			appBuildSelectors(owner_team_id: $ownerTeamId, resource_type: $resourceType, search: $search, limit: $limit, offset: $offset) {
				total
				items { resource_type resource_id display_name }
			}
		}
	`
	var response struct {
		Page AppBuildSelectorPage `json:"appBuildSelectors"`
	}
	variables := pageVars(opts)
	variables["ownerTeamId"] = ownerTeamID
	variables["resourceType"] = resourceType
	variables["search"] = search
	err := c.EngineGraphQL(query, variables, &response)
	return &response.Page, err
}
