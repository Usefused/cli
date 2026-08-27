package api

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

// formatMissingCredentialDetails renders the existing Engine metadata without
// additional lookups, credential reads, or changes to interactive/JSON contracts.
func formatMissingCredentialDetails(bucket *MissingCredentialBucket, requirements []MissingCredentialRequirement) string {
	// Other error categories and old responses retain their existing output.
	if len(requirements) == 0 {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "\nMissing credential requirements: %d in %s:", len(requirements), credentialBucketLabel(bucket))
	// Keep human output bounded while JSON retains the complete typed requirements.
	for index, requirement := range requirements {
		// A large plan should not flood the terminal with credential metadata.
		if index == 20 {
			fmt.Fprintf(&out, "\n  ... %d more requirements; use --json for the full list.", len(requirements)-index)
			break
		}
		fmt.Fprintf(&out, "\n  - %s (%s): missing %s", credentialServiceLabel(requirement), credentialAuthLabel(requirement), credentialFieldLabels(requirement))
	}
	return out.String()
}

// credentialBucketLabel names the authoritative selected bucket without guessing a default.
func credentialBucketLabel(bucket *MissingCredentialBucket) string {
	// Older Engine errors may omit the typed target entirely.
	if bucket == nil {
		return "the selected bucket"
	}
	// Display names are useful only when safe to render in an error.
	if name := safeCredentialMetadata(bucket.Name); name != "" {
		return fmt.Sprintf("bucket %q", name)
	}
	return credentialIdentityLabel("bucket", bucket.ID)
}

// credentialServiceLabel uses supplied names, falling back to an opaque validated
// ID for apply errors, which intentionally avoid service-name lookup queries.
func credentialServiceLabel(requirement MissingCredentialRequirement) string {
	// A safe Engine-provided name is preferable to an internal identifier.
	if name := safeCredentialMetadata(requirement.Service); name != "" {
		return fmt.Sprintf("%q", name)
	}
	return credentialIdentityLabel("service", requirement.ServiceID)
}

// credentialIdentityLabel accepts only real UUIDs as an unnamed target fallback.
func credentialIdentityLabel(kind, value string) string {
	id, err := uuid.Parse(value)
	// Malformed identity text must not become a second path for unsafe server output.
	if err != nil || id == uuid.Nil {
		return "the selected " + kind
	}
	return kind + " " + id.String()
}

// credentialAuthLabel distinguishes multiple required schemes on one service;
// the type is an allowlisted family and the name is metadata, never its value.
func credentialAuthLabel(requirement MissingCredentialRequirement) string {
	types := map[string]string{"basic": "basic", "bearer": "bearer", "api_key": "api_key", "mtls": "mtls", "oauth": "oauth", "oidc": "oidc"}
	label := types[requirement.AuthType]
	// Unknown future auth families still produce a useful, non-echoing diagnostic.
	if label == "" {
		label = "authentication"
	}
	// A named scheme disambiguates several requirements for the same service.
	if name := safeCredentialMetadata(requirement.AuthName); name != "" {
		label += fmt.Sprintf(", auth %q", name)
	}
	return label
}

// credentialFieldLabels includes exact secret-key names, not values, for actionable setup guidance.
func credentialFieldLabels(requirement MissingCredentialRequirement) string {
	labels := make([]string, 0, min(len(requirement.RequiredFields), 8))
	// Field order follows the authoritative Engine response.
	for index, field := range requirement.RequiredFields {
		// Bound terminal detail independently of an unexpectedly large remote array.
		if index == 8 {
			labels = append(labels, "additional fields (see --json)")
			break
		}
		labels = append(labels, credentialFieldLabel(requirement.AuthType, field))
	}
	// Missing field metadata must not make a reported readiness failure disappear.
	if len(labels) == 0 {
		return "required authentication material (field details unavailable)"
	}
	return strings.Join(labels, ", ")
}

// credentialFieldLabel describes only reviewed prompt labels and secret keys.
func credentialFieldLabel(authType string, field MissingCredentialField) string {
	label := safeCredentialMetadata(field.Name)
	// Unsafe or absent labels get a neutral fallback rather than being echoed.
	if label == "" {
		label = "credential field"
	}
	// A secret key identifies where to store material but never reveals that material.
	if key := safeCredentialMetadata(field.SecretKey); key != "" {
		label += fmt.Sprintf(" (secret key %q)", key)
	}
	return label
}

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
