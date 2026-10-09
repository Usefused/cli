package configfile

import (
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

// TestImportedMCPConfigRoundTrip protects imported-only apps and exact identity preservation through local config parsing.
func TestImportedMCPConfigRoundTrip(t *testing.T) {
	raw := []byte(`apiVersion: fused/v1
kind: mcp
name: docs
version: 1.0.0
description: Read service documentation.
bucket: default
services:
  docs:
    version: v1
    mcp:
      revision_id: 00000000-0000-4000-8000-000000000003
      tools: ["echo,exact"]
      prompts: [echo]
      resources: ["docs://guide"]
      resource_templates: ["docs://{+path}"]
`)
	parsed, err := Parse(raw, "mcp.yaml")
	// Imported-only service scope is useful without implicit physical operations.
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := yaml.Marshal(parsed.MCP)
	// A portable round trip must retain provider-owned punctuation and native categories.
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := Parse(encoded, "mcp.yaml")
	// Every selected protocol category must survive serialization intact.
	if err != nil || reparsed.MCP.Services["docs"].MCP.Tools[0] != "echo,exact" || reparsed.MCP.Services["docs"].MCP.ResourceTemplates[0] != "docs://{+path}" {
		t.Fatalf("round trip: %s (%v)", encoded, err)
	}
	for _, change := range []struct{ old, new string }{
		{"revision_id: 00000000-0000-4000-8000-000000000003", "revision_id: invalid"},
		{"tools: [\"echo,exact\"]", "tools: [echo, echo]"},
		{"tools: [\"echo,exact\"]", "url: https://example.com/mcp"},
	} {
		_, err := Parse([]byte(strings.Replace(string(raw), change.old, change.new, 1)), "mcp.yaml")
		// Unknown provider connection fields and malformed grants must fail before plan traffic.
		if err == nil {
			t.Fatalf("accepted %s", change.new)
		}
	}
}

// TestImportedMCPRejectsOtherAppKinds keeps the CLI's capability boundary aligned with Engine admission.
func TestImportedMCPRejectsOtherAppKinds(t *testing.T) {
	selection := &ImportedMCPSelection{RevisionID: "00000000-0000-4000-8000-000000000003", Tools: []string{"echo"}}
	for _, kind := range []ConfigKind{KindSDK, KindUnifiedApp} {
		// Even physically valid selections cannot smuggle imported dispatch into unsupported adapters.
		if err := validateAppService("docs", SDKService{Operations: []string{"read"}, MCP: selection}, kind); err == nil {
			t.Fatalf("accepted imported selection for %s", kind)
		}
	}
	// An empty imported object cannot bypass normal useful-service validation.
	if err := ValidateImportedMCP(&ImportedMCPSelection{RevisionID: selection.RevisionID}, KindMCP); err == nil {
		t.Fatal("accepted empty imported selection")
	}
}
