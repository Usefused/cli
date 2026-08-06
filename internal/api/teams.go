package api

type Team struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Slug        string        `json:"slug"`
	Description string        `json:"description"`
	Status      string        `json:"status"`
	Bindings    []TeamBinding `json:"bindings"`
	CreatedAt   string        `json:"created_at"`
	UpdatedAt   string        `json:"updated_at"`
}

type TeamBinding struct {
	ID                  string `json:"id"`
	TeamID              string `json:"team_id"`
	RoleSlug            string `json:"role_slug"`
	RoleDisplayName     string `json:"role_display_name"`
	ResourceType        string `json:"resource_type"`
	ResourceID          string `json:"resource_id"`
	ResourceDisplayName string `json:"resource_display_name"`
	CreatedAt           string `json:"created_at"`
}

type TeamPage struct {
	Items []Team `json:"items"`
	Total int    `json:"total"`
}

type CreateTeamInput struct {
	Name        string `json:"name"`
	Slug        string `json:"slug,omitempty"`
	Description string `json:"description,omitempty"`
}

// UpdateTeamInput uses pointers so omitted fields remain unchanged while an
// explicit empty description can clear the current description.
type UpdateTeamInput struct {
	Name        *string `json:"name,omitempty"`
	Slug        *string `json:"slug,omitempty"`
	Description *string `json:"description,omitempty"`
}

type TeamMutationPayload struct {
	Team                  Team  `json:"team"`
	AuthorizationRevision int64 `json:"authorization_revision"`
	Changed               bool  `json:"changed"`
}

type TeamBindingMutationPayload struct {
	Binding               *TeamBinding `json:"binding"`
	AuthorizationRevision int64        `json:"authorization_revision"`
	Changed               bool         `json:"changed"`
}

const teamFields = `
		id name slug description status created_at updated_at
		bindings {
			id team_id role_slug role_display_name resource_type resource_id
			resource_display_name created_at
		}
`

func (c *Client) ListTeams(search string, includeArchived bool, opts PageOptions) (*TeamPage, error) {
	query := `
		query Teams($search: String!, $limit: Int!, $offset: Int!, $includeArchived: Boolean!) {
			teams(search: $search, limit: $limit, offset: $offset, include_archived: $includeArchived) {
				total
				items { ` + teamFields + ` }
			}
		}
	`
	var response struct {
		Teams TeamPage `json:"teams"`
	}
	variables := pageVars(opts)
	variables["search"] = search
	variables["includeArchived"] = includeArchived
	err := c.EngineGraphQL(query, variables, &response)
	return &response.Teams, err
}

func (c *Client) GetTeam(id string) (*Team, error) {
	query := `query Team($id: ID!) { team(id: $id) { ` + teamFields + ` } }`
	var response struct {
		Team Team `json:"team"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"id": id}, &response)
	return &response.Team, err
}

func (c *Client) CreateTeam(input CreateTeamInput) (*TeamMutationPayload, error) {
	query := `
		mutation CreateTeam($input: CreateTeamInput!) {
			createTeam(input: $input) {
				team { ` + teamFields + ` }
				authorization_revision changed
			}
		}
	`
	var response struct {
		Payload TeamMutationPayload `json:"createTeam"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"input": input}, &response)
	return &response.Payload, err
}

func (c *Client) UpdateTeam(id string, input UpdateTeamInput) (*TeamMutationPayload, error) {
	query := `
		mutation UpdateTeam($id: ID!, $input: UpdateTeamInput!) {
			updateTeam(id: $id, input: $input) {
				team { ` + teamFields + ` }
				authorization_revision changed
			}
		}
	`
	var response struct {
		Payload TeamMutationPayload `json:"updateTeam"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"id": id, "input": input}, &response)
	return &response.Payload, err
}

func (c *Client) ArchiveTeam(id string) (*TeamMutationPayload, error) {
	query := `
		mutation ArchiveTeam($id: ID!) {
			archiveTeam(id: $id) {
				team { ` + teamFields + ` }
				authorization_revision changed
			}
		}
	`
	var response struct {
		Payload TeamMutationPayload `json:"archiveTeam"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"id": id}, &response)
	return &response.Payload, err
}

