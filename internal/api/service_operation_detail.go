package api

import "fmt"

// ServiceOperationDetailOptions keeps large body contracts opt-in so a search
// result can be inspected without unnecessarily expanding an agent's context.
type ServiceOperationDetailOptions struct {
	IncludeRequest   bool
	IncludeResponses bool
}

// ServiceOperationDetail adds exact-contract fields to the lightweight
// Integration summary used by service operation lists.
type ServiceOperationDetail struct {
	Integration
	StableKey        string                                    `json:"stable_key,omitempty"`
	ResourceName     string                                    `json:"resource_name,omitempty"`
	Version          string                                    `json:"version,omitempty"`
	NormalizedPath   string                                    `json:"normalized_path,omitempty"`
	Deprecated       bool                                      `json:"deprecated"`
	Parameters       []ServiceOperationParameter               `json:"parameters,omitempty"`
	RequestContent   *ServiceRuntimeRequestContent             `json:"request_content,omitempty"`
	Responses        map[string]ServiceRuntimeResponseContract `json:"responses,omitempty"`
	GraphQLQuery     *string                                   `json:"graphql_query,omitempty"`
	ProviderProtocol string                                    `json:"provider_protocol,omitempty"`
	OperationKind    string                                    `json:"operation_kind,omitempty"`
}

// ServiceOperationParameter mirrors the fields exposed by Registry's public
// Integration parameter projection. Full schema/media contracts remain in the
// optional request_content and responses fields.
type ServiceOperationParameter struct {
	Name         string `json:"name"`
	In           string `json:"in"`
	Required     bool   `json:"required"`
	Type         string `json:"type,omitempty"`
	Description  string `json:"description,omitempty"`
	PathEncoding string `json:"path_encoding,omitempty"`
}

const serviceOperationDetailFields = `
	id
	service_id
	stable_key
	name
	description
	resource_name
	version
	method
	path
	normalized_path
	deprecated
	parameters { name in required type description path_encoding }
	documentation
	graphql_query
	provider_protocol
	operation_kind
	` + securityRequirementsGraphQLFields

func (c *Client) GetServiceOperation(serviceID, version, operationName string, options ServiceOperationDetailOptions) (*ServiceOperationDetail, error) {
	fields := serviceOperationDetailFields
	if options.IncludeRequest {
		fields += "\nrequest_content"
	}
	if options.IncludeResponses {
		fields += "\nresponses"
	}
	query := fmt.Sprintf(`
		query ServiceOperation($serviceId: String!, $version: String!, $name: String!) {
			endpointByName(serviceId: $serviceId, version: $version, name: $name) {
				%s
			}
		}
	`, fields)
	var response struct {
		Operation *ServiceOperationDetail `json:"endpointByName"`
	}
	err := c.GraphQL(query, map[string]interface{}{
		"serviceId": serviceID,
		"version":   version,
		"name":      operationName,
	}, &response)
	if err != nil {
		return nil, err
	}
	if response.Operation == nil {
		return nil, fmt.Errorf("operation %s not found in service version %s", operationName, version)
	}
	return response.Operation, nil
}
