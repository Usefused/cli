package cmd

import (
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

// TestUnifiedAppExecutionURL keeps copied names safe as one segment and preserves unambiguous UUID fallback routes.
func TestUnifiedAppExecutionURL(t *testing.T) {
	const family = "40e5eff1-3b32-4d4d-854d-f2621a43ad6e"
	for _, test := range []struct{ name, reference string }{
		{" Café: Billing/100% ", "Caf%C3%A9:%20Billing%2F100%25"},
		{"billing", "billing"}, {"", family}, {"  ", family}, {family, family},
		{"40e5eff13b324d4d854df2621a43ad6e", family}, {"{" + family + "}", family}, {"urn:uuid:" + family, family},
	} {
		got := unifiedAppExecutionURL("https://engine.example/base/", family, test.name)
		// The origin's base path and one escaped reference must survive together.
		if want := "https://engine.example/base/v1/apps/" + test.reference + "/executions"; got != want {
			t.Fatalf("name %q: got %s, want %s", test.name, got, want)
		}
	}
}

// TestUnifiedAppApplyOutputPrefersName retains machine-readable deployment UUIDs beside the named execution URL.
func TestUnifiedAppApplyOutputPrefersName(t *testing.T) {
	paths := []string{}
	server := newPromptExecutionTestServer(t, "version-id", &paths)
	defer server.Close()
	cfg := &configfile.ParsedConfig{UnifiedApp: &configfile.UnifiedAppConfig{Name: "billing"}}
	result, err := applyExecutionVersion(api.NewClient(server.URL, "test-control-key"), cfg, planReceipt{PlanID: "plan-1"})
	// Structured apply responses must keep UUID identity available without advertising it as the default route.
	if err != nil || result.ExecutionURL != server.URL+"/v1/apps/billing/executions" || result.AppFamilyID != "family-1" || result.AppID != "version-id" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}
