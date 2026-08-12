package ratelimitpolicy_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Usefused/cli/internal/ratelimitpolicy"
)

func TestLegacyFixturesAreRejected(t *testing.T) {
	for _, name := range []string{"v2_fixed_window.json", "v2_token_bucket.json", "v2_mixed.json", "invalid_discriminator.json"} {
		t.Run(name, func(t *testing.T) {
			payload := readFixture(t, name)
			var config ratelimitpolicy.Config
			if err := json.Unmarshal(payload, &config); err == nil {
				t.Fatalf("canonical CLI transport accepted legacy quota %s", payload)
			}
		})
	}
}

func TestV3FixturesRoundTripSemantically(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("..", "..", "..", "contract-fixtures", "rate-limit", "v3_*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 11 {
		t.Fatalf("v3 fixture count = %d, want 11", len(fixtures))
	}
	for _, fixture := range fixtures {
		t.Run(filepath.Base(fixture), func(t *testing.T) {
			payload, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			var config ratelimitpolicy.Config
			if err := json.Unmarshal(payload, &config); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			assertSemanticJSON(t, encoded, payload)
		})
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
		t.Fatalf("transport changed JSON\ngot:  %s\nwant: %s", gotPayload, wantPayload)
	}
}

func TestLegacyAndUnknownFieldsAreRejected(t *testing.T) {
	for _, payload := range [][]byte{
		readFixture(t, "invalid_legacy.json"),
		[]byte(`{"version":3,"policies":[],"strategy":"fixed_window"}`),
		[]byte(`{"version":3,"policies":[{"name":"minute","mode":"enforce","unit":"requests","identity":{"inputs":[]},"cost":{"default":1,"rules":[]},"algorithm":"fixed_window","fixed_window":{"limit":10,"duration_ms":1000,"requests_per_second":10}}]}`),
		[]byte(`{"version":3,"policies":[{"name":"burst","mode":"enforce","unit":"requests","identity":{"inputs":[]},"cost":{"default":1,"rules":[]},"algorithm":"token_bucket","token_bucket":{"capacity":10,"refill_units":1,"refill_interval_ms":1000,"burst":20}}]}`),
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
