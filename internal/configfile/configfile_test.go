package configfile_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/configfile"
)

// TestWorkspaceRejectsBucketCredentialState proves generic buckets cannot transport provider credential configuration.
func TestWorkspaceRejectsBucketCredentialState(t *testing.T) {
	_, err := configfile.Parse([]byte(`
apiVersion: fused/v1
kind: workspace
services:
  jira:
    versions: [{version: "v1"}]
buckets:
  default:
    service_config:
      jira:
        auth: {auth_type: bearer, token: $TOKEN}
`), "workspace.yaml")
	// Strict decoding must reject the retired provider-credential field while preserving generic buckets.
	if err == nil || !strings.Contains(err.Error(), "field service_config not found") {
		t.Fatalf("legacy workspace service_config error = %v", err)
	}
}

// TestWorkspaceBucketSecretsRemainApplyOnly preserves the generic webhook-secret path without admitting provider auth.
func TestWorkspaceBucketSecretsRemainApplyOnly(t *testing.T) {
	t.Setenv("FUSED_TEST_WEBHOOK_SECRET", "resolved-webhook-secret")
	parsed, err := configfile.Parse([]byte(`
apiVersion: fused/v1
kind: workspace
services: {}
buckets:
  prod:
    secrets:
      webhook_signing: $FUSED_TEST_WEBHOOK_SECRET
`), "workspace.yaml")
	// A generic named secret remains valid because it is not provider credential state.
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	materials, err := parsed.WorkspaceBucketSecretMaterials()
	// Apply-time resolution must preserve bucket and key scope without exposing the value in YAML.
	if err != nil || materials["prod\x00webhook_signing"] != "resolved-webhook-secret" {
		t.Fatalf("WorkspaceBucketSecretMaterials() = %#v, %v", materials, err)
	}
}

// TestWorkspaceBucketSecretsRejectLiteralValue keeps plaintext generic secrets out of workspace YAML.
func TestWorkspaceBucketSecretsRejectLiteralValue(t *testing.T) {
	_, err := configfile.Parse([]byte(`
apiVersion: fused/v1
kind: workspace
services: {}
buckets:
  prod:
    secrets:
      webhook_signing: literal
`), "workspace.yaml")
	// Only an environment reference may cross the declarative parse boundary.
	if err == nil || !strings.Contains(err.Error(), "requires a $ENV reference") {
		t.Fatalf("literal workspace bucket secret error = %v", err)
	}
}

// TestAppAuthReferenceRoundTripsSDKAndMCP proves both app kinds preserve one exact Engine-owned credential reference.
func TestAppAuthReferenceRoundTripsSDKAndMCP(t *testing.T) {
	for _, kind := range []string{"sdk", "mcp"} {
		// Shared app parsing must keep the same reference contract for generated and hosted runtimes.
		t.Run(kind, func(t *testing.T) {
			language := ""
			description := ""
			// Only SDK configs select a generated package language.
			if kind == "sdk" {
				language = "language: typescript\n"
			}
			// Only MCP configs advertise authored server identity during protocol initialization.
			if kind == "mcp" {
				description = "description: Find and coordinate customer support work.\n"
			}
			parsed, err := configfile.Parse([]byte(fmt.Sprintf(`apiVersion: fused/v1
kind: %s
name: support
version: 1.0.0
%s%sservices:
  confluence:
    version: v1
    operations: [issues.list]
    auth:
      type: oauth
      name: confluenceOAuth
      ref: "${bucket.auth.jira.jiraOAuth}"
`, kind, description, language)), kind+".yaml")
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			config := parsed.SDK
			// MCP and SDK aliases share one AppConfig transport shape.
			if kind == "mcp" {
				config = parsed.MCP
			}
			auth := config.Services["confluence"].Auth
			if auth == nil || auth.Type != "oauth" || auth.Name != "confluenceOAuth" || auth.Ref != "${bucket.auth.jira.jiraOAuth}" {
				t.Fatalf("auth reference changed: %#v", auth)
			}
			payload, err := json.Marshal(config)
			if err != nil || !strings.Contains(string(payload), `"ref":"${bucket.auth.jira.jiraOAuth}"`) {
				t.Fatalf("plan JSON lost auth ref: payload=%s err=%v", payload, err)
			}
		})
	}
}

