package api

// AppScaffoldSelection is the fully merged provider surface whose routing
// requirements the Engine resolves before the CLI writes an SDK or MCP draft.
type AppScaffoldSelection struct {
	Service    string   `json:"service"`
	Version    string   `json:"version"`
	Operations []string `json:"operations"`
	SelectAll  bool     `json:"select_all"`
}

// AppScaffoldRequirement identifies one declared server-template target
// without returning a bucket reference or provider value.
type AppScaffoldRequirement struct {
	Service  string `json:"service"`
	Variable string `json:"variable"`
}

// AppScaffoldRequirements resolves every selected service in one Engine
// GraphQL request so scaffold enrichment cannot become a per-service lookup.
func (c *Client) AppScaffoldRequirements(selections []AppScaffoldSelection) ([]AppScaffoldRequirement, error) {
	// An empty app skeleton has no provider routing decision and remains usable offline.
	if len(selections) == 0 {
		return []AppScaffoldRequirement{}, nil
	}
	query := `
		query AppScaffoldRequirements($selections: [AppScaffoldSelectionInput!]!) {
			appScaffoldRequirements(selections: $selections) { service variable }
		}
	`
	var response struct {
		Requirements []AppScaffoldRequirement `json:"appScaffoldRequirements"`
	}
	// A partial or failed requirements read cannot safely produce an executable
	// scaffold, so the caller receives the Engine error before writing its file.
	if err := c.EngineGraphQL(query, map[string]interface{}{"selections": selections}, &response); err != nil {
		return nil, err
	}
	return response.Requirements, nil
}
