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
	"github.com/spf13/cobra"
)

// TestSDKOperationSearchLabelIncludesAdmittedMetadata protects every useful keyboard-filter dimension in the Registry projection.
func TestSDKOperationSearchLabelIncludesAdmittedMetadata(t *testing.T) {
	label := sdkOperationSearchLabel(api.Integration{
		Name: "issueUpdate", Method: "patch", Path: "/issues/{id}", Description: "Legacy update wording",
		Documentation: &api.OperationDocumentation{
			Summary: "Update an issue", Description: "Changes the requested issue", Tags: []string{"issues", "write"},
		},
	})
	for _, token := range []string{"issueUpdate", "PATCH", "/issues/{id}", "Legacy update wording", "Update an issue", "Changes the requested issue", "issues", "write"} {
		// Each asserted token is text huh can match with its built-in case-insensitive filter.
		if !strings.Contains(label, token) {
			t.Errorf("search label %q does not contain %q", label, token)
		}
	}
}

// TestRequireSDKOperationSelectionRejectsEmptyNarrowing keeps Choose operations distinct from the complete-surface choice.
func TestRequireSDKOperationSelectionRejectsEmptyNarrowing(t *testing.T) {
	// Empty explicit scope is invalid because the caller must go back to the visible All operations choice.
	if err := requireSDKOperationSelection(nil); err == nil || !strings.Contains(err.Error(), "choose All operations") {
		t.Fatalf("empty selection error = %v", err)
	}
	if err := requireSDKOperationSelection([]string{"issueUpdate"}); err != nil {
		t.Fatalf("valid operation selection: %v", err)
	}
}

// TestPromptSDKOperationSelectionEnterAcceptsVisibleAllDefault exercises the exact one-keystroke onboarding path.
func TestPromptSDKOperationSelectionEnterAcceptsVisibleAllDefault(t *testing.T) {
	selection, err := promptSDKOperationSelection(strings.NewReader("\r"), io.Discard, "linear", "v1", []api.Integration{{Name: "issueUpdate"}})
	if err != nil {
		t.Fatalf("accept visible All operations default: %v", err)
	}
	// Enter confirms the highlighted first row and returns structured select_all intent.
	if !selection.selectAll || len(selection.operations) != 1 || selection.operations[0] != "issueUpdate" {
		t.Fatalf("selection = %#v", selection)
	}
}

// TestSelectSDKOperationsForServicePreservesAllChoice verifies init can persist select_all while sharing the operation-add selector.
func TestSelectSDKOperationsForServicePreservesAllChoice(t *testing.T) {
	server, _ := newSDKInitLifecycleServer(t)
	defer server.Close()
	restore := stubSDKOperationSelectionRunner(t, func(_ io.Reader, _ io.Writer, serviceName, serviceVersion string, endpoints []api.Integration) (sdkOperationSelection, error) {
		if serviceName != "linear" || serviceVersion != "v1" || len(endpoints) != 1 || endpoints[0].Name != "issueUpdate" {
			t.Fatalf("selector input = %s %s %#v", serviceName, serviceVersion, endpoints)
		}
		return sdkOperationSelection{operations: operationNames(endpoints), selectAll: true}, nil
	})
	defer restore()

	operations, selectAll, err := selectSDKOperationsForService(api.NewClient(server.URL, "test-key"), "00000000-0000-4000-8000-000000000001", "linear", "v1", nil, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("select all operations: %v", err)
	}
	if !selectAll || len(operations) != 1 || operations[0] != "issueUpdate" {
		t.Fatalf("selection = %#v, selectAll=%v", operations, selectAll)
	}
}

// TestSDKInitOperationSelectorsShareOneInputStream protects sequential prompts from losing buffered keyboard input between services.
func TestSDKInitOperationSelectorsShareOneInputStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode Registry request: %v", err)
		}
		serviceID, _ := body.Variables["serviceId"].(string)
		_, _ = writer.Write([]byte(`{"data":{"searchEndpoints":[{"id":"endpoint-1","name":"run","method":"POST","path":"/run","service_id":"` + serviceID + `"}]}}`))
	}))
	defer server.Close()

	oldInput, oldNoInput := sdkInput, NoInput
	sdkInput = strings.NewReader("12")
	NoInput = false
	t.Setenv("CI", "")
	t.Cleanup(func() {
		sdkInput, NoInput = oldInput, oldNoInput
	})
	var keys strings.Builder
	restore := stubSDKOperationSelectionRunner(t, func(input io.Reader, _ io.Writer, _ string, _ string, endpoints []api.Integration) (sdkOperationSelection, error) {
		var key [1]byte
		if _, err := io.ReadFull(input, key[:]); err != nil {
			return sdkOperationSelection{}, err
		}
		keys.WriteByte(key[0])
		// The second prompt exercises the complete-surface branch on the same reader.
		if key[0] == '2' {
			return sdkOperationSelection{operations: operationNames(endpoints), selectAll: true}, nil
		}
		return sdkOperationSelection{operations: operationNames(endpoints)}, nil
	})
	t.Cleanup(restore)

	request, err := completeSDKInitOperationSelections(newCommandWithDiscardedOutput(), api.NewClient(server.URL, "test-key"), scaffoldRequest{}, []sdkInitResolvedService{
		{target: workspaceServiceAddTarget{slug: "linear", serviceID: "service-linear"}, version: "v1"},
		{target: workspaceServiceAddTarget{slug: "slack", serviceID: "service-slack"}, version: "v2"},
	})
	if err != nil {
		t.Fatalf("select operations across services: %v", err)
	}
	if keys.String() != "12" || len(request.operations) != 1 || len(request.selectAll) != 1 || request.selectAll[0] != "slack" {
		t.Fatalf("keys=%q operations=%#v selectAll=%#v", keys.String(), request.operations, request.selectAll)
	}
}

// newCommandWithDiscardedOutput provides the minimal Cobra output boundary required by SDK init selection tests.
func newCommandWithDiscardedOutput() *cobra.Command {
	command := &cobra.Command{}
	command.SetOut(io.Discard)
	return command
}

// stubSDKOperationSelectionRunner installs one deterministic terminal choice and returns a restoration closure.
func stubSDKOperationSelectionRunner(t *testing.T, runner func(io.Reader, io.Writer, string, string, []api.Integration) (sdkOperationSelection, error)) func() {
	t.Helper()
	oldRunner := sdkOperationSelectionRunner
	sdkOperationSelectionRunner = runner
	return func() {
		// Global command seams must be restored so unrelated command tests retain the production prompt.
		sdkOperationSelectionRunner = oldRunner
	}
}