// TestAppAuthReferenceRejectsInvalidSelectors pins the local syntax and OAuth/OIDC destination boundary.
func TestAppAuthReferenceRejectsInvalidSelectors(t *testing.T) {
	tests := []struct{ name, auth, want string }{
		{name: "static family", auth: `type: basic, name: target, ref: "${bucket.auth.jira.source}"`, want: "requires type oauth or oidc"},
		{name: "missing target name", auth: `type: oauth, ref: "${bucket.auth.jira.source}"`, want: "requires an exact name"},
		{name: "padded target name", auth: `type: oauth, name: " target ", ref: "${bucket.auth.jira.source}"`, want: "requires an exact name"},
		{name: "named bucket", auth: `type: oauth, name: target, ref: "${bucket.prod.auth.jira.source}"`, want: "must use ${bucket.auth.<source-service>.<source-authName>}"},
		{name: "padded reference", auth: `type: oauth, name: target, ref: " ${bucket.auth.jira.source}"`, want: "must use ${bucket.auth.<source-service>.<source-authName>}"},
		{name: "missing source auth", auth: `type: oidc, name: target, ref: "${bucket.auth.jira}"`, want: "must name one source service and auth scheme"},
		{name: "nested source", auth: `type: oauth, name: target, ref: "${bucket.auth.team.jira.source}"`, want: "must name one source service and auth scheme"},
		{name: "spaced source", auth: `type: oauth, name: target, ref: "${bucket.auth.jira team.source}"`, want: "must name one source service and auth scheme"},
		{name: "extra terminator", auth: `type: oauth, name: target, ref: "${bucket.auth.jira.source}}"`, want: "must name one source service and auth scheme"},
	}
	for _, test := range tests {
		// Each malformed selector passes through the production strict parser.
		t.Run(test.name, func(t *testing.T) {
			body := fmt.Sprintf(`apiVersion: fused/v1
kind: sdk
name: support
version: 1.0.0
language: typescript
services:
  confluence:
    version: v1
    operations: [issues.list]
    auth: {%s}
`, test.auth)
			_, err := configfile.Parse([]byte(body), "sdk.yaml")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestWorkspacePaginationV2IsRejected(t *testing.T) {
	_, err := configfile.ParseFile("testdata/workspace_pagination_v2.yaml")
	if err == nil || !strings.Contains(err.Error(), "field type not found") {
		t.Fatalf("legacy workspace pagination must fail strict YAML decoding, got %v", err)
	}
}

func TestWorkspacePaginationV3RoundTripsWithoutCLIInterpretation(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", "pagination", "v3_graphql_templates.json"))
	if err != nil {
		t.Fatal(err)
	}
	document := fmt.Sprintf(`{"apiVersion":"fused/v1","kind":"workspace","services":{"graphql":{"execution_policy":{"pagination":%s}}}}`, payload)
	parsed, err := configfile.Parse([]byte(document), "workspace.json")
	if err != nil {
		t.Fatalf("parse pagination v3: %v", err)
	}
	policy := parsed.Workspace.Services["graphql"].ExecutionPolicy.Pagination
	if policy == nil || policy.GraphQL == nil || len(policy.Continuation) != 1 {
		t.Fatalf("composable pagination changed: %#v", policy)
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CLI interpreted pagination v3\ngot:  %s\nwant: %s", encoded, payload)
	}
}

func TestWorkspacePaginationRejectsLegacyFieldsWithoutInventingCompatibility(t *testing.T) {
	legacy, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", "pagination", "invalid_legacy_shape.json"))
	if err != nil {
		t.Fatal(err)
	}
	document := fmt.Sprintf(`{"apiVersion":"fused/v1","kind":"workspace","services":{"legacy":{"execution_policy":{"pagination":%s}}}}`, legacy)
	_, err = configfile.Parse([]byte(document), "legacy.json")
	if err == nil || !strings.Contains(err.Error(), "request_param") {
		t.Fatalf("legacy pagination fields must be rejected by strict config decoding, got %v", err)
	}
}

func TestWorkspacePaginationRejectsLegacyMultipleStrategies(t *testing.T) {
	multiple, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", "pagination", "invalid_multiple_strategies.json"))
	if err != nil {
		t.Fatal(err)
	}
	document := fmt.Sprintf(`{"apiVersion":"fused/v1","kind":"workspace","services":{"mixed":{"execution_policy":{"pagination":%s}}}}`, multiple)
	_, err = configfile.Parse([]byte(document), "mixed.json")
	if err == nil || !strings.Contains(err.Error(), "type") {
		t.Fatalf("legacy pagination strategies must be rejected, got %v", err)
	}
}

func TestWorkspaceRateLimitV2IsRejected(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", "rate-limit", "v2_mixed.json"))
	if err != nil {
		t.Fatal(err)
	}
	document := fmt.Sprintf(`{"apiVersion":"fused/v1","kind":"workspace","services":{"drive":{"execution_policy":{"rate_limit":%s}}}}`, payload)
	_, err = configfile.Parse([]byte(document), "workspace.json")
	if err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("legacy rate-limit policy must be rejected, got %v", err)
	}
}

func TestWorkspaceQuotaAndRetryV3RoundTripWithoutCLIEnforcement(t *testing.T) {
	rateLimit := readContractFixture(t, "rate-limit", "v3_dynamic_headers.json")
	retry := readContractFixture(t, "retry", "v3_idempotency_predicates.json")
	document := fmt.Sprintf(`{"apiVersion":"fused/v1","kind":"workspace","services":{"api":{"execution_policy":{"rate_limit":%s,"retry":%s}}}}`, rateLimit, retry)
	parsed, err := configfile.Parse([]byte(document), "workspace.json")
	if err != nil {
		t.Fatalf("parse v3 execution policy: %v", err)
	}
	policy := parsed.Workspace.Services["api"].ExecutionPolicy
	if policy.RateLimit == nil || policy.Retry == nil {
		t.Fatalf("v3 policy missing: %#v", policy)
	}
	encodedRate, err := json.Marshal(policy.RateLimit)
	if err != nil {
		t.Fatal(err)
	}
	encodedRetry, err := json.Marshal(policy.Retry)
	if err != nil {
		t.Fatal(err)
	}
	assertSemanticPayload(t, encodedRate, rateLimit)
	assertSemanticPayload(t, encodedRetry, retry)
}

// TestWorkspaceSignaturePolicyRoundTripsAsSecretReferenceOnly ensures fixture
// cleanup cannot turn an out-of-band secret reference into persisted material.
func TestWorkspaceSignaturePolicyRoundTripsAsSecretReferenceOnly(t *testing.T) {
	signature := readContractFixture(t, "signature", "v1_raw_body_callback_signature.json")
	document := fmt.Sprintf(`{"apiVersion":"fused/v1","kind":"workspace","services":{"api":{"execution_policy":{"incoming_webhook_config":{"auth_type":"hmac_signature","signature_policy":%s}}}}}`, signature)
	parsed, err := configfile.Parse([]byte(document), "workspace.json")
	if err != nil {
		t.Fatalf("parse structured signature policy: %v", err)
	}
	policy := parsed.Workspace.Services["api"].ExecutionPolicy.IncomingWebhookConfig.SignaturePolicy
	if policy == nil || len(policy.Rules) != 1 {
		t.Fatalf("structured signature policy missing: %#v", policy)
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	assertSemanticPayload(t, encoded, signature)
}

func readContractFixture(t *testing.T, directory, name string) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", directory, name))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func assertSemanticPayload(t *testing.T, gotPayload, wantPayload []byte) {
	t.Helper()
	var got, want any
	if err := json.Unmarshal(gotPayload, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wantPayload, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("policy changed\ngot:  %s\nwant: %s", gotPayload, wantPayload)
	}
}

func TestWorkspaceRateLimitRejectsLegacyFieldsWithoutCompatibility(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", "rate-limit", "invalid_legacy.json"))
	if err != nil {
		t.Fatal(err)
	}
	document := fmt.Sprintf(`{"apiVersion":"fused/v1","kind":"workspace","services":{"legacy":{"execution_policy":{"rate_limit":%s}}}}`, payload)
	_, err = configfile.Parse([]byte(document), "workspace.json")
	if err == nil || !strings.Contains(err.Error(), "strategy") {
		t.Fatalf("legacy rate-limit fields must be rejected, got %v", err)
	}
}

func TestWorkspaceRateLimitLeavesDiscriminatorValidationToEngine(t *testing.T) {
	// Both branches are canonical fields; Engine remains responsible for the
	// semantic exactly-one-algorithm decision.
	payload := []byte(`{"version":3,"policies":[{"name":"conflicting","mode":"enforce","unit":"requests","identity":{"inputs":[{"kind":"service_version"}]},"cost":{"default":1,"rules":[]},"algorithm":"fixed_window","fixed_window":{"limit":100,"duration_ms":60000},"token_bucket":{"capacity":10,"refill_units":1,"refill_interval_ms":1000}}]}`)
	document := fmt.Sprintf(`{"apiVersion":"fused/v1","kind":"workspace","services":{"mixed":{"execution_policy":{"rate_limit":%s}}}}`, payload)
	parsed, err := configfile.Parse([]byte(document), "workspace.json")
	if err != nil {
		t.Fatalf("CLI must not duplicate Engine discriminator validation: %v", err)
	}
	policy := parsed.Workspace.Services["mixed"].ExecutionPolicy.RateLimit.Policies[0]
	if policy.FixedWindow == nil || policy.TokenBucket == nil {
		t.Fatalf("CLI normalized known algorithm branches: %#v", policy)
	}
}

func TestLoadRun_SingleSDKFile(t *testing.T) {
	path := writeFile(t, t.TempDir(), "fused.yaml", `
apiVersion: fused/v1
kind: sdk
name: security-detection
version: "1.2.0"
language: typescript
services:
  okta:
    version: "2026-07-01"
    operations:
      - listLogEvents
      - getUser
`)

	run, err := configfile.LoadRun(path)
	if err != nil {
		t.Fatalf("LoadRun failed: %v", err)
	}
	if len(run.Configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(run.Configs))
	}

	cfg := run.Configs[0]
	assertSingleSDKConfig(t, cfg)
}

func assertSingleSDKConfig(t *testing.T, cfg *configfile.ParsedConfig) {
	t.Helper()
	if cfg.Kind != configfile.KindSDK {
		t.Fatalf("kind: got %q", cfg.Kind)
	}
	if cfg.ConfigKey != "sdk:security-detection:1.2.0" {
		t.Errorf("config key: got %q", cfg.ConfigKey)
	}
	if cfg.SDK.Services["okta"].Version != "2026-07-01" {
		t.Errorf("service version was not parsed: %+v", cfg.SDK.Services["okta"])
	}
	if got := cfg.SDK.Services["okta"].Operations; len(got) != 2 || got[0] != "listLogEvents" || got[1] != "getUser" {
		t.Errorf("operations were not parsed: %+v", got)
	}
	if cfg.SourceHash == "" || !strings.HasPrefix(cfg.SourceHash, "sha256:") {
		t.Errorf("expected sha256 source hash, got %q", cfg.SourceHash)
	}
}

func TestLoadRun_SDKServiceVersionIsOptional(t *testing.T) {
	path := writeFile(t, t.TempDir(), "security.yaml", `
apiVersion: fused/v1
kind: sdk
name: security-detection
version: "1.2.0"
language: typescript
services:
  okta:
    operations:
      - listLogEvents
`)

	run, err := configfile.LoadRun(path)
	if err != nil {
		t.Fatalf("LoadRun failed: %v", err)
	}
	if got := run.Configs[0].SDK.Services["okta"].Version; got != "" {
		t.Fatalf("expected omitted SDK service version to remain empty before Engine plan, got %q", got)
	}
}

func TestLoadRun_SingleWorkspaceFile(t *testing.T) {
	path := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions:
      - version: "2026-07-01"
        service_version_id: "10000000-0000-0000-0000-000000000001"
      - version: "2026-08-01"
deprecations:
  - service_id: "00000000-0000-0000-0000-000000000001"
    version: "2026-07-01"
    effective_at: "2026-10-01"
    reason: "migration"
`)

	run, err := configfile.LoadRun(path)
	if err != nil {
		t.Fatalf("LoadRun failed: %v", err)
	}
	assertWorkspaceConfigParsed(t, run.Configs[0])
}

func assertWorkspaceConfigParsed(t *testing.T, cfg *configfile.ParsedConfig) {
	t.Helper()
	if cfg.Kind != configfile.KindWorkspace {
		t.Fatalf("kind: got %q", cfg.Kind)
	}
	if cfg.ConfigKey != "workspace" {
		t.Errorf("config key: got %q", cfg.ConfigKey)
	}
	versions := cfg.Workspace.Services["okta"].Versions
	if len(versions) != 2 || versions[0].Version != "2026-07-01" {
		t.Errorf("versions: got %+v", versions)
	}
	if got := cfg.Workspace.Services["okta"].ServiceID; got != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("service_id: got %q", got)
	}
	if got := versions[0].ServiceVersionID; got != "10000000-0000-0000-0000-000000000001" {
		t.Errorf("service_version_id not parsed: %+v", versions)
	}
	if got := cfg.Workspace.Deprecations; len(got) != 1 || got[0].EffectiveAt != "2026-10-01" {
		t.Errorf("deprecations not parsed: %+v", got)
	}
}

func TestLoadRun_WorkspaceAllowsSlugOnlyService(t *testing.T) {
	path := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  okta:
    versions: [{version: "2026-07-01"}]
`)

	run, err := configfile.LoadRun(path)
	if err != nil {
		t.Fatalf("LoadRun failed: %v", err)
	}
	if got := run.Configs[0].Workspace.Services["okta"].ServiceID; got != "" {
		t.Fatalf("expected slug-only service_id to remain empty before Engine plan, got %q", got)
	}
}

func TestWorkspaceExecutionPolicyTimeoutValidation(t *testing.T) {
	valid := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  stripe:
    execution_policy:
      timeout_ms: 45000
`)
	parsed, err := configfile.ParseFile(valid)
	if err != nil {
		t.Fatalf("valid timeout_ms: %v", err)
	}
	timeoutMs := parsed.Workspace.Services["stripe"].ExecutionPolicy.TimeoutMs
	if timeoutMs == nil || *timeoutMs != 45000 {
		t.Fatalf("timeout_ms = %v, want 45000", timeoutMs)
	}

	invalid := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  stripe:
    execution_policy:
      timeout_ms: 0
`)
	if _, err := configfile.ParseFile(invalid); err == nil || !strings.Contains(err.Error(), "timeout_ms") {
		t.Fatalf("invalid timeout error = %v", err)
	}
}

