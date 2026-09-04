package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

const (
	generationRepairServiceID = "00000000-0000-4000-8000-000000000001"
	generationRepairVersionID = "00000000-0000-4000-8000-000000000002"
)

// unifiedInitGenerationRepairFixture controls only the two failure boundaries needed by repair regression tests.
type unifiedInitGenerationRepairFixture struct {
	refreshErrorCode string
	retryErrorCode   string
	firstErrorCode   string
}

// unifiedInitGenerationRepairCalls records the complete remote sequence and both plan payloads.
type unifiedInitGenerationRepairCalls struct {
	paths        []string
	planBodies   [][]byte
	planCalls    int
	listCalls    int
	refreshCalls int
	applyCalls   int
}

// TestUnifiedInitGenerationPinRefreshesExactVersionAndRetriesOnce proves repair preserves the plan intent and completes onboarding.
func TestUnifiedInitGenerationPinRefreshesExactVersionAndRetriesOnce(t *testing.T) {
	directory := t.TempDir()
	withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
	originalNoInput := NoInput
	NoInput = true
	t.Cleanup(func() { NoInput = originalNoInput })
	server, calls := newUnifiedInitGenerationRepairServer(t, unifiedInitGenerationRepairFixture{firstErrorCode: "generation_contract_pin_unavailable"})
	defer server.Close()

	path := filepath.Join(directory, "support-sdk.yaml")
	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&output)
	err := createPlanApplyUnifiedInit(command, api.NewClient(server.URL, "test-key"), unifiedInitModeSDK, unifiedInitFailureTestRequest(path), false, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	if err != nil {
		t.Fatalf("generated SDK init repair: %v", err)
	}
	// Exactly one list and exact refresh sit between two byte-identical plans before the normal apply boundary.
	wantPaths := []string{"/sdk-config/plan", "/engine/graphql", "/workspace/services/" + generationRepairServiceID + "/versions/" + generationRepairVersionID + "/refresh", "/sdk-config/plan", "/sdk-config/apply"}
	if !reflect.DeepEqual(calls.paths, wantPaths) || calls.planCalls != 2 || calls.listCalls != 1 || calls.refreshCalls != 1 || calls.applyCalls != 1 || len(calls.planBodies) != 2 || !bytes.Equal(calls.planBodies[0], calls.planBodies[1]) {
		t.Fatalf("calls=%#v plan payloads equal=%v", calls, len(calls.planBodies) == 2 && bytes.Equal(calls.planBodies[0], calls.planBodies[1]))
	}
	// Successful repair must publish the candidate and visibly identify only the non-secret service tag.
	if _, statErr := os.Stat(path); statErr != nil || !strings.Contains(output.String(), "Refreshing immutable SDK generation snapshot for linear@v1") || !strings.Contains(output.String(), "Refreshed immutable SDK generation snapshot for linear@v1") {
		t.Fatalf("statErr=%v output=%q", statErr, output.String())
	}
}

// TestUnifiedInitGenerationPinRefreshFailureStopsBeforeRetry proves dependency failure cannot publish or re-plan an incomplete repair.
func TestUnifiedInitGenerationPinRefreshFailureStopsBeforeRetry(t *testing.T) {
	directory := t.TempDir()
	server, calls := newUnifiedInitGenerationRepairServer(t, unifiedInitGenerationRepairFixture{
		firstErrorCode: "generation_contract_pin_unavailable", refreshErrorCode: "runtime_contract_dependency_unavailable",
	})
	defer server.Close()
	path := filepath.Join(directory, "support-sdk.yaml")
	command := &cobra.Command{}
	command.SetOut(&bytes.Buffer{})
	err := createPlanApplyUnifiedInit(command, api.NewClient(server.URL, "test-key"), unifiedInitModeSDK, unifiedInitFailureTestRequest(path), false, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	var apiErr *api.APIError
	_, statErr := os.Stat(path)
	// One failed exact refresh retains its typed cause and stops before retry, apply, receipt, or config publication.
	if !errors.As(err, &apiErr) || apiErr.Code != "runtime_contract_dependency_unavailable" || calls.planCalls != 1 || calls.refreshCalls != 1 || calls.applyCalls != 0 || !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("error=%v APIError=%#v calls=%#v statErr=%v", err, apiErr, calls, statErr)
	}
}

