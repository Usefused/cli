package configfile

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// validateManagedApplicationID preserves the default while rejecting ambiguous named application selectors.
func validateManagedApplicationID(ref, id string) error {
	// Omission selects the broker's permanently pinned default publication.
	if id == "" {
		return nil
	}
	parsed, err := uuid.Parse(id)
	// A named application is a canonical identity on the explicitly selected broker source.
	if err != nil || parsed == uuid.Nil || parsed.String() != id || !strings.HasPrefix(ref, "${fused.bucket.auth.") {
		return fmt.Errorf("managed_application_id requires a managed ref and canonical nonzero UUID")
	}
	return nil
}