func TestWorkspaceExecutionPolicyServerVariablesValidation(t *testing.T) {
	valid := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  confluence:
    execution_policy:
      server_variables:
        your-domain: acme
        region: eu1
`)
	parsed, err := configfile.ParseFile(valid)
	if err != nil {
		t.Fatalf("valid server_variables: %v", err)
	}
	variables := parsed.Workspace.Services["confluence"].ExecutionPolicy.ServerVariables
	if !reflect.DeepEqual(variables, map[string]string{"your-domain": "acme", "region": "eu1"}) {
		t.Fatalf("server_variables = %#v", variables)
	}

	invalidCases := []string{
		"bad name: acme",
		"tenant: evil/path",
	}
	for _, variablesYAML := range invalidCases {
		path := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  confluence:
    execution_policy:
      server_variables:
        `+variablesYAML+`
`)
		if _, err := configfile.ParseFile(path); err == nil || !strings.Contains(err.Error(), "server_variables") {
			t.Fatalf("invalid server_variables %q error = %v", variablesYAML, err)
		}
	}
}

// ─── kind: webhook services[*].secret: ${bucket.<name>.secret.<key>} grammar ──
// (migrated from the removed runtime_config.webhooks -- see
// plans/plan-webhook-kind.md; no backward compatibility, so these now
// exercise kind: webhook's identical secret-ref grammar instead.)

