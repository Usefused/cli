package api

type User struct {
	ID          string               `json:"id"`
	Email       string               `json:"email"`
	DisplayName string               `json:"display_name"`
	Status      string               `json:"status"`
	Memberships []UserTeamMembership `json:"memberships"`
	Credentials []ControlCredential  `json:"credentials"`
	CreatedAt   string               `json:"created_at"`
	UpdatedAt   string               `json:"updated_at"`
}

type UserTeamMembership struct {
	TeamID         string `json:"team_id"`
	TeamName       string `json:"team_name"`
	TeamSlug       string `json:"team_slug"`
	MembershipRole string `json:"membership_role"`
	CreatedAt      string `json:"created_at"`
}

type ControlCredential struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	KeyPrefix  string `json:"key_prefix"`
	ExpiresAt  string `json:"expires_at"`
	LastUsedAt string `json:"last_used_at"`
	RevokedAt  string `json:"revoked_at"`
	CreatedAt  string `json:"created_at"`
}

type UserPage struct {
	Items []User `json:"items"`
	Total int    `json:"total"`
}

type CreateUserInput struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

type UpdateUserInput struct {
	Email       *string `json:"email,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
}

type UserMutationPayload struct {
	User                  User  `json:"user"`
	AuthorizationRevision int64 `json:"authorization_revision"`
	Changed               bool  `json:"changed"`
}

type TeamMember struct {
	UserID         string `json:"user_id"`
	Email          string `json:"email"`
	DisplayName    string `json:"display_name"`
	Status         string `json:"status"`
	MembershipRole string `json:"membership_role"`
	CreatedAt      string `json:"created_at"`
}

type TeamMemberPage struct {
	Items []TeamMember `json:"items"`
	Total int          `json:"total"`
}

type TeamMembershipMutationPayload struct {
	Membership            *TeamMember `json:"membership"`
	AuthorizationRevision int64       `json:"authorization_revision"`
	Changed               bool        `json:"changed"`
}

type IssuedCredentialPayload struct {
	Credential            ControlCredential `json:"credential"`
	Secret                string            `json:"secret"`
	AuthorizationRevision int64             `json:"authorization_revision"`
	Changed               bool              `json:"changed"`
}

type CredentialMutationPayload struct {
	Credential            *ControlCredential `json:"credential"`
	AuthorizationRevision int64              `json:"authorization_revision"`
	Changed               bool               `json:"changed"`
}

const userSummaryFields = `id email display_name status created_at updated_at`
const userFields = userSummaryFields + `
	memberships { team_id team_name team_slug membership_role created_at }
	credentials { id name key_prefix expires_at last_used_at revoked_at created_at }
`
const credentialFields = `id name key_prefix expires_at last_used_at revoked_at created_at`
const teamMemberFields = `user_id email display_name status membership_role created_at`

func (c *Client) ListUsers(search string, includeSuspended bool, opts PageOptions) (*UserPage, error) {
	query := `query Users($search: String!, $limit: Int!, $offset: Int!, $includeSuspended: Boolean!) {
		users(search: $search, limit: $limit, offset: $offset, include_suspended: $includeSuspended) {
			total items { ` + userSummaryFields + ` }
		}
	}`
	var response struct {
		Users UserPage `json:"users"`
	}
	variables := pageVars(opts)
	variables["search"] = search
	variables["includeSuspended"] = includeSuspended
	err := c.EngineGraphQL(query, variables, &response)
	return &response.Users, err
}

func (c *Client) GetUser(id string) (*User, error) {
	query := `query User($id: ID!) { user(id: $id) { ` + userFields + ` } }`
	var response struct {
		User User `json:"user"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"id": id}, &response)
	return &response.User, err
}

func (c *Client) CreateUser(input CreateUserInput) (*UserMutationPayload, error) {
	return c.mutateUser("createUser", `input: $input`, "$input: CreateUserInput!", map[string]interface{}{"input": input})
}

