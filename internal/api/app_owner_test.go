package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAppPlansSendOwnerTeamOnlyAtPlanTime(t *testing.T) {
	const ownerTeamSlug = "platform"
	requests := make([]map[string]any, 0, 6)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s: %v", r.URL.Path, err)
		}
		requests = append(requests, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(appPlanTestResponse(t, r.URL.Path)))
	}))
	defer server.Close()

	client := NewClient(server.URL, "fsk_test")
	intent := DesiredConfigPlanIntent{SourceHash: "hash", ConfigKey: "sdk:test:1", OwnerTeamSlug: ownerTeamSlug, Config: json.RawMessage(`{"kind":"sdk"}`)}
	_, err := client.PlanSDKConfig(intent)
	requireAppTestNoError(t, err)
	_, err = client.PlanMCPConfig(intent)
	requireAppTestNoError(t, err)
	_, err = client.PlanWebhookConfig(intent)
	requireAppTestNoError(t, err)
	_, err = client.ApplySDKConfig("plan-1", "hash")
	requireAppTestNoError(t, err)
	_, err = client.ApplyMCPConfig("plan-1", "hash")
	requireAppTestNoError(t, err)
	_, err = client.ApplyWebhookConfig("plan-1", "hash")
	requireAppTestNoError(t, err)

	for index, request := range requests {
		_, hasOwner := request["owner_team"]
		if index < 3 && (!hasOwner || request["owner_team"] != ownerTeamSlug) {
			t.Fatalf("plan request %d owner = %#v", index, request["owner_team"])
		}
		if index >= 3 && hasOwner {
			t.Fatalf("apply request %d forged owner override: %#v", index, request)
		}
	}
}

func appPlanTestResponse(t *testing.T, path string) string {
	t.Helper()
	responses := map[string]string{
		"/sdk-config/plan":      `{"plan_id":"plan-1","config_key":"sdk:test:1","source_hash":"hash","summary":{}}`,
		"/mcp-config/plan":      `{"plan_id":"plan-1","config_key":"sdk:test:1","source_hash":"hash","summary":{}}`,
		"/webhook-config/plan":  `{"plan_id":"plan-1","config_key":"sdk:test:1","source_hash":"hash","summary":{}}`,
		"/sdk-config/apply":     `{"status":"applied","plan_id":"plan-1","app_family_id":"family-1","app_id":"app-1","job_id":"job-1"}`,
		"/mcp-config/apply":     `{"status":"applied","plan_id":"plan-1","app_family_id":"family-1","app_id":"app-1"}`,
		"/webhook-config/apply": `{"status":"applied","plan_id":"plan-1","config_key":"webhook:test","name":"test","registrations":[]}`,
	}
	response, ok := responses[path]
	if !ok {
		t.Fatalf("unexpected path %s", path)
	}
	return response
}

func requireAppTestNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestAppUpdatePlanCanOmitOwnerForEngineInference(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"plan_id":"plan-1","summary":{}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "fsk_test")
	_, err := client.PlanSDKConfig(DesiredConfigPlanIntent{SourceHash: "hash", ConfigKey: "sdk:existing:1", Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := request["owner_team"]; exists {
		t.Fatalf("omitted update owner was sent: %#v", request)
	}
}