// TestWebhookSecret_AcceptsExplicitBucketForm proves the full
// "${bucket.<name>.secret.<key>}" reference parses without error -- this is a
// syntax check only; Engine apply is the authority on whether the bucket
// actually exists.
func TestWebhookSecret_AcceptsExplicitBucketForm(t *testing.T) {
	path := writeFile(t, t.TempDir(), "webhook.yaml", `
apiVersion: fused/v1
kind: webhook
name: repo-a
services:
  github:
    secret: "${bucket.prod.secret.webhook_signing}"
`)
	if _, err := configfile.ParseFile(path); err != nil {
		t.Fatalf("expected explicit ${bucket.<name>.secret.<key>} to parse, got %v", err)
	}
}

// TestWebhookSecret_AcceptsDefaultBucketShorthand proves the
// bucket-name-omitted shorthand "${bucket.secret.<key>}" also parses.
func TestWebhookSecret_AcceptsDefaultBucketShorthand(t *testing.T) {
	path := writeFile(t, t.TempDir(), "webhook.yaml", `
apiVersion: fused/v1
kind: webhook
name: repo-a
services:
  github:
    secret: "${bucket.secret.webhook_signing}"
`)
	if _, err := configfile.ParseFile(path); err != nil {
		t.Fatalf("expected default-bucket shorthand to parse, got %v", err)
	}
}

