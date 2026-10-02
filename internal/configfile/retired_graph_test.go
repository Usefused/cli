package configfile

import (
	"strings"
	"testing"
)

// TestRetiredGraphConfigRejected prevents removed authoring fields from being silently ignored.
func TestRetiredGraphConfigRejected(t *testing.T) {
	document := `apiVersion: fused/v1
kind: mcp
name: assistant
version: 1.0.0
bucket: default
services:
  tickets:
    version: v1
    operations: [listTickets]
unified_operations: {}
`
	_, err := Parse([]byte(document), "mcp.yaml")
	// An explicit decoding error is required before any plan could be created.
	if err == nil || !strings.Contains(err.Error(), "unified_operations") {
		t.Fatalf("retired graph authoring must fail explicitly: %v", err)
	}
}
