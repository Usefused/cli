package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

// TestPlanOneConfig_Webhook exercises the kind: webhook branch added to
// planOneConfig (config_runner.go) -- it must hit /webhook-config/plan (the
// same shared route pattern SDK/MCP use) and never surface a notifications
// section, since kind: webhook never touches another artifact's state.
func TestPlanOneConfig_Webhook(t *testing.T) {
	var gotPath string
	var gotBody struct {
		SourceHash string          `json:"source_hash"`
		ConfigKey  string          `json:"config_key"`
		Config     json.RawMessage `json:"config"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode plan request: %v", err)
		}
		_, _ = w.Write([]byte(`{"plan_id":"plan-1","config_key":"webhook:team-x","source_hash":"abc","summary":{}}`))
	}))
	defer server.Close()

	client := &api.Client{BaseURL: server.URL, HTTP: server.Client()}
	cfg := &configfile.ParsedConfig{
		Kind: configfile.KindWebhook, ConfigKey: "webhook:team-x", SourceHash: "abc",
		Webhook: &configfile.WebhookArtifactConfig{
			Name:     "team-x",
			Services: map[string]configfile.WebhookArtifactService{"github": {Secret: "${bucket.default.secret.github_signing}"}},
		},
	}

	result, err := planOneConfig(client, cfg, "https://engine.example.com", "")
	if err != nil {
		t.Fatalf("planOneConfig: %v", err)
	}
	if gotPath != "/webhook-config/plan" {
		t.Fatalf("expected plan request to hit /webhook-config/plan, got %q", gotPath)
	}
	if gotBody.ConfigKey != "webhook:team-x" {
		t.Fatalf("expected config_key webhook:team-x in request body, got %q", gotBody.ConfigKey)
	}
	if result.receipt.PlanID != "plan-1" {
		t.Fatalf("expected plan id plan-1 in receipt, got %q", result.receipt.PlanID)
	}
	if len(result.notifications.Items) != 0 || len(result.notifications.Warnings) != 0 {
		t.Fatalf("expected no notifications for a webhook plan, got %#v", result.notifications)
	}
}

// TestApplyOneConfig_Webhook exercises the kind: webhook branch added to
// applyOneConfig -- it must hit /webhook-config/apply and print each
// registration's reconstructed URL, mirroring workspace apply's webhook
// printout but sourced from kind: webhook's own response shape.
func TestApplyOneConfig_Webhook(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"status":"applied","plan_id":"plan-1","config_key":"webhook:team-x","name":"team-x","registrations":[{"service":"github","slug":"slugaaaaaaaaaaaaaaaaa"}]}`))
	}))
	defer server.Close()

	client := &api.Client{BaseURL: server.URL, HTTP: server.Client()}
	cfg := &configfile.ParsedConfig{
		Kind: configfile.KindWebhook, ConfigKey: "webhook:team-x", SourceHash: "abc",
		Webhook: &configfile.WebhookArtifactConfig{Name: "team-x"},
	}

	out := captureStdout(t, func() {
		item := preparedConfigApply{
			config:  cfg,
			receipt: planReceipt{ConfigKey: "webhook:team-x", PlanID: "plan-1", SourceHash: "abc", EngineURL: server.URL},
		}
		if err := applyPreparedConfig(client, item, false); err != nil {
			t.Fatalf("applyPreparedConfig: %v", err)
		}
	})

	if gotPath != "/webhook-config/apply" {
		t.Fatalf("expected apply request to hit /webhook-config/apply, got %q", gotPath)
	}
	if !strings.Contains(out, "Successfully applied webhook team-x") {
		t.Fatalf("expected success line naming the artifact, got:\n%s", out)
	}
	if !strings.Contains(out, `service "github" -> `+server.URL+"/webhook/slugaaaaaaaaaaaaaaaaa-github") {
		t.Fatalf("expected reconstructed registration URL, got:\n%s", out)
	}
}
