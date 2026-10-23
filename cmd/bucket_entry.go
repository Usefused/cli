package cmd

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/charmbracelet/huh"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

type bucketEntryOptions struct {
	bucket, expires string
	exportable      bool
}

type bucketEntry struct {
	name, value string
	supplied    bool
}

var collectBucketEntryValue = promptBucketEntryValue

// addBucketEntryCommands gives named secrets, environment values, and certificates one consistent grammar.
func addBucketEntryCommands(parent *cobra.Command, kind string) {
	label := map[string]string{"secret": "secret", "env": "environment value", "cert": "PEM certificate"}[kind]
	for _, action := range []string{"set", "get", "remove"} {
		options := &bucketEntryOptions{}
		use := action + " <" + kind + "-name>"
		// Only set accepts assignments and multiple items; reads and removals require exact names.
		if action == "set" {
			use = "set <" + kind + "-name>[=<value>]..."
		}
		command := &cobra.Command{Use: use, Short: strings.ToUpper(action[:1]) + action[1:] + " a bucket " + label}
		// Validate all input before bucket resolution or collecting any secret material.
		command.Args = func(cmd *cobra.Command, args []string) error {
			return validateBucketEntryArgs(cmd, action, args, *options)
		}
		// All kinds share collection and output rules while retaining their own storage namespace.
		command.RunE = WithTelemetry("cli.bucket."+kind+"."+action, func(cmd *cobra.Command, args []string) error {
			return runBucketEntry(cmd, kind, action, args, *options)
		})
		command.Flags().StringVar(&options.bucket, "bucket", "default", "Bucket name or UUID")
		// Input and storage policy flags belong only to writes.
		if action == "set" {
			// Secret expiry and reveal policy do not apply to non-secret environment values or certificates.
			if kind == "secret" {
				command.Flags().StringVar(&options.expires, "expires-at", "", "Optional RFC3339 expiry")
				command.Flags().BoolVar(&options.exportable, "exportable", false, "Allow authorized callers to retrieve this secret with get")
			}
		}
		addJSONOutputFlag(command)
		parent.AddCommand(command)
	}
}

// parseBucketEntry splits only the first equals sign so URLs, tokens, and PEM contents retain every subsequent byte.
func parseBucketEntry(argument string) (bucketEntry, error) {
	name, value, supplied := strings.Cut(argument, "=")
	// Public names use the existing bucket-reference grammar.
	if err := validateBucketSecretName(name); err != nil {
		return bucketEntry{}, err
	}
	return bucketEntry{name: name, value: value, supplied: supplied}, nil
}

// validateBucketEntryArgs rejects ambiguous input modes before any network request or partial batch mutation.
func validateBucketEntryArgs(cmd *cobra.Command, action string, args []string, options bucketEntryOptions) error {
	// Retrieval and deletion never accept values that could accidentally enter output or URLs.
	if action != "set" {
		return validateBucketEntryNameArg(action, args)
	}
	// At least one named assignment is required, even when values will be prompted.
	if len(args) == 0 {
		return fmt.Errorf("set requires at least one name[=value]")
	}
	names := make(map[string]bool)
	for _, argument := range args {
		entry, err := parseBucketEntry(argument)
		// Invalid names fail before a preceding valid assignment can be written.
		if err != nil {
			return err
		}
		// Duplicate names would conceal an unintended overwrite in the same batch.
		if names[entry.name] {
			return fmt.Errorf("duplicate entry name")
		}
		names[entry.name] = true
		// Each assignment must own exactly one input channel before the batch can proceed.
		if err := validateBucketEntryInput(cmd, entry); err != nil {
			return err
		}
	}
	_, err := parseBucketSecretExpiresAt(options.expires)
	return err
}

// validateBucketEntryNameArg requires a single public identity for reads and removals.
func validateBucketEntryNameArg(action string, args []string) error {
	// Values are meaningful only for set and must never be interpreted as a deletion target.
	if len(args) != 1 || strings.Contains(args[0], "=") {
		return fmt.Errorf("%s requires one name without a value", action)
	}
	return validateBucketSecretName(args[0])
}

// validateBucketEntryInput keeps unattended writes from waiting for terminal input.
func validateBucketEntryInput(cmd *cobra.Command, entry bucketEntry) error {
	// Omitted values require a terminal, while explicitly empty env values remain valid.
	if !entry.supplied && (nonInteractive() || wantsJSON(cmd)) {
		return fmt.Errorf("value is required; pass name=value in non-interactive mode")
	}
	return nil
}

