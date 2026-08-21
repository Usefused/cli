package connectionprofile_test

import (
	"bytes"
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

// TestPostAuthDiscoveryRejectsUnknownJSON keeps credentials and unknown
// discovery semantics outside the CLI mirror.
func TestPostAuthDiscoveryRejectsUnknownJSON(t *testing.T) {
	var discovery connectionprofile.ResourceDiscoveryConfig
	if err := json.Unmarshal([]byte(`{"version":1,"stage":"post_auth","operation_id":"list","id_path":"$.id","resource_type":"project","token":"secret"}`), &discovery); err == nil {
		t.Fatal("credential-bearing discovery field was accepted")
	}
}

// TestResourceInputDiscoveryMatchStrictRoundTrip proves the CLI preserves the
// exact input-to-discovery constraint in both supported config transports.
func TestResourceInputDiscoveryMatchStrictRoundTrip(t *testing.T) {
	profile := connectionprofile.Profile{
		AuthType: "oauth",
		ResourceInput: &connectionprofile.ResourceInputConfig{
			Fields: []connectionprofile.ResourceInputField{
				{
					Name: "subdomain", Label: "Jira site", Type: connectionprofile.ResourceInputFieldTypeText,
					Placeholder: "fused-test", Description: "Enter the site prefix shown before atlassian.net.",
					Required: true, Pattern: `^[a-z0-9-]+$`,
				},
				{
					Name: "region", Label: "Region", Type: connectionprofile.ResourceInputFieldTypeSelect,
					Description: "Choose the provider region for this site.",
					Options:     []connectionprofile.ResourceInputOption{{Value: "eu", Label: "Europe"}, {Value: "us"}},
				},
			},
			BaseURLTemplate: "https://{subdomain}.atlassian.net",
			ResourceType:    "jira_site",
			AllowedHosts:    []string{"*.atlassian.net"},
			DiscoveryMatch:  &connectionprofile.ResourceInputDiscoveryMatch{MetadataKey: "site_url"},
		},
	}
	encodedJSON, err := json.Marshal(profile)
	// JSON is the Registry transport and must preserve the nested matcher.
	if err != nil {
		t.Fatal(err)
	}
	var fromJSON connectionprofile.Profile
	// Strict decoding exercises both the profile and nested input boundaries.
	if err := json.Unmarshal(encodedJSON, &fromJSON); err != nil {
		t.Fatal(err)
	}
	// Round-tripped values must retain the exact customer-to-discovery constraint.
	if !reflect.DeepEqual(fromJSON, profile) {
		t.Fatalf("JSON transport changed resource input: %s", encodedJSON)
	}
	encodedYAML, err := yaml.Marshal(profile)
	// YAML is the throwaway workspace authoring transport used by the live CLI recipe.
	if err != nil {
		t.Fatal(err)
	}
	var fromYAML connectionprofile.Profile
	decoder := yaml.NewDecoder(bytes.NewReader(encodedYAML))
	decoder.KnownFields(true)
	// KnownFields keeps authored workspace profiles closed at every nested level.
	if err := decoder.Decode(&fromYAML); err != nil {
		t.Fatal(err)
	}
	// YAML and JSON must project the same matcher semantics.
	if !reflect.DeepEqual(fromYAML, profile) {
		t.Fatalf("YAML transport changed resource input:\n%s", encodedYAML)
	}
}

// TestResourceInputFieldOmittedTypeStaysOmitted proves transport does not
// rewrite the authoritative text default into authored configuration.
func TestResourceInputFieldOmittedTypeStaysOmitted(t *testing.T) {
	field := connectionprofile.ResourceInputField{Name: "tenant"}
	payload, err := json.Marshal(field)
	// Omission remains distinguishable from an explicitly authored type.
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(`"type"`)) {
		t.Fatalf("omitted field type was materialized: %s", payload)
	}
	var decoded connectionprofile.ResourceInputField
	// The empty mirror value is intentionally normalized only at the authoritative boundary.
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Type != "" {
		t.Fatalf("omitted field type changed to %q", decoded.Type)
	}
	yamlPayload, err := yaml.Marshal(field)
	// YAML authoring preserves omission for the same authoritative default.
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(yamlPayload, []byte("type:")) {
		t.Fatalf("omitted YAML field type was materialized:\n%s", yamlPayload)
	}
}

// TestResourceInputDiscoveryMatchRejectsUnknownFields proves strict transport
// rejects extensions at both the input and nested match boundaries.
func TestResourceInputDiscoveryMatchRejectsUnknownFields(t *testing.T) {
	jsonPayloads := []string{
		`{"resource_input":{"fields":[],"base_url_template":"https://example.test","resource_type":"site","unexpected":true}}`,
		`{"resource_input":{"fields":[],"base_url_template":"https://example.test","resource_type":"site","discovery_match":{"metadata_key":"site_url","unexpected":true}}}`,
		`{"resource_input":{"fields":[{"name":"region","type":"select","unexpected":true}],"base_url_template":"https://example.test","resource_type":"site"}}`,
		`{"resource_input":{"fields":[{"name":"region","type":"select","options":[{"value":"eu","unexpected":true}]}],"base_url_template":"https://example.test","resource_type":"site"}}`,
	}
	// Both the input object and nested match object reject unknown JSON fields.
	for _, payload := range jsonPayloads {
		var profile connectionprofile.Profile
		// Accepting either payload would let the CLI silently erase contract semantics.
		if err := json.Unmarshal([]byte(payload), &profile); err == nil {
			t.Fatalf("strict JSON accepted %s", payload)
		}
	}
	var profile connectionprofile.Profile
	decoder := yaml.NewDecoder(bytes.NewBufferString(`resource_input:
  fields: []
  base_url_template: https://example.test
  resource_type: site
  discovery_match:
    metadata_key: site_url
    unexpected: true
`))
	decoder.KnownFields(true)
	// YAML authoring has the same closed-world guarantee as Registry JSON transport.
	if err := decoder.Decode(&profile); err == nil {
		t.Fatal("strict YAML accepted an unknown discovery match field")
	}

	optionDecoder := yaml.NewDecoder(bytes.NewBufferString(`resource_input:
  fields:
    - name: region
      type: select
      options:
        - value: eu
          unexpected: true
  base_url_template: https://example.test
  resource_type: site
`))
	optionDecoder.KnownFields(true)
	// Select options retain the same closed YAML boundary as the containing profile.
	if err := optionDecoder.Decode(&profile); err == nil {
		t.Fatal("strict YAML accepted an unknown resource input option field")
	}
}

// assertSemanticJSON compares decoded values so formatting does not affect the contract check.
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
