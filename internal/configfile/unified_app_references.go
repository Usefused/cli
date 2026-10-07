package configfile

import (
	"fmt"
	"regexp"
	"strings"
)

var unifiedAppReferenceAlias = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// validateUnifiedAppReferences mirrors Engine's finite, immutable attachment contract before planning.
func validateUnifiedAppReferences(cfg *AppConfig, kind ConfigKind) error {
	// All execution adapters share explicit, immutable hosted dependencies.
	if len(cfg.UnifiedApps) > 0 && kind != KindSDK && kind != KindMCP && kind != KindUnifiedApp {
		return fmt.Errorf("unified_apps requires kind sdk, mcp, or unified_app")
	}
	// Bound the dependency surface independently of provider selections.
	if len(cfg.UnifiedApps) > 16 {
		return fmt.Errorf("at most 16 Unified Apps may be attached")
	}
	for alias, ref := range cfg.UnifiedApps {
		// Mirror the same alias and immutable version contract used by Engine.
		if err := validateUnifiedAppReference(alias, ref, cfg.UnifiedApps); err != nil {
			return err
		}
	}
	return nil
}

// validateUnifiedAppReference prevents ambiguous generated names and implicit dependency upgrades.
func validateUnifiedAppReference(alias string, ref UnifiedAppReference, refs map[string]UnifiedAppReference) error {
	// Python exposes a synchronous companion, so aliases cannot shadow each other.
	if _, collision := refs[alias+"_sync"]; collision {
		return fmt.Errorf("Unified App aliases conflict with a synchronous companion method")
	}
	// Portable identifiers retain one callable name across all execution adapters.
	if !unifiedAppReferenceAlias.MatchString(alias) || strings.Contains(" false true null none self cls async await and as assert break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield constructor then execute __proto__ ", " "+alias+" ") {
		return fmt.Errorf("Unified App alias must be a non-reserved identifier")
	}
	// Exact versions prevent an immutable consumer from silently changing dependencies.
	if strings.TrimSpace(ref.Name) == "" || !appVersionPattern.MatchString(ref.Version) {
		return fmt.Errorf("unified_apps requires an app name and exact SemVer version")
	}
	return nil
}
