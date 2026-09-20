package configfile

import (
	"encoding/json"
	"testing"
)

// TestWebhookRelaySourceSurvivesYAMLAndJSON proves CLI transport retains the exact connection source without importing secrets.
func TestWebhookRelaySourceSurvivesYAMLAndJSON(t *testing.T) {
	parsed, err := Parse([]byte(`apiVersion: fused/v1
kind: webhook
name: managed-events
services:
  slack:
    relay:
      source:
        bucket: connections
        connection_id: 11111111-1111-4111-8111-111111111111
        registration_id: 22222222-2222-4222-8222-222222222222
`), "webhook.yaml")
	// Invalid YAML must fail before testing the serialized API representation.
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(parsed.Webhook)
	// Transport serialization must succeed independently of YAML parsing.
	if err != nil {
		t.Fatal(err)
	}
	var decoded WebhookConfig
	// The Engine-facing JSON must preserve the same typed source contract.
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	service := decoded.Services["slack"]
	// Missing relay fields must produce a clear assertion rather than a nil dereference.
	if service.Relay == nil || service.Relay.Source == nil {
		t.Fatal("relay source missing after transport round trip")
	}
	source := service.Relay.Source
	// All source identities must survive transport without supplying a local signing secret.
	if source.Bucket != "connections" || source.ConnectionID != "11111111-1111-4111-8111-111111111111" || source.RegistrationID != "22222222-2222-4222-8222-222222222222" || service.Secret != "" {
		t.Fatal("relay source or secret changed during transport round trip")
	}
}
