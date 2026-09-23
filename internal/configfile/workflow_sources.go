package configfile

import (
	"fmt"
	"github.com/google/uuid"
	"regexp"
)

var workflowSourceHashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// validateWorkflowSources bounds local provenance without treating it as execution authorization.
func validateWorkflowSources(sources []WorkflowSource) error {
	// A hand-authored config uses the same maximum selected set as the library UI.
	if len(sources) > 32 {
		return fmt.Errorf("at most 32 workflow sources are supported")
	}
	seen := map[string]bool{}
	for _, source := range sources {
		id, err := uuid.Parse(source.ID)
		// Release identity, version, and digest are required even when definitions are customized locally.
		if err != nil || id == uuid.Nil || seen[source.ID] || source.Version == "" || len(source.Version) > 128 || !workflowSourceHashPattern.MatchString(source.Hash) {
			return fmt.Errorf("invalid or duplicate workflow source")
		}
		seen[source.ID] = true
	}
	return nil
}
