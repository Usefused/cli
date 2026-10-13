package configfile

import (
	"fmt"
	"strings"
)

// validateRuntimeBucketConfig keeps opt-in and bounded scope identical to Engine admission.
func validateRuntimeBucketConfig(cfg *AppConfig, kind ConfigKind) error {
	// Omitted scope preserves the existing static routing for every app kind.
	if len(cfg.RuntimeBuckets) == 0 {
		return nil
	}
	if kind != KindUnifiedApp || len(cfg.RuntimeBuckets) > 32 {
		return fmt.Errorf("runtime_buckets requires a Unified App and at most 32 bucket names")
	}
	seen := make(map[string]bool)
	for _, name := range cfg.RuntimeBuckets {
		// A wildcard or ambiguous name must not turn a finite declaration into ambient bucket access.
		if name == "" || name != strings.TrimSpace(name) || name == "*" || seen[name] {
			return fmt.Errorf("runtime_buckets requires distinct, nonempty bucket names without wildcards")
		}
		seen[name] = true
	}
	return nil
}