func (c *Client) UpdateUser(id string, input UpdateUserInput) (*UserMutationPayload, error) {
	return c.mutateUser("updateUser", `id: $id, input: $input`, "$id: ID!, $input: UpdateUserInput!", map[string]interface{}{"id": id, "input": input})
}

func (c *Client) SuspendUser(id string) (*UserMutationPayload, error) {
	return c.mutateUser("suspendUser", `id: $id`, "$id: ID!", map[string]interface{}{"id": id})
}

func (c *Client) ReactivateUser(id string) (*UserMutationPayload, error) {
	return c.mutateUser("reactivateUser", `id: $id`, "$id: ID!", map[string]interface{}{"id": id})
}

func (c *Client) mutateUser(field, arguments, variableTypes string, variables map[string]interface{}) (*UserMutationPayload, error) {
	query := `mutation UserMutation(` + variableTypes + `) { ` + field + `(` + arguments + `) {
		user { ` + userFields + ` } authorization_revision changed
	} }`
	var response map[string]UserMutationPayload
	err := c.EngineGraphQL(query, variables, &response)
	payload := response[field]
	return &payload, err
}

func (c *Client) ListTeamMembers(teamID string, opts PageOptions) (*TeamMemberPage, error) {
	query := `query TeamMembers($teamId: ID!, $limit: Int!, $offset: Int!) {
		teamMembers(team_id: $teamId, limit: $limit, offset: $offset) { total items { ` + teamMemberFields + ` } }
	}`
	var response struct {
		Members TeamMemberPage `json:"teamMembers"`
	}
	variables := pageVars(opts)
	variables["teamId"] = teamID
	err := c.EngineGraphQL(query, variables, &response)
	return &response.Members, err
}

func (c *Client) AddTeamMember(teamID, email, role string) (*TeamMembershipMutationPayload, error) {
	query := `mutation AddTeamMember($teamId: ID!, $email: String!, $role: TeamMembershipRole!) {
		addTeamMember(team_id: $teamId, email: $email, membership_role: $role) {
			membership { ` + teamMemberFields + ` } authorization_revision changed
		}
	}`
	return c.mutateTeamMembership("addTeamMember", query, map[string]interface{}{"teamId": teamID, "email": email, "role": role})
}

func (c *Client) RemoveTeamMember(teamID, userID string) (*TeamMembershipMutationPayload, error) {
	query := `mutation RemoveTeamMember($teamId: ID!, $userId: ID!) {
		removeTeamMember(team_id: $teamId, user_id: $userId) {
			membership { ` + teamMemberFields + ` } authorization_revision changed
		}
	}`
	return c.mutateTeamMembership("removeTeamMember", query, map[string]interface{}{"teamId": teamID, "userId": userID})
}

func (c *Client) mutateTeamMembership(field, query string, variables map[string]interface{}) (*TeamMembershipMutationPayload, error) {
	var response map[string]TeamMembershipMutationPayload
	err := c.EngineGraphQL(query, variables, &response)
	payload := response[field]
	return &payload, err
}

func (c *Client) IssueUserCredential(userID, name string) (*IssuedCredentialPayload, error) {
	query := `mutation IssueUserCredential($userId: ID!, $name: String!) {
		issueUserCredential(user_id: $userId, name: $name) {
			credential { ` + credentialFields + ` } secret authorization_revision changed
		}
	}`
	var response struct {
		Payload IssuedCredentialPayload `json:"issueUserCredential"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"userId": userID, "name": name}, &response)
	return &response.Payload, err
}

func (c *Client) RevokeUserCredential(userID, credentialID string) (*CredentialMutationPayload, error) {
	query := `mutation RevokeUserCredential($userId: ID!, $credentialId: ID!) {
		revokeUserCredential(user_id: $userId, credential_id: $credentialId) {
			credential { ` + credentialFields + ` } authorization_revision changed
		}
	}`
	var response struct {
		Payload CredentialMutationPayload `json:"revokeUserCredential"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"userId": userID, "credentialId": credentialID}, &response)
	return &response.Payload, err
}
