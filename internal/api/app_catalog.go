package api

// ApplicationSummary contains family-level discovery without choosing an arbitrary version.
type ApplicationSummary struct {
	AppFamilyID      string           `json:"app_family_id"`
	Name             string           `json:"name"`
	VersionCount     int              `json:"version_count"`
	StableVersion    string           `json:"stable_version,omitempty"`
	StableVersionID  string           `json:"stable_version_id,omitempty"`
	DefaultTransport string           `json:"default_transport,omitempty"`
	TransportURLs    *ApplicationURLs `json:"transport_urls,omitempty"`
}

// ApplicationURLs exposes only upgrade-safe routes on application listings.
type ApplicationURLs struct {
	StreamableHTTP string `json:"streamable_http"`
	SSE            string `json:"sse"`
}

type ApplicationPage struct {
	Items []ApplicationSummary `json:"items"`
	Total int                  `json:"total"`
}

// ListApplications reads Engine-owned application grouping, counts, and pagination in one request.
func (c *Client) ListApplications(kind string, opts PageOptions) (*ApplicationPage, error) {
	query := `query ApplicationFamilies($kind: String!, $limit: Int!, $offset: Int!) {
  appFamilies(kind: $kind, limit: $limit, offset: $offset) {
   total items { app_family_id name version_count stable_version stable_version_id default_transport
    transport_urls { streamable_http sse }
   }
  }
 }`
	var response struct {
		Page ApplicationPage `json:"appFamilies"`
	}
	variables := pageVars(opts)
	variables["kind"] = kind
	err := c.EngineGraphQL(query, variables, &response)
	return &response.Page, err
}

// ListAppVersions lists exact versions, optionally restricted to one authoritatively resolved application.
func (c *Client) ListAppVersions(kind, reference string, opts PageOptions) (*AppSummaryPage, error) {
	// With no application selector this is the previous all-version catalogue behavior.
	if reference == "" {
		return c.ListApps(kind, opts)
	}
	familyID, err := c.resolveAppFamilyReference(reference, kind)
	// Kind-scoped resolution rejects missing names and other application kinds before version discovery.
	if err != nil {
		return nil, err
	}
	query := `query ApplicationVersions($appFamilyId: String!) {
  appVersions(app_family_id: $appFamilyId) { ` + appSummaryFields + ` }
 }`
	var response struct {
		Items []AppSummary `json:"appVersions"`
	}
	// Engine applies the same authorization rules to the resolved family's versions.
	if err := c.EngineGraphQL(query, map[string]interface{}{"appFamilyId": familyID}, &response); err != nil {
		return nil, err
	}
	start := min(normalOffset(opts.Offset), len(response.Items))
	end := min(start+normalLimit(opts.Limit), len(response.Items))
	return &AppSummaryPage{Items: response.Items[start:end], Total: len(response.Items)}, nil
}
