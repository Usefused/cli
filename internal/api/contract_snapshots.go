package api

const refreshMissingServiceContractsMutation = `mutation RefreshMissingServiceContracts($limit: Int!) {
	refreshMissingServiceContracts(limit: $limit) {
		status
		missing
		refreshed
		failed
		results {
			service_id
			service_version_id
			version
			contract_hash
			error
		}
	}
}`

type RefreshMissingServiceContractsResult struct {
	Status    string                         `json:"status"`
	Missing   int                            `json:"missing"`
	Refreshed int                            `json:"refreshed"`
	Failed    int                            `json:"failed"`
	Results   []RefreshMissingContractResult `json:"results"`
}

type RefreshMissingContractResult struct {
	ServiceID        string `json:"service_id"`
	ServiceVersionID string `json:"service_version_id"`
	Version          string `json:"version"`
	ContractHash     string `json:"contract_hash"`
	Error            string `json:"error"`
}

func (c *Client) RefreshMissingServiceContracts(limit int) (*RefreshMissingServiceContractsResult, error) {
	var out struct {
		Result RefreshMissingServiceContractsResult `json:"refreshMissingServiceContracts"`
	}
	variables := map[string]interface{}{"limit": normalizeRefreshMissingContractsLimit(limit)}
	if err := c.EngineGraphQL(refreshMissingServiceContractsMutation, variables, &out); err != nil {
		return nil, err
	}
	return &out.Result, nil
}

func normalizeRefreshMissingContractsLimit(limit int) int {
	// Why clamp client-side too: a typo should not ask the Engine for an
	// unexpectedly large migration batch, even though the resolver also bounds it.
	if limit <= 0 || limit > 100 {
		return 100
	}
	return limit
}
