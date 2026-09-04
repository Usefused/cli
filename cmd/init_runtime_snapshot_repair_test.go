package cmd

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

// TestUnifiedAPIInitRefreshesColdRuntimeContractOnce proves a newly enabled service becomes usable without a second manual command.
func TestUnifiedAPIInitRefreshesColdRuntimeContractOnce(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	path := filepath.Join(directory, "direct-api.yaml")
	request := unifiedInitFailureTestRequest(path)
	request.generate, request.generateSet = false, true
	resolverCalls, refreshCalls, planCalls, applyCalls := 0, 0, 0, 0
	server := newUnifiedInitRuntimeRepairServer(t, &refreshCalls, &planCalls, &applyCalls, http.StatusOK)
	defer server.Close()
	resolver := func([]api.AppScaffoldSelection) ([]api.AppScaffoldRequirement, error) {
		resolverCalls++
		// The first read models the short cache gap immediately after workspace activation.
		if resolverCalls == 1 {
			return nil, &api.APIError{Code: "graphql_dependency_failed", Message: "app scaffold requirements unavailable"}
		}
		return []api.AppScaffoldRequirement{}, nil
	}
	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&output)
	err := createPlanApplyUnifiedInit(command, api.NewClient(server.URL, "test-key"), unifiedInitModeAPI, request, true, false, resolver, defaultTestScaffoldBucket)
	// One exact refresh sits between two scaffold reads, followed by the ordinary plan/apply boundary.
	if err != nil || resolverCalls != 2 || refreshCalls != 1 || planCalls != 1 || applyCalls != 1 {
		t.Fatalf("error=%v resolver=%d refresh=%d plan=%d apply=%d output=%q", err, resolverCalls, refreshCalls, planCalls, applyCalls, output.String())
	}
	// Success publishes the reviewed config only after the repaired preflight and names the immutable REST route.
	if _, statErr := os.Stat(path); statErr != nil || !strings.Contains(output.String(), "Refreshed runtime contract for linear@v1.") || !strings.Contains(output.String(), "/v1/apps/api-version-1/executions") {
		t.Fatalf("statErr=%v output=%q", statErr, output.String())
	}
}

// TestUnifiedMCPInitRefreshesColdRuntimeContractOnce protects the hosted-app path from inheriting SDK-only repair assumptions.
func TestUnifiedMCPInitRefreshesColdRuntimeContractOnce(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	path := filepath.Join(directory, "hosted-mcp.yaml")
	request := unifiedInitFailureTestRequest(path)
	request.kind = configfile.KindMCP
	request.language, request.languageSet = "", false
	request.generate, request.generateSet = false, false
	request.description, request.descriptionSet = "Manage support issues.", true
	resolverCalls, refreshCalls, planCalls, applyCalls := 0, 0, 0, 0
	server := newUnifiedInitMCPRuntimeRepairServer(t, &refreshCalls, &planCalls, &applyCalls)
	defer server.Close()
	resolver := func([]api.AppScaffoldSelection) ([]api.AppScaffoldRequirement, error) {
		resolverCalls++
		// One cold read followed by success models the exact activation-to-cache race shared with direct API init.
		if resolverCalls == 1 {
			return nil, &api.APIError{Code: "graphql_dependency_failed", Message: "app scaffold requirements unavailable"}
		}
		return []api.AppScaffoldRequirement{}, nil
	}
	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&output)
	err := createPlanApplyUnifiedInit(command, api.NewClient(server.URL, "test-key"), unifiedInitModeMCP, request, true, false, resolver, defaultTestScaffoldBucket)
	// MCP must execute the same bounded repair followed by its own plan/apply endpoints and publish only after success.
	if err != nil || resolverCalls != 2 || refreshCalls != 1 || planCalls != 1 || applyCalls != 1 {
		t.Fatalf("error=%v resolver=%d refresh=%d plan=%d apply=%d output=%q", err, resolverCalls, refreshCalls, planCalls, applyCalls, output.String())
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("MCP config was not published after repaired apply: %v", statErr)
	}
}

