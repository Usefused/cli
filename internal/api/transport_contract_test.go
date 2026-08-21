package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

type transportContractFixture struct {
	AuthConfig           api.AuthConfig           `json:"auth_config"`
	OAuthAuthConfig      api.AuthConfig           `json:"oauth_auth_config"`
	SecurityRequirements api.SecurityRequirements `json:"security_requirements"`
	Server               api.ServiceServer        `json:"server"`
}

// TestTransportContractFixtureRoundTripsWithoutNormalization protects wire
// distinctions that would be lost if the CLI inferred provider conventions.
func TestTransportContractFixtureRoundTripsWithoutNormalization(t *testing.T) {
	data := readTransportContractFixture(t)
	var fixture transportContractFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalentJSON(t, encoded, data)
	assertTransportContractFixture(t, fixture)
}

// assertTransportContractFixture checks independent auth and routing decisions
// together because this fixture is the CLI's cross-field wire boundary.
func assertTransportContractFixture(t *testing.T, fixture transportContractFixture) {
	t.Helper()
	if fixture.AuthConfig.BasicPasswordMode != "empty" {
		t.Fatalf("basic password mode changed: %#v", fixture.AuthConfig)
	}
	if fixture.OAuthAuthConfig.TokenEndpointAuthMethod != api.TokenEndpointAuthMethodClientSecretBasic {
		t.Fatalf("OAuth token endpoint auth method changed: %#v", fixture.OAuthAuthConfig)
	}
	assertOAuthEdgePolicy(t, fixture.OAuthAuthConfig)
	assertSecurityRequirementFixture(t, fixture.SecurityRequirements)
	assertServerVariableFixture(t, fixture.Server)
}

// assertOAuthEdgePolicy keeps reviewed OAuth controls distinct from flow URLs.
func assertOAuthEdgePolicy(t *testing.T, auth api.AuthConfig) {
	t.Helper()
	if !auth.PKCERequired || auth.ScopesDelimiter != "comma" || auth.ExtraAuthParams["prompt"] != "consent" || auth.ExtraTokenParams["audience"] != "records" || !auth.RefreshTokenRequired || !auth.RefreshTokenRotates {
		t.Fatalf("OAuth edge policy changed: %#v", auth)
	}
	flow, ok := auth.OAuth2Flows["authorizationCode"]
	if !ok || flow.AuthorizationURL == "" || flow.TokenURL == "" || len(flow.Scopes) != 2 {
		t.Fatalf("canonical OAuth2 flow changed: %#v", auth.OAuth2Flows)
	}
}

// assertSecurityRequirementFixture preserves ordered OR-of-AND alternatives.
func assertSecurityRequirementFixture(t *testing.T, requirements api.SecurityRequirements) {
	t.Helper()
	if len(requirements) != 3 || len(requirements[0].Schemes) != 2 {
		t.Fatalf("ordered OR-of-AND requirements changed: %#v", requirements)
	}
}

// assertServerVariableFixture protects required-without-default semantics.
func assertServerVariableFixture(t *testing.T, server api.ServiceServer) {
	t.Helper()
	if len(server.Variables) != 2 || server.Variables[0].Default != nil || server.Variables[1].Default == nil {
		t.Fatalf("server variable optional defaults changed: %#v", server.Variables)
	}
}

