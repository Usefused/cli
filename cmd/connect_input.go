package cmd

import (
	"fmt"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

// parseConnectAssignments accepts one comma-separated list and preserves equals signs inside values.
func parseConnectAssignments(assignments []string) (map[string]string, error) {
	fields := make(map[string]string)
	// Omission selects interactive collection; an explicit list must be one shell argument.
	if len(assignments) == 0 {
		return fields, nil
	}
	// A single grammar keeps automation and terminal examples consistent.
	if len(assignments) != 1 {
		return nil, fmt.Errorf("provide one quoted name=value, name=value argument")
	}
	for _, assignment := range strings.Split(assignments[0], ",") {
		name, value, supplied := strings.Cut(assignment, "=")
		name = strings.TrimSpace(name)
		// Invalid input errors omit the argument because it may contain credential material.
		if !supplied || name == "" {
			return nil, fmt.Errorf("credentials must use name=value, name=value")
		}
		// Duplicate fields must not silently replace earlier credential input.
		if _, exists := fields[name]; exists {
			return nil, fmt.Errorf("duplicate credential field")
		}
		fields[name] = value
	}
	return fields, nil
}

// connectCredentialInput shares interactive collectors and validates explicit fields before persistence.
func connectCredentialInput(cmd *cobra.Command, auth *api.AuthConfig, fields map[string]string) (secretCredentialInput, error) {
	// Omitted assignments select the normal interactive workflow only when prompting is allowed.
	if len(fields) == 0 {
		// Machine-readable and unattended commands must never open hidden prompts.
		if nonInteractive() || wantsJSON(cmd) {
			return secretCredentialInput{}, fmt.Errorf("credential input is required; pass name=value with --no-input")
		}
		return collectSecretCredentialInput(auth, "")
	}
	// Closed field sets prevent typos from silently discarding supplied credential material.
	if err := validateConnectFields(auth, fields); err != nil {
		return secretCredentialInput{}, err
	}
	switch canonicalSecretAuthType(auth) { // Family validators retain atomic pair and password policy semantics.
	case "oauth", "oidc":
		id, secret, err := validateOAuthApplicationSecretInput(fields["client_id"], fields["client_secret"], len(fields))
		return secretCredentialInput{clientID: id, clientSecret: secret}, err
	case "basic":
		username, password, err := validateBasicSecretInput(auth.BasicPasswordMode, fields["username"], fields["password"])
		return secretCredentialInput{username: username, password: password}, err
	case "mtls":
		cert, key := fields["cert"], fields["key"]
		return secretCredentialInput{cert: cert, key: key}, validateMTLSSecretPair(cert, key)
	default:
		value := fields[secretAuthCredentialName(auth)]
		// Explicitly empty input must fail instead of triggering the token collector's prompt fallback.
		if value == "" {
			return secretCredentialInput{}, fmt.Errorf("credential value cannot be empty")
		}
		return secretCredentialInput{token: value}, nil
	}
}

// validateConnectFields ties assignments to the selected scheme instead of inventing credential identities.
func validateConnectFields(auth *api.AuthConfig, fields map[string]string) error {
	allowed := []string{secretAuthCredentialName(auth)}
	switch canonicalSecretAuthType(auth) { // Paired families accept their declared input fields, never storage-key guesses.
	case "oauth", "oidc":
		allowed = []string{"client_id", "client_secret"}
	case "basic":
		allowed = []string{"username", "password"}
	case "mtls":
		allowed = []string{"cert", "key"}
	}
	for name := range fields {
		valid := false
		for _, candidate := range allowed {
			valid = valid || name == candidate
		}
		// Print only metadata-derived names, never caller input that could be a misplaced secret.
		if !valid {
			return fmt.Errorf("expected credential fields: %s", strings.Join(allowed, ", "))
		}
	}
	return nil
}
