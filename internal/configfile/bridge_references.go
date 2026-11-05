package configfile

import (
	"fmt"
	"github.com/google/uuid"
	"regexp"
	"strings"
	"unicode"
)

// BridgeReference pins a credential-free local operation to its accepted manifest.
type BridgeConnectionSelection struct {
	Operations any `json:"operations" yaml:"operations"`
}

type BridgeReference struct {
	Bridge       string                               `json:"bridge,omitempty" yaml:"bridge,omitempty"`
	Profile      string                               `json:"profile,omitempty" yaml:"profile,omitempty"`
	Connections  map[string]BridgeConnectionSelection `json:"connections,omitempty" yaml:"connections,omitempty"`
	PairingID    string                               `json:"pairing_id,omitempty" yaml:"pairing_id,omitempty"`
	ProfileID    string                               `json:"profile_id,omitempty" yaml:"profile_id,omitempty"`
	ManifestHash string                               `json:"manifest_hash,omitempty" yaml:"manifest_hash,omitempty"`
	ConnectionID string                               `json:"connection_id,omitempty" yaml:"connection_id,omitempty"`
	OperationID  string                               `json:"operation_id,omitempty" yaml:"operation_id,omitempty"`
}

var bridgeHash = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var bridgeIdentifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,127}$`)

// validateBridgeReferences rejects mutable or incomplete device selections before plan submission.
func validateBridgeReferences(cfg *AppConfig) error {
	// A bounded selection keeps generated surfaces and Engine admission consistent.
	if len(cfg.Bridges) > 100 {
		return fmt.Errorf("at most 100 Bridge capabilities may be attached")
	}
	for alias, ref := range cfg.Bridges {
		// Reject generated sync companion collisions before submitting a plan.
		if _, exists := cfg.Bridges[alias+"_sync"]; exists {
			return fmt.Errorf("conflicting Bridge aliases")
		}
		// Reuse portable generated-method naming restrictions across local and hosted capabilities.
		if err := validateUnifiedAppReference(alias, UnifiedAppReference{Name: "Bridge", Version: "1.0.0"}, nil); err != nil {
			return err
		}
		if ref.Bridge != "" || ref.Profile != "" || ref.Connections != nil {
			if err := validateBridgeGroup(ref); err != nil {
				return fmt.Errorf("bridge %s: %w", alias, err)
			}
			continue
		}
		_, err := uuid.Parse(ref.PairingID)
		// Catalogue hashes must be copied verbatim; profile names alone are not immutable authority.
		if err != nil || !bridgeHash.MatchString(ref.ManifestHash) || !bridgeIdentifier.MatchString(ref.ProfileID) || !bridgeIdentifier.MatchString(ref.ConnectionID) || !bridgeIdentifier.MatchString(ref.OperationID) {
			return fmt.Errorf("Bridge requires pairing_id, profile_id, exact manifest_hash, connection_id and operation_id")
		}
	}
	return nil
}

func validateBridgeGroup(ref BridgeReference) error {
	if strings.TrimSpace(ref.Bridge) == "" || len(ref.Bridge) > 128 || strings.ContainsFunc(ref.Bridge, unicode.IsControl) || !bridgeIdentifier.MatchString(ref.Profile) || len(ref.Connections) == 0 || len(ref.Connections) > 100 {
		return fmt.Errorf("group requires bridge name, profile and 1 to 100 connections")
	}
	if ref.PairingID != "" || ref.ProfileID != "" || ref.ManifestHash != "" || ref.ConnectionID != "" || ref.OperationID != "" {
		return fmt.Errorf("group cannot mix readable selectors with explicit pins")
	}
	for name, conn := range ref.Connections {
		if !bridgeIdentifier.MatchString(name) {
			return fmt.Errorf("invalid connection name %q", name)
		}
		if all, ok := conn.Operations.(string); ok {
			if all != "*" {
				return fmt.Errorf("connection %s operations must be a nonempty list or \"*\"", name)
			}
			continue
		}
		var names []string
		switch ops := conn.Operations.(type) {
		case []string:
			names = ops
		case []any:
			for _, v := range ops {
				op, ok := v.(string)
				if !ok {
					return fmt.Errorf("operation names must be strings")
				}
				names = append(names, op)
			}
		default:
			return fmt.Errorf("connection %s operations must be a nonempty list or \"*\"", name)
		}
		if len(names) == 0 || len(names) > 100 {
			return fmt.Errorf("connection requires 1 to 100 operations")
		}
		seen := map[string]bool{}
		for _, op := range names {
			if !bridgeIdentifier.MatchString(op) || seen[op] {
				return fmt.Errorf("invalid or duplicate operation %q", op)
			}
			seen[op] = true
		}
	}
	return nil
}
