package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// TestInitMCPCapabilityFlagsUseSharedLifecycle proves public init flags reach the existing writer with exact provider identities.
func TestInitMCPCapabilityFlagsUseSharedLifecycle(t *testing.T) {
	var got scaffoldRequest
	// Capture the lifecycle boundary so this command test never mutates a workspace.
	executeUnifiedInitForTest(t, func(_ *cobra.Command, _ unifiedInitMode, request scaffoldRequest) error { got = request; return nil }, "docs", "--mcp", "--description", "Read docs.", "--service", "demo@v1", "--mcp-tool", "demo=echo", "--mcp-prompt", "demo=echo", "--mcp-resource", "demo=docs://guide", "--mcp-resource-template", "demo=docs://{x,y}?q=a=b", "--no-apply")
	options := got.mcpRequests["demo"]
	// Commas and equals signs belong to the URI, not to CLI list syntax.
	if got.kind != configfile.KindMCP || !got.noApply || options.selection.ResourceTemplates[0] != "docs://{x,y}?q=a=b" || len(got.operations) != 0 {
		t.Fatalf("request=%+v", got)
	}
	// Combined and SDK apps must not acquire an unsupported dispatch adapter through these flags.
	err := executeUnifiedInitForTestWithError(t, func(*cobra.Command, unifiedInitMode, scaffoldRequest) error {
		t.Fatal("unsupported mode reached lifecycle")
		return nil
	}, "docs", "--sdk", "--service", "demo@v1", "--mcp-tool", "demo=echo")
	// Unsupported modes must report the explicit MCP remediation.
	if err == nil || !strings.Contains(err.Error(), "require --mcp") {
		t.Fatalf("mode error=%v", err)
	}
}

// TestInitMCPExtensionPreservesScopeAndRequiresReviewedRefresh verifies additive merge and immutable pin invariants.
func TestInitMCPExtensionPreservesScopeAndRequiresReviewedRefresh(t *testing.T) {
	snapshot := importedCatalogFixture()
	prior := &configfile.ImportedMCPSelection{RevisionID: snapshot.ID, Tools: []string{"echo"}}
	selected, err := resolveInitMCPSelection(snapshot, mcpSelectOptions{selection: configfile.ImportedMCPSelection{Prompts: []string{"echo"}}}, prior)
	// Same-named prompts and tools remain separate grants and earlier tools survive extension.
	if err != nil || len(selected.Tools) != 1 || len(selected.Prompts) != 1 || len(prior.Prompts) != 0 {
		t.Fatalf("merged=%+v err=%v", selected, err)
	}
	config := &configfile.AppConfig{Services: map[string]configfile.AppService{"demo": {Version: "v1", Operations: []string{"list"}, Bucket: "private", Auth: &configfile.AppAuth{Type: "bearer", Name: "token"}, MCP: prior}}}
	changed, err := mergeInitMCPCapabilities(config, map[string]*configfile.ImportedMCPSelection{"demo": selected})
	// Only the imported field changes; authored routing and endpoint scope survive.
	if err != nil || !changed || config.Services["demo"].Bucket != "private" || config.Services["demo"].Operations[0] != "list" || config.Services["demo"].Auth.Name != "token" {
		t.Fatalf("merge=%+v %v", config, err)
	}
	changed, err = mergeInitMCPCapabilities(config, map[string]*configfile.ImportedMCPSelection{"demo": selected})
	// Exact repeats must not trigger the shared successor-version inference.
	if err != nil || changed {
		t.Fatal("idempotent selection changed app")
	}
	snapshot.ID = "00000000-0000-4000-8000-000000000099"
	options := mcpSelectOptions{selection: configfile.ImportedMCPSelection{Prompts: []string{"echo"}}}
	_, err = resolveInitMCPSelection(snapshot, options, prior)
	// Moving the catalog pin changes existing definitions and needs explicit reviewed intent.
	if err == nil || !strings.Contains(err.Error(), "--mcp-revision") {
		t.Fatalf("refresh error=%v", err)
	}
	options.revision = snapshot.ID
	_, err = resolveInitMCPSelection(snapshot, options, prior)
	// Explicitly reviewing a still-compatible catalog permits the successor.
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Catalog.Tools = nil
	_, err = resolveInitMCPSelection(snapshot, options, prior)
	// Even an explicitly reviewed revision cannot silently drop a previously granted tool.
	if err == nil {
		t.Fatal("removed prior tool silently")
	}
}