func (c *Client) SetTeamWorkspaceRole(teamID string, role *string) (*TeamBindingMutationPayload, error) {
	return c.mutateTeamBinding("setTeamWorkspaceRole", `team_id: $teamId, role: $role`, map[string]interface{}{
		"teamId": teamID,
		"role":   role,
	}, "$teamId: ID!, $role: TeamWorkspaceRole")
}

func (c *Client) GrantTeamServiceAccess(teamID, serviceID, level string) (*TeamBindingMutationPayload, error) {
	return c.mutateTeamBinding("grantTeamServiceAccess", `team_id: $teamId, service_id: $resourceId, level: $level`, map[string]interface{}{
		"teamId":     teamID,
		"resourceId": serviceID,
		"level":      level,
	}, "$teamId: ID!, $resourceId: ID!, $level: TeamAccessLevel!")
}

func (c *Client) RevokeTeamServiceAccess(teamID, serviceID, level string) (*TeamBindingMutationPayload, error) {
	return c.mutateTeamBinding("revokeTeamServiceAccess", `team_id: $teamId, service_id: $resourceId, level: $level`, map[string]interface{}{
		"teamId":     teamID,
		"resourceId": serviceID,
		"level":      level,
	}, "$teamId: ID!, $resourceId: ID!, $level: TeamAccessLevel!")
}

func (c *Client) GrantTeamBucketAccess(teamID, bucketID, level string) (*TeamBindingMutationPayload, error) {
	return c.mutateTeamBinding("grantTeamBucketAccess", `team_id: $teamId, bucket_id: $resourceId, level: $level`, map[string]interface{}{
		"teamId":     teamID,
		"resourceId": bucketID,
		"level":      level,
	}, "$teamId: ID!, $resourceId: ID!, $level: TeamAccessLevel!")
}

func (c *Client) RevokeTeamBucketAccess(teamID, bucketID, level string) (*TeamBindingMutationPayload, error) {
	return c.mutateTeamBinding("revokeTeamBucketAccess", `team_id: $teamId, bucket_id: $resourceId, level: $level`, map[string]interface{}{
		"teamId":     teamID,
		"resourceId": bucketID,
		"level":      level,
	}, "$teamId: ID!, $resourceId: ID!, $level: TeamAccessLevel!")
}

func (c *Client) GrantTeamAppAccess(teamID, appFamilyID, level string) (*TeamBindingMutationPayload, error) {
	return c.mutateTeamBinding("grantTeamAppAccess", `team_id: $teamId, app_family_id: $resourceId, level: $level`, map[string]interface{}{
		"teamId":     teamID,
		"resourceId": appFamilyID,
		"level":      level,
	}, "$teamId: ID!, $resourceId: ID!, $level: TeamAppAccessLevel!")
}

func (c *Client) RevokeTeamAppAccess(teamID, appFamilyID, level string) (*TeamBindingMutationPayload, error) {
	return c.mutateTeamBinding("revokeTeamAppAccess", `team_id: $teamId, app_family_id: $resourceId, level: $level`, map[string]interface{}{
		"teamId":     teamID,
		"resourceId": appFamilyID,
		"level":      level,
	}, "$teamId: ID!, $resourceId: ID!, $level: TeamAppAccessLevel!")
}

func (c *Client) mutateTeamBinding(field, arguments string, variables map[string]interface{}, variableTypes string) (*TeamBindingMutationPayload, error) {
	query := `mutation TeamBinding(` + variableTypes + `) {
		` + field + `(` + arguments + `) {
			binding { id team_id role_slug role_display_name resource_type resource_id resource_display_name created_at }
			authorization_revision changed
		}
	}`
	var response map[string]TeamBindingMutationPayload
	err := c.EngineGraphQL(query, variables, &response)
	payload := response[field]
	return &payload, err
}
