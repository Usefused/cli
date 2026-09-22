package cmd

import (
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
	"testing"
)

// TestUnifiedInitCarriesClassifierConsent proves the public creation flag reaches the immutable scaffold request.
func TestUnifiedInitCarriesClassifierConsent(t *testing.T) {
	var request scaffoldRequest
	// Capture the admitted request without contacting Registry or modifying workspace state.
	executeUnifiedInitForTest(t, func(_ *cobra.Command, _ unifiedInitMode, got scaffoldRequest) error { request = got; return nil }, "assistant", "--mcp", "--description", "Review GitHub repositories.", "--service", "github@v1", "--select-all", "github", "--fused-intelligent-classifier")
	if !request.fusedIntelligentClassifier {
		t.Fatal("creation flag lost classifier consent")
	} // CLI flags and YAML must select the same remote provider.
}

// TestMCPClassifierSuccessorCanDisableConsent distinguishes explicit false from omission.
func TestMCPClassifierSuccessorCanDisableConsent(t *testing.T) {
	config := &configfile.AppConfig{FusedIntelligentClassifier: true}
	request := scaffoldRequest{kind: configfile.KindMCP}
	changed, err := mergeMCPFusedIntelligentClassifier(config, request, true)
	// Omission on an extension preserves the previous choice.
	if err != nil || changed || !config.FusedIntelligentClassifier {
		t.Fatal("omission changed classifier consent")
	}
	request.classifierSet = true
	changed, err = mergeMCPFusedIntelligentClassifier(config, request, true)
	// Explicit false on a successor removes remote-processing consent.
	if err != nil || !changed || config.FusedIntelligentClassifier {
		t.Fatal("explicit false did not disable classifier")
	}
}
