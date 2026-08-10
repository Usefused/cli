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
		"v2_cursor_body.json",
		"v2_cursor_header_numeric.json",
		"v2_offset.json",
		"v2_page_number.json",
		"v2_next_url_link.json",
		// CLI transport intentionally preserves multiple known branches; Registry
		// and Engine own the semantic discriminator rejection.
		"invalid_multiple_strategies.json",
	}
	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			assertGoldenRoundTrip(t, fixture)
		})
	}
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
