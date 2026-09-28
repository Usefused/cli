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

// ExecutionBuildSelection pins one reviewed operation to Engine-owned immutable IDs.
type ExecutionBuildSelection struct {
	Service          string `json:"service"`
	Operation        string `json:"operation"`
	ServiceID        string `json:"serviceId"`
	ServiceVersionID string `json:"serviceVersionId"`
	EndpointID       string `json:"endpointId"`
}

// ExecutionBuildSelections resolves a finite operation set in one authorized Engine query.
func (c *Client) ExecutionBuildSelections(selections []AppScaffoldSelection) ([]ExecutionBuildSelection, error) {
	var response struct {
		Selections []ExecutionBuildSelection `json:"executionBuildSelections"`
	}
	query := `query ExecutionBuildSelections($selections: [AppScaffoldSelectionInput!]!) {
		executionBuildSelections(selections: $selections) { service operation serviceId serviceVersionId endpointId }
	}`
	// An incomplete response cannot become a bundle authority when Engine omits an operation.
	if err := c.EngineGraphQL(query, map[string]interface{}{"selections": selections}, &response); err != nil {
		return nil, err
	}
	return response.Selections, nil
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
