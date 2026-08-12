package connectionprofile_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Usefused/cli/internal/connectionprofile"
	"gopkg.in/yaml.v3"
)

// TestPostAuthDiscoveryStrictJSONAndYAMLRoundTrip preserves authoritative
// discovery semantics without attaching them to one provider name.
func TestPostAuthDiscoveryStrictJSONAndYAMLRoundTrip(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", "discovery", "v1_post_auth_resource_discovery.json"))
	if err != nil {
		t.Fatal(err)
	}
	var discovery connectionprofile.ResourceDiscoveryConfig
	if err := json.Unmarshal(payload, &discovery); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(discovery)
	if err != nil {
		t.Fatal(err)
	}
	assertSemanticJSON(t, encoded, payload)
	yamlPayload, err := yaml.Marshal(discovery)
	if err != nil {
		t.Fatal(err)
	}
	var fromYAML connectionprofile.ResourceDiscoveryConfig
	if err := yaml.Unmarshal(yamlPayload, &fromYAML); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromYAML, discovery) {
		t.Fatalf("YAML transport changed resource discovery:\n%s", yamlPayload)
	}
}

func TestPostAuthDiscoveryRejectsUnknownJSON(t *testing.T) {
	var discovery connectionprofile.ResourceDiscoveryConfig
	if err := json.Unmarshal([]byte(`{"version":1,"stage":"post_auth","operation_id":"list","id_path":"$.id","resource_type":"project","token":"secret"}`), &discovery); err == nil {
		t.Fatal("credential-bearing discovery field was accepted")
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
		t.Fatalf("discovery transport changed JSON\ngot:  %s\nwant: %s", gotPayload, wantPayload)
	}
}
