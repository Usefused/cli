package pagination

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestConfigJSONGoldenRoundTrip(t *testing.T) {
	fixtures := []string{
		"v3_token.json",
		"v3_offset.json",
		"v3_hybrid.json",
		"v3_rfc_link.json",
		"v3_graphql.json",
		"v3_graphql_templates.json",
		"v3_conditional_items.json",
		"v3_bare_array_cursor.json",
	}
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			assertGoldenRoundTrip(t, fixture)
		})
	}
}

func TestConfigRejectsLegacyStrategies(t *testing.T) {
	fixtures := []string{
		"v2_cursor_body.json", "v2_cursor_header_numeric.json", "v2_offset.json",
		"v2_page_number.json", "v2_next_url_link.json", "invalid_multiple_strategies.json",
	}
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			payload := readFixture(t, fixture)
			var config Config
			if err := json.Unmarshal(payload, &config); err == nil {
				t.Fatalf("canonical CLI transport accepted legacy pagination %s", payload)
			}
		})
	}
}

func TestConfigV3PreservesConditionalAndGraphQLFields(t *testing.T) {
	conditional := readConfig(t, "v3_conditional_items.json")
	if len(conditional.Response.Items.Paths) != 2 || conditional.Request[0].Constant.Boolean == nil {
		t.Fatalf("conditional request/response fields changed: %#v", conditional)
	}
	graphql := readConfig(t, "v3_graphql.json")
	if graphql.GraphQL == nil || graphql.GraphQL.FirstPageTemplate == graphql.GraphQL.SubsequentPageTemplate {
		t.Fatalf("GraphQL page templates changed: %#v", graphql.GraphQL)
	}
	if graphql.Response.Values[1].Source.ValueType != "boolean" {
		t.Fatalf("GraphQL response value type changed: %#v", graphql.Response.Values)
	}
}

func readConfig(t *testing.T, fixture string) Config {
	t.Helper()
	data := readFixture(t, fixture)
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

// readFixture centralizes the cross-module fixture path so rejection and
// round-trip tests exercise the same reviewed bytes.
func readFixture(t *testing.T, fixture string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", "pagination", fixture))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertGoldenRoundTrip(t *testing.T, fixture string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", "pagination", fixture))
	if err != nil {
		t.Fatal(err)
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pagination transport changed\ngot:  %s\nwant: %s", encoded, data)
	}
}
