package configfile

import (
	"github.com/google/uuid"
	"strings"
	"testing"
)

// TestBridgeOnlyConfig retains exact pins in a provider-free Unified App and rejects mutable selectors.
func TestBridgeOnlyConfig(t *testing.T) {
	ref := BridgeReference{PairingID: uuid.NewString(), ProfileID: "local", ManifestHash: "sha256:" + strings.Repeat("a", 64), ConnectionID: "mqtt", OperationID: "publish"}
	cfg := &AppConfig{Name: "Local automation", Version: "1.0.0", Bucket: "default", Source: "export default {};", Bridges: map[string]BridgeReference{"publish": ref}}
	// An immutable local reference is sufficient without a placeholder provider.
	if err := validateAppConfig(cfg, KindUnifiedApp); err != nil {
		t.Fatal(err)
	}
	ref.ManifestHash = "latest"
	cfg.Bridges["publish"] = ref
	// A mutable selector cannot identify the contract reviewed at plan time.
	if validateAppConfig(cfg, KindUnifiedApp) == nil {
		t.Fatal("mutable Bridge selector accepted")
	}
}

// TestBridgeAliasCompanionCollision rejects ambiguous generated Python method names during local validation.
func TestBridgeAliasCompanionCollision(t *testing.T) {
	ref := BridgeReference{PairingID: uuid.NewString(), ProfileID: "local", ManifestHash: "sha256:" + strings.Repeat("a", 64), ConnectionID: "mqtt", OperationID: "publish"}
	cfg := &AppConfig{Bridges: map[string]BridgeReference{"publish": ref, "publish_sync": ref}}
	// Both valid aliases together would overwrite a generated sync companion.
	if validateBridgeReferences(cfg) == nil {
		t.Fatal("sync companion collision accepted")
	}
}
