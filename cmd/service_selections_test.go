package cmd

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

// TestAppServiceSelectorsPreserveScope verifies shell arguments reach each lifecycle with the intended per-service boundary.
func TestAppServiceSelectorsPreserveScope(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		services   []scaffoldService
		operations []scaffoldOperation
		all        []string
	}{
		{name: "bare", args: []string{"--service", "jira;slack"}, services: []scaffoldService{{name: "jira"}, {name: "slack"}}, all: []string{"jira", "slack"}},
		{name: "scoped", args: []string{"--service", " slack=chatPostMessage,conversationsList ; jira@v3=issueUpdate "}, services: []scaffoldService{{name: "slack"}, {name: "jira", version: "v3"}}, operations: []scaffoldOperation{{service: "slack", operation: "chatPostMessage"}, {service: "slack", operation: "conversationsList"}, {service: "jira", operation: "issueUpdate"}}},
		{name: "mixed", args: []string{"--service", "@google/drive@v3=listFiles;slack"}, services: []scaffoldService{{name: "@google/drive", version: "v3"}, {name: "slack"}}, operations: []scaffoldOperation{{service: "@google/drive", operation: "listFiles"}}, all: []string{"slack"}},
		{name: "legacy", args: []string{"--service", "jira,slack", "--operation", "jira=issueUpdate", "--operation", "slack=chatPostMessage"}, services: []scaffoldService{{name: "jira"}, {name: "slack"}}, operations: []scaffoldOperation{{service: "jira", operation: "issueUpdate"}, {service: "slack", operation: "chatPostMessage"}}},
		{name: "repeated", args: []string{"--service", "jira=issueUpdate", "--service", "slack=chatPostMessage"}, services: []scaffoldService{{name: "jira"}, {name: "slack"}}, operations: []scaffoldOperation{{service: "jira", operation: "issueUpdate"}, {service: "slack", operation: "chatPostMessage"}}},
	}
	for _, test := range tests {
		// Exercise Cobra itself because CSV flag parsing would corrupt the operation comma list.
		t.Run(test.name, func(t *testing.T) {
			for _, mode := range []string{"", "--sdk", "--rest", "--mcp"} {
				args := []string{"support"}
				// Omitting the method flag covers the combined App default.
				if mode != "" {
					args = append(args, mode)
				}
				// Hosted MCP requires authored identity even when all capabilities are explicit.
				if mode == "--mcp" {
					args = append(args, "--description", "Manage support issues.")
				}
				var got scaffoldRequest
				// Capture local intent without permitting an Engine mutation.
				executeUnifiedInitForTest(t, func(_ *cobra.Command, _ unifiedInitMode, request scaffoldRequest) error {
					got = request
					return nil
				}, append(args, test.args...)...)
				// Resolve defaults locally for these canonical fixture names, as production does after provider resolution.
				completeAppServiceSelections(&got, nil)
				// Exact service pins and ownership must survive every delivery mode.
				if !got.defaultAllOperations || !reflect.DeepEqual(got.services, test.services) || !reflect.DeepEqual(got.operations, append([]scaffoldOperation{}, test.operations...)) || strings.Join(got.selectAll, ";") != strings.Join(test.all, ";") {
					t.Fatalf("mode=%q request=%#v", mode, got)
				}
			}
		})
	}
}

// TestAppServiceSelectorsRejectMalformedGroups proves malformed allowlists cannot silently become full service access.
func TestAppServiceSelectorsRejectMalformedGroups(t *testing.T) {
	for _, selector := range []string{"", ";jira", "jira;", "jira;;slack", "jira=", "jira=issueUpdate,", "jira=,issueUpdate", "jira=issueUpdate,,issueGet", "=issueUpdate", "jira,slack=chatPostMessage", "jira=issueUpdate,slack=chatPostMessage", "jira@=issueUpdate"} {
		_, _, err := parseAppServiceSelections([]string{selector}, nil)
		// Every invalid group must fail before any request is returned to the lifecycle.
		if err == nil {
			t.Errorf("selector %q unexpectedly accepted", selector)
		}
	}
}

