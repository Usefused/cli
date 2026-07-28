package configfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/configfile"
)

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
      - "2026-07-01"
      - "2026-08-01"
    resolved_versions:
      - version: "2026-07-01"
        service_version_id: "10000000-0000-0000-0000-000000000001"
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
	if got := cfg.Workspace.Services["okta"].Versions; len(got) != 2 || got[0] != "2026-07-01" {
		t.Errorf("versions: got %q", got)
	}
	if got := cfg.Workspace.Services["okta"].ServiceID; got != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("service_id: got %q", got)
	}
	if got := cfg.Workspace.Services["okta"].ResolvedVersions; len(got) != 1 || got[0].ServiceVersionID != "10000000-0000-0000-0000-000000000001" {
		t.Errorf("resolved_versions not parsed: %+v", got)
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
    versions: ["2026-07-01"]
`)

	run, err := configfile.LoadRun(path)
	if err != nil {
		t.Fatalf("LoadRun failed: %v", err)
	}
	if got := run.Configs[0].Workspace.Services["okta"].ServiceID; got != "" {
		t.Fatalf("expected slug-only service_id to remain empty before Engine plan, got %q", got)
	}
}

func TestWorkspaceConnectMaterials_RejectsLegacyConnectEnvFields(t *testing.T) {
	path := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
buckets:
  prod:
    service_config:
      github:
        connect:
          auth_type: oauth
          client_id_env: FUSED_TEST_CLIENT_ID
          client_secret_env: FUSED_TEST_CLIENT_SECRET
          redirect_uri: https://engine.example.com/connect/callback
`)
	_, err := configfile.ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "not *_env fields") {
		t.Fatalf("expected legacy *_env rejection, got %v", err)
	}
}

func TestWorkspaceConnectMaterials_ResolvesDollarEnvRefs(t *testing.T) {
	t.Setenv("FUSED_TEST_CLIENT_ID", "resolved-client")
	t.Setenv("FUSED_TEST_CLIENT_SECRET", "resolved-secret")
	path := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
buckets:
  prod:
    service_config:
      github:
        connect:
          auth_type: oauth
          client_id: $FUSED_TEST_CLIENT_ID
          client_secret: ${FUSED_TEST_CLIENT_SECRET}
          redirect_uri: https://engine.example.com/connect/callback
`)
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	materials, err := parsed.WorkspaceConnectMaterials()
	if err != nil {
		t.Fatalf("WorkspaceConnectMaterials failed: %v", err)
	}
	material := materials["prod\x00github"]
	if material.ClientID != "resolved-client" || material.ClientSecret != "resolved-secret" {
		t.Fatalf("expected dollar env refs resolved, got %#v", material)
	}
}

// TestWorkspaceConnectMaterials_PassesThroughBucketSecretRef proves a
// client_secret written as the ambient ${bucket.secret.<key>} shorthand
// parses/validates without a local env var present, and passes through
// unresolved -- Engine, not the CLI, resolves this form server-side at
// apply time against the connect config's own already-known bucket.
func TestWorkspaceConnectMaterials_PassesThroughBucketSecretRef(t *testing.T) {
	path := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
buckets:
  prod:
    service_config:
      github:
        connect:
          auth_type: oauth
          client_id: some-client-id
          client_secret: "${bucket.secret.GITHUB_SECRET}"
          redirect_uri: https://engine.example.com/connect/callback
`)
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	materials, err := parsed.WorkspaceConnectMaterials()
	if err != nil {
		t.Fatalf("WorkspaceConnectMaterials failed: %v", err)
	}
	material := materials["prod\x00github"]
	if material.ClientSecret != "${bucket.secret.GITHUB_SECRET}" {
		t.Fatalf("expected the bucket secret reference to pass through unresolved, got %#v", material)
	}
}

