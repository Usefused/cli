package configfile

import (
	"fmt"
	"regexp"
	"strings"
)

var unifiedAppReferenceAlias = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// validateUnifiedAppReferences mirrors Engine's finite, immutable attachment contract before planning.
func validateUnifiedAppReferences(cfg *AppConfig, kind ConfigKind) error {
	// Hosted code cannot recursively attach another app's runtime authority.
	if len(cfg.UnifiedApps) > 0 && kind != KindSDK && kind != KindMCP {
		return fmt.Errorf("unified_apps requires kind sdk or mcp")
	}
	if len(cfg.UnifiedApps) > 16 {
		return fmt.Errorf("at most 16 Unified Apps may be attached")
	}
	for alias, ref := range cfg.UnifiedApps {
		// Python exposes a synchronous companion, so aliases cannot shadow each other.
		if _, collision := cfg.UnifiedApps[alias+"_sync"]; collision {
			return fmt.Errorf("Unified App aliases conflict with a synchronous companion method")
		}
		// Aliases are portable generated identifiers; exact versions prevent implicit upgrades.
		if (!unifiedAppReferenceAlias.MatchString(alias) || strings.Contains(" false true null none self cls async await and as assert break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield constructor then execute __proto__ ", " "+alias+" ")) || strings.TrimSpace(ref.Name) == "" || !appVersionPattern.MatchString(ref.Version) {
			return fmt.Errorf("unified_apps requires an identifier alias, app name, and exact SemVer version")
		}
	}
	return nil
}