// TestUnifiedAPIInitColdRuntimeRefreshFailurePreservesNoAppState proves a failed exact repair cannot reach plan, apply, or file publication.
func TestUnifiedAPIInitColdRuntimeRefreshFailurePreservesNoAppState(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	path := filepath.Join(directory, "direct-api.yaml")
	request := unifiedInitFailureTestRequest(path)
	request.generate, request.generateSet = false, true
	refreshCalls, planCalls, applyCalls := 0, 0, 0
	server := newUnifiedInitRuntimeRepairServer(t, &refreshCalls, &planCalls, &applyCalls, http.StatusBadGateway)
	defer server.Close()
	resolver := func([]api.AppScaffoldSelection) ([]api.AppScaffoldRequirement, error) {
		return nil, &api.APIError{Code: "graphql_dependency_failed", Message: "app scaffold requirements unavailable"}
	}
	err := createPlanApplyUnifiedInit(&cobra.Command{}, api.NewClient(server.URL, "test-key"), unifiedInitModeAPI, request, true, false, resolver, defaultTestScaffoldBucket)
	_, statErr := os.Stat(path)
	// The typed refresh failure retains the separately committed workspace boundary while proving no app mutation occurred.
	if err == nil || refreshCalls != 1 || planCalls != 0 || applyCalls != 0 || !errors.Is(statErr, os.ErrNotExist) || !strings.Contains(err.Error(), "failed during runtime contract refresh") {
		t.Fatalf("error=%v refresh=%d plan=%d apply=%d statErr=%v", err, refreshCalls, planCalls, applyCalls, statErr)
	}
}

// TestUnifiedAPIInitColdRuntimeRetryStopsAfterOneRefresh proves a still-cold Engine cannot trigger an unbounded repair loop.
func TestUnifiedAPIInitColdRuntimeRetryStopsAfterOneRefresh(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	path := filepath.Join(directory, "direct-api.yaml")
	request := unifiedInitFailureTestRequest(path)
	request.generate, request.generateSet = false, true
	resolverCalls, refreshCalls, planCalls, applyCalls := 0, 0, 0, 0
	server := newUnifiedInitRuntimeRepairServer(t, &refreshCalls, &planCalls, &applyCalls, http.StatusOK)
	defer server.Close()
	resolver := func([]api.AppScaffoldSelection) ([]api.AppScaffoldRequirement, error) {
		resolverCalls++
		return nil, &api.APIError{Code: "graphql_dependency_failed", Message: "app scaffold requirements unavailable"}
	}
	err := createPlanApplyUnifiedInit(&cobra.Command{}, api.NewClient(server.URL, "test-key"), unifiedInitModeAPI, request, true, false, resolver, defaultTestScaffoldBucket)
	_, statErr := os.Stat(path)
	// Exactly two reads and one refresh prove the bounded retry stopped before app planning, apply, or local publication.
	if err == nil || resolverCalls != 2 || refreshCalls != 1 || planCalls != 0 || applyCalls != 0 || !errors.Is(statErr, os.ErrNotExist) || !strings.Contains(err.Error(), "configuration preflight retry") {
		t.Fatalf("error=%v resolver=%d refresh=%d plan=%d apply=%d statErr=%v", err, resolverCalls, refreshCalls, planCalls, applyCalls, statErr)
	}
}

// TestUnifiedInitRuntimeRefreshClassifierRejectsUnrelatedFailures keeps automatic mutation behind one exact typed dependency code.
func TestUnifiedInitRuntimeRefreshClassifierRejectsUnrelatedFailures(t *testing.T) {
	tests := []struct {
		name  string
		cause error
		want  bool
	}{
		{name: "exact scaffold dependency", cause: &api.APIError{Code: "graphql_dependency_failed"}, want: true},
		{name: "validation", cause: errors.New("operation is required")},
		{name: "other dependency", cause: &api.APIError{Code: "generation_contract_pin_unavailable"}},
	}
	// Each row distinguishes the one recoverable cache race from failures that must remain mutation-free.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// A classifier mismatch could either skip the safe repair or mutate state for an unrelated failure.
			if got := unifiedInitCanRefreshRuntimeSnapshots(test.cause); got != test.want {
				t.Fatalf("unifiedInitCanRefreshRuntimeSnapshots(%v) = %t, want %t", test.cause, got, test.want)
			}
		})
	}
}

