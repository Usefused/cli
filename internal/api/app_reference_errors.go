package api

import (
	"errors"
	"fmt"
	"strings"
)

// appFamilyReferenceError names the caller's missing SDK/MCP without claiming
// visibility into inaccessible apps or reclassifying failed lookups as absence.
func appFamilyReferenceError(err error, reference, kind string) error {
	var apiErr *APIError
	// Only the Engine's reviewed not-found category supports app-specific guidance.
	if !errors.As(err, &apiErr) || apiErr.Code != "resource_not_found" {
		return err
	}
	label := strings.ToUpper(kind)
	// Caller-supplied names are useful only when safe for terminal rendering.
	if name := safeCredentialMetadata(reference); name != "" {
		label += fmt.Sprintf(" %q", name)
	}
	result := *apiErr
	result.Message = label + " was not found or is not accessible in this workspace."
	result.Remediation = fmt.Sprintf("Run `fused-cli %s list` and check the name and selected workspace.", kind)
	result.Details.ServerDetail = ""
	return &result
}
