package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

const (
	maxSDKOpenAPIDocumentBytes = 16 << 20
	maxSDKOpenAPIErrorBytes    = 64 << 10
)

// ExportSDKOpenAPI fetches the bounded OpenAPI document for one exact SDK Version ID.
func (c *Client) ExportSDKOpenAPI(appID, operation string) ([]byte, error) {
	endpoint, err := sdkOpenAPIEndpoint(c.BaseURL, appID, operation)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.oai.openapi+json;version=3.1.0")
	if c.APIKey != "" {
		request.Header.Set("X-API-Key", c.APIKey)
	}
	response, err := c.doRequestWithoutRedirect(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := readSDKOpenAPIResponse(response.Body, maxSDKOpenAPIErrorBytes)
		return nil, fmt.Errorf("OpenAPI export failed (HTTP %d): %w", response.StatusCode, newHTTPError(response.StatusCode, body))
	}
	document, err := readSDKOpenAPIResponse(response.Body, maxSDKOpenAPIDocumentBytes)
	if err != nil {
		return nil, fmt.Errorf("OpenAPI export failed: %w", err)
	}
	return document, nil
}

// sdkOpenAPIEndpoint builds the protected exact-app route without admitting URL credentials or fragments.
func sdkOpenAPIEndpoint(baseURL, appID, operation string) (string, error) {
	parsedID, err := uuid.Parse(strings.TrimSpace(appID))
	if err != nil {
		return "", errors.New("OpenAPI export requires an exact Version ID")
	}
	endpoint, err := parseSDKOpenAPIBaseURL(baseURL)
	if err != nil {
		return "", err
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/apps/" + parsedID.String() + "/openapi"
	endpoint.RawPath = ""
	if operation != "" {
		query := endpoint.Query()
		query.Set("operation", operation)
		endpoint.RawQuery = query.Encode()
	}
	return endpoint.String(), nil
}

// parseSDKOpenAPIBaseURL admits only an absolute credential-free Engine base.
func parseSDKOpenAPIBaseURL(raw string) (*url.URL, error) {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("Engine URL must be an absolute URL without credentials, query, or fragment")
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, errors.New("Engine URL must use http or https")
	}
	return endpoint, nil
}

// doRequestWithoutRedirect prevents the control credential from crossing an Engine redirect boundary.
func (c *Client) doRequestWithoutRedirect(request *http.Request) (*http.Response, error) {
	client := *c.HTTP
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		// X-API-Key is a custom header and Go does not classify it as redirect-sensitive.
		return http.ErrUseLastResponse
	}
	response, err := client.Do(request)
	// The redirect boundary stays strict, while genuine transport failures share
	// the same URL-hiding typed contract as other control-plane requests.
	if err != nil {
		return nil, safeControlPlaneTransportError(err)
	}
	return response, nil
}

// readSDKOpenAPIResponse enforces one explicit memory bound for success and error bodies.
func readSDKOpenAPIResponse(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("Engine OpenAPI response exceeds the supported size")
	}
	return data, nil
}
