package cmd

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"
)

var bucketSecretCmd = &cobra.Command{
	Use:   "secret",
	Short: "Manage service-independent bucket secrets",
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

// validateBucketSecretName preserves the single-segment reference invariant
// used by ${bucket.<name>.secret.<key>} consumers.
func validateBucketSecretName(value string) error {
	name := strings.TrimSpace(value)
	// Normalizing whitespace would silently change the key consumers must reference.
	if name == "" || name != value {
		return errors.New("secret name must be non-empty and cannot start or end with whitespace")
	}
	for _, char := range name {
		// Separators, interpolation syntax, and whitespace would make the generated reference ambiguous.
		if unicode.IsSpace(char) || strings.ContainsRune(".${}", char) {
			return errors.New("secret name must be one reference-safe segment without whitespace, periods, braces, or dollar signs")
		}
	}
	return nil
}

// parseBucketSecretExpiresAt converts optional RFC3339 input without sharing
// mutable flags with the provider-specific secret command.
func parseBucketSecretExpiresAt(value string) (*time.Time, error) {
	// Blank input means the secret has no configured expiry.
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	// Malformed lifecycle metadata must fail before the secret-bearing request.
	if err != nil {
		return nil, fmt.Errorf("invalid --expires-at %q, expected RFC3339 (e.g. 2026-12-31T23:59:59Z): %w", value, err)
	}
	return &parsed, nil
}

// init registers named secret operations under their owning bucket.
func init() {
	bucketCmd.AddCommand(bucketSecretCmd)
	addBucketEntryCommands(bucketSecretCmd, "secret")
}
