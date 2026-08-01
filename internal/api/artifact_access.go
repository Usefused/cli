package api

type ArtifactOwningTeam struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type ArtifactOwningTeamPage struct {
	Items []ArtifactOwningTeam `json:"items"`
	Total int                  `json:"total"`
}

type ArtifactBuildSelector struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	DisplayName  string `json:"display_name"`
}

type ArtifactBuildSelectorPage struct {
	Items []ArtifactBuildSelector `json:"items"`
	Total int                     `json:"total"`
}

func (c *Client) ListArtifactOwningTeams(search string, opts PageOptions) (*ArtifactOwningTeamPage, error) {
	query := `
		query ArtifactOwningTeams($search: String, $limit: Int!, $offset: Int!) {
			artifactOwningTeams(search: $search, limit: $limit, offset: $offset) {
				total
				items { id name slug }
			}
		}
	`
	var response struct {
		Page ArtifactOwningTeamPage `json:"artifactOwningTeams"`
	}
	variables := pageVars(opts)
	variables["search"] = search
	err := c.EngineGraphQL(query, variables, &response)
	return &response.Page, err
}

func (c *Client) ListArtifactBuildSelectors(ownerTeamID, resourceType, search string, opts PageOptions) (*ArtifactBuildSelectorPage, error) {
	query := `
		query ArtifactBuildSelectors($ownerTeamId: ID!, $resourceType: ArtifactSelectorResourceType!, $search: String, $limit: Int!, $offset: Int!) {
			artifactBuildSelectors(owner_team_id: $ownerTeamId, resource_type: $resourceType, search: $search, limit: $limit, offset: $offset) {
				total
				items { resource_type resource_id display_name }
			}
		}
	`
	var response struct {
		Page ArtifactBuildSelectorPage `json:"artifactBuildSelectors"`
	}
	variables := pageVars(opts)
	variables["ownerTeamId"] = ownerTeamID
	variables["resourceType"] = resourceType
	variables["search"] = search
	err := c.EngineGraphQL(query, variables, &response)
	return &response.Page, err
}
