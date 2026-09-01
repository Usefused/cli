package api

import (
	"strings"
	"testing"
)

// TestSafeCredentialMetadataRejectsSecretAndTerminalShapes protects the shared
// metadata sanitizer without retaining the retired plan-error renderer.
func TestSafeCredentialMetadataRejectsSecretAndTerminalShapes(t *testing.T) {
	unsafe := []string{
		"fsk_never_print",
		"https://user:password@example.test",
		"Authorization: Bearer abc",
		"password=hidden",
		"-----BEGIN PRIVATE KEY-----",
		"\x1b[31mJira",
		"Jira\nforged error",
		"Jira\u202eforged",
		strings.Repeat("a", 257),
	}
	// Every unsafe metadata shape must be omitted instead of normalized into output.
	for _, value := range unsafe {
		if got := safeCredentialMetadata(value); got != "" {
			t.Fatalf("unsafe metadata %q became %q", value, got)
		}
	}
	// Ordinary provider labels remain available to reviewed diagnostics.
	if got := safeCredentialMetadata("  Jira Cloud  "); got != "Jira Cloud" {
		t.Fatalf("safe metadata = %q", got)
	}
}
