package api

import (
	"encoding/json"
	"testing"
)

// TestRuntimeSharedSchemasRoundTrip retains the version dictionary and reference scope without expanding either.
func TestRuntimeSharedSchemasRoundTrip(t *testing.T) {
	source := []byte(`{"schema_definitions":{"Thing":{"dialect":"https://json-schema.org/draft/2020-12/schema","raw":{"type":"integer","minimum":9007199254740993},"content_hash":"example","projection":{"type":"integer"},"shared_definitions":true}}}`)
	var contract ServiceRuntimeContract
	// Typed readers must retain additive shared-schema fields before any re-emission.
	if err := json.Unmarshal(source, &contract); err != nil {
		t.Fatal(err)
	}
	definition := contract.SchemaDefinitions["Thing"]
	// Raw JSON bytes retain exact numbers; the CLI must never project them through float64.
	if !definition.SharedDefinitions || string(definition.Raw) != `{"type":"integer","minimum":9007199254740993}` {
		t.Fatalf("definition=%+v", definition)
	}
	encoded, err := json.Marshal(contract)
	// Re-serialization must preserve the dictionary rather than silently dropping unknown top-level fields.
	if err != nil {
		t.Fatal(err)
	}
	var replay ServiceRuntimeContract
	// A second typed reader demonstrates the wire contract survives the entire CLI round trip.
	if err := json.Unmarshal(encoded, &replay); err != nil {
		t.Fatal(err)
	}
	if !replay.SchemaDefinitions["Thing"].SharedDefinitions || string(replay.SchemaDefinitions["Thing"].Raw) != string(definition.Raw) {
		t.Fatalf("round trip lost dictionary: %s", encoded)
	}
}
