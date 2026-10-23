package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestSharedAppInvokeRejectsMCPBeforeTokenRead keeps MCP execution on its protocol transport even without a runtime token.
func TestSharedAppInvokeRejectsMCPBeforeTokenRead(t *testing.T) {
	for _, source := range []string{"missing", "environment", "stdin"} {
		// Every token source must produce the same capability error without attempting execution.
		t.Run(source, func(t *testing.T) {
			resetSDKInvokeTestState(t)
			t.Setenv(defaultSDKTokenEnvironment, "")
			// A supplied environment token must not enable unsupported CLI execution.
			if source == "environment" {
				t.Setenv(defaultSDKTokenEnvironment, "test-runtime-token")
			}
			sdkInvokeTokenStdin = source == "stdin"
			metadataReads, runtimeRequests := 0, 0
			// Only authoritative app metadata is needed to explain the unsupported transport.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Any non-management request would reproduce the live Engine's misleading 403.
				if r.URL.Path != "/engine/graphql" {
					runtimeRequests++
					http.Error(w, "execution must not be attempted", http.StatusForbidden)
					return
				}
				query := decodeGraphQLTestRequest(t, r)
				// Resolve the exact version before reporting its authoritative kind.
				if strings.Contains(query.Query, "appReference(") {
					fmt.Fprintf(w, `{"data":{"appReference":{"id":%q,"kind":"app"}}}`, sdkInvokeTestAppID)
					return
				}
				metadataReads++
				fmt.Fprintf(w, `{"data":{"app":{"app_id":%q,"kind":"mcp"}}}`, sdkInvokeTestAppID)
			}))
			defer server.Close()
			EngineURL, APIKey = server.URL, "test-control-key"
			input := bytes.NewBufferString("test-runtime-token")
			command := &cobra.Command{Use: "invoke"}
			command.SetIn(input)
			err := runSharedAppInvoke(command, []string{sdkInvokeTestAppID, "listIssues"})
			var invocationError *sdkInvokeError
			// The typed validation error must guide both humans and JSON callers to an MCP client.
			if !errors.As(err, &invocationError) || invocationError.code != "unsupported_app_type" || invocationError.category != "validation" || !strings.Contains(err.Error(), "use an MCP client") {
				t.Fatalf("unexpected MCP invocation error: %v", err)
			}
			// Even stdin token bytes remain unread when the selected app cannot use this execution surface.
			if metadataReads != 1 || runtimeRequests != 0 || input.String() != "test-runtime-token" {
				t.Fatalf("metadata=%d runtime=%d remaining input=%q", metadataReads, runtimeRequests, input.String())
			}
		})
	}
}

// TestSharedAppInvokePreservesSupportedRoutes protects SDK/REST and Unified App dispatch while excluding MCP.
func TestSharedAppInvokePreservesSupportedRoutes(t *testing.T) {
	const familyID = "0fe91476-b2dc-47e9-bc04-875ae79208be"
	for _, kind := range []string{"sdk", "unified_app"} {
		// REST apps share the SDK kind; hosted execution uses a distinct family route and envelope.
		t.Run(kind, func(t *testing.T) {
			resetSDKInvokeTestState(t)
			t.Setenv(defaultSDKTokenEnvironment, "test-runtime-token")
			operation, runtimeID := "listIssues", sdkInvokeTestAppID
			// Unified Apps execute authored code through their family with an exact-version fence.
			if kind == "unified_app" {
				operation, runtimeID = "execute", familyID
			}
			runtimeRequests := 0
			// Simulate metadata resolution and verify the sole supported runtime request.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Management metadata must remain separate from the runtime bearer request.
				if r.URL.Path == "/engine/graphql" {
					query := decodeGraphQLTestRequest(t, r)
					// Exact name resolution precedes app-kind inspection.
					if strings.Contains(query.Query, "appReference(") {
						fmt.Fprintf(w, `{"data":{"appReference":{"id":%q,"kind":"app"}}}`, sdkInvokeTestAppID)
						return
					}
					fmt.Fprintf(w, `{"data":{"app":{"app_id":%q,"app_family_id":%q,"kind":%q}}}`, sdkInvokeTestAppID, familyID, kind)
					return
				}
				runtimeRequests++
				// Supported dispatch must retain the correct identity and token boundary.
				if r.URL.Path != "/v1/apps/"+runtimeID+"/executions" || r.Header.Get("Authorization") != "Bearer test-runtime-token" || r.Header.Get("X-API-Key") != "" {
					t.Errorf("incorrect runtime route or credentials: %s", r.URL.Path)
				}
				// Hosted execution and physical operations deliberately return different success envelopes.
				if kind == "unified_app" {
					body := decodeSDKInvokeTestRequest(t, r)
					// Family routing cannot silently invoke another immutable version.
					if body.ExpectedAppID != sdkInvokeTestAppID {
						t.Error("missing exact-version fence")
					}
					fmt.Fprint(w, `{"status":"succeeded","output":{"ok":true}}`)
					return
				}
				fmt.Fprintf(w, `{"app_id":%q,"operation":%q,"kind":"physical","status_code":200,"results":[{"ok":true}]}`, sdkInvokeTestAppID, operation)
			}))
			defer server.Close()
			EngineURL, APIKey = server.URL, "test-control-key"
			command := &cobra.Command{Use: "invoke"}
			command.SetOut(&bytes.Buffer{})
			// One successful execution demonstrates the kind check did not disable supported routes.
			if err := runSharedAppInvoke(command, []string{sdkInvokeTestAppID, operation}); err != nil || runtimeRequests != 1 {
				t.Fatalf("invoke error=%v runtime requests=%d", err, runtimeRequests)
			}
		})
	}
}
