package api

type PageOptions struct {
	Limit  int
	Offset int
}

type BucketSummaryResponse struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	IsDefault   bool   `json:"is_default"`
	SecretCount int    `json:"secret_count"`
	ValueCount  int    `json:"value_count"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type BucketSummaryPageResponse struct {
	Items []BucketSummaryResponse `json:"items"`
	Total int                     `json:"total"`
}

type BucketValuePageResponse struct {
	Items []BucketValueResponse `json:"items"`
	Total int                   `json:"total"`
}

type BucketServiceSummaryResponse struct {
	ServiceID          string `json:"service_id"`
	ServiceName        string `json:"service_name"`
	SecretCount        int    `json:"secret_count"`
	ValueCount         int    `json:"value_count"`
	ConnectConfigCount int    `json:"connect_config_count"`
	ConnectedUserCount int    `json:"connected_user_count"`
}

type BucketServiceSummaryPageResponse struct {
	Items []BucketServiceSummaryResponse `json:"items"`
	Total int                            `json:"total"`
}

type BucketSDKSummaryResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Active    bool   `json:"active"`
	CreatedAt string `json:"created_at"`
}

type BucketSDKSummaryPageResponse struct {
	Items []BucketSDKSummaryResponse `json:"items"`
	Total int                        `json:"total"`
}

type AuthConnectionResponse struct {
	ID                 string   `json:"id"`
	BucketID           string   `json:"bucket_id"`
	ServiceID          string   `json:"service_id"`
	EndUserRef         string   `json:"end_user_ref"`
	AuthType           string   `json:"auth_type"`
	TokenType          string   `json:"token_type"`
	Scopes             []string `json:"scopes"`
	ExpiresAt          string   `json:"expires_at"`
	LastUsedAt         string   `json:"last_used_at"`
	RefreshState       string   `json:"refresh_state"`
	LastFailureCode    string   `json:"last_failure_code"`
	LastFailureAt      string   `json:"last_failure_at"`
	LastFailureTraceID string   `json:"last_failure_trace_id"`
	CreatedAt          string   `json:"created_at"`
	UpdatedAt          string   `json:"updated_at"`
	CreatedBySDKID     string   `json:"created_by_sdk_id"`
}

type AuthConnectionPageResponse struct {
	Items []AuthConnectionResponse `json:"items"`
	Total int                      `json:"total"`
}

type BucketResponse struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	IsDefault   bool   `json:"is_default"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type MCPServerResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	MCPURL        string `json:"mcp_url"`
	Active        bool   `json:"active"`
	DeactivatedAt string `json:"deactivated_at"`
	CreatedAt     string `json:"created_at"`
}

type MCPServerPageResponse struct {
	Items []MCPServerResponse `json:"items"`
	Total int                 `json:"total"`
}

func (c *Client) ListBucketSummariesPage(opts PageOptions) (*BucketSummaryPageResponse, error) {
	query := `
		query BucketSummaryPage($limit: Int!, $offset: Int!) {
			bucketSummaryPage(limit: $limit, offset: $offset) {
				total
				items { id workspace_id name is_default secret_count value_count created_at updated_at }
			}
		}
	`
	var resp struct {
		Page BucketSummaryPageResponse `json:"bucketSummaryPage"`
	}
	err := c.EngineGraphQL(query, pageVars(opts), &resp)
	return &resp.Page, err
}

