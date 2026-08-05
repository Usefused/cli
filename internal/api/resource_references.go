package api

type ResolvedResourceReference struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

type ArtifactSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Kind      string `json:"kind"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"created_at"`
}

type ArtifactSummaryPage struct {
	Items []ArtifactSummary `json:"items"`
	Total int               `json:"total"`
}

type ArtifactServiceSummary struct {
	ServiceID     string `json:"service_id"`
	ServiceSlug   string `json:"service_slug"`
	ServiceName   string `json:"service_name"`
	Version       string `json:"version"`
	SelectAll     bool   `json:"select_all"`
	EndpointCount int    `json:"endpoint_count"`
	WebhookCount  int    `json:"webhook_count"`
}

func (c *Client) ListArtifacts(kind string, opts PageOptions) (*ArtifactSummaryPage, error) {
	query := `query Artifacts($kind: String, $limit: Int!, $offset: Int!) {
		artifactSnapshots(kind: $kind, limit: $limit, offset: $offset) {
			total
			items { id name version kind active created_at }
		}
	}`
	var response struct {
		Page ArtifactSummaryPage `json:"artifactSnapshots"`
	}
	variables := pageVars(opts)
	variables["kind"] = kind
	err := c.EngineGraphQL(query, variables, &response)
	return &response.Page, err
}

func (c *Client) GetArtifactSummary(reference, kind string) (*ArtifactSummary, error) {
	query := `query Artifact($reference: String!, $kind: String) {
		artifact(reference: $reference, kind: $kind) { id name version kind active created_at }
	}`
	var response struct {
		Artifact ArtifactSummary `json:"artifact"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"reference": reference, "kind": kind}, &response)
	return &response.Artifact, err
}

func (c *Client) ListArtifactServices(reference, kind string) ([]ArtifactServiceSummary, error) {
	query := `query ArtifactServices($reference: String!, $kind: String) {
		artifactServices(reference: $reference, kind: $kind) {
			service_id service_slug service_name version select_all endpoint_count webhook_count
		}
	}`
	var response struct {
		Services []ArtifactServiceSummary `json:"artifactServices"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"reference": reference, "kind": kind}, &response)
	return response.Services, err
}

func (c *Client) ResolveBucketReference(reference string) (string, error) {
	return c.resolveResourceReference("bucketReference", reference)
}

func (c *Client) ResolveServiceReference(reference string) (string, error) {
	return c.resolveResourceReference("serviceReference", reference)
}

func (c *Client) ResolveArtifactReference(reference string) (string, error) {
	return c.resolveArtifactReference(reference, "")
}

func (c *Client) ResolveSDKReference(reference string) (string, error) {
	return c.resolveArtifactReference(reference, "sdk")
}

func (c *Client) ResolveMCPReference(reference string) (string, error) {
	return c.resolveArtifactReference(reference, "mcp")
}

func (c *Client) resolveArtifactReference(reference, kind string) (string, error) {
	query := `query ResolveArtifactReference($reference: String!, $kind: String) {
		artifactReference(reference: $reference, kind: $kind) { id kind }
	}`
	var response map[string]ResolvedResourceReference
	err := c.EngineGraphQL(query, map[string]interface{}{"reference": reference, "kind": kind}, &response)
	return response["artifactReference"].ID, err
}

func (c *Client) resolveResourceReference(field, reference string) (string, error) {
	query := `query ResolveResourceReference($reference: String!) {
		` + field + `(reference: $reference) { id kind }
	}`
	var response map[string]ResolvedResourceReference
	err := c.EngineGraphQL(query, map[string]interface{}{"reference": reference}, &response)
	return response[field].ID, err
}
