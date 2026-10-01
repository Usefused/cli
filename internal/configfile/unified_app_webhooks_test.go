package configfile

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestUnifiedAppWebhookConfigPreservesTriggerScope verifies CLI plan transport retains existing attachment and event fields.
func TestUnifiedAppWebhookConfigPreservesTriggerScope(t *testing.T) {
	const document = `apiVersion: fused/v1
kind: unified_app
name: issue-handler
version: 1.0.0
bucket: default
source: 'export default buildUnifiedApp({})'
webhook_attachment: team-events
services:
  jira:
    version: v1
    operations: [getIssue]
    webhooks: [issue.created]
`
	parsed, err := Parse([]byte(document), "issue-handler.yaml")
	// Native triggers use ordinary Unified App config validation, never a separate deployment kind.
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(parsed.UnifiedApp)
	// Serialization is the actual CLI-to-Engine contract and must carry both attachment and allowlist.
	if err != nil || !strings.Contains(string(raw), `"webhook_attachment":"team-events"`) || !strings.Contains(string(raw), `"webhooks":["issue.created"]`) {
		t.Fatalf("config=%s error=%v", raw, err)
	}
	_, err = Parse([]byte(strings.Replace(document, "webhook_attachment: team-events\n", "", 1)), "issue-handler.yaml")
	// Event selections without a registration must fail before any Engine mutation.
	if err == nil || !strings.Contains(err.Error(), "webhook_attachment") {
		t.Fatalf("unattached event accepted: %v", err)
	}
	changed, err := Parse([]byte(strings.Replace(document, "issue.created", "issue.updated", 1)), "issue-handler.yaml")
	// Changing trigger scope must change desired-state identity and require a new immutable version.
	if err != nil || changed.SourceHash == parsed.SourceHash {
		t.Fatalf("trigger change lost source identity: %v", err)
	}
}
