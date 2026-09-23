package cmd

import "github.com/Usefused/cli/internal/configfile"

// rewriteSDKInitUnifiedServices preserves execution-step identity while canonicalizing provider routing like physical selections.
func rewriteSDKInitUnifiedServices(operations map[string]configfile.UnifiedOperation, aliases map[string]string) map[string]configfile.UnifiedOperation {
	// Ordinary apps retain their existing absent-field representation.
	if operations == nil {
		return nil
	}
	rewritten := make(map[string]configfile.UnifiedOperation, len(operations))
	for name, operation := range operations {
		bindings := make(map[string]configfile.UnifiedOperationBinding, len(operation.Bindings))
		for step, binding := range operation.Bindings {
			key := binding.Service
			// Compact bindings select the provider by step name but the step itself must not be renamed.
			if key == "" {
				key = step
			}
			// Only known provider aliases may change routing; unknown references remain available for validation.
			if canonical, ok := aliases[key]; ok && canonical != key {
				binding.Service = canonical
			}
			bindings[step] = binding
		}
		operation.Bindings = bindings
		rewritten[name] = operation
	}
	return rewritten
}