// TestUnifiedInitGenerationPinRetryFailureReportsCompletedRefresh proves durable snapshot repair remains explicit when the second plan fails.
func TestUnifiedInitGenerationPinRetryFailureReportsCompletedRefresh(t *testing.T) {
	directory := t.TempDir()
	server, calls := newUnifiedInitGenerationRepairServer(t, unifiedInitGenerationRepairFixture{
		firstErrorCode: "generation_contract_pin_unavailable", retryErrorCode: "generation_contract_pin_unavailable",
	})
	defer server.Close()
	path := filepath.Join(directory, "support-sdk.yaml")
	command := &cobra.Command{}
	command.SetOut(&bytes.Buffer{})
	err := createPlanApplyUnifiedInit(command, api.NewClient(server.URL, "test-key"), unifiedInitModeSDK, unifiedInitFailureTestRequest(path), false, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	var apiErr *api.APIError
	_, statErr := os.Stat(path)
	// Retry failure retains the second typed cause, records the completed mutation, and leaves no desired-state file behind.
	if !errors.As(err, &apiErr) || apiErr.Code != "generation_contract_pin_unavailable" || calls.planCalls != 2 || calls.refreshCalls != 1 || calls.applyCalls != 0 || !errors.Is(statErr, os.ErrNotExist) || !strings.Contains(err.Error(), "exact runtime contract refresh completed for [linear@v1]") || !strings.Contains(err.Error(), "Engine still did not retain the immutable API contract") {
		t.Fatalf("error=%v APIError=%#v calls=%#v statErr=%v", err, apiErr, calls, statErr)
	}
}

// TestUnifiedInitUnrelatedPlanFailureNeverRefreshes proves typed repair is not a generic plan retry policy.
func TestUnifiedInitUnrelatedPlanFailureNeverRefreshes(t *testing.T) {
	directory := t.TempDir()
	server, calls := newUnifiedInitGenerationRepairServer(t, unifiedInitGenerationRepairFixture{firstErrorCode: "graphql_dependency_failed"})
	defer server.Close()
	path := filepath.Join(directory, "support-sdk.yaml")
	command := &cobra.Command{}
	command.SetOut(&bytes.Buffer{})
	err := createPlanApplyUnifiedInit(command, api.NewClient(server.URL, "test-key"), unifiedInitModeSDK, unifiedInitFailureTestRequest(path), false, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	var apiErr *api.APIError
	_, statErr := os.Stat(path)
	// An unrelated dependency error must remain one read-only plan call with no workspace list or snapshot mutation.
	if !errors.As(err, &apiErr) || apiErr.Code != "graphql_dependency_failed" || calls.planCalls != 1 || calls.listCalls != 0 || calls.refreshCalls != 0 || !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("error=%v APIError=%#v calls=%#v statErr=%v", err, apiErr, calls, statErr)
	}
}

// TestUnifiedInitGenerationRepairExcludesDirectAPI proves shared kind:sdk representation cannot enable codegen repair for API mode.
func TestUnifiedInitGenerationRepairExcludesDirectAPI(t *testing.T) {
	cause := &api.APIError{Code: "generation_contract_pin_unavailable", Message: "pin unavailable"}
	request := unifiedInitFailureTestRequest(filepath.Join(t.TempDir(), "direct-api.yaml"))
	request.generate, request.generateSet = false, true
	_, parsed, _, err := prepareUnifiedInitPlanInput(unifiedInitModeAPI, request, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	if err != nil {
		t.Fatalf("prepare direct API candidate: %v", err)
	}
	// API mode stays REST-only even though its durable resource kind is SDK.
	if unifiedInitCanRefreshGenerationSnapshot(unifiedInitModeAPI, parsed, cause) {
		t.Fatal("direct API candidate unexpectedly enabled generation snapshot repair")
	}
}

// TestSDKInitCompatibilityGenerationRepairUsesGenerateDefault proves the hidden alias reaches the shared repair admission path.
func TestSDKInitCompatibilityGenerationRepairUsesGenerateDefault(t *testing.T) {
	request := unifiedInitFailureTestRequest(filepath.Join(t.TempDir(), "compatibility-sdk.yaml"))
	request.generate, request.generateSet = false, false
	_, parsed, _, err := prepareUnifiedInitPlanInput(unifiedInitModeSDK, request, noOpScaffoldRequirements, defaultTestScaffoldBucket)
	if err != nil {
		t.Fatalf("prepare compatibility SDK candidate: %v", err)
	}
	cause := &api.APIError{Code: "generation_contract_pin_unavailable", Message: "pin unavailable"}
	// Absent generate retains the compatibility command's historical generated-package default and therefore the shared repair.
	if !unifiedInitCanRefreshGenerationSnapshot(unifiedInitModeSDK, parsed, cause) {
		t.Fatal("compatibility SDK candidate did not enable generation snapshot repair")
	}
}

// newUnifiedInitGenerationRepairServer serves one exact workspace version and bounded plan/refresh outcomes.
func newUnifiedInitGenerationRepairServer(t *testing.T, fixture unifiedInitGenerationRepairFixture) (*httptest.Server, *unifiedInitGenerationRepairCalls) {
	t.Helper()
	calls := &unifiedInitGenerationRepairCalls{}
	// Serve the bounded membership response while preserving this fixture's command-specific checks.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		calls.paths = append(calls.paths, request.URL.Path)
		// Each route models one Engine boundary so an unexpected retry or mutation is observable.
		switch request.URL.Path {
		case "/sdk-config/plan":
			calls.planCalls++
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("read SDK plan: %v", err)
			}
			calls.planBodies = append(calls.planBodies, body)
			// The first configured failure proves the repair admission code path.
			if calls.planCalls == 1 && fixture.firstErrorCode != "" {
				writeUnifiedInitGenerationRepairError(writer, fixture.firstErrorCode)
				return
			}
			// A distinct retry failure proves there is no second refresh loop.
			if calls.planCalls == 2 && fixture.retryErrorCode != "" {
				writeUnifiedInitGenerationRepairError(writer, fixture.retryErrorCode)
				return
			}
			writeUnifiedInitGenerationRepairPlan(t, writer, body)
		case "/engine/graphql":
			calls.listCalls++
			_, _ = writer.Write([]byte(`{"data":{"workspaceServicePage":{"data":[{"id":"00000000-0000-4000-8000-000000000003","workspace_id":"00000000-0000-4000-8000-000000000004","service_id":"` + generationRepairServiceID + `","service_version_id":"` + generationRepairVersionID + `","version":"v1","service_name":"Linear","service_slug":"linear","enabled_versions":[{"id":"00000000-0000-4000-8000-000000000005","service_version_id":"` + generationRepairVersionID + `","version":"v1","status":"public"}]}],"total":1}}}`))
		case "/workspace/services/" + generationRepairServiceID + "/versions/" + generationRepairVersionID + "/refresh":
			calls.refreshCalls++
			// A configured refresh dependency failure must remain typed and prevent plan replay.
			if fixture.refreshErrorCode != "" {
				writeUnifiedInitGenerationRepairError(writer, fixture.refreshErrorCode)
				return
			}
			_, _ = writer.Write([]byte(`{"status":"refreshed","service_id":"` + generationRepairServiceID + `","service_version_id":"` + generationRepairVersionID + `","version":"v1","contract_hash":"sha256:contract"}`))
		case "/sdk-config/apply":
			calls.applyCalls++
			_, _ = writer.Write([]byte(`{"status":"applied","plan_id":"plan-sdk","app_family_id":"family-1","app_id":"app-1"}`))
		default:
			t.Fatalf("unexpected generation repair path %q", request.URL.Path)
		}
	}))
	return server, calls
}

