package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/google/uuid"
)

// remediateSDKPlanReadiness optionally applies the successful plan's typed
// warning through the same secure secret mutation path used by secret set.
func remediateSDKPlanReadiness(client *api.Client, cfg *configfile.ParsedConfig, readiness *api.CredentialReadiness, opts planOptions) error {
	// A successful plan must still supply a complete typed target before prompting can mutate credentials.
	if readiness == nil || readiness.Bucket == nil || len(readiness.MissingCredentials) == 0 {
		return errors.New("Engine returned incomplete credential readiness metadata")
	}
	bucket, err := validateSDKPlanCredentialTarget(cfg, readiness.Bucket)
	if err != nil {
		return err
	}
	requirements, err := validateMissingCredentialRequirements(readiness.MissingCredentials)
	if err != nil {
		return err
	}
	for _, requirement := range requirements {
		if err := applySDKPlanCredentialRequirement(client, bucket, requirement, opts); err != nil {
			return err
		}
	}
	return nil
}

// validateSDKPlanCredentialTarget proves readiness authorizes writes only to the YAML-selected Engine bucket.
func validateSDKPlanCredentialTarget(cfg *configfile.ParsedConfig, bucket *api.MissingCredentialBucket) (*api.MissingCredentialBucket, error) {
	if _, err := uuid.Parse(strings.TrimSpace(bucket.ID)); err != nil {
		return nil, errors.New("Engine returned an invalid credential bucket ID")
	}
	resolvedName := strings.TrimSpace(bucket.Name)
	if resolvedName == "" {
		return nil, errors.New("Engine returned an unnamed credential bucket")
	}
	// An explicit YAML bucket must match the Engine's authoritative resolution.
	// When omitted, the typed Engine target is the existing default-bucket result.
	yamlBucket := strings.TrimSpace(cfg.SDK.Bucket)
	if yamlBucket != "" && resolvedName != yamlBucket {
		return nil, fmt.Errorf("Engine resolved bucket %q but SDK YAML selects %q; no credentials were changed", bucket.Name, yamlBucket)
	}
	return bucket, nil
}

func validateMissingCredentialRequirements(requirements []api.MissingCredentialRequirement) ([]api.MissingCredentialRequirement, error) {
	seen := make(map[string]bool, len(requirements))
	unique := make([]api.MissingCredentialRequirement, 0, len(requirements))
	for _, requirement := range requirements {
		requirement.AuthType = canonicalSecretTypeName(requirement.AuthType)
		if err := validateMissingCredentialRequirement(requirement); err != nil {
			return nil, err
		}
		key := requirement.ServiceID + "\x00" + requirement.AuthType + "\x00" + requirement.AuthName
		if seen[key] {
			return nil, errors.New("Engine returned duplicate credential remediation requirements")
		}
		seen[key] = true
		unique = append(unique, requirement)
	}
	return unique, nil
}

func validateMissingCredentialRequirement(requirement api.MissingCredentialRequirement) error {
	if _, err := uuid.Parse(strings.TrimSpace(requirement.ServiceID)); err != nil {
		return errors.New("Engine returned an invalid service ID for credential remediation")
	}
	if !supportedSDKPlanAuthType(requirement.AuthType) || len(requirement.RequiredFields) == 0 {
		return errors.New("Engine returned incomplete credential remediation metadata")
	}
	if requirement.AuthType == "basic" && !validBasicPasswordMode(requirement.BasicPasswordMode) {
		return errors.New("Engine returned an invalid Basic password mode")
	}
	return validateMissingCredentialFields(requirement)
}

func validBasicPasswordMode(mode api.BasicPasswordMode) bool {
	return mode == "" || mode == "required" || mode == "optional" || mode == "empty"
}

// validateMissingCredentialFields accepts only Engine fields that map to the
// deterministic bucket keys used by the shared secret mutation path.
func validateMissingCredentialFields(requirement api.MissingCredentialRequirement) error {
	// Every supplied field must be safe before any value is collected from the operator.
	for _, field := range requirement.RequiredFields {
		// Readiness metadata is untrusted input and must never choose an arbitrary secret key.
		if invalidRemediationSecretKey(field.SecretKey) {
			return fmt.Errorf("Engine returned invalid %s credential field %q", requirement.AuthType, field.Name)
		}
	}
	expected := expectedSecretFields(missingCredentialAuth(requirement))
	// Semantic names and storage keys must agree with the shared mutation contract.
	for _, field := range requirement.RequiredFields {
		name := canonicalSecretTypeName(field.Name)
		// Rejecting unknown or mismatched keys prevents Engine metadata from creating ad-hoc secrets.
		if expected[name] == "" || field.SecretKey != expected[name] {
			return fmt.Errorf("Engine returned invalid %s credential field %q", requirement.AuthType, field.Name)
		}
	}
	return nil
}

