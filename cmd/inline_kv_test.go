package cmd

import (
	"strings"
	"testing"
)

// TestParseInlineKeyValuePairsAcceptsValidAssignments verifies normalization
// and the split-once invariant needed by URLs, tokens, and encoded values.
func TestParseInlineKeyValuePairsAcceptsValidAssignments(t *testing.T) {
	pairs, err := parseInlineKeyValuePairs("Username=alice;token = header.payload== ;region = eu ")
	// A fully formed assignment list must parse without local validation errors.
	if err != nil {
		t.Fatalf("parse valid inline assignments: %v", err)
	}
	// Keys are case-insensitive for all credential consumers.
	if pairs["username"] != "alice" || pairs["region"] != "eu" {
		t.Fatalf("expected normalized keys and trimmed values, got %#v", pairs)
	}
	// Only the first equals sign is structural; later signs belong to the value.
	if pairs["token"] != "header.payload==" {
		t.Fatalf("expected embedded equals signs to be preserved, got %#v", pairs)
	}
}

// TestParseInlineKeyValuePairsRejectsAmbiguousAssignments covers every input
// shape that previously became a silent omission or overwrite.
func TestParseInlineKeyValuePairsRejectsAmbiguousAssignments(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		wantError string
	}{
		{name: "missing separator", value: "username=alice;password", wantError: "segment 2 must contain '='"},
		{name: "duplicate exact key", value: "token=first;token=second", wantError: `duplicate key "token"`},
		{name: "duplicate normalized key", value: "Token=first; token =second", wantError: `duplicate key "token"`},
		{name: "empty key", value: "=secret", wantError: "segment 1 has an empty key"},
		{name: "empty value", value: "client_secret=", wantError: `key "client_secret" has an empty value`},
		{name: "whitespace value", value: "client_secret=   ", wantError: `key "client_secret" has an empty value`},
	}
	// Each rejected shape should retain the parser's exact actionable reason.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseInlineKeyValuePairs(test.value)
			// Invalid input must return the specific local diagnostic expected by the caller.
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("parseInlineKeyValuePairs(%q) error = %v, want containing %q", test.value, err, test.wantError)
			}
		})
	}
}

// TestParseInlineKeyValuePairsEmpty preserves the empty aggregate as the
// internal signal used by interactive credential collection.
func TestParseInlineKeyValuePairsEmpty(t *testing.T) {
	pairs, err := parseInlineKeyValuePairs("")
	// Interactive-mode signaling must remain valid and allocate no assignments.
	if err != nil || len(pairs) != 0 {
		t.Fatalf("expected empty input to return an empty map, got %#v err=%v", pairs, err)
	}
}
