package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

// initReadinessFixture uses the current Engine envelope, including a named scheme and exact bucket identity.
func initReadinessFixture(bucketName string) *api.CredentialReadiness {
	return &api.CredentialReadiness{Buckets: []api.MissingCredentialBucket{{ID: sdkPlanTestBucketID, Name: bucketName}}, MissingCredentials: []api.MissingCredentialRequirement{{
		ServiceID: sdkPlanTestServiceID, Service: "@payments/stripe", BucketID: sdkPlanTestBucketID, BucketName: bucketName,
		AuthType: "bearer", AuthName: "bearerAuth", RequiredFields: []api.MissingCredentialField{{Name: "token", SecretKey: "bearerAuth"}},
	}}}
}

// withInitCredentialChoices replaces only terminal interaction and rejects unexpected extra prompts.
func withInitCredentialChoices(t *testing.T, choices ...string) {
	t.Helper()
	previous := chooseInitCredentialAction
	t.Cleanup(func() { chooseInitCredentialAction = previous })
	chooseInitCredentialAction = func() (string, error) {
		// An extra prompt indicates a loop or an unexpected mutation boundary.
		if len(choices) == 0 {
			t.Error("unexpected credential review prompt")
			return "cancel", nil
		}
		action := choices[0]
		choices = choices[1:]
		return action, nil
	}
}

// TestInitCredentialReviewSharesSDKAndMCP exercises real plan transports, secure setup, browser review and automation.
func TestInitCredentialReviewSharesSDKAndMCP(t *testing.T) {
	for _, kind := range []configfile.ConfigKind{configfile.KindSDK, configfile.KindMCP} {
		for _, scenario := range []struct {
			name                                string
			choices                             []string
			interactive                         bool
			ready                               bool
			wantPlans, wantWrites, wantBrowsers int
			wantError                           bool
		}{
			{"proceed", []string{"proceed"}, true, false, 1, 0, 0, false},
			{"cancel", []string{"cancel"}, true, false, 1, 0, 0, true},
			{"recheck", []string{"recheck"}, true, true, 2, 0, 0, false},
			{"still_missing", []string{"recheck", "proceed"}, true, false, 2, 0, 0, false},
			{"browser", []string{"bucket", "recheck"}, true, true, 2, 0, 1, false},
			{"setup", []string{"setup"}, true, true, 2, 1, 0, false},
			{"no_input", nil, false, false, 1, 0, 0, false},
		} {
			t.Run(string(kind)+"/"+scenario.name, func(t *testing.T) {
				cfg := sdkPlanTestConfig()
				// MCP uses the same app document through its own Engine endpoint.
				if kind == configfile.KindMCP {
					cfg.Kind = kind
					cfg.MCP = cfg.SDK
					cfg.SDK = nil
					cfg.ConfigKey = "mcp:google:1.0.0"
				}
				withInitCredentialChoices(t, scenario.choices...)
				plans, writes, browsers := 0, 0, 0
				client := sdkPlanTestClient(t, func(request *http.Request) (int, string) {
					// Only reviewed setup can reach the ordinary secret API; apply remains outside this prepublication test.
					if request.URL.Path == "/workspace/secrets" {
						writes++
						var payload map[string]any
						decodeSDKPlanRequest(t, request, &payload)
						if payload["bucket_id"] != sdkPlanTestBucketID || payload["key_name"] != "bearerAuth" {
							t.Error("incorrect credential destination")
						}
						return http.StatusOK, `{}`
					}
					if request.URL.Path != "/"+string(kind)+"-config/plan" {
						t.Errorf("unexpected request %s", request.URL.Path)
						return 500, `{}`
					}
					plans++
					var readiness *api.CredentialReadiness
					// Readiness can clear only on the explicitly requested second plan.
					if plans == 1 || !scenario.ready {
						readiness = initReadinessFixture("production")
					}
					payload, err := json.Marshal(map[string]any{"plan_id": fmt.Sprintf("plan-%d", plans), "credential_readiness": readiness})
					if err != nil {
						t.Fatal(err)
					}
					return http.StatusOK, string(payload)
				})
				previousBrowser := openInitCredentialBrowser
				t.Cleanup(func() { openInitCredentialBrowser = previousBrowser })
				openInitCredentialBrowser = func(_ context.Context, link string) error {
					browsers++
					parsed, err := url.Parse(link)
					if err != nil || parsed.Host != "engine.test" || parsed.Query().Get("bucket") != sdkPlanTestBucketID || strings.Contains(link, "control-key") {
						t.Error("invalid bucket browser target")
					}
					return nil
				}
				withSDKPlanPromptFakes(t, func(*api.AuthConfig, string) (secretCredentialInput, error) {
					return secretCredentialInput{token: "test-only-value"}, nil
				}, func(string) (bool, error) { return true, nil })
				var out bytes.Buffer
				plan, err := planConfigWithRemediation(client, cfg, client.BaseURL, planOptions{interactive: scenario.interactive, reviewCredentials: true, output: &out})
				if (err != nil) != scenario.wantError || plans != scenario.wantPlans || writes != scenario.wantWrites || browsers != scenario.wantBrowsers {
					t.Fatalf("err=%v plans=%d writes=%d browsers=%d", err, plans, writes, browsers)
				}
				// Proceed and successful rechecks must return the latest exact receipt to the caller.
				if err == nil && plan.receipt.PlanID != fmt.Sprintf("plan-%d", plans) {
					t.Error("stale plan returned")
				}
				if strings.Contains(out.String(), "test-only-value") {
					t.Error("credential value leaked to output")
				}
			})
		}
	}
}

