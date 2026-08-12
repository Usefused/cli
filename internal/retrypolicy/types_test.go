package retrypolicy_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Usefused/cli/internal/retrypolicy"
)

func TestV3FixtureRoundTripsWithoutInterpretation(t *testing.T) {
	payload := readRetryFixture(t, "v3_idempotency_predicates.json")
	var config retrypolicy.Config
	if err := json.Unmarshal(payload, &config); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	assertRetrySemanticJSON(t, encoded, payload)
	if len(config.Rules) != 4 || config.Rules[2].Predicates.IdempotencyKey.Header != "Idempotency-Key" {
		t.Fatalf("retry predicates changed: %#v", config)
	}
}

func TestLegacyRetryIsRejected(t *testing.T) {
	payload := []byte(`{"strategy":"exponential_backoff","max_retries":3,"backoff_ms":500}`)
	var config retrypolicy.Config
	if err := json.Unmarshal(payload, &config); err == nil {
		t.Fatalf("canonical CLI transport accepted legacy retry %s", payload)
	}
}

func TestRetryUnknownFieldsAreRejected(t *testing.T) {
	var config retrypolicy.Config
	err := json.Unmarshal([]byte(`{"version":3,"rules":[],"provider":"stripe"}`), &config)
	if err == nil {
		t.Fatal("provider-specific retry field was accepted")
	}
}

func readRetryFixture(t *testing.T, name string) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", "retry", name))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func assertRetrySemanticJSON(t *testing.T, gotPayload, wantPayload []byte) {
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
