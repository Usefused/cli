package configfile

import "testing"

// TestUnifiedAppReferencesRequireExactConsumerDeclarations protects immutable and non-recursive attachment selection.
func TestUnifiedAppReferencesRequireExactConsumerDeclarations(t *testing.T) {
	cfg := &AppConfig{UnifiedApps: map[string]UnifiedAppReference{"customer_lookup": {Name: "Customer lookup", Version: "1.0.0"}}}
	for _, kind := range []ConfigKind{KindSDK, KindMCP} {
		// Both consumer adapters accept the same portable name/version contract.
		if err := validateUnifiedAppReferences(cfg, kind); err != nil {
			t.Fatal(err)
		}
	}
	// Hosted apps cannot form recursive dependency chains.
	if validateUnifiedAppReferences(cfg, KindUnifiedApp) == nil {
		t.Fatal("recursive dependency accepted")
	}
	cfg.UnifiedApps["customer_lookup"] = UnifiedAppReference{Name: "Customer lookup", Version: "latest"}
	if validateUnifiedAppReferences(cfg, KindSDK) == nil {
		t.Fatal("implicit latest accepted")
	}
}
