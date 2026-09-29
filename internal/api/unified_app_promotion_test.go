package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestUnifiedAppPromotionContract verifies exact endpoints, authentication, and the concurrency precondition.
func TestUnifiedAppPromotionContract(t *testing.T) {
	const appID = "11111111-1111-4111-8111-111111111111"
	const previous = "22222222-2222-4222-8222-222222222222"
	calls := 0
	// The fixture rejects clients that bypass discovery or discard its observed target.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// Management identity must travel only in the normal API header.
		if r.Header.Get("x-api-key") != "test-key" {
			t.Error("missing management credential")
		}
		active := previous
		// The second request must promote precisely the requested version with its reviewed predecessor.
		if calls == 2 {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if r.Method != "POST" || r.URL.Path != "/apps/"+appID+"/promote" || body["expected_active_app_id"] != previous {
				t.Errorf("invalid promotion: %s %s %#v", r.Method, r.URL.Path, body)
			}
			active = appID
		} else if r.Method != "GET" || r.URL.Path != "/apps/"+appID+"/traffic" {
			t.Error("invalid traffic discovery")
		}
		_ = json.NewEncoder(w).Encode(UnifiedAppTraffic{AppFamilyID: appID, ActiveAppID: active})
	}))
	defer server.Close()
	client := NewClient(server.URL, "test-key")
	current, err := client.GetUnifiedAppTraffic(appID)
	// Failed discovery cannot supply a valid concurrency precondition.
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.PromoteUnifiedApp(appID, current.ActiveAppID)
	// Receipt identity must be checked before reporting success to CLI callers.
	if err != nil || result.ActiveAppID != appID || calls != 2 {
		t.Fatalf("result=%#v err=%v calls=%d", result, err, calls)
	}
}

// TestUnifiedAppPromotionConflictIsNotRetried protects a competing deployment from automatic overwrite.
func TestUnifiedAppPromotionConflictIsNotRetried(t *testing.T) {
	calls := 0
	// A stale deployment is actionable conflict rather than a retryable transport failure.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "traffic changed", http.StatusConflict)
	}))
	defer server.Close()
	_, err := NewClient(server.URL, "").PromoteUnifiedApp("11111111-1111-4111-8111-111111111111", "")
	// Exactly one failed mutation must be surfaced to the user.
	if err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

// TestUnifiedAppNamedVersionLookupRejectsMissing prevents a failed exact lookup from falling back to another deployment.
func TestUnifiedAppNamedVersionLookupRejectsMissing(t *testing.T) {
	cases := []string{`{"data":{"appReference":null}}`, `{"errors":[{"message":"not found"}]}`}
	for _, body := range cases {
		// Each fixture simulates an unavailable exact name/version without offering a fallback ID.
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			id, err := NewClient(server.URL, "").ResolveUnifiedAppReference("Customer lookup", "1.0.0")
			// Missing or malformed success cannot become promotion authority.
			if err == nil || id != "" {
				t.Fatalf("id=%q err=%v", id, err)
			}
		})
	}
}