func (c *Client) GetBucketSummary(bucketID string) (*BucketSummaryResponse, error) {
	query := `
		query BucketSummary($bucketId: String!) {
			bucketSummary(bucket_id: $bucketId) {
				id workspace_id name is_default secret_count value_count created_at updated_at
			}
		}
	`
	var resp struct {
		Bucket BucketSummaryResponse `json:"bucketSummary"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"bucketId": bucketID}, &resp)
	return &resp.Bucket, err
}

func (c *Client) ListBucketValuesPage(bucketID string, opts PageOptions) (*BucketValuePageResponse, error) {
	query := `
		query BucketValuePage($bucketId: String!, $limit: Int!, $offset: Int!) {
			bucketValuePage(bucket_id: $bucketId, limit: $limit, offset: $offset) {
				total
				items { id service_id key_name location value }
			}
		}
	`
	var resp struct {
		Page BucketValuePageResponse `json:"bucketValuePage"`
	}
	err := c.EngineGraphQL(query, bucketPageVars(bucketID, opts), &resp)
	return &resp.Page, err
}

func (c *Client) ListSecretMetaPage(bucketID string, opts PageOptions) (*SecretMetaPageResponse, error) {
	query := `
		query SecretMetaPage($bucketId: String!, $limit: Int!, $offset: Int!) {
			secretMetaPage(bucket_id: $bucketId, limit: $limit, offset: $offset) {
				total
				items { id bucket_id service_id key_name credential_type expires_at created_at updated_at }
			}
		}
	`
	var resp struct {
		Page SecretMetaPageResponse `json:"secretMetaPage"`
	}
	err := c.EngineGraphQL(query, bucketPageVars(bucketID, opts), &resp)
	return &resp.Page, err
}

func (c *Client) ListAuthConnectionPage(bucketID string, serviceID string, endUserRef string, opts PageOptions) (*AuthConnectionPageResponse, error) {
	query := `
		query AuthConnectionPage($bucketId: String!, $serviceId: String, $endUserRef: String, $limit: Int!, $offset: Int!) {
			authConnectionPage(bucket_id: $bucketId, service_id: $serviceId, end_user_ref: $endUserRef, limit: $limit, offset: $offset) {
				total
				items { id bucket_id service_id end_user_ref auth_type token_type scopes expires_at last_used_at refresh_state last_failure_code last_failure_at last_failure_trace_id created_at updated_at created_by_sdk_id }
			}
		}
	`
	var resp struct {
		Page AuthConnectionPageResponse `json:"authConnectionPage"`
	}
	vars := bucketPageVars(bucketID, opts)
	vars["serviceId"] = serviceID
	vars["endUserRef"] = endUserRef
	err := c.EngineGraphQL(query, vars, &resp)
	return &resp.Page, err
}

func (c *Client) ListBucketServicePage(bucketID string, opts PageOptions) (*BucketServiceSummaryPageResponse, error) {
	query := `
		query BucketServicePage($bucketId: String!, $limit: Int!, $offset: Int!) {
			bucketServicePage(bucket_id: $bucketId, limit: $limit, offset: $offset) {
				total
				items { service_id service_name secret_count value_count connect_config_count connected_user_count }
			}
		}
	`
	var resp struct {
		Page BucketServiceSummaryPageResponse `json:"bucketServicePage"`
	}
	err := c.EngineGraphQL(query, bucketPageVars(bucketID, opts), &resp)
	return &resp.Page, err
}

func (c *Client) ListBucketSDKPage(bucketID string, opts PageOptions) (*BucketSDKSummaryPageResponse, error) {
	query := `
		query BucketSDKPage($bucketId: String!, $limit: Int!, $offset: Int!) {
			bucketSDKPage(bucket_id: $bucketId, limit: $limit, offset: $offset) {
				total
				items { id name kind active created_at }
			}
		}
	`
	var resp struct {
		Page BucketSDKSummaryPageResponse `json:"bucketSDKPage"`
	}
	err := c.EngineGraphQL(query, bucketPageVars(bucketID, opts), &resp)
	return &resp.Page, err
}

func (c *Client) ListSDKBuckets(sdkID string) ([]BucketResponse, error) {
	query := `
		query SDKBuckets($sdkId: String!) {
			sdkBuckets(sdk_id: $sdkId) { id workspace_id name is_default created_at updated_at }
		}
	`
	var resp struct {
		Buckets []BucketResponse `json:"sdkBuckets"`
	}
	err := c.EngineGraphQL(query, map[string]interface{}{"sdkId": sdkID}, &resp)
	return resp.Buckets, err
}

func (c *Client) ListMCPServers(opts PageOptions) (*MCPServerPageResponse, error) {
	query := `
		query MCPServers($limit: Int!, $offset: Int!) {
			mcpServers(limit: $limit, offset: $offset) {
				total
				items { id name version mcp_url active deactivated_at created_at }
			}
		}
	`
	var resp struct {
		Page MCPServerPageResponse `json:"mcpServers"`
	}
	err := c.EngineGraphQL(query, pageVars(opts), &resp)
	return &resp.Page, err
}

func pageVars(opts PageOptions) map[string]interface{} {
	return map[string]interface{}{"limit": normalLimit(opts.Limit), "offset": normalOffset(opts.Offset)}
}

func bucketPageVars(bucketID string, opts PageOptions) map[string]interface{} {
	vars := pageVars(opts)
	vars["bucketId"] = bucketID
	return vars
}

func normalLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func normalOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}
