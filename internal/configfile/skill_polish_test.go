package configfile_test

import (
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/configfile"
)

// TestMCPSkillPolishConfigIsExplicitAndHashed keeps the opt-in off by default, boolean-only, MCP-only and part of version identity.
func TestMCPSkillPolishConfigIsExplicitAndHashed(t *testing.T) {
	source := `apiVersion: fused/v1
kind: mcp
name: assistant
version: 1.0.0
description: Read repository data.
bucket: default
services:
  github:
    version: v1
    operations: [listRepos]
`
	local, err := configfile.Parse([]byte(source), "mcp.yaml")
	// Omission is the default and must stay valid.
	if err != nil || local.MCP.FusedSkillPolish {
		t.Fatalf("default config=%+v err=%v", local, err)
	}
	polished, err := configfile.Parse([]byte(source+"fused-skill-polish: true\n"), "mcp.yaml")
	// Consent survives strict parsing and changes the immutable source identity.
	if err != nil || !polished.MCP.FusedSkillPolish || polished.SourceHash == local.SourceHash {
		t.Fatalf("opt-in config=%+v err=%v", polished, err)
	}
	// Only a real boolean may consent to sending metadata to the Registry LLM.
	if _, err := configfile.Parse([]byte(source+"fused-skill-polish: \"true\"\n"), "mcp.yaml"); err == nil {
		t.Fatal("string boolean accepted")
	}
	sdk := strings.Replace(source, "kind: mcp", "kind: sdk", 1)
	sdk = strings.Replace(sdk, "description: Read repository data.", "language: typescript", 1)
	// SDK versions serve no skill resource, so the field is rejected rather than ignored.
	if _, err := configfile.Parse([]byte(sdk+"fused-skill-polish: true\n"), "sdk.yaml"); err == nil {
		t.Fatal("SDK accepted MCP-only skill polish")
	}
}
