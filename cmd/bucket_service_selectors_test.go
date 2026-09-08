package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	cliapi "github.com/Usefused/cli/internal/api"
)

// TestResolveConnectionServiceSelectorsSupportsMixedMultiValues verifies whole-service and exact-version selectors form one deduplicated union.
func TestResolveConnectionServiceSelectorsSupportsMixedMultiValues(t *testing.T) {
	const githubID = "11111111-1111-4111-8111-111111111111"
	const slackID = "22222222-2222-4222-8222-222222222222"
	const githubV1ID = "33333333-3333-4333-8333-333333333333"
	const githubV2ID = "44444444-4444-4444-8444-444444444444"
	infoCalls := 0
	versionCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode Registry request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		// Service identity and version identity are resolved through their bounded Registry queries.
		switch {
		case strings.Contains(body.Query, "query GetServiceInfo"):
			infoCalls++
			serviceID := githubID
			// The fixture gives the unversioned selector a distinct immutable service identity.
			if body.Variables["id"] == "slack" {
				serviceID = slackID
			}
			_, _ = w.Write([]byte(`{"data":{"service":{"id":"` + serviceID + `"}}}`))
		case strings.Contains(body.Query, "query ServiceVersions"):
			versionCalls++
			_, _ = w.Write([]byte(`{"data":{"serviceVersions":[{"id":"` + githubV1ID + `","service_id":"` + githubID + `","name":"v1"},{"id":"` + githubV2ID + `","service_id":"` + githubID + `","name":"v2"}]}}`))
		default:
			t.Fatalf("unexpected Registry query: %s", body.Query)
		}
	}))
	defer server.Close()

	client := cliapi.NewClient(server.URL, "fsk_test")
	serviceIDs, versionIDs, err := resolveConnectionServiceSelectors(client, []string{"github@v1", "slack", "github@v2", "github@v1"})
	if err != nil {
		t.Fatalf("resolveConnectionServiceSelectors: %v", err)
	}
	if !reflect.DeepEqual(serviceIDs, []string{slackID}) || !reflect.DeepEqual(versionIDs, []string{githubV1ID, githubV2ID}) {
		t.Fatalf("selectors resolved to services=%v versions=%v", serviceIDs, versionIDs)
	}
	// Repeated selectors should reuse both service and version lookups.
	if infoCalls != 2 || versionCalls != 1 {
		t.Fatalf("unexpected Registry calls: service info=%d versions=%d", infoCalls, versionCalls)
	}
}

// TestResolveConnectionServiceSelectorsLetsWholeServiceSubsumeVersions protects union minimization for mixed forms of one service.
func TestResolveConnectionServiceSelectorsLetsWholeServiceSubsumeVersions(t *testing.T) {
	const serviceID = "11111111-1111-4111-8111-111111111111"
	const versionID = "33333333-3333-4333-8333-333333333333"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode Registry request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		// Return the minimum identity shape needed by each Registry resolver.
		if strings.Contains(body.Query, "query ServiceVersions") {
			_, _ = w.Write([]byte(`{"data":{"serviceVersions":[{"id":"` + versionID + `","service_id":"` + serviceID + `","name":"v1"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"service":{"id":"` + serviceID + `"}}}`))
	}))
	defer server.Close()

	serviceIDs, versionIDs, err := resolveConnectionServiceSelectors(cliapi.NewClient(server.URL, "fsk_test"), []string{"github@v1", "github"})
	if err != nil {
		t.Fatalf("resolveConnectionServiceSelectors: %v", err)
	}
	if !reflect.DeepEqual(serviceIDs, []string{serviceID}) || len(versionIDs) != 0 {
		t.Fatalf("whole service did not subsume exact version: services=%v versions=%v", serviceIDs, versionIDs)
	}
}