// runBucketEntry resolves the selected bucket once and dispatches a single scoped operation.
func runBucketEntry(cmd *cobra.Command, kind, action string, args []string, options bucketEntryOptions) error {
	client, bucketID, err := bucketClientAndID(options.bucket)
	// Lookup failures must not select another bucket or create one implicitly.
	if err != nil {
		return err
	}
	switch action { // Only set collects values; get and remove operate on names alone.
	case "set":
		return setBucketEntries(cmd, client, bucketID, kind, args, options)
	case "get":
		value, err := getBucketEntry(client, bucketID, kind, args[0])
		// Retrieval errors must never print partial or synthetic values.
		if err != nil {
			return err
		}
		// JSON is explicit value output, while mutation receipts remain metadata-only.
		if wantsJSON(cmd) {
			return writeJSON(cmd, map[string]string{"name": args[0], "value": value})
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), value)
		return err
	default:
		err := removeBucketEntry(client, bucketID, kind, args[0])
		// Only confirmed deletion produces a successful receipt.
		if err != nil {
			return err
		}
		recordAppliedChange(cmd.Context(), cmd.CommandPath(), "bucket_"+kind)
		return writeBucketEntryReceipt(cmd, bucketID, kind, []string{args[0]}, "removed")
	}
}

// collectBucketEntries completes validation and prompting for the batch before the first write.
func collectBucketEntries(cmd *cobra.Command, kind string, args []string) ([]bucketEntry, error) {
	entries := make([]bucketEntry, 0, len(args))
	for _, argument := range args {
		entry, err := parseBucketEntry(argument)
		// Direct callers receive the same validation as command parsing.
		if err != nil {
			return nil, err
		}
		// Only omitted values open a prompt; explicit bytes are never reinterpreted.
		if !entry.supplied {
			entry.value, err = collectBucketEntryValue(kind, entry.name)
		}
		// A cancelled prompt or truncated input must not leave a partially written batch.
		if err != nil {
			return nil, err
		}
		// Empty environment variables are legitimate; secret and certificate material must be present.
		if kind != "env" && entry.value == "" {
			return nil, fmt.Errorf("%s value cannot be empty", kind)
		}
		// Certificates must never become an accidental private-key storage channel.
		if kind == "cert" {
			if err := validateBucketCertificate(entry.value); err != nil {
				return nil, err
			} // Validate all PEM blocks before storage.
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// setBucketEntries writes validated entries in order and reports only names, never values.
func setBucketEntries(cmd *cobra.Command, client *api.Client, bucketID, kind string, args []string, options bucketEntryOptions) error {
	entries, err := collectBucketEntries(cmd, kind, args)
	// Collecting and validating the batch must finish before any mutation.
	if err != nil {
		return err
	}
	expires, err := parseBucketSecretExpiresAt(options.expires)
	// Invalid expiry must not silently become an unlimited lifetime.
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		// Generic secrets use encrypted storage; env and certificate values use their non-secret namespaces.
		if kind == "secret" {
			err = client.UpsertBucketSecret(bucketID, entry.name, entry.value, expires, options.exportable)
		} else {
			err = client.UpsertBucketValue(bucketID, uuid.Nil.String(), bucketEntryKey(kind, entry.name), kind, entry.value)
		}
		// Never replay a failed mutation; report confirmed progress without exposing values.
		if err != nil {
			return fmt.Errorf("%d entry(s) saved before %s write failed: %w", len(names), kind, err)
		}
		recordAppliedChange(cmd.Context(), cmd.CommandPath(), "bucket_"+kind)
		names = append(names, entry.name)
	}
	return writeBucketEntryReceipt(cmd, bucketID, kind, names, "saved")
}

// bucketEntryKey keeps certificates separate from environment values sharing the same public name.
func bucketEntryKey(kind, name string) string {
	// Secret keys already use Engine's established generic-secret namespace.
	if kind == "secret" || kind == "cert" {
		return kind + ":" + name
	}
	return name
}

// removeBucketEntry deletes an exact bucket-scoped item without involving provider credential identities.
func removeBucketEntry(client *api.Client, bucketID, kind, name string) error {
	// Generic secrets live in the credential store; public values use the bucket-value API.
	if kind == "secret" {
		return client.DeleteSecret(uuid.Nil.String(), bucketEntryKey(kind, name), bucketID)
	}
	return client.DeleteBucketValue(bucketID, uuid.Nil.String(), bucketEntryKey(kind, name))
}

// getBucketEntry resolves an exact namespace and preserves Engine's opt-in secret reveal authorization.
func getBucketEntry(client *api.Client, bucketID, kind, name string) (string, error) {
	// Secret metadata is safe to inspect, but plaintext retrieval must go through the dedicated audited endpoint.
	if kind == "secret" {
		metadata, err := findNamedBucketSecret(client, bucketID, bucketEntryKey(kind, name))
		// A missing or denied metadata read must not attempt another bucket or provider secret.
		if err != nil {
			return "", err
		}
		return client.RevealBucketSecret(bucketID, metadata.ID)
	}
	return findNamedBucketValue(client, bucketID, kind, bucketEntryKey(kind, name))
}

// findNamedBucketSecret walks bounded metadata pages and never selects a same-name provider credential.
func findNamedBucketSecret(client *api.Client, bucketID, key string) (*api.SecretMetaResponse, error) {
	for offset := 0; ; {
		page, err := client.ListSecretMetaPage(bucketID, api.PageOptions{Limit: 100, Offset: offset})
		// A denial is not absence and must propagate without another lookup strategy.
		if err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			// Namespace and nil service identity jointly isolate generic secrets.
			if item.ServiceID == uuid.Nil.String() && item.KeyName == key && item.CredentialType == "bucket_secret" {
				return &item, nil
			}
		}
		offset += len(page.Items)
		// Only a complete inventory establishes that the requested name is missing.
		if offset >= page.Total {
			return nil, fmt.Errorf("bucket secret not found")
		}
		// Stop inconsistent pagination instead of looping forever or revealing the wrong row.
		if len(page.Items) == 0 {
			return nil, fmt.Errorf("secret metadata pagination made no progress")
		}
	}
}

// findNamedBucketValue preserves type and service isolation when resolving an environment value or certificate.
func findNamedBucketValue(client *api.Client, bucketID, kind, key string) (string, error) {
	for offset := 0; ; {
		page, err := client.ListBucketValuesPage(bucketID, api.PageOptions{Limit: 100, Offset: offset})
		// Values are never returned after a failed authorization or transport check.
		if err != nil {
			return "", err
		}
		for _, item := range page.Items {
			// Typed namespaces prevent returning service bindings or another kind's same-name value.
			if item.ServiceID == uuid.Nil.String() && item.KeyName == key && item.Location == kind {
				return item.Value, nil
			}
		}
		offset += len(page.Items)
		// A complete page sequence establishes absence without fallback.
		if offset >= page.Total {
			return "", fmt.Errorf("bucket %s not found", kind)
		}
		// An inconsistent page cannot be treated as a successful empty value.
		if len(page.Items) == 0 {
			return "", fmt.Errorf("bucket value pagination made no progress")
		}
	}
}

// promptBucketEntryValue masks secrets and offers multiline input for PEM certificates.
func promptBucketEntryValue(kind, name string) (string, error) {
	// Direct helper callers must respect automation settings too.
	if err := requireInteractive("pass name=value"); err != nil {
		return "", err
	}
	var value string
	// Certificate PEM is multiline public material; a text area preserves its block layout.
	if kind == "cert" {
		err := huh.NewText().Title("PEM certificate for " + name).Value(&value).Run()
		return value, err
	}
	input := huh.NewInput().Title(kind + " value for " + name).Value(&value)
	// Only secret material needs masked terminal echo.
	if kind == "secret" {
		input.EchoMode(huh.EchoModePassword)
	}
	err := input.Run()
	return value, err
}

// validateBucketCertificate accepts certificate chains while excluding keys and trailing non-PEM material.
func validateBucketCertificate(value string) error {
	remaining := strings.TrimSpace(value)
	// Empty PEM is not a certificate.
	if remaining == "" {
		return fmt.Errorf("a PEM certificate is required")
	}
	for remaining != "" {
		// Decode must start at the boundary rather than skipping pasted private material or prose.
		if !strings.HasPrefix(remaining, "-----BEGIN CERTIFICATE-----") {
			return fmt.Errorf("cert accepts PEM certificates only")
		}
		block, rest := pem.Decode([]byte(remaining))
		// All blocks must contain a parseable X.509 certificate.
		if block == nil || block.Type != "CERTIFICATE" {
			return fmt.Errorf("invalid PEM certificate")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return fmt.Errorf("invalid X.509 certificate")
		} // Never echo malformed PEM contents.
		remaining = strings.TrimSpace(string(rest))
	}
	return nil
}

// writeBucketEntryReceipt keeps mutation output useful without retaining any supplied values.
func writeBucketEntryReceipt(cmd *cobra.Command, bucketID, kind string, names []string, status string) error {
	// JSON receipts contain only public identities and confirmed mutation state.
	if wantsJSON(cmd) {
		return writeJSON(cmd, map[string]any{"bucket_id": bucketID, "kind": kind, "names": names, "status": status})
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %d %s entry(s) in bucket %s.\n", status, len(names), kind, bucketID)
	return err
}

// init exposes environment and certificate storage alongside generic secrets.
func init() {
	for _, kind := range []string{"env", "cert"} {
		group := commandGroup(kind, "Manage named bucket "+kind+" entries")
		addBucketEntryCommands(group, kind)
		bucketCmd.AddCommand(group)
	}
}