// newUnifiedInitRuntimeRepairServer serves one exact workspace identity plus bounded refresh and app lifecycle outcomes.
func newUnifiedInitRuntimeRepairServer(t *testing.T, refreshCalls, planCalls, applyCalls *int, refreshStatus int) *httptest.Server {
	t.Helper()
	// Serve the bounded membership response while preserving this fixture's command-specific checks.
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		// Each route is counted independently so an accidental retry loop or early mutation is observable.
		switch request.URL.Path {
		case "/engine/graphql":
			_, _ = writer.Write([]byte(`{"data":{"workspaceServicePage":{"data":[{"service_id":"` + generationRepairServiceID + `","service_slug":"linear","service_name":"Linear","enabled_versions":[{"service_version_id":"` + generationRepairVersionID + `","version":"v1","status":"public"}]}],"total":1}}}`))
		case "/workspace/services/" + generationRepairServiceID + "/versions/" + generationRepairVersionID + "/refresh":
			*refreshCalls = *refreshCalls + 1
			// The error response remains structured so production APIError classification is exercised.
			if refreshStatus != http.StatusOK {
				writer.WriteHeader(refreshStatus)
				_, _ = writer.Write([]byte(`{"error":{"code":"runtime_contract_dependency_unavailable","message":"Registry contract unavailable","category":"dependency","retryable":true}}`))
				return
			}
			_, _ = writer.Write([]byte(`{"status":"refreshed","service_id":"` + generationRepairServiceID + `","service_version_id":"` + generationRepairVersionID + `","version":"v1","contract_hash":"sha256:test"}`))
		case "/sdk-config/plan":
			*planCalls = *planCalls + 1
			writeSDKInitPlanResponse(t, writer, request, "plan-sdk")
		case "/sdk-config/apply":
			*applyCalls = *applyCalls + 1
			_, _ = writer.Write([]byte(`{"status":"applied","plan_id":"plan-sdk","app_family_id":"api-family-1","app_id":"api-version-1","generation_status":"skipped"}`))
		default:
			t.Fatalf("unexpected runtime repair path %q", request.URL.Path)
		}
	}))
}

// newUnifiedInitMCPRuntimeRepairServer serves the MCP-specific plan/apply boundary around the shared exact refresh.
func newUnifiedInitMCPRuntimeRepairServer(t *testing.T, refreshCalls, planCalls, applyCalls *int) *httptest.Server {
	t.Helper()
	// Serve the bounded membership response while preserving this fixture's command-specific checks.
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		// Route counts distinguish shared refresh behavior from the MCP-specific publication endpoints.
		switch request.URL.Path {
		case "/engine/graphql":
			_, _ = writer.Write([]byte(`{"data":{"workspaceServicePage":{"data":[{"service_id":"` + generationRepairServiceID + `","service_slug":"linear","service_name":"Linear","enabled_versions":[{"service_version_id":"` + generationRepairVersionID + `","version":"v1","status":"public"}]}],"total":1}}}`))
		case "/workspace/services/" + generationRepairServiceID + "/versions/" + generationRepairVersionID + "/refresh":
			*refreshCalls = *refreshCalls + 1
			_, _ = writer.Write([]byte(`{"status":"refreshed","service_id":"` + generationRepairServiceID + `","service_version_id":"` + generationRepairVersionID + `","version":"v1","contract_hash":"sha256:test"}`))
		case "/mcp-config/plan":
			*planCalls = *planCalls + 1
			writeSDKInitPlanResponse(t, writer, request, "plan-mcp")
		case "/mcp-config/apply":
			*applyCalls = *applyCalls + 1
			_, _ = writer.Write([]byte(`{"status":"applied","plan_id":"plan-mcp","app_family_id":"mcp-family-1","app_id":"mcp-version-1","default_transport":"streamable_http","transport_urls":{}}`))
		default:
			t.Fatalf("unexpected MCP runtime repair path %q", request.URL.Path)
		}
	}))
}