// TestWorkspaceConnectMaterials_RejectsNamedBucketSecretRef proves the
// named-bucket form kind: webhook accepts is rejected for connect -- a
// connect config already belongs to one specific bucket, so naming a
// different one would only ever mean reading a secret out of the wrong
// bucket.
func TestWorkspaceConnectMaterials_RejectsNamedBucketSecretRef(t *testing.T) {
	path := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
buckets:
  prod:
    service_config:
      github:
        connect:
          auth_type: oauth
          client_id: some-client-id
          client_secret: "${bucket.prod.secret.GITHUB_SECRET}"
          redirect_uri: https://engine.example.com/connect/callback
`)
	_, err := configfile.ParseFile(path)
	if err == nil {
		t.Fatal("expected the named-bucket form to be rejected for connect client_secret")
	}
}

// TestWorkspaceBucketSecrets_RejectsLiteralValue pins
// plans/plan-service-config-restructure.md item 4's core safety property:
// buckets.<name>.secrets.<key> must be a $ENV reference, never a literal, the
// same discipline already enforced for Auth/Connect fields.
func TestWorkspaceBucketSecrets_RejectsLiteralValue(t *testing.T) {
	path := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
buckets:
  prod:
    secrets:
      webhook_signing: not-an-env-ref
`)
	_, err := configfile.ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "requires a $ENV reference") {
		t.Fatalf("expected literal bucket secret rejection, got %v", err)
	}
}

// TestWorkspaceBucketSecretMaterials_ResolvesDollarEnvRefs mirrors
// TestWorkspaceConnectMaterials_ResolvesDollarEnvRefs for the new generic
// bucket secrets field -- plan/state keeps the $ENV ref, apply resolves it
// out-of-band, keyed the same "<bucket>\x00<key>" way as auth/connect
// material.
func TestWorkspaceBucketSecretMaterials_ResolvesDollarEnvRefs(t *testing.T) {
	t.Setenv("FUSED_TEST_WEBHOOK_SECRET", "resolved-webhook-secret")
	path := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
buckets:
  prod:
    secrets:
      webhook_signing: $FUSED_TEST_WEBHOOK_SECRET
`)
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	materials, err := parsed.WorkspaceBucketSecretMaterials()
	if err != nil {
		t.Fatalf("WorkspaceBucketSecretMaterials failed: %v", err)
	}
	if got := materials["prod\x00webhook_signing"]; got != "resolved-webhook-secret" {
		t.Fatalf("expected resolved secret value, got %q", got)
	}
}

// TestWorkspaceBucketSecretMaterials_MissingEnvVarErrors ensures apply fails
// loudly (not with a silently empty secret) when the referenced environment
// variable is not set locally.
func TestWorkspaceBucketSecretMaterials_MissingEnvVarErrors(t *testing.T) {
	path := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
buckets:
  prod:
    secrets:
      webhook_signing: $FUSED_TEST_UNSET_WEBHOOK_SECRET
`)
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	if _, err := parsed.WorkspaceBucketSecretMaterials(); err == nil {
		t.Fatal("expected an error for an unset environment variable")
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

// TestWebhookSecret_RejectsLiteralValue proves a literal (or any other
// malformed) value fails validation at parse time instead of only being
// discovered once Engine apply rejects the reference -- mirrors
// TestWorkspaceBucketSecrets_RejectsLiteralValue's discipline for the
// declaration side of this mechanism.
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

// TestWorkspaceConnectMaterialsResolvesProfileBindingEnvOutOfBand proves plan
// data keeps the env reference while apply receives the local resolved value.
func TestWorkspaceConnectMaterialsResolvesProfileBindingEnvOutOfBand(t *testing.T) {
	t.Setenv("FUSED_TEST_CLIENT_ID", "resolved-client")
	t.Setenv("FUSED_TEST_CLIENT_SECRET", "resolved-secret")
	t.Setenv("SHOPIFY_API_VERSION", "2026-07")
	path := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  shopify:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
    connection_profiles:
      - version: "2026-07-01"
        auth_type: oauth
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
buckets:
  prod:
    service_config:
      shopify:
        connect:
          auth_type: oauth
          client_id: $FUSED_TEST_CLIENT_ID
          client_secret: $FUSED_TEST_CLIENT_SECRET
          redirect_uri: https://engine.example.com/connect/callback
`)
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	materials, err := parsed.WorkspaceProfileMaterials()
	if err != nil {
		t.Fatalf("WorkspaceConnectMaterials: %v", err)
	}
	if materials["shopify"].BindingValues["SHOPIFY_API_VERSION"] != "2026-07" {
		t.Fatalf("profile binding env was not handed off: %#v", materials["shopify"])
	}
}

// TestWorkspaceConnectProfileDetachRequiresExclusiveIntent protects bucket
// routing from a config that simultaneously requests replacement and removal.
func TestWorkspaceConnectProfileDetachRequiresExclusiveIntent(t *testing.T) {
	dir := t.TempDir()
	valid := writeFile(t, dir, "detach.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  jira:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
    connection_profiles:
      - version: "2026-07-01"
        auth_type: oauth
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
    versions: ["2026-07-01"]
    connection_profiles:
      - version: "2026-07-01"
        auth_type: oauth
        profile_id: "00000000-0000-0000-0000-000000000002"
        reset: true
`)
	if _, err := configfile.ParseFile(conflict); err == nil {
		t.Fatal("profile detach with profile_id was accepted")
	}
}

