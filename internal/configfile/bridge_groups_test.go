package configfile

import (
	"encoding/json"
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func TestBridgeGroupYAMLRoundTrip(t *testing.T) {
	for _, ops := range []string{`[device.info, volume.set]`, `"*"`} {
		var cfg AppConfig
		raw := "bridges:\n  home:\n    bridge: home-bridge\n    profile: echo-live\n    connections:\n      echo-show-8:\n        operations: " + ops + "\n"
		if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
			t.Fatal(err)
		}
		if err := validateBridgeReferences(&cfg); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var decoded AppConfig
		if err = json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if err = validateBridgeReferences(&decoded); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "pairing_id") {
			t.Fatal("group emitted empty legacy pins")
		}
	}
}

func TestBridgeGroupRejectsMalformedSelections(t *testing.T) {
	for _, ops := range []any{nil, true, "all", []any{}, []any{"*"}, []any{"device.info", "device.info"}, []any{42}, map[string]any{"all": true}} {
		cfg := &AppConfig{Bridges: map[string]BridgeReference{"home": {Bridge: "home-bridge", Profile: "echo-live", Connections: map[string]BridgeConnectionSelection{"echo-show-8": {Operations: ops}}}}}
		if validateBridgeReferences(cfg) == nil {
			t.Fatalf("accepted invalid operations %#v", ops)
		}
	}
	ref := BridgeReference{Bridge: "home-bridge", Profile: "echo-live", PairingID: "legacy", Connections: map[string]BridgeConnectionSelection{"echo-show-8": {Operations: "*"}}}
	if validateBridgeGroup(ref) == nil {
		t.Fatal("mixed grouped and legacy selectors accepted")
	}
}
