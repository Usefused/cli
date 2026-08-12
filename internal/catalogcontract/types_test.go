package catalogcontract_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Usefused/cli/internal/catalogcontract"
	"gopkg.in/yaml.v3"
)

// TestCatalogCompositionStrictJSONAndYAMLRoundTrip keeps namespace collision
// decisions identical across the CLI's two accepted configuration formats.
func TestCatalogCompositionStrictJSONAndYAMLRoundTrip(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", "catalog", "v1_namespaced_source_collision.json"))
	if err != nil {
		t.Fatal(err)
	}
	var composition catalogcontract.Composition
	if err := json.Unmarshal(payload, &composition); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(composition)
	if err != nil {
		t.Fatal(err)
	}
	assertSemanticJSON(t, encoded, payload)
	yamlPayload, err := yaml.Marshal(composition)
	if err != nil {
		t.Fatal(err)
	}
	var fromYAML catalogcontract.Composition
	if err := yaml.Unmarshal(yamlPayload, &fromYAML); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromYAML, composition) {
		t.Fatalf("YAML transport changed catalog composition:\n%s", yamlPayload)
	}
}

func TestCatalogCompositionRejectsUnknownJSON(t *testing.T) {
	var composition catalogcontract.Composition
	if err := json.Unmarshal([]byte(`{"version":1,"collision_policy":"reject","sources":[],"merge_components":true}`), &composition); err == nil {
		t.Fatal("unsafe catalog merge field was accepted")
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
		t.Fatalf("catalog transport changed JSON\ngot:  %s\nwant: %s", gotPayload, wantPayload)
	}
}