// TestWebhookSecret_RejectsLiteralValue proves literals cannot bypass webhook reference validation.
func TestWebhookSecret_RejectsLiteralValue(t *testing.T) {
	path := writeFile(t, t.TempDir(), "webhook.yaml", `
apiVersion: fused/v1
kind: webhook
name: repo-a
services:
  github:
    secret: "whsec_literal_value"
`)
	_, err := configfile.ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), `"${bucket.<name>.secret.<key>}"`) {
		t.Fatalf("expected a malformed secret reference to be rejected, got %v", err)
	}
}

// TestWorkspaceProfileMaterialsResolvesBindingEnvOutOfBand proves plan
// data keeps the env reference while apply receives the local resolved value.
func TestWorkspaceProfileMaterialsResolvesBindingEnvOutOfBand(t *testing.T) {
	t.Setenv("SHOPIFY_API_VERSION", "2026-07")
	path := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  shopify:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions:
      - version: "2026-07-01"
        connection_profiles:
          - auth_type: oauth
            profile:
              auth_type: oauth
              resource_input:
                fields: [{name: shop, required: true}]
                base_url_template: "https://{shop}.myshopify.com"
                resource_type: shop
                allowed_hosts: ["*.myshopify.com"]
              bindings:
                - value: $SHOPIFY_API_VERSION
                  location: header
                  name: X-Shopify-API-Version
                  mode: force
`)
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	materials, err := parsed.WorkspaceProfileMaterials()
	if err != nil {
		t.Fatalf("WorkspaceProfileMaterials: %v", err)
	}
	if materials["shopify"].BindingValues["SHOPIFY_API_VERSION"] != "2026-07" {
		t.Fatalf("profile binding env was not handed off: %#v", materials["shopify"])
	}
}

// TestWorkspaceConnectionProfileDetachRequiresExclusiveIntent protects bucket
// routing from a config that simultaneously requests replacement and removal.
func TestWorkspaceConnectionProfileDetachRequiresExclusiveIntent(t *testing.T) {
	dir := t.TempDir()
	valid := writeFile(t, dir, "detach.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  jira:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions:
      - version: "2026-07-01"
        connection_profiles:
          - auth_type: oauth
            reset: true
`)
	if _, err := configfile.ParseFile(valid); err != nil {
		t.Fatalf("valid profile detach: %v", err)
	}
	conflict := writeFile(t, dir, "detach-conflict.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  jira:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions:
      - version: "2026-07-01"
        connection_profiles:
          - auth_type: oauth
            profile_id: "00000000-0000-0000-0000-000000000002"
            reset: true
`)
	if _, err := configfile.ParseFile(conflict); err == nil {
		t.Fatal("profile detach with profile_id was accepted")
	}
}

func TestWorkspaceConnectionProfileValidatesOAuth2FlowSelection(t *testing.T) {
	tests := []struct {
		name    string
		auth    string
		flow    string
		wantErr bool
	}{
		{name: "authorization code", auth: "oauth", flow: "authorizationCode"},
		{name: "client credentials", auth: "oauth2", flow: "clientCredentials"},
		{name: "unknown flow", auth: "oauth", flow: "deviceCode", wantErr: true},
		{name: "non oauth", auth: "mtls", flow: "authorizationCode", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeOAuth2FlowWorkspace(t, test.auth, test.flow)
			_, err := configfile.ParseFile(path)
			if (err != nil) != test.wantErr {
				t.Fatalf("ParseFile error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

// TestWorkspaceVersionRetainsInlineOAuthProfileWithTimeout proves the live Jira config shape survives CLI parsing.
func TestWorkspaceVersionRetainsInlineOAuthProfileWithTimeout(t *testing.T) {
	parsed, err := configfile.Parse([]byte(`
apiVersion: fused/v1
kind: workspace
services:
  jira:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions:
      - version: "2026-08-01"
        service_version_id: "00000000-0000-0000-0000-000000000002"
        execution_policy: {timeout_ms: 60000}
        connection_profiles:
          - auth_type: oauth
            profile:
              auth_type: oauth
              auth_name: JiraOAuth
              oauth2_flow: authorizationCode
              resource_discovery:
                version: 1
                stage: post_auth
                operation_id: getAccessibleResources
                server: api
                id_path: "$[*].id"
                name_path: "$[*].name"
                scopes_path: "$[*].scopes"
                base_url_template: "https://api.atlassian.com/ex/jira/{id}"
                resource_type: jira_site
                auto_run: after_oauth_callback
                lifecycle: authoritative
                allowed_hosts: [api.atlassian.com]
              bindings:
                - value: "${resource.base_url}"
                  location: base_url
                  mode: force
                  operations: [listProjects, listCreateIssueTypes, getCreateFieldMetadata, createIssue]
`), "workspace.yaml")
	// Valid raw entries must survive CLI parsing without Engine-owned reinterpretation.
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	version := parsed.Workspace.Services["jira"].Versions[0]
	assertJiraVersionPolicyFixture(t, version.ExecutionPolicy)
	profile := decodeJiraProfileFixture(t, version.ConnectionProfiles)
	if !reflect.DeepEqual(profile, expectedJiraProfileFixture()) {
		t.Fatalf("inline Jira profile changed: %#v", profile)
	}
}

// TestWorkspaceConnectionProfileRetainsOuterAuthName proves the raw CLI transport preserves Registry selection identity beside an inline profile.
func TestWorkspaceConnectionProfileRetainsOuterAuthName(t *testing.T) {
	parsed, err := configfile.Parse([]byte(`
apiVersion: fused/v1
kind: workspace
services:
  jira:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions:
      - version: "2026-08-01"
        connection_profiles:
          - auth_type: oauth
            auth_name: JiraOAuth
            profile:
              auth_type: oauth
              auth_name: JiraOAuth
`), "workspace.yaml")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	entry := parsed.Workspace.Services["jira"].Versions[0].ConnectionProfiles[0]
	// The outer selector is separate from the nested runtime profile identity.
	if entry["auth_name"] != "JiraOAuth" {
		t.Fatalf("outer auth_name changed during parsing: %#v", entry)
	}
	payload, err := json.Marshal(parsed.Workspace)
	// Plan marshaling must carry the same selector to Engine unchanged.
	if err != nil || !strings.Contains(string(payload), `"auth_name":"JiraOAuth"`) {
		t.Fatalf("outer auth_name changed during JSON transport: payload=%s err=%v", payload, err)
	}
}

type jiraProfileFixture struct {
	AuthType          string                       `json:"auth_type"`
	AuthName          string                       `json:"auth_name"`
	OAuth2Flow        string                       `json:"oauth2_flow"`
	ResourceDiscovery jiraResourceDiscoveryFixture `json:"resource_discovery"`
	Bindings          []jiraBindingFixture         `json:"bindings"`
}

type jiraResourceDiscoveryFixture struct {
	Version         int      `json:"version"`
	Stage           string   `json:"stage"`
	OperationID     string   `json:"operation_id"`
	Server          string   `json:"server"`
	IDPath          string   `json:"id_path"`
	NamePath        string   `json:"name_path"`
	ScopesPath      string   `json:"scopes_path"`
	BaseURLTemplate string   `json:"base_url_template"`
	ResourceType    string   `json:"resource_type"`
	AutoRun         string   `json:"auto_run"`
	Lifecycle       string   `json:"lifecycle"`
	AllowedHosts    []string `json:"allowed_hosts"`
}

type jiraBindingFixture struct {
	Value      string   `json:"value"`
	Location   string   `json:"location"`
	Mode       string   `json:"mode"`
	Operations []string `json:"operations"`
}

// assertJiraVersionPolicyFixture requires only the intended local timeout.
func assertJiraVersionPolicyFixture(t *testing.T, policy *configfile.ExecutionPolicy) {
	t.Helper()
	if policy == nil || policy.TimeoutMs == nil || *policy.TimeoutMs != 60000 {
		t.Fatalf("timeout-only version policy was not retained: %#v", policy)
	}
	if policy.Retry != nil || policy.RetryConfig != nil {
		t.Fatalf("unexpected retry policy: %#v", policy)
	}
}

// decodeJiraProfileFixture extracts the exact inline profile from raw CLI config data.
func decodeJiraProfileFixture(t *testing.T, intents []map[string]interface{}) jiraProfileFixture {
	t.Helper()
	if len(intents) != 1 || intents[0]["auth_type"] != "oauth" {
		t.Fatalf("inline OAuth profile intent was not retained: %#v", intents)
	}
	encoded, err := json.Marshal(intents[0]["profile"])
	if err != nil {
		t.Fatal(err)
	}
	var profile jiraProfileFixture
	if err := json.Unmarshal(encoded, &profile); err != nil {
		t.Fatal(err)
	}
	return profile
}

// expectedJiraProfileFixture returns the tenant-neutral routing contract owned by workspace YAML.
func expectedJiraProfileFixture() jiraProfileFixture {
	return jiraProfileFixture{
		AuthType: "oauth", AuthName: "JiraOAuth", OAuth2Flow: "authorizationCode",
		ResourceDiscovery: jiraResourceDiscoveryFixture{
			Version: 1, Stage: "post_auth", OperationID: "getAccessibleResources", Server: "api",
			IDPath: "$[*].id", NamePath: "$[*].name", ScopesPath: "$[*].scopes",
			BaseURLTemplate: "https://api.atlassian.com/ex/jira/{id}", ResourceType: "jira_site",
			AutoRun: "after_oauth_callback", Lifecycle: "authoritative", AllowedHosts: []string{"api.atlassian.com"},
		},
		Bindings: []jiraBindingFixture{{
			Value: "${resource.base_url}", Location: "base_url", Mode: "force",
			Operations: []string{"listProjects", "listCreateIssueTypes", "getCreateFieldMetadata", "createIssue"},
		}},
	}
}

func writeOAuth2FlowWorkspace(t *testing.T, authType, flow string) string {
	t.Helper()
	return writeFile(t, t.TempDir(), "workspace.yaml", fmt.Sprintf(`
apiVersion: fused/v1
kind: workspace
services:
  example:
    versions:
      - version: "v1"
        connection_profiles:
          - auth_type: %s
            profile:
              auth_type: %s
              oauth2_flow: %s
`, authType, authType, flow))
}

func TestLoadRun_DiscoversFusedFolderInOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".fused/workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: [{version: "2026-07-01"}]
`)
	writeFile(t, dir, ".fused/sdks/security.yaml", `
apiVersion: fused/v1
kind: sdk
name: security
version: "1.0.0"
language: typescript
services:
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`)

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	run, err := configfile.LoadRun("")
	if err != nil {
		t.Fatalf("LoadRun failed: %v", err)
	}
	if len(run.Configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(run.Configs))
	}
	if run.Configs[0].Kind != configfile.KindWorkspace || run.Configs[1].Kind != configfile.KindSDK {
		t.Fatalf("expected workspace before sdk, got %q then %q", run.Configs[0].Kind, run.Configs[1].Kind)
	}
}

