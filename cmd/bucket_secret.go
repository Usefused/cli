package cmd

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

var bucketSecretCmd = &cobra.Command{
	Use:   "secret",
	Short: "Manage service-independent bucket secrets",
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var bucketSecretSetInteractive bool
var bucketSecretSetValueStdin bool
var bucketSecretSetExpiresAt string

// bucketSecretInput groups the public name and lifecycle metadata with secret
// material while keeping the value out of command arguments and receipts.
type bucketSecretInput struct {
	KeyName   string
	Value     string
	ExpiresAt string
}

// bucketSecretSetResult is the stable mutation receipt and deliberately omits
// both plaintext and Engine storage-key details.
type bucketSecretSetResult struct {
	BucketID  string     `json:"bucket_id"`
	KeyName   string     `json:"key_name"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// collectBucketSecretInput is replaceable in tests so prompt behavior can be
// verified without attaching a real terminal.
var collectBucketSecretInput = promptBucketSecretInput

var bucketSecretSetCmd = &cobra.Command{
	Use:   "set <bucket-name-or-id> [secret-name]",
	Short: "Set a service-independent secret in a bucket",
	Args:  validateBucketSecretSetArgs,
	RunE: WithTelemetry("cli.bucket.secret.set", func(cmd *cobra.Command, args []string) error {
		keyName := ""
		// A second positional value names the secret; values never occupy argv.
		if len(args) == 2 {
			keyName = args[1]
		}
		return runBucketSecretSet(cmd, args[0], keyName)
	}),
}

// validateBucketSecretSetArgs keeps bucket selection mandatory while allowing
// the secret name itself to be collected only in an interactive terminal.
func validateBucketSecretSetArgs(cmd *cobra.Command, args []string) error {
	// Exactly one bucket and at most one public secret name are accepted; no value can enter argv.
	if err := cobra.RangeArgs(1, 2)(cmd, args); err != nil {
		return err
	}
	// Explicit prompt and stdin modes cannot safely share the same input stream.
	if bucketSecretSetInteractive && bucketSecretSetValueStdin {
		return errors.New("choose only one secret input: --interactive or --value-stdin")
	}
	// A forced prompt must fail clearly when global automation settings disable interaction.
	if bucketSecretSetInteractive {
		if err := requireInteractive("omit --interactive and pass the secret name with --value-stdin, or unset --no-input/CI"); err != nil {
			return err
		}
	}
	// Stdin mode cannot prompt for the public name after consuming a redirected stream.
	if bucketSecretSetValueStdin && len(args) != 2 {
		return errors.New("secret name is required with --value-stdin")
	}
	// Fully non-interactive execution requires both deterministic identity and the secret-safe stdin channel.
	if nonInteractive() {
		if len(args) != 2 {
			return errors.New("secret name is required in non-interactive mode")
		}
		if !bucketSecretSetValueStdin {
			return errors.New("secret input is required in non-interactive mode; use --value-stdin")
		}
	}
	// An argv-supplied public name should fail before bucket resolution or stdin reads.
	if len(args) == 2 {
		return validateBucketSecretName(args[1])
	}
	return nil
}

// runBucketSecretSet resolves the explicit bucket, securely collects input,
// and emits only non-sensitive mutation metadata.
func runBucketSecretSet(cmd *cobra.Command, bucketReference, keyName string) error {
	client, bucketID, err := bucketClientAndID(bucketReference)
	// Bucket resolution is authoritative and always happens before collecting secret material.
	if err != nil {
		return err
	}
	input := bucketSecretInput{KeyName: keyName, ExpiresAt: bucketSecretSetExpiresAt}
	// Prompting remains the default terminal workflow unless stdin was selected explicitly.
	if bucketSecretSetUsesInteractiveInput() {
		input, err = collectBucketSecretInput(input)
		// Terminal collection failures must stop before any mutation request.
		if err != nil {
			return err
		}
	} else {
		input.Value, err = readSensitiveValue(cmd, "bucket secret")
		// Stdin failures cannot be replaced with an empty or partially read secret.
		if err != nil {
			return err
		}
	}
	// Prompted names receive the same reference-safety validation as argv names.
	if err := validateBucketSecretName(input.KeyName); err != nil {
		return err
	}
	// Empty values are rejected locally before a credential-bearing request is sent.
	if input.Value == "" {
		return errors.New("bucket secret value cannot be empty")
	}
	expiresAt, err := parseBucketSecretExpiresAt(input.ExpiresAt)
	if err != nil {
		return err
	}
	// Engine remains authoritative for encryption, authorization, and persistence.
	if err := client.UpsertBucketSecret(bucketID, input.KeyName, input.Value, expiresAt); err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "bucket_secret")
	receipt := bucketSecretSetResult{BucketID: bucketID, KeyName: input.KeyName, ExpiresAt: expiresAt}
	// JSON output is a metadata-only receipt suitable for automation logs.
	if wantsJSON(cmd) {
		return writeJSON(cmd, receipt)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Bucket secret %q set in %q.\n", input.KeyName, bucketReference)
	return nil
}

// bucketSecretSetUsesInteractiveInput makes secure prompting the ordinary
// terminal path while preserving deterministic stdin automation.
func bucketSecretSetUsesInteractiveInput() bool {
	return !bucketSecretSetValueStdin && !nonInteractive()
}

// promptBucketSecretInput asks only for fields the caller did not already
// provide and masks the credential material on screen.
func promptBucketSecretInput(input bucketSecretInput) (bucketSecretInput, error) {
	// The public reference name can be omitted only when a terminal is available to collect it.
	if input.KeyName == "" {
		// Prompt failures cannot be interpreted as an intentionally blank public name.
		if err := huh.NewInput().Title("Secret name:").Value(&input.KeyName).Run(); err != nil {
			return bucketSecretInput{}, fmt.Errorf("reading bucket secret name: %w", err)
		}
	}
	// Secret input is always masked because terminal scrollback is not a safe credential channel.
	if err := huh.NewInput().Title("Secret value:").EchoMode(huh.EchoModePassword).Value(&input.Value).Run(); err != nil {
		return bucketSecretInput{}, fmt.Errorf("reading bucket secret value: %w", err)
	}
	// An explicit expiry flag remains authoritative; otherwise the prompt permits a blank no-expiry choice.
	if input.ExpiresAt == "" {
		// Prompt failures must not silently convert an intended expiry into no expiry.
		if err := huh.NewInput().Title("Expiry (optional RFC3339):").Value(&input.ExpiresAt).Run(); err != nil {
			return bucketSecretInput{}, fmt.Errorf("reading bucket secret expiry: %w", err)
		}
	}
	return input, nil
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

// init registers generic secret management beneath the bucket namespace so it
// remains distinct from provider-specific secret commands.
func init() {
	bucketCmd.AddCommand(bucketSecretCmd)
	bucketSecretCmd.AddCommand(bucketSecretSetCmd)
	bucketSecretSetCmd.Flags().BoolVarP(&bucketSecretSetInteractive, "interactive", "i", false, "Require masked prompts (the terminal default)")
	bucketSecretSetCmd.Flags().BoolVar(&bucketSecretSetValueStdin, "value-stdin", false, "Read the secret value from stdin")
	bucketSecretSetCmd.Flags().StringVar(&bucketSecretSetExpiresAt, "expires-at", "", "RFC3339 expiry timestamp; omit for no expiry")
	addJSONOutputFlag(bucketSecretSetCmd)
}
