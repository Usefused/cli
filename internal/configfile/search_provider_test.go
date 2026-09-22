package configfile_test

import (
	"github.com/Usefused/cli/internal/configfile"
	"strings"
	"testing"
)

// TestMCPClassifierConfigIsExplicitAndHashed prevents accidental remote-processing defaults or silent provider typos.
func TestMCPClassifierConfigIsExplicitAndHashed(t *testing.T) {
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
	if err != nil {
		t.Fatal(err)
	} // The existing default config must remain valid.
	remote, err := configfile.Parse([]byte(source+"fused-intelligent-classifier: true\n"), "mcp.yaml")
	if err != nil {
		t.Fatal(err)
	} // Explicit remote discovery must survive strict parsing.
	if local.SourceHash == remote.SourceHash || !remote.MCP.FusedIntelligentClassifier {
		t.Fatal("classifier consent missing from immutable source identity")
	} // Consent changes require a different plan/version.
	_, err = configfile.Parse([]byte(source+"fused-intelligent-classifier: \"true\"\n"), "mcp.yaml")
	if err == nil {
		t.Fatal("string boolean accepted")
	} // Only boolean values may opt into remote processing.

	disabled, err := configfile.Parse([]byte(source+"fused-intelligent-classifier: false\n"), "mcp.yaml")
	// Explicit false must preserve the local-search default.
	if err != nil || disabled.MCP.FusedIntelligentClassifier {
		t.Fatalf("false config=%+v err=%v", disabled, err)
	}
	_, err = configfile.Parse([]byte(source+"search_provider: fused-intelligent-classifier\n"), "mcp.yaml")
	// The replaced field must fail strict parsing rather than imply consent.
	if err == nil {
		t.Fatal("removed search_provider field accepted")
	}
	sdk := strings.Replace(source, "kind: mcp", "kind: sdk", 1)
	sdk = strings.Replace(sdk, "description: Read repository data.", "language: typescript", 1)
	_, err = configfile.Parse([]byte(sdk+"fused-intelligent-classifier: true\n"), "sdk.yaml")
	if err == nil {
		t.Fatal("SDK accepted MCP-only classifier")
	} // Remote search has no meaning on an SDK config.
}
