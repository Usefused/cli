package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestArtifactPlansSendOwnerTeamOnlyAtPlanTime(t *testing.T) {
	const ownerTeamID = "11111111-1111-1111-1111-111111111111"
	requests := make([]map[string]any, 0, 6)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s: %v", r.URL.Path, err)
		}
		requests = append(requests, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(artifactPlanTestResponse(t, r.URL.Path)))
	}))
	defer server.Close()

	client := NewClient(server.URL, "fsk_test")
	intent := ArtifactPlanIntent{SourceHash: "hash", ConfigKey: "artifact:test:1", OwnerTeamID: ownerTeamID, Config: json.RawMessage(`{"kind":"sdk"}`)}
	_, err := client.PlanSDKConfig(intent)
	requireArtifactTestNoError(t, err)
	_, err = client.PlanMCPConfig(intent)
	requireArtifactTestNoError(t, err)
	_, err = client.PlanWebhookConfig(intent)
	requireArtifactTestNoError(t, err)
	_, err = client.ApplySDKConfig("plan-1", "hash")
	requireArtifactTestNoError(t, err)
	_, err = client.ApplyMCPConfig("plan-1", "hash")
	requireArtifactTestNoError(t, err)
	_, err = client.ApplyWebhookConfig("plan-1", "hash")
	requireArtifactTestNoError(t, err)

	for index, request := range requests {
		_, hasOwner := request["owner_team_id"]
		if index < 3 && (!hasOwner || request["owner_team_id"] != ownerTeamID) {
			t.Fatalf("plan request %d owner = %#v", index, request["owner_team_id"])
		}
		if index >= 3 && hasOwner {
			t.Fatalf("apply request %d forged owner override: %#v", index, request)
		}
	}
}

func artifactPlanTestResponse(t *testing.T, path string) string {
	t.Helper()
	responses := map[string]string{
		"/sdk-config/plan":      `{"plan_id":"plan-1","config_key":"artifact:test:1","source_hash":"hash","summary":{}}`,
		"/mcp-config/plan":      `{"plan_id":"plan-1","config_key":"artifact:test:1","source_hash":"hash","summary":{}}`,
		"/webhook-config/plan":  `{"plan_id":"plan-1","config_key":"artifact:test:1","source_hash":"hash","summary":{}}`,
		"/sdk-config/apply":     `{"status":"applied","plan_id":"plan-1","artifact_id":"artifact-1","job_id":"job-1"}`,
		"/mcp-config/apply":     `{"status":"applied","plan_id":"plan-1","artifact_id":"artifact-1"}`,
		"/webhook-config/apply": `{"status":"applied","plan_id":"plan-1","config_key":"webhook:test","name":"test","registrations":[]}`,
	}
	response, ok := responses[path]
	if !ok {
		t.Fatalf("unexpected path %s", path)
	}
	return response
}

func requireArtifactTestNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestArtifactUpdatePlanCanOmitOwnerForEngineInference(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"plan_id":"plan-1","summary":{}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "fsk_test")
	_, err := client.PlanSDKConfig(ArtifactPlanIntent{SourceHash: "hash", ConfigKey: "sdk:existing:1", Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := request["owner_team_id"]; exists {
		t.Fatalf("omitted update owner was sent: %#v", request)
	}
}
