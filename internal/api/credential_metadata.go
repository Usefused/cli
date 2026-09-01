package api

import (
	"strings"
	"unicode"
)

// safeCredentialMetadata bounds display-only metadata and rejects known secret,
// URL, terminal-control, and bidi-control shapes before human error rendering.
func safeCredentialMetadata(value string) string {
	// Reject before trimming so leading terminal controls cannot be normalized away.
	if len(value) > 256 || containsCredentialMaterial(value) {
		return ""
	}
	// Invisible controls could spoof the service or field attached to a requirement.
	for _, char := range value {
		if unicode.IsControl(char) || unicode.Is(unicode.Cf, char) {
			return ""
		}
	}
	return strings.TrimSpace(value)
}
