package cmd

import (
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

// TestUnifiedInitCarriesSkillPolishConsent proves the creation flag reaches the immutable scaffold request.
func TestUnifiedInitCarriesSkillPolishConsent(t *testing.T) {
	var request scaffoldRequest
	// Capture the admitted request without contacting Fused or writing workspace state.
	executeUnifiedInitForTest(t, func(_ *cobra.Command, _ unifiedInitMode, got scaffoldRequest) error { request = got; return nil }, "assistant", "--mcp", "--description", "Review GitHub repositories.", "--service", "github@v1", "--select-all", "github", "--fused-skill-polish")
	// The flag must arrive as explicit consent, distinguishable from omission.
	if !request.fusedSkillPolish || !request.skillPolishSet {
		t.Fatalf("creation flag lost skill-polish consent: %+v", request)
	}
}

// TestUnifiedInitRejectsSkillPolishOutsideMCP keeps consent off kinds that serve no skill.
func TestUnifiedInitRejectsSkillPolishOutsideMCP(t *testing.T) {
	err := executeUnifiedInitForTestWithError(t, func(*cobra.Command, unifiedInitMode, scaffoldRequest) error { return nil }, "assistant", "--sdk", "--service", "github@v1", "--select-all", "github", "--fused-skill-polish")
	// SDK versions have no served skill resource to polish.
	if err == nil || !strings.Contains(err.Error(), "--fused-skill-polish requires --mcp") {
		t.Fatalf("err = %v", err)
	}
}

// TestMCPSkillPolishChangeNeedsSuccessor mirrors classifier consent: omission preserves, change needs a new version.
func TestMCPSkillPolishChangeNeedsSuccessor(t *testing.T) {
	config := &configfile.AppConfig{FusedSkillPolish: true}
	request := scaffoldRequest{kind: configfile.KindMCP}
	changed, err := mergeMCPFusedSkillPolish(config, request, false)
	// Omission on an extension preserves the previous choice.
	if err != nil || changed || !config.FusedSkillPolish {
		t.Fatal("omission changed skill-polish consent")
	}
	request.skillPolishSet = true
	_, err = mergeMCPFusedSkillPolish(config, request, false)
	// Changing consent in place would silently alter an immutable published version.
	if err == nil || !strings.Contains(err.Error(), "requires an MCP successor version") {
		t.Fatalf("in-place change err = %v", err)
	}
	changed, err = mergeMCPFusedSkillPolish(config, request, true)
	// An explicit successor may withdraw consent.
	if err != nil || !changed || config.FusedSkillPolish {
		t.Fatal("successor did not disable skill polish")
	}
}