func TestLoadRun_RejectsDuplicateConfigIdentities(t *testing.T) {
	dir := t.TempDir()
	writeSDK(t, dir, ".fused/sdks/one.yaml", "security")
	writeSDK(t, dir, ".fused/sdks/two.yaml", "security")

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	_, err = configfile.LoadRun("")
	if err == nil || !strings.Contains(err.Error(), "duplicate config identity") {
		t.Fatalf("expected duplicate config identity error, got %v", err)
	}
}

func TestLoadRun_DiscoversVersionedSDKAndMCPConfigs(t *testing.T) {
	dir := t.TempDir()
	writeSDK(t, dir, ".fused/sdks/security-v1.yaml", "security")
	writeFile(t, dir, ".fused/sdks/security-v2.yaml", `
apiVersion: fused/v1
kind: sdk
name: security
version: "2.0.0"
language: typescript
services:
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`)
	writeFile(t, dir, ".fused/mcps/security.yaml", `
apiVersion: fused/v1
kind: mcp
name: security
version: "1.0.0"
description: Search and review security events in Okta.
services:
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`)

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	run, err := configfile.LoadRun("")
	if err != nil {
		t.Fatalf("LoadRun failed: %v", err)
	}
	if len(run.Configs) != 3 {
		t.Fatalf("expected two SDK versions and one MCP config, got %d", len(run.Configs))
	}
	if run.Configs[2].ConfigKey != "mcp:security:1.0.0" {
		t.Fatalf("expected discovered MCP config, got %#v", run.Configs[2])
	}
}