// TestExtendMCPFlagsHydrateExistingService proves capability-only extension works without repeating --service or rewriting unrelated fields.
func TestExtendMCPFlagsHydrateExistingService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.yaml")
	config := configfile.AppConfig{BaseConfig: configfile.BaseConfig{APIVersion: configfile.APIVersionV1, Kind: configfile.KindMCP}, Name: "docs", Version: "1.0.0", Description: "Read docs.", Bucket: "default", Services: map[string]configfile.AppService{"demo": {Version: "v1", MCP: &configfile.ImportedMCPSelection{RevisionID: importedCatalogFixture().ID, Tools: []string{"echo"}}}}}
	data, err := yaml.Marshal(config)
	// The fixture is an ordinary valid imported-only app declaration.
	if err != nil {
		t.Fatal(err)
	}
	// Publish a private fixture for the normal file parser.
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	originalFile, originalNoInput := ConfigFile, NoInput
	ConfigFile, NoInput = path, true
	// Public flags rely on shared globals, which must be restored for other tests.
	t.Cleanup(func() { ConfigFile, NoInput = originalFile, originalNoInput })
	var got scaffoldRequest
	// Keep the lifecycle injectable while exercising normal target and mode inference.
	command := newUnifiedExtendCommandWithRunner(func(_ *cobra.Command, _ unifiedInitMode, request scaffoldRequest) error { got = request; return nil })
	command.SetArgs([]string{"docs", "--mcp-prompt", "demo=echo", "--no-apply", "--no-token"})
	// The public command must accept imported-only additive intent.
	if err = command.Execute(); err != nil {
		t.Fatal(err)
	}
	got, err = hydrateSDKInitExtendServiceReferences(got)
	// The existing provider version is the only valid pin for this selection-only extension.
	if err != nil || !got.noApply || !got.noToken || len(got.services) != 1 || got.services[0].name != "demo" || got.services[0].version != "v1" {
		t.Fatalf("hydrated=%+v err=%v", got, err)
	}
}

// TestInteractiveInitMCPCombinesEndpointAndNativeChoices exercises actual catalog resolution around a replaceable terminal boundary.
func TestInteractiveInitMCPCombinesEndpointAndNativeChoices(t *testing.T) {
	const serviceID = "00000000-0000-4000-8000-000000000001"
	const versionID = "00000000-0000-4000-8000-000000000002"
	snapshot := importedCatalogFixture()
	// Fixtures answer the production Engine membership and catalog routes; physical query behavior remains covered by its own selector tests.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the catalog and capability reads needed by this lifecycle are admitted.
		switch r.URL.Path {
		case "/graphql":
			_, _ = w.Write([]byte(`{"data":{"serviceOperations":[{"name":"list","method":"GET","path":"/items"}]}}`))
		case "/engine/graphql":
			fmt.Fprintf(w, `{"data":{"workspaceServicePage":{"data":[{"service_id":%q,"service_slug":"demo","enabled_versions":[{"version":"v1","service_version_id":%q}]}],"total":1}}}`, serviceID, versionID)
		case "/workspace/services/" + serviceID + "/versions/" + versionID + "/mcp-catalog":
			_ = json.NewEncoder(w).Encode(map[string]any{"catalog": snapshot})
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	original := NoInput
	NoInput = true
	// Restore global interaction mode for the rest of the command suite.
	t.Cleanup(func() { NoInput = original })
	request := scaffoldRequest{kind: configfile.KindMCP, mcpRequests: map[string]mcpSelectOptions{"alias": {selection: configfile.ImportedMCPSelection{Tools: []string{"echo"}, Resources: []string{"docs://guide"}}}}}
	services := []sdkInitResolvedService{{target: workspaceServiceAddTarget{slug: "demo", serviceID: serviceID, enabledVersions: []string{"v1"}, requestedRefs: []string{"alias"}}, version: "v1"}}
	resolved, err := completeInitMCPCapabilities(&cobra.Command{}, api.NewClient(server.URL, "fixture"), request, services)
	// Automation resolves the canonical service and needs no unrelated endpoint grant or terminal prompt.
	if err != nil || !sdkInitServiceHasOperationSelection(resolved, "demo") || len(resolved.operations) != 0 {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	NoInput = false
	t.Setenv("CI", "")
	originalRunner := initMCPCapabilityRunner
	// Restore the terminal boundary so other command tests use their own selector.
	t.Cleanup(func() { initMCPCapabilityRunner = originalRunner })
	// Select mixed physical and namespaced imported rows through the real orchestration path.
	initMCPCapabilityRunner = func(_ io.Reader, _ io.Writer, _ string, choices []initMCPCapabilityChoice) ([]int, error) {
		selected := []int{}
		for index, choice := range choices {
			// Matching display names in different namespaces remain independent selections.
			if choice.kind == "endpoint" || choice.kind == "tools" || choice.kind == "prompts" {
				selected = append(selected, index)
			}
		}
		return selected, nil
	}
	interactive, err := completeInitMCPCapabilities(&cobra.Command{}, api.NewClient(server.URL, "fixture"), scaffoldRequest{kind: configfile.KindMCP}, services)
	// Imported selection must coexist with the endpoint and stop the later picker from prompting again.
	if err != nil || len(interactive.operations) != 1 || importedSelectionCount(interactive.mcpSelections["demo"]) != 2 {
		t.Fatalf("interactive=%+v err=%v", interactive, err)
	}
	cancelled := errors.New("cancelled")
	// Cancellation must propagate before configuration writing or remote mutation.
	initMCPCapabilityRunner = func(io.Reader, io.Writer, string, []initMCPCapabilityChoice) ([]int, error) { return nil, cancelled }
	_, err = completeInitMCPCapabilities(&cobra.Command{}, api.NewClient(server.URL, "fixture"), scaffoldRequest{kind: configfile.KindMCP}, services)
	// A cancelled terminal never becomes a confirmed empty or all-items grant.
	if !errors.Is(err, cancelled) {
		t.Fatalf("cancellation=%v", err)
	}
}
