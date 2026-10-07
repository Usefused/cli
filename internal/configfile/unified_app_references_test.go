package configfile

import "testing"

// TestUnifiedAppReferencesRequireExactConsumerDeclarations protects immutable attachment selection across adapters.
func TestUnifiedAppReferencesRequireExactConsumerDeclarations(t *testing.T) {
	cfg := &AppConfig{UnifiedApps: map[string]UnifiedAppReference{"customer_lookup": {Name: "Customer lookup", Version: "1.0.0"}}}
	for _, kind := range []ConfigKind{KindSDK, KindMCP, KindUnifiedApp} {
		// All consumer adapters accept the same portable name/version contract.
		if err := validateUnifiedAppReferences(cfg, kind); err != nil {
			t.Fatal(err)
		}
	}
	// Non-execution configs cannot acquire hosted authority.
	if validateUnifiedAppReferences(cfg, KindWorkspace) == nil {
		t.Fatal("workspace dependency accepted")
	}
	cfg.UnifiedApps["customer_lookup"] = UnifiedAppReference{Name: "Customer lookup", Version: "latest"}
	// Exact versions prevent a dependency from changing beneath immutable source.
	if validateUnifiedAppReferences(cfg, KindSDK) == nil {
		t.Fatal("implicit latest accepted")
	}
}

// TestUnifiedAppReferenceOnlyConfig admits composition without a placeholder provider operation.
func TestUnifiedAppReferenceOnlyConfig(t *testing.T) {
	cfg := &AppConfig{Name: "Composition", Version: "1.0.0", Bucket: "default", Source: "export default {};", UnifiedApps: map[string]UnifiedAppReference{"lookup": {Name: "Lookup", Version: "1.0.0"}}}
	// Full config validation must agree with the reference validator before reaching Engine.
	if err := validateAppConfig(cfg, KindUnifiedApp); err != nil {
		t.Fatal(err)
	}
}