func TestLoadRun_RejectsInvalidFiles(t *testing.T) {
	tests := map[string]string{
		"unknown kind": `
apiVersion: fused/v1
kind: service
`,
		"invalid language": `
apiVersion: fused/v1
kind: sdk
name: security
version: "1.0.0"
language: ruby
services:
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`,
		"invalid target": `
apiVersion: fused/v1
kind: sdk
name: security
version: "1.0.0"
language: typescript
target: mobile
services:
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`,
		"malformed sdk service": `
apiVersion: fused/v1
kind: sdk
name: security
version: "1.0.0"
language: typescript
services:
  okta:
    version: "2026-07-01"
`,
		"legacy endpoints key rejected": `
apiVersion: fused/v1
kind: sdk
name: security
version: "1.0.0"
language: typescript
services:
  okta:
    version: "2026-07-01"
    endpoints: ["listLogEvents"]
`,
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), "fused.yaml", body)
			if _, err := configfile.LoadRun(path); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func writeSDK(t *testing.T, dir, rel, name string) {
	t.Helper()
	writeFile(t, dir, rel, `
apiVersion: fused/v1
kind: sdk
name: `+name+`
version: "1.0.0"
language: typescript
services:
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`)
}

func writeFile(t *testing.T, dir, rel, body string) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseRejectsRetiredAppFields(t *testing.T) {
	for name, body := range map[string]string{
		"numeric version": "kind: sdk\nversion: 1\nname: reader\nsdkVersion: 1.0.0\nlanguage: typescript\nservices: {}\n",
		"sdkVersion":      "apiVersion: fused/v1\nkind: sdk\nname: reader\nversion: 1.0.0\nsdkVersion: 1.0.0\nlanguage: typescript\nservices: {}\n",
		"target":          "apiVersion: fused/v1\nkind: sdk\nname: reader\nversion: 1.0.0\nlanguage: typescript\ntarget: mcp\nservices: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := configfile.Parse([]byte(body), "test.yaml"); err == nil {
				t.Fatal("expected retired config field to be rejected")
			}
		})
	}
}

