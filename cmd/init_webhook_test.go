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

	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

// TestWebhookInitFlagsProduceIngressConfig checks the public command's routing and secret-reference boundary.
func TestWebhookInitFlagsProduceIngressConfig(t *testing.T) {
	var request scaffoldRequest
	// Capture resolved local intent without permitting an Engine call.
	executeUnifiedInitForTest(t, func(_ *cobra.Command, mode unifiedInitMode, got scaffoldRequest) error {
		// A webhook must never enter an SDK or MCP lifecycle by alias.
		if mode != unifiedInitModeWebhook {
			t.Fatalf("mode=%s", mode)
		}
		request = got
		return nil
	}, "alerts", "--webhook", "--service", "linear", "--secret", "linear=${bucket.default.secret.signing}")
	data, _, err := newScaffoldData(request, nil, nil)
	// Nil app resolvers prove webhook scaffolding requires neither app operations nor an implicit bucket.
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := configfile.Parse(data, request.path)
	// The generated document must survive the ordinary loader with the exact signing reference.
	if err != nil || parsed.Kind != configfile.KindWebhook || parsed.Webhook.Services["linear"].Secret != "${bucket.default.secret.signing}" || request.path != filepath.Join(".fused", "webhooks", "alerts.yaml") {
		t.Fatalf("request=%+v parsed=%+v err=%v", request, parsed, err)
	}
}

// TestWebhookInitIgnoresNoTokenFlag proves --no-token is accepted rather than
// rejected for --webhook: a webhook registration never issues an execution
// token, so the flag is simply irrelevant here instead of an error.
func TestWebhookInitIgnoresNoTokenFlag(t *testing.T) {
	var reached bool
	executeUnifiedInitForTest(t, func(_ *cobra.Command, mode unifiedInitMode, _ scaffoldRequest) error {
		reached = true
		if mode != unifiedInitModeWebhook {
			t.Fatalf("mode=%s", mode)
		}
		return nil
	}, "alerts", "--webhook", "--no-token", "--service", "linear", "--secret", "linear=${bucket.default.secret.signing}")
	if !reached {
		t.Fatal("--no-token with --webhook must reach the lifecycle runner, not fail validation")
	}
}

// TestWebhookInitRejectsAppFlagsAndLiteralSecrets keeps invalid ingress intent entirely local.
func TestWebhookInitRejectsAppFlagsAndLiteralSecrets(t *testing.T) {
	tests := [][]string{{"--sdk"}, {"--operation", "linear=issueUpdate"}, {"--select-all", "linear"}, {"--version", "2.0.0"}, {"--language", "python"}, {"--bucket", "default"}, {"--extend"}, {"--secret", "linear=literal-do-not-echo"}, {"--secret", "other=${bucket.default.secret.signing}"}}
	for _, extra := range tests {
		old := NoInput
		NoInput = true
		// Any invoked runner would prove validation happened too late.
		cmd := newUnifiedInitCommandWithRunner(func(*cobra.Command, unifiedInitMode, scaffoldRequest) error {
			t.Fatal("invalid input reached lifecycle")
			return nil
		})
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		cmd.SilenceUsage = true
		cmd.SetArgs(append([]string{"alerts", "--webhook", "--service", "linear"}, extra...))
		err := cmd.Execute()
		NoInput = old
		// Literal signing material must not be reflected into errors or command output.
		if err == nil || strings.Contains(err.Error(), "literal-do-not-echo") || strings.Contains(output.String(), "literal-do-not-echo") {
			t.Fatalf("flags=%v err=%v output=%s", extra, err, output.String())
		}
	}
}

// TestWebhookInitLifecycle uses the real root command and generic plan/apply machinery against an isolated Engine fixture.
func TestWebhookInitLifecycle(t *testing.T) {
	for _, scenario := range []string{"apply", "deferred", "plan-rejected"} {
		// Each scenario owns its local config, receipts, and Engine request ledger.
		t.Run(scenario, func(t *testing.T) {
			directory := t.TempDir()
			withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
			base, calls := newSDKInitLifecycleServer(t)
			defer base.Close()
			// Only webhook endpoints augment the shared workspace fixture; app endpoints remain visible in its call ledger.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				// The endpoint dispatch proves this mode never requests SDK generation or MCP creation.
				switch r.URL.Path {
				case "/webhook-config/plan":
					*calls = append(*calls, "webhook-plan")
					// Rejection after workspace activation must leave no speculative webhook config.
					if scenario == "plan-rejected" {
						http.Error(w, `{"error":"rejected"}`, 400)
						return
					}
					writeSDKInitPlanResponse(t, w, r, "plan-webhook")
				case "/webhook-config/apply":
					*calls = append(*calls, "webhook-apply")
					_, _ = w.Write([]byte(`{"name":"alerts","registrations":[{"service":"linear","slug":"alerts"}]}`))
				default:
					base.Config.Handler.ServeHTTP(w, r)
				}
			}))
			defer server.Close()
			oldURL, oldKey, oldFile, oldInput := EngineURL, APIKey, ConfigFile, NoInput
			// Restore process-global CLI configuration after the isolated command completes.
			t.Cleanup(func() { EngineURL, APIKey, ConfigFile, NoInput = oldURL, oldKey, oldFile, oldInput })
			EngineURL, APIKey, ConfigFile, NoInput = server.URL, "fsk_test", "", true
			args := []string{"alerts", "--webhook", "--service", "linear", "--secret", "linear=${bucket.default.secret.signing}"}
			// Deferred mode must stop before either workspace or webhook apply.
			if scenario == "deferred" {
				args = append(args, "--no-apply")
			}
			cmd := newUnifiedInitCommand()
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs(args)
			err := cmd.Execute()
			path := filepath.Join(directory, ".fused", "webhooks", "alerts.yaml")
			// A failed plan leaves only the already-committed workspace boundary visible.
			if scenario == "plan-rejected" {
				// The registration plan failure must remain visible after workspace success.
				if err == nil {
					t.Fatal("expected rejected plan")
				}
				// Rejected plans must not leave a file implying an accepted registration.
				if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("speculative config remains: %v", statErr)
				}
				return
			}
			// Successful and deferred runs both publish a valid typed registration.
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := configfile.ParseFile(path)
			// The stored reference must survive service resolution and atomic publication.
			if err != nil || parsed.Webhook.Services["linear"].Secret != "${bucket.default.secret.signing}" {
				t.Fatalf("parsed=%+v err=%v", parsed, err)
			}
			want := "workspace-plan,workspace-apply,webhook-plan,webhook-apply"
			// The deferred fixture has missing workspace services, so no webhook plan can exist yet.
			if scenario == "deferred" {
				want = "workspace-plan"
			}
			// Exact ordering guards against accidental app generation or deferred mutations.
			if strings.Join(*calls, ",") != want {
				t.Fatalf("calls=%v want=%s", *calls, want)
			}
		})
	}
}
