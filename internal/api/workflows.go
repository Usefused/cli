package api

import "fmt"

// WorkflowRelease carries intentionally published authoring bytes and their immutable digest.
type WorkflowRelease struct {
	ID        string `json:"id"`
	Publisher string `json:"publisher"`
	Hash      string `json:"hash"`
	Public    bool   `json:"public"`
	Template  string `json:"template"`
}

type WorkflowPage struct {
	Items []WorkflowRelease `json:"items"`
	Total int               `json:"total"`
}

// ListWorkflows uses one licensed catalogue request for either discovery or an exact selection.
func (c *Client) ListWorkflows(search string, ids []string, limit, offset int) (WorkflowPage, error) {
	var response struct {
		Workflows WorkflowPage `json:"workflows"`
	}
	// Bound before transport to avoid turning workflow selection into an unbounded catalogue download.
	if len(ids) > 32 || limit < 1 || limit > 50 || offset < 0 {
		return response.Workflows, fmt.Errorf("workflow requests support at most 32 IDs and pages of 1..50")
	}
	err := c.GraphQL(`query Workflows($search: String!, $ids: [ID!], $limit: Int!, $offset: Int!) {
		workflows(search: $search, ids: $ids, limit: $limit, offset: $offset) { total items { id publisher hash public template } }
	}`, map[string]any{"search": search, "ids": ids, "limit": limit, "offset": offset}, &response)
	return response.Workflows, err
}

// PublishWorkflow sends only an explicitly chosen template, never the user's installed app config.
func (c *Client) PublishWorkflow(template string, public bool) (WorkflowRelease, error) {
	var response struct {
		Release WorkflowRelease `json:"publishWorkflow"`
	}
	err := c.GraphQL(`mutation PublishWorkflow($template: String!, $public: Boolean!) {
		publishWorkflow(template: $template, public: $public) { id publisher hash public template }
	}`, map[string]any{"template": template, "public": public}, &response)
	return response.Release, err
}
