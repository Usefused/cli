package api

type ResolvedResourceReference struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

type AppSelection struct {
	ServiceID               string            `json:"service_id"`
	ServiceVersionID        string            `json:"service_version_id"`
	DefinitionSchemaVersion int               `json:"definition_schema_version"`
	EndpointIDs             []string          `json:"endpoint_ids"`
	OperationNames          []string          `json:"operation_names"`
	WebhookIDs              []string          `json:"webhook_ids"`
	WebhookNames            []string          `json:"webhook_names"`
	SelectAll               bool              `json:"select_all"`
	WebhookSelectAll        bool              `json:"webhook_select_all"`
	AuthType                string            `json:"auth_type"`
	AuthName                string            `json:"auth_name"`
	RequiredAuth            []AppRequiredAuth `json:"required_auth"`
	ConnectScopes           []string          `json:"connect_scopes"`
	Injections              []InjectionConfig `json:"injections"`
}

type AppRequiredAuth struct {
	AuthType          string `json:"auth_type"`
	AuthName          string `json:"auth_name"`
	BasicPasswordMode string `json:"basic_password_mode"`
}

type AppSummary struct {
	AppFamilyID           string         `json:"app_family_id"`
	AppID                 string         `json:"app_id"`
	Name                  string         `json:"name"`
	Description           string         `json:"description"`
	Version               string         `json:"version"`
	Kind                  string         `json:"kind"`
	Status                string         `json:"status"`
	CreatedAt             string         `json:"created_at"`
	TargetLanguage        string         `json:"target_language"`
	GeneratorVersion      string         `json:"generator_version"`
	Readme                string         `json:"readme"`
	Selections            []AppSelection `json:"selections"`
	PlannedDeactivationAt string         `json:"planned_deactivation_at"`
}

type AppSummaryPage struct {
	Items []AppSummary `json:"items"`
	Total int          `json:"total"`
}

type AppServiceSummary struct {
	ServiceID     string `json:"service_id"`
	ServiceSlug   string `json:"service_slug"`
	ServiceName   string `json:"service_name"`
	Version       string `json:"version"`
	SelectAll     bool   `json:"select_all"`
	EndpointCount int    `json:"endpoint_count"`
	WebhookCount  int    `json:"webhook_count"`
}

const appSummaryFields = `
	app_family_id app_id name description version kind status created_at
	target_language generator_version readme planned_deactivation_at
	selections {
		service_id service_version_id definition_schema_version
		endpoint_ids operation_names webhook_ids webhook_names
		select_all webhook_select_all auth_type auth_name connect_scopes
		required_auth { auth_type auth_name basic_password_mode }
		injections { location name value mode }
	}`

func (c *Client) ListApps(kind string, opts PageOptions) (*AppSummaryPage, error) {
	query := `query Apps($kind: String, $limit: Int!, $offset: Int!) {
		apps(kind: $kind, limit: $limit, offset: $offset) {
			total
			items { ` + appSummaryFields + ` }
		}
	}`
	var response struct {
		Page AppSummaryPage `json:"apps"`
	}
	variables := pageVars(opts)
	variables["kind"] = kind
	err := c.EngineGraphQL(query, variables, &response)
	return &response.Page, err
}

func (c *Client) GetApp(appID string) (*AppSummary, error) {
	query := `query App($appId: String!) {
		app(app_id: $appId) { ` + appSummaryFields + ` }
	}`
	var response struct {
		App AppSummary `json:"app"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"appId": appID}, &response)
	return &response.App, err
}

func (c *Client) ListAppServices(appID string) ([]AppServiceSummary, error) {
	query := `query AppServices($appId: String!) {
		appServices(app_id: $appId) {
			service_id service_slug service_name version select_all endpoint_count webhook_count
		}
	}`
	var response struct {
		Services []AppServiceSummary `json:"appServices"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"appId": appID}, &response)
	return response.Services, err
}

func (c *Client) ResolveBucketReference(reference string) (string, error) {
	return c.resolveResourceReference("bucketReference", reference)
}

func (c *Client) ResolveServiceReference(reference string) (string, error) {
	return c.resolveResourceReference("serviceReference", reference)
}

func (c *Client) ResolveSDKAppReference(reference, version string) (string, error) {
	return c.resolveAppReference(reference, version, "sdk")
}

func (c *Client) ResolveMCPAppReference(reference, version string) (string, error) {
	return c.resolveAppReference(reference, version, "mcp")
}

func (c *Client) ResolveSDKFamilyReference(reference string) (string, error) {
	return c.resolveAppFamilyReference(reference, "sdk")
}

func (c *Client) ResolveMCPFamilyReference(reference string) (string, error) {
	return c.resolveAppFamilyReference(reference, "mcp")
}

func (c *Client) resolveAppReference(reference, version, kind string) (string, error) {
	query := `query ResolveAppReference($reference: String!, $version: String!, $kind: String!) {
		appReference(reference: $reference, version: $version, kind: $kind) { id kind }
	}`
	var response map[string]ResolvedResourceReference
	variables := map[string]interface{}{"reference": reference, "kind": kind, "version": version}
	err := c.EngineGraphQL(query, variables, &response)
	return response["appReference"].ID, err
}

func (c *Client) resolveAppFamilyReference(reference, kind string) (string, error) {
	query := `query ResolveAppFamilyReference($reference: String!, $kind: String!) {
		appFamilyReference(reference: $reference, kind: $kind) { id kind }
	}`
	var response map[string]ResolvedResourceReference
	err := c.EngineGraphQL(query, map[string]interface{}{"reference": reference, "kind": kind}, &response)
	return response["appFamilyReference"].ID, err
}

func (c *Client) resolveResourceReference(field, reference string) (string, error) {
	query := `query ResolveResourceReference($reference: String!) {
		` + field + `(reference: $reference) { id kind }
	}`
	var response map[string]ResolvedResourceReference
	err := c.EngineGraphQL(query, map[string]interface{}{"reference": reference}, &response)
	return response[field].ID, err
}
