package api

// ClassifyPromptOperation asks Engine to ground one intent in an exact Registry service version using its licensed classifier.
func (c *Client) ClassifyPromptOperation(serviceID, version, intent string) (string, error) {
	var result struct {
		Operation string `json:"classifyPromptOperation"`
	}
	err := c.EngineGraphQL(`query ClassifyPromptOperation($service_id: String!, $version: String!, $query: String!) { classifyPromptOperation(service_id: $service_id, version: $version, query: $query) }`, map[string]any{"service_id": serviceID, "version": version, "query": intent}, &result)
	return result.Operation, err
}