// TestExtendGroupedServicesInheritsPins verifies inline operation scope uses the existing immutable provider version.
func TestExtendGroupedServicesInheritsPins(t *testing.T) {
	dir := t.TempDir()
	writeUnifiedExtendFixture(t, filepath.Join(dir, ".fused", "sdks", "support.yaml"), unifiedExtendSDKFixture("support", ""))
	withUnifiedExtendWorkingDirectory(t, dir)
	oldConfigFile, oldNoInput := ConfigFile, NoInput
	ConfigFile, NoInput = "", true
	// Restore process-wide command state after exercising the root wrapper.
	t.Cleanup(func() { ConfigFile, NoInput = oldConfigFile, oldNoInput })
	var got scaffoldRequest
	// Capture the successor request before remote resolution.
	command := newUnifiedExtendCommandWithRunner(func(_ *cobra.Command, _ unifiedInitMode, request scaffoldRequest) error {
		got = request
		return nil
	})
	command.SetArgs([]string{"support", "--service", "linear=issueUpdate,issueCreate;slack"})
	// Cobra must preserve commas inside one service's operation allowlist.
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	completeAppServiceSelections(&got, nil)
	// Existing service pins survive while newly added bare services receive explicit all-operation intent.
	if !got.extend || !got.defaultAllOperations || got.services[0].version != "v1" || len(got.operations) != 2 || strings.Join(got.selectAll, ";") != "slack" {
		t.Fatalf("extension request=%#v", got)
	}
}

// TestWebhookGroupedServicesRejectOperations preserves ingress semantics under the shared service flag.
func TestWebhookGroupedServicesRejectOperations(t *testing.T) {
	var got scaffoldRequest
	// Capture only the local ingress request.
	executeUnifiedInitForTest(t, func(_ *cobra.Command, _ unifiedInitMode, request scaffoldRequest) error {
		got = request
		return nil
	}, "support-events", "--webhook", "--service", "jira;slack")
	// Ingress services must not acquire physical operation selections.
	if len(got.services) != 2 || len(got.selectAll) != 0 || len(got.operations) != 0 {
		t.Fatalf("webhook request=%#v", got)
	}
	// Inline app operations must be rejected before the runner can publish a webhook.
	err := executeUnifiedInitForTestWithError(t, func(_ *cobra.Command, _ unifiedInitMode, _ scaffoldRequest) error {
		t.Fatal("invalid webhook reached lifecycle")
		return nil
	}, "support-events", "--webhook", "--service", "jira=issueUpdate")
	// The diagnostic must explain the unsupported ingress selection.
	if err == nil || !strings.Contains(err.Error(), "operation lists cannot be used with --webhook") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestServiceSelectorsCompleteNoInputLifecycle verifies both all-operation and narrow selection reach persisted config through plan/apply.
func TestServiceSelectorsCompleteNoInputLifecycle(t *testing.T) {
	for _, selector := range []string{"linear", "linear=issueUpdate"} {
		// A separate fixture keeps each lifecycle's workspace and app receipts independent.
		t.Run(selector, func(t *testing.T) {
			dir := t.TempDir()
			server, calls := newSDKInitLifecycleServer(t)
			defer server.Close()
			oldNoInput := NoInput
			NoInput = true
			// Restore global prompting policy for later tests.
			t.Cleanup(func() { NoInput = oldNoInput })
			runCommandInDirOutput(t, dir, server.URL, []string{"init", "--sdk", "support-sdk", "--service", selector, "--no-input"})
			parsed, err := configfile.ParseFile(filepath.Join(dir, ".fused", "sdks", "support-sdk.yaml"))
			// The saved config is the user-visible result, beyond successful flag parsing.
			if err != nil {
				t.Fatal(err)
			}
			service := parsed.SDK.Services["linear"]
			// Both choices must retain the version resolved by the existing workspace lifecycle.
			if service.Version != "v1" || strings.Join(*calls, ",") != "workspace-plan,workspace-apply,sdk-plan,sdk-apply" {
				t.Fatalf("service=%#v calls=%v", service, *calls)
			}
			// Publication freezes select-all against this fixture's sole operation, matching an explicit narrow selection.
			if service.SelectAll || !reflect.DeepEqual(service.Operations, []string{"issueUpdate"}) {
				t.Fatalf("selector=%q service=%#v", selector, service)
			}
		})
	}
}

// TestServiceDefaultsRespectCanonicalScope prevents aliases from widening explicit physical, event, or imported selections.
func TestServiceDefaultsRespectCanonicalScope(t *testing.T) {
	request := scaffoldRequest{
		services:    []scaffoldService{{name: "jira"}, {name: "slack"}, {name: "stripe"}, {name: "linear"}},
		operations:  []scaffoldOperation{{service: "jira", operation: "issueUpdate"}},
		events:      []scaffoldEvent{{service: "stripe", event: "payment.created"}},
		mcpRequests: map[string]mcpSelectOptions{"Slack": {}},
	}
	completeAppServiceSelections(&request, map[string]string{"Slack": "slack"})
	// Only the service with no authored scope may receive all operations.
	if !reflect.DeepEqual(request.selectAll, []string{"linear"}) {
		t.Fatalf("unexpected automatic scope: %v", request.selectAll)
	}
}