// TestParseMCPConfigCarriesAuthPolicyWithoutCredentials verifies MCP identity and routing policy remain credential-free.
func TestParseMCPConfigCarriesAuthPolicyWithoutCredentials(t *testing.T) {
	parsed, err := configfile.Parse([]byte(`
apiVersion: fused/v1
kind: mcp
name: github-agent
version: 1.0.0
description: Review repositories and help manage GitHub work.
bucket: customers
services:
  github:
    version: "2026-07-01"
    operations: [reposList]
    auth:
      type: oauth
      name: oauthAuth
    connect:
      scopes: [read:user]
`), "mcp.yaml")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	service := parsed.MCP.Services["github"]
	if parsed.ConfigKey != "mcp:github-agent:1.0.0" || parsed.MCP.Description != "Review repositories and help manage GitHub work." || service.Auth.Type != "oauth" || service.Connect.Scopes[0] != "read:user" {
		t.Fatalf("unexpected parsed MCP config: %#v", parsed)
	}
}

// TestParseMCPConfigRequiresServerDescription prevents newly planned servers from advertising only generic runtime identity.
func TestParseMCPConfigRequiresServerDescription(t *testing.T) {
	_, err := configfile.Parse([]byte(`
apiVersion: fused/v1
kind: mcp
name: github-agent
version: 1.0.0
services:
  github:
    version: "2026-07-01"
    operations: [reposList]
`), "mcp.yaml")
	// The diagnostic must name the missing authoring field so an agent can repair the config directly.
	if err == nil || !strings.Contains(err.Error(), "description") {
		t.Fatalf("missing description error = %v", err)
	}
}

// TestParseSDKConfigCarriesGenerateAsTriState proves absent and explicit true
// are distinguishable from an explicit false, so the historical
// always-build-a-package default survives configs written before the field.
func TestParseSDKConfigCarriesGenerateAsTriState(t *testing.T) {
	body := `
apiVersion: fused/v1
kind: sdk
name: ledger
version: 1.0.0
language: typescript
services:
  stripe:
    version: "v1"
    operations: [chargesList]
`
	absent, err := configfile.Parse([]byte(body), "sdk.yaml")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if absent.SDK.Generate != nil {
		t.Fatalf("absent generate must stay nil, got %#v", absent.SDK.Generate)
	}

	off, err := configfile.Parse([]byte(body+"generate: false\n"), "sdk.yaml")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if off.SDK.Generate == nil || *off.SDK.Generate {
		t.Fatalf("generate: false must decode to an explicit false, got %#v", off.SDK.Generate)
	}

	on, err := configfile.Parse([]byte(body+"generate: true\n"), "sdk.yaml")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if on.SDK.Generate == nil || !*on.SDK.Generate {
		t.Fatalf("generate: true must decode to an explicit true, got %#v", on.SDK.Generate)
	}
}

// TestParseMCPConfigRejectsGenerate guards the shared AppConfig: packaging has
// no meaning for an Engine-hosted MCP server.
func TestParseMCPConfigRejectsGenerate(t *testing.T) {
	_, err := configfile.Parse([]byte(`
apiVersion: fused/v1
kind: mcp
name: github-agent
version: 1.0.0
generate: false
services:
  github:
    version: "2026-07-01"
    operations: [reposList]
`), "mcp.yaml")
	if err == nil {
		t.Fatal("expected kind: mcp to reject generate")
	}
	if !strings.Contains(err.Error(), "generate") {
		t.Fatalf("error must name the field, got %q", err)
	}
}

// TestParseSDKConfigRejectsMCPDescription keeps server identity prose out of package-only immutable state.
func TestParseSDKConfigRejectsMCPDescription(t *testing.T) {
	_, err := configfile.Parse([]byte(`
apiVersion: fused/v1
kind: sdk
name: github-client
version: 1.0.0
language: typescript
description: Manage GitHub repositories.
services:
  github:
    version: "2026-07-01"
    operations: [reposList]
`), "sdk.yaml")
	// The diagnostic must identify the inert cross-kind field directly.
	if err == nil || !strings.Contains(err.Error(), "description") {
		t.Fatalf("SDK description error = %v", err)
	}
}