func TestWorkspaceConnectMaterials_RejectsOAuth2AuthType(t *testing.T) {
	path := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
buckets:
  prod:
    service_config:
      github:
        connect:
          auth_type: oauth2
          client_id: $FUSED_TEST_CLIENT_ID
          client_secret: $FUSED_TEST_CLIENT_SECRET
          redirect_uri: https://engine.example.com/connect/callback
`)
	_, err := configfile.ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported auth_type") {
		t.Fatalf("expected oauth2 auth_type rejection, got %v", err)
	}
}

func TestWorkspaceConnectMaterials_RejectsImportedAuthTypeAliases(t *testing.T) {
	for _, authType := range []string{"openidconnect", "open_id_connect"} {
		path := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
buckets:
  prod:
    service_config:
      github:
        connect:
          auth_type: `+authType+`
          client_id: $FUSED_TEST_CLIENT_ID
          client_secret: $FUSED_TEST_CLIENT_SECRET
          redirect_uri: https://engine.example.com/connect/callback
`)
		_, err := configfile.ParseFile(path)
		if err == nil || !strings.Contains(err.Error(), "unsupported auth_type") {
			t.Fatalf("expected auth_type %q rejection, got %v", authType, err)
		}
	}
}

func TestWorkspaceConnectMaterials_RejectsInlineClientSecret(t *testing.T) {
	path := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
buckets:
  prod:
    service_config:
      github:
        connect:
          auth_type: oauth
          client_id: public-client-id
          client_secret: inline-secret
          redirect_uri: https://engine.example.com/connect/callback
`)
	_, err := configfile.ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "client_secret: $ENV") {
		t.Fatalf("expected inline client_secret rejection, got %v", err)
	}
}

func TestWorkspaceConnectMaterials_RejectsMissingConnectEnvRef(t *testing.T) {
	t.Setenv("FUSED_TEST_CLIENT_SECRET", "resolved-secret")
	path := writeFile(t, t.TempDir(), "workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  github:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
buckets:
  default:
    service_config:
      github:
        connect:
          auth_type: oauth
          client_id: $FUSED_TEST_MISSING_CLIENT_ID
          client_secret: $FUSED_TEST_CLIENT_SECRET
          redirect_uri: https://engine.example.com/connect/callback
`)
	parsed, err := configfile.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile failed: %v", err)
	}
	_, err = parsed.WorkspaceConnectMaterials()
	if err == nil || !strings.Contains(err.Error(), "FUSED_TEST_MISSING_CLIENT_ID is not set") {
		t.Fatalf("expected missing env error, got %v", err)
	}
}

func TestLoadRun_DiscoversFusedFolderInOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".fused/workspace.yaml", `
apiVersion: fused/v1
kind: workspace
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
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

func TestLoadRun_RejectsDuplicateArtifactIdentities(t *testing.T) {
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
	if err == nil || !strings.Contains(err.Error(), "duplicate artifact identity") {
		t.Fatalf("expected duplicate artifact identity error, got %v", err)
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

func TestParseRejectsRetiredArtifactFields(t *testing.T) {
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

func TestParseMCPConfigCarriesAuthPolicyWithoutCredentials(t *testing.T) {
	parsed, err := configfile.Parse([]byte(`
apiVersion: fused/v1
kind: mcp
name: github-agent
version: 1.0.0
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
	if parsed.ConfigKey != "mcp:github-agent:1.0.0" || service.Auth.Type != "oauth" || service.Connect.Scopes[0] != "read:user" {
		t.Fatalf("unexpected parsed MCP config: %#v", parsed)
	}
}
