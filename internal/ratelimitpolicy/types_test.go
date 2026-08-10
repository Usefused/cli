package ratelimitpolicy_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Usefused/cli/internal/ratelimitpolicy"
)

func TestCanonicalFixturesRoundTripWithoutNormalization(t *testing.T) {
	for _, name := range []string{"v2_fixed_window.json", "v2_token_bucket.json", "v2_mixed.json", "invalid_discriminator.json"} {
		t.Run(name, func(t *testing.T) {
			payload := readFixture(t, name)
			var first ratelimitpolicy.Config
			if err := json.Unmarshal(payload, &first); err != nil {
				t.Fatalf("decode canonical transport: %v", err)
			}
			encoded, err := json.Marshal(first)
			if err != nil {
				t.Fatalf("encode canonical transport: %v", err)
			}
			var second ratelimitpolicy.Config
			if err := json.Unmarshal(encoded, &second); err != nil {
				t.Fatalf("decode round trip: %v", err)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("transport changed policy\nfirst:  %#v\nsecond: %#v", first, second)
			}
		})
	}
}

func TestLegacyAndUnknownFieldsAreRejected(t *testing.T) {
	for _, payload := range [][]byte{
		readFixture(t, "invalid_legacy.json"),
		[]byte(`{"version":2,"policies":[],"strategy":"fixed_window"}`),
		[]byte(`{"version":2,"policies":[{"name":"minute","unit":"requests","scope":"service_version","default_cost":1,"operation_costs":{},"algorithm":"fixed_window","fixed_window":{"limit":10,"duration_ms":1000,"requests_per_second":10}}]}`),
		[]byte(`{"version":2,"policies":[{"name":"burst","unit":"requests","scope":"service_version","default_cost":1,"operation_costs":{},"algorithm":"token_bucket","token_bucket":{"capacity":10,"refill_units":1,"refill_interval_ms":1000,"burst":20}}]}`),
	} {
		var config ratelimitpolicy.Config
		if err := json.Unmarshal(payload, &config); err == nil {
			t.Fatalf("strict transport accepted %s", payload)
		}
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", "rate-limit", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return payload
}