// TestGetServiceInfoProjectsAndDecodesTransportContract verifies GraphQL and
// CLI DTOs expose the same provider-neutral transport contract.
func TestGetServiceInfoProjectsAndDecodesTransportContract(t *testing.T) {
	data := readTransportContractFixture(t)
	var fixture transportContractFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	deprecated := true
	fixture.Server.Name = "primary"
	fixture.OAuthAuthConfig.OAuth2MetadataURL = "https://auth.example.test/.well-known/oauth-authorization-server"
	fixture.OAuthAuthConfig.Deprecated = &deprecated
	if fixture.OAuthAuthConfig.OAuth2Flows == nil {
		fixture.OAuthAuthConfig.OAuth2Flows = make(map[string]api.OAuth2FlowContract)
	}
	fixture.OAuthAuthConfig.OAuth2Flows["deviceAuthorization"] = api.OAuth2FlowContract{
		DeviceAuthorizationURL: "https://auth.example.test/device", TokenURL: "https://auth.example.test/token", Scopes: map[string]string{"read": "Read inventory"},
	}

	server := newGraphQLContractServer(t, func(query string) any {
		assertGraphQLFields(t, query, []string{
			"basic_password_mode", "token_endpoint_auth_method", "open_id_connect_url", "oauth2_metadata_url", "deprecated",
			"pkce_required", "scopes_delimiter", "extra_auth_params", "extra_token_params", "refresh_token_required", "refresh_token_rotates",
			"oauth2_flows", "strategy", "policy_provenance",
			"variables { name default enum required }", "environment", "is_default",
		})
		return map[string]any{"service": map[string]any{
			"id": "svc-1", "name": "Confluence", "slug": "confluence", "base_url": fixture.Server.URL,
			"servers": []api.ServiceServer{fixture.Server}, "auth_configs": []api.AuthConfig{fixture.AuthConfig, fixture.OAuthAuthConfig},
		}}
	})
	defer server.Close()

	info, err := api.NewClient(server.URL, "test-key").GetServiceInfo("tenant-api")
	if err != nil {
		t.Fatal(err)
	}
	if info == nil || !reflect.DeepEqual(info.Servers, []api.ServiceServer{fixture.Server}) || !reflect.DeepEqual(info.AuthConfigs, []api.AuthConfig{fixture.AuthConfig, fixture.OAuthAuthConfig}) {
		t.Fatalf("transport contract metadata changed: %#v", info)
	}
}

// TestOperationQueriesProjectAndDecodeOrderedSecurityRequirements covers both
// operation list paths so one query cannot silently drop auth alternatives.
func TestOperationQueriesProjectAndDecodeOrderedSecurityRequirements(t *testing.T) {
	data := readTransportContractFixture(t)
	var fixture transportContractFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}

	server := newGraphQLContractServer(t, func(query string) any {
		assertGraphQLFields(t, query, []string{"security_requirements", "schemes { scheme scopes }", "server_selection", "documentation"})
		operation := api.Integration{ID: "ep-1", Name: "createTransfer", Method: "POST", Path: "/transfers", ServiceID: "svc-1", SecurityRequirements: fixture.SecurityRequirements}
		if strings.Contains(query, "searchEndpoints") {
			return map[string]any{"searchEndpoints": []api.Integration{operation}}
		}
		return map[string]any{"serviceOperations": []api.Integration{operation}}
	})
	defer server.Close()

	client := api.NewClient(server.URL, "test-key")
	searched, err := client.SearchEndpoints("svc-1", "v1", "transfer")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := client.ServiceOperations("svc-1", "v1")
	if err != nil {
		t.Fatal(err)
	}
	for name, operations := range map[string][]api.Integration{"search": searched, "list": listed} {
		if len(operations) != 1 || !reflect.DeepEqual(operations[0].SecurityRequirements, fixture.SecurityRequirements) {
			t.Fatalf("%s security requirements changed: %#v", name, operations)
		}
	}
}

// readTransportContractFixture centralizes the repository-relative boundary.
func readTransportContractFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", "security", "v1_transport.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// assertEquivalentJSON ignores key order while rejecting semantic normalization.
func assertEquivalentJSON(t *testing.T, got, want []byte) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("transport changed\ngot:  %s\nwant: %s", got, want)
	}
}

// newGraphQLContractServer keeps projections deterministic and network-local.
func newGraphQLContractServer(t *testing.T, response func(query string) any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"data": response(body.Query)}); err != nil {
			t.Fatal(err)
		}
	}))
}

// assertGraphQLFields fails when a consumer stops requesting contract fields.
func assertGraphQLFields(t *testing.T, query string, fields []string) {
	t.Helper()
	for _, field := range fields {
		if !strings.Contains(query, field) {
			t.Errorf("GraphQL projection missing %q: %s", field, query)
		}
	}
}
