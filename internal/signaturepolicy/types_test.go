package signaturepolicy_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Usefused/cli/internal/signaturepolicy"
	"gopkg.in/yaml.v3"
)

// TestStructuredSignatureFixturesJSONAndYAMLRoundTrip protects the distinct
// raw-body, form, and conditional verification recipes across config formats.
func TestStructuredSignatureFixturesJSONAndYAMLRoundTrip(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("..", "..", "..", "contract-fixtures", "signature", "v1_*.json"))
	if err != nil {
		t.Fatal(err)
	}
	// Canonical fixtures are named explicitly so losing coverage fails loudly,
	// while newly added v1 fixtures automatically join the round-trip matrix.
	required := map[string]bool{
		"v1_generic_header.json":              false,
		"v1_conditional_challenge_jwt.json":   false,
		"v1_raw_body_callback_signature.json": false,
		"v1_url_form_signature.json":          false,
	}
	for _, fixture := range fixtures {
		name := filepath.Base(fixture)
		if _, ok := required[name]; ok {
			required[name] = true
		}
		t.Run(name, func(t *testing.T) {
			payload, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			var config signaturepolicy.Config
			if err := json.Unmarshal(payload, &config); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			assertSemanticJSON(t, encoded, payload)
			assertYAMLRoundTrip(t, config)
		})
	}
	for name, found := range required {
		if !found {
			t.Errorf("required signature fixture %q is missing", name)
		}
	}
}

func assertYAMLRoundTrip(t *testing.T, config signaturepolicy.Config) {
	t.Helper()
	yamlPayload, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var fromYAML signaturepolicy.Config
	if err := yaml.Unmarshal(yamlPayload, &fromYAML); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromYAML, config) {
		t.Fatalf("YAML transport changed signature policy:\n%s", yamlPayload)
	}
}

func TestStructuredSignatureRejectsUnknownOrTrailingJSON(t *testing.T) {
	for _, payload := range []string{
		`{"version":1,"rules":[],"signing_secret":"literal"}`,
		`{"version":1,"rules":[]} {}`,
	} {
		var config signaturepolicy.Config
		if err := json.Unmarshal([]byte(payload), &config); err == nil {
			t.Fatalf("accepted non-canonical signature policy: %s", payload)
		}
	}
}

func assertSemanticJSON(t *testing.T, gotPayload, wantPayload []byte) {
	t.Helper()
	var got, want any
	if err := json.Unmarshal(gotPayload, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wantPayload, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("signature transport changed JSON\ngot:  %s\nwant: %s", gotPayload, wantPayload)
	}
}
