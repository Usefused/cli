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

type providerTransportFixture struct {
	AuthConfig           api.AuthConfig           `json:"auth_config"`
	OAuthAuthConfig      api.AuthConfig           `json:"oauth_auth_config"`
	SecurityRequirements api.SecurityRequirements `json:"security_requirements"`
	Server               api.ServiceServer        `json:"server"`
}

func TestProviderAuthRoutingFixtureRoundTripsWithoutNormalization(t *testing.T) {
	data := readProviderTransportFixture(t)
	var fixture providerTransportFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalentJSON(t, encoded, data)

	if fixture.AuthConfig.BasicPasswordMode != "empty" {
		t.Fatalf("basic password mode changed: %#v", fixture.AuthConfig)
	}
	if fixture.OAuthAuthConfig.TokenEndpointAuthMethod != api.TokenEndpointAuthMethodClientSecretBasic {
		t.Fatalf("OAuth token endpoint auth method changed: %#v", fixture.OAuthAuthConfig)
	}
	if !fixture.OAuthAuthConfig.PKCERequired || fixture.OAuthAuthConfig.ScopesDelimiter != "comma" || fixture.OAuthAuthConfig.ExtraAuthParams["prompt"] != "consent" || fixture.OAuthAuthConfig.ExtraTokenParams["audience"] != "payments" || !fixture.OAuthAuthConfig.RefreshTokenRotates {
		t.Fatalf("OAuth edge policy changed: %#v", fixture.OAuthAuthConfig)
	}
	if len(fixture.SecurityRequirements) != 3 || len(fixture.SecurityRequirements[0].Schemes) != 2 {
		t.Fatalf("ordered OR-of-AND requirements changed: %#v", fixture.SecurityRequirements)
	}
	if len(fixture.Server.Variables) != 2 || fixture.Server.Variables[0].Default != nil || fixture.Server.Variables[1].Default == nil {
		t.Fatalf("server variable optional defaults changed: %#v", fixture.Server.Variables)
	}
}

func TestGetServiceInfoProjectsAndDecodesProviderAuthRouting(t *testing.T) {
	data := readProviderTransportFixture(t)
	var fixture providerTransportFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}

	server := newGraphQLContractServer(t, func(query string) any {
		assertGraphQLFields(t, query, []string{
			"basic_password_mode", "flow", "token_url", "token_endpoint_auth_method", "authorization_url", "open_id_connect_url", "scopes",
			"pkce_required", "scopes_delimiter", "extra_auth_params", "extra_token_params", "refresh_token_rotates",
			"variables { name default enum required }", "environment", "is_default",
		})
		return map[string]any{"service": map[string]any{
			"id": "svc-1", "name": "Confluence", "slug": "confluence", "base_url": fixture.Server.URL,
			"servers": []api.ServiceServer{fixture.Server}, "auth_configs": []api.AuthConfig{fixture.AuthConfig, fixture.OAuthAuthConfig},
		}}
	})
	defer server.Close()

	info, err := api.NewClient(server.URL, "test-key").GetServiceInfo("confluence")
	if err != nil {
		t.Fatal(err)
	}
	if info == nil || !reflect.DeepEqual(info.Servers, []api.ServiceServer{fixture.Server}) || !reflect.DeepEqual(info.AuthConfigs, []api.AuthConfig{fixture.AuthConfig, fixture.OAuthAuthConfig}) {
		t.Fatalf("provider auth/routing metadata changed: %#v", info)
	}
}

func TestOperationQueriesProjectAndDecodeOrderedSecurityRequirements(t *testing.T) {
	data := readProviderTransportFixture(t)
	var fixture providerTransportFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}

	server := newGraphQLContractServer(t, func(query string) any {
		assertGraphQLFields(t, query, []string{"security_requirements", "schemes { scheme scopes }"})
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

func readProviderTransportFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", "auth-routing", "v1_transport.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

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

func assertGraphQLFields(t *testing.T, query string, fields []string) {
	t.Helper()
	for _, field := range fields {
		if !strings.Contains(query, field) {
			t.Errorf("GraphQL projection missing %q: %s", field, query)
		}
	}
}