// TestInitCredentialCancellationStopsBeforePublication verifies init wiring, not just the shared reviewer in isolation.
func TestInitCredentialCancellationStopsBeforePublication(t *testing.T) {
	for _, mode := range []unifiedInitMode{unifiedInitModeSDK, unifiedInitModeMCP} {
		t.Run(string(mode), func(t *testing.T) {
			directory := t.TempDir()
			withUnifiedInitGenerationRepairWorkingDirectory(t, directory)
			t.Setenv("CI", "false")
			old := NoInput
			NoInput = false
			t.Cleanup(func() { NoInput = old })
			withInitCredentialChoices(t, "cancel")
			client := sdkPlanTestClient(t, func(request *http.Request) (int, string) {
				// Cancellation must not reach secret writes, app apply, package generation or download.
				if !strings.HasSuffix(request.URL.Path, "-config/plan") {
					t.Errorf("unexpected mutation %s", request.URL.Path)
					return 500, `{}`
				}
				payload, _ := json.Marshal(map[string]any{"plan_id": "cancelled", "credential_readiness": initReadinessFixture("default")})
				return 200, string(payload)
			})
			path := filepath.Join(directory, "draft.yaml")
			request := unifiedInitFailureTestRequest(path)
			// MCP's config identity requires a description before it can reach credential review.
			if mode == unifiedInitModeMCP {
				request.kind = configfile.KindMCP
				request.description = "Create customers in Stripe"
			}
			command := &cobra.Command{}
			command.SetOut(io.Discard)
			err := createPlanApplyUnifiedInit(command, client, mode, request, false, false, noOpScaffoldRequirements, defaultTestScaffoldBucket)
			if err == nil || !strings.Contains(err.Error(), "app creation cancelled") {
				t.Fatalf("unexpected cancellation: %v", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Error("cancelled init published its candidate")
			}
		})
	}
}

// TestCredentialReadinessUsesServiceBucketOverrides proves modern responses route to the reviewed bucket and reject alien destinations.
func TestCredentialReadinessUsesServiceBucketOverrides(t *testing.T) {
	cfg := sdkPlanTestConfig()
	cfg.SDK.Services = map[string]configfile.AppService{"stripe": {Version: "v1", Bucket: "billing"}}
	readiness := initReadinessFixture("billing")
	targets, err := credentialReadinessTargets(cfg, readiness)
	if err != nil || len(targets) != 1 || targets[0].bucket.Name != "billing" {
		t.Fatalf("override: %v", err)
	}
	readiness.Buckets[0].Name = "unrelated"
	readiness.MissingCredentials[0].BucketName = "unrelated"
	if _, err := credentialReadinessTargets(cfg, readiness); err == nil {
		t.Error("accepted a bucket outside the app config")
	}
}
