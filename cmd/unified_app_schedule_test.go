package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

// TestUnifiedScheduleCLI exercises real Cobra commands, family resolution, private stdin, and optimistic mutations.
func TestUnifiedScheduleCLI(t *testing.T) {
	fixture := &scheduleCLIFixture{t: t}
	server := httptest.NewServer(fixture)
	defer server.Close()
	oldURL, oldKey := EngineURL, APIKey
	// Global credential settings must not escape this fixture.
	t.Cleanup(func() { EngineURL, APIKey = oldURL, oldKey })
	EngineURL, APIKey = server.URL, "test-management-key"
	runScheduleCLI(t, "create", "Billing", "--name", "Daily", "--cron", "0 9 * * *", "--timezone", "Europe/London", "--input-file", "-", "--json")
	assertCreatedSchedule(t, fixture.saved)
	// Default listings must not print private input into ordinary terminal history.
	if strings.Contains(runScheduleCLI(t, "list", "Billing"), "saved-value") {
		t.Fatal("private input leaked")
	}
	runScheduleCLI(t, "pause", "Billing", "--id", fixture.saved.ID)
	if fixture.saved.Enabled {
		t.Fatal("pause did not disable schedule")
	}
	runScheduleCLI(t, "update", "Billing", "--id", fixture.saved.ID, "--name", "Renamed")
	if fixture.saved.Enabled || fixture.saved.Name != "Renamed" || string(fixture.saved.Input) != `{"private":"saved-value"}` {
		t.Fatal("partial edit changed saved authority or input")
	}
	runScheduleCLI(t, "delete", "Billing", "--id", fixture.saved.ID)
	if fixture.writes != 3 {
		t.Fatalf("unexpected retry count %d", fixture.writes)
	}
}

// TestScheduleInputRejectsMalformedJSON prevents private invalid data from being echoed or sent to Engine.
func TestScheduleInputRejectsMalformedJSON(t *testing.T) {
	command := newUnifiedScheduleCommand()
	create, _, err := command.Find([]string{"create"})
	if err != nil {
		t.Fatal(err)
	}
	_ = create.Flags().Set("input", `{"private":"never-log-this"`)
	_, err = readScheduleInput(create)
	if err == nil || strings.Contains(err.Error(), "never-log-this") {
		t.Fatal("invalid input was accepted or disclosed")
	}
}

// scheduleCLIFixture accepts only typed management calls and records revisions across CLI invocations.
type scheduleCLIFixture struct {
	t      *testing.T
	saved  api.UnifiedAppScheduleInput
	writes int
}

// ServeHTTP supplies family discovery and routes mutations through the shared schedule endpoint.
func (fixture *scheduleCLIFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	const appID = "11111111-1111-4111-8111-111111111111"
	if r.Header.Get("x-api-key") != "test-management-key" {
		fixture.t.Error("missing management auth")
		w.WriteHeader(403)
		return
	}
	if r.URL.Path == "/engine/graphql" {
		body, _ := io.ReadAll(r.Body)
		// Name lookup and management routing remain distinct from future active-version execution.
		if bytes.Contains(body, []byte("appFamilyReference")) {
			_, _ = io.WriteString(w, `{"data":{"appFamilyReference":{"id":"`+appID+`","kind":"app_family"}}}`)
		} else {
			_, _ = io.WriteString(w, `{"data":{"appVersions":[{"app_id":"`+appID+`"}]}}`)
		}
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/apps/"+appID+"/schedules") {
		fixture.t.Error("unexpected route")
		w.WriteHeader(404)
		return
	}
	fixture.respond(w, r)

}

// respond validates wire revisions rather than relying on the command's in-memory state.
func (fixture *scheduleCLIFixture) respond(w http.ResponseWriter, r *http.Request) {
	// Each method checks the exact wire contract instead of mirroring command implementation.
	switch r.Method {
	case http.MethodPut:
		var item api.UnifiedAppScheduleInput
		if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
			fixture.t.Error(err)
			w.WriteHeader(400)
			return
		}
		if fixture.writes > 0 && item.Revision != fixture.saved.Revision {
			fixture.t.Error("lost optimistic revision")
		}
		fixture.writes++
		item.Revision++
		fixture.saved = item
		_ = json.NewEncoder(w).Encode(map[string]any{"id": item.ID, "saved": true})
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]any{"schedules": []api.UnifiedAppSchedule{{UnifiedAppScheduleInput: fixture.saved}}})
	case http.MethodDelete:
		if r.URL.Query().Get("revision") != "3" {
			fixture.t.Error("delete revision")
		}
		_, _ = io.WriteString(w, `{"deleted":true}`)
	default:
		fixture.t.Error("unexpected method")
	}
}

// runScheduleCLI creates fresh flags and captures output for a single simulated process invocation.
func runScheduleCLI(t *testing.T, args ...string) string {
	t.Helper()
	command := newUnifiedScheduleCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetIn(strings.NewReader(`{"private":"saved-value"}`))
	command.SetArgs(args)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	return output.String()
}

// assertCreatedSchedule verifies stdin input and timing survive the complete CLI wire path.
func assertCreatedSchedule(t *testing.T, item api.UnifiedAppScheduleInput) {
	t.Helper()
	if string(item.Input) != `{"private":"saved-value"}` || item.Cron != "0 9 * * *" || item.Timezone != "Europe/London" || !item.Enabled {
		t.Fatalf("incorrect saved configuration: %+v", item)
	}
}
