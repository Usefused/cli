package configfile

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestUnifiedAppWebhookConfigPreservesTriggerScope verifies CLI plan transport retains existing attachment and event fields.
func TestUnifiedAppWebhookConfigPreservesTriggerScope(t *testing.T) {
	const document = `export const fusedConfig = {"name":"issue-handler","version":"1.0.0","bucket":"default","webhook_attachment":"team-events","services":{"jira":{"version":"v1","operations":["getIssue"],"webhooks":["issue.created"]}}};
export default buildUnifiedApp({});`
	parsed, err := ParseUnifiedAppSource([]byte(document), "issue-handler.ts")
	// Native triggers use ordinary Unified App config validation, never a separate deployment kind.
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(parsed.UnifiedApp)
	// Serialization is the actual CLI-to-Engine contract and must carry both attachment and allowlist.
	if err != nil || !strings.Contains(string(raw), `"webhook_attachment":"team-events"`) || !strings.Contains(string(raw), `"webhooks":["issue.created"]`) {
		t.Fatalf("config=%s error=%v", raw, err)
	}
	_, err = ParseUnifiedAppSource([]byte(strings.Replace(document, `"webhook_attachment":"team-events",`, "", 1)), "issue-handler.ts")
	// Event selections without a registration must fail before any Engine mutation.
	if err == nil || !strings.Contains(err.Error(), "webhook_attachment") {
		t.Fatalf("unattached event accepted: %v", err)
	}
	changed, err := ParseUnifiedAppSource([]byte(strings.Replace(document, "issue.created", "issue.updated", 1)), "issue-handler.ts")
	// Changing trigger scope must change desired-state identity and require a new immutable version.
	if err != nil || changed.SourceHash == parsed.SourceHash {
		t.Fatalf("trigger change lost source identity: %v", err)
	}
}

// TestUnifiedAppWebhookOnlyConfig admits a hosted event handler with no unnecessary provider operation grant.
func TestUnifiedAppWebhookOnlyConfig(t *testing.T) {
	const document = `export const fusedConfig = {"name":"issue-handler","version":"1.0.0","bucket":"default","webhook_attachment":"team-events","services":{"jira":{"version":"v1","webhooks":["issue.created"]}}};
export default buildUnifiedApp({});`
	parsed, err := ParseUnifiedAppSource([]byte(document), "issue-handler.ts")
	// Event-only source still has a finite inbound capability and must survive local validation.
	if err != nil || parsed.UnifiedApp == nil || len(parsed.UnifiedApp.Services["jira"].Operations) != 0 {
		t.Fatalf("event-only config = %#v, %v", parsed, err)
	}
}