func invalidRemediationSecretKey(key string) bool {
	key = strings.TrimSpace(key)
	return key == "" || len(key) > 256 || strings.ContainsAny(key, "\r\n\x00") || strings.HasPrefix(key, "<")
}

// expectedSecretFields mirrors Engine readiness naming so interactive planning
// cannot write credentials under keys that runtime resolution will not read.
func expectedSecretFields(auth *api.AuthConfig) map[string]string {
	name := secretAuthCredentialName(auth)
	// Each supported family maps provider semantics onto its deterministic bucket keys.
	switch canonicalSecretAuthType(auth) {
	case "basic":
		return map[string]string{"username": name + "_username", "password": name + "_password"}
	case "mtls":
		return map[string]string{"certificate": name + "_cert", "private_key": name + "_key"}
	case "bearer":
		return map[string]string{"token": name}
	case "api_key":
		return map[string]string{"api_key": name}
	case "oauth", "oidc":
		// OAuth and OIDC registrations are stored atomically under one named application family.
		return map[string]string{"client_id": name + "_client_id", "client_secret": name + "_client_secret"}
	default:
		return nil
	}
}

func missingCredentialAuth(requirement api.MissingCredentialRequirement) *api.AuthConfig {
	name := strings.TrimSpace(requirement.AuthName)
	if name == "" {
		name = credentialNameFromRequiredFields(requirement)
	}
	return &api.AuthConfig{
		Name: name, Type: requirement.AuthType,
		BasicPasswordMode: requirement.BasicPasswordMode,
	}
}

func credentialNameFromRequiredFields(requirement api.MissingCredentialRequirement) string {
	if len(requirement.RequiredFields) == 0 {
		return ""
	}
	key := requirement.RequiredFields[0].SecretKey
	suffixes := map[string][]string{
		"basic": {"_username", "_password"},
		"mtls":  {"_cert", "_key"},
	}
	for _, suffix := range suffixes[requirement.AuthType] {
		if strings.HasSuffix(key, suffix) {
			return strings.TrimSuffix(key, suffix)
		}
	}
	return key
}

func supportedSDKPlanAuthType(authType string) bool {
	switch authType {
	case "bearer", "api_key", "basic", "mtls", "oauth", "oidc":
		return true
	default:
		return false
	}
}

func isConnectAuthType(authType string) bool {
	return authType == "oauth" || authType == "oidc"
}

func applySDKPlanCredentialRequirement(client *api.Client, bucket *api.MissingCredentialBucket, requirement api.MissingCredentialRequirement, opts planOptions) error {
	mutation := credentialMutationOptions{
		confirmation: credentialConfirmationMessage(requirement, bucket),
		auditCtx:     opts.auditCtx, auditAction: opts.auditAction,
	}
	display := credentialServiceDisplay(requirement)
	if isConnectAuthType(requirement.AuthType) {
		mutation.resourceKind = "secret"
		return setSecretForAuth(client, requirement.ServiceID, bucket.ID, missingCredentialAuth(requirement), "", nil, display, opts.output, mutation)
	}
	mutation.resourceKind = "secret"
	return setSecretForAuth(client, requirement.ServiceID, bucket.ID, missingCredentialAuth(requirement), "", nil, display, opts.output, mutation)
}

func credentialConfirmationMessage(requirement api.MissingCredentialRequirement, bucket *api.MissingCredentialBucket) string {
	return fmt.Sprintf("Store %s credentials for %s in bucket %q?", requirement.AuthType, credentialServiceDisplay(requirement), bucket.Name)
}

func credentialServiceDisplay(requirement api.MissingCredentialRequirement) string {
	if service := strings.TrimSpace(requirement.Service); service != "" {
		return service
	}
	return requirement.ServiceID
}