// writeUnifiedInitGenerationRepairError returns the current nested Engine envelope for one stable code.
func writeUnifiedInitGenerationRepairError(writer http.ResponseWriter, code string) {
	status := http.StatusConflict
	// Refresh dependency failures use their real gateway status while retaining the same reviewed envelope.
	if code == "runtime_contract_dependency_unavailable" {
		status = http.StatusBadGateway
	}
	writer.WriteHeader(status)
	_, _ = fmt.Fprintf(writer, `{"error":{"code":%q,"message":%q,"category":"dependency","retryable":true}}`, code, code)
}

// writeUnifiedInitGenerationRepairPlan echoes the immutable plan identity required by receipt verification.
func writeUnifiedInitGenerationRepairPlan(t *testing.T, writer http.ResponseWriter, body []byte) {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode SDK plan: %v", err)
	}
	_, _ = fmt.Fprintf(writer, `{"plan_id":"plan-sdk","config_key":%q,"source_hash":%q,"summary":{}}`, payload["config_key"], payload["source_hash"])
}

// withUnifiedInitGenerationRepairWorkingDirectory contains the successful plan receipt inside the test directory.
func withUnifiedInitGenerationRepairWorkingDirectory(t *testing.T, directory string) {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// The command writes its receipt relative to the working directory after successful planning.
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Cleanup restores process-global state for later command tests.
		if err := os.Chdir(original); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}
