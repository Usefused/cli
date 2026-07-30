package cmd

import "testing"

// TestParseInlineKeyValuePairs covers the shape basic auth, mTLS, and connect
// all depend on: well-formed pairs parse, a key present-but-blank still
// appears in the map (distinct from a key never mentioned at all), and a
// malformed segment (no "=") is dropped rather than panicking or erroring.
func TestParseInlineKeyValuePairs(t *testing.T) {
	pairs := parseInlineKeyValuePairs("Username=alice;PASSWORD=;malformed;region = eu ")
	if pairs["username"] != "alice" {
		t.Fatalf("expected lowercase key match, got %#v", pairs)
	}
	if v, ok := pairs["password"]; !ok || v != "" {
		t.Fatalf("expected password present-but-blank, got ok=%v v=%q", ok, v)
	}
	if _, ok := pairs["malformed"]; ok {
		t.Fatalf("expected a segment with no '=' to be dropped, got %#v", pairs)
	}
	if pairs["region"] != "eu" {
		t.Fatalf("expected surrounding whitespace trimmed, got %#v", pairs)
	}
}

// TestParseInlineKeyValuePairs_Empty proves the empty-string input (the
// signal to fall back to interactive prompts) returns an empty, non-nil map
// rather than requiring every caller to nil-check separately.
func TestParseInlineKeyValuePairs_Empty(t *testing.T) {
	pairs := parseInlineKeyValuePairs("")
	if len(pairs) != 0 {
		t.Fatalf("expected empty map for empty input, got %#v", pairs)
	}
}
