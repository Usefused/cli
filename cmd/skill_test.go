package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// --- test helpers for the package-level state skill.go reads -------------

func setTestVersion(t *testing.T, v string) {
	t.Helper()
	old := Version
	Version = v
	t.Cleanup(func() { Version = old })
}

func setTestRawContentBaseURL(t *testing.T, url string) {
	t.Helper()
	old := skillRawContentBaseURL
	skillRawContentBaseURL = url
	t.Cleanup(func() { skillRawContentBaseURL = old })
}

func setTestExecutablePath(t *testing.T, executable string) {
	t.Helper()
	old := skillExecutablePath
	skillExecutablePath = func() (string, error) { return executable, nil }
	t.Cleanup(func() { skillExecutablePath = old })
}

// --- skillSpec / manifest ---------------------------------------------------

// TestSkillSpecByName keeps every installable skill reachable by its public name.
func TestSkillSpecByName(t *testing.T) {
	for _, name := range []string{"fused-cli", "fused-workspace", "fused-sdk", "fused-unified-operations", "fused-mcp", "fused-bucket", "fused-config", "fused-webhook", "fused-notifications"} {
		if _, ok := skillSpecByName(name); !ok {
			t.Errorf("expected a spec for %q", name)
		}
	}
	if _, ok := skillSpecByName("does-not-exist"); ok {
		t.Errorf("expected no spec for an unknown name")
	}
}

func TestSortedSkillSpecNames(t *testing.T) {
	names := sortedSkillSpecNames()
	if len(names) != len(skillSpecs) {
		t.Fatalf("expected %d names, got %d: %v", len(skillSpecs), len(names), names)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Errorf("names not sorted: %v", names)
		}
	}
}

func TestSkillManifests_SkillMDFirstNoDuplicates(t *testing.T) {
	for _, spec := range skillSpecs {
		if len(spec.manifest) == 0 || spec.manifest[0] != "SKILL.md" {
			t.Errorf("%s: expected SKILL.md first in manifest, got %v", spec.name, spec.manifest)
		}
		seen := map[string]bool{}
		for _, f := range spec.manifest {
			if seen[f] {
				t.Errorf("%s: duplicate manifest entry %q", spec.name, f)
			}
			seen[f] = true
		}
	}
}

func TestFusedCLISkillShipsAccessManagementReference(t *testing.T) {
	spec, ok := skillSpecByName("fused-cli")
	if !ok {
		t.Fatal("fused-cli skill spec not found")
	}
	for _, path := range spec.manifest {
		if path == "reference/access-management.md" {
			return
		}
	}
	t.Fatalf("fused-cli manifest does not include access management reference: %v", spec.manifest)
}

func TestFusedCLISkillShipsBuildWorkflowReference(t *testing.T) {
	spec, ok := skillSpecByName("fused-cli")
	if !ok {
		t.Fatal("fused-cli skill spec not found")
	}
	for _, path := range spec.manifest {
		if path == "reference/build-sdk-or-mcp.md" {
			return
		}
	}
	t.Fatalf("fused-cli manifest does not include build workflow reference: %v", spec.manifest)
}

// TestFusedSDKSkillShipsEngineExecutionAPIReference keeps direct Engine calls
// available after skill installation instead of only in the source checkout.
func TestFusedSDKSkillShipsEngineExecutionAPIReference(t *testing.T) {
	spec, ok := skillSpecByName("fused-sdk")
	if !ok {
		t.Fatal("fused-sdk skill is missing")
	}
	wantPath := "reference/engine-execution-api.md"
	found := false
	for _, path := range spec.manifest {
		if path == wantPath {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("fused-sdk manifest does not include Engine execution reference: %v", spec.manifest)
	}
	data, err := os.ReadFile(filepath.Join("..", "skills", "dev", "fused-sdk", wantPath))
	if err != nil {
		t.Fatalf("read Engine execution reference: %v", err)
	}
	for _, contract := range []string{
		"POST {ENGINE_URL}/v1/apps/{APP_ID}/executions",
		"Authorization: Bearer {SDK_EXECUTION_TOKEN}",
		"Physical operation request",
		"Unified operation request",
		"Idempotency-Key",
	} {
		if !strings.Contains(string(data), contract) {
			t.Errorf("Engine execution reference is missing %q", contract)
		}
	}
}

func TestFusedCLISkillDocumentsImportTargetScope(t *testing.T) {
	path := filepath.Join("..", "skills", "dev", "fused-cli", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, token := range []string{"--target all|endpoints|webhooks", "defaults to `endpoints`", "plan persists that choice", "non-destructive to the other target"} {
		if !strings.Contains(string(data), token) {
			t.Errorf("fused-cli skill missing import target guidance %q", token)
		}
	}
}

func TestDevSkillsDocumentServicePublicationGate(t *testing.T) {
	paths := []string{
		filepath.Join("..", "skills", "dev", "fused-cli", "SKILL.md"),
		filepath.Join("..", "skills", "dev", "fused-workspace", "SKILL.md"),
	}
	var combined strings.Builder
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		combined.Write(data)
		combined.WriteByte('\n')
	}
	for _, token := range []string{
		"service-level `public` as the pre-publication gate",
		"cannot expose a private service",
		"do not fail private validation",
		"Never substitute version visibility for the service gate",
	} {
		if !strings.Contains(combined.String(), token) {
			t.Errorf("publication guidance missing %q", token)
		}
	}
}

// TestWorkspaceSkillUsesSingleWorkspaceFirstAddWorkflow keeps config-only and
// scoped-apply guidance explicit in the one documented composite workflow.
func TestWorkspaceSkillUsesSingleWorkspaceFirstAddWorkflow(t *testing.T) {
	path := filepath.Join("..", "skills", "dev", "fused-workspace", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	content := string(data)
	// These phrases cover discovery, local-only defaults, and the opt-in mutation
	// boundary without prescribing an extra catalogue command to agents.
	for _, token := range []string{
		"single discovery-and-author",
		"Only when absent",
		"Registry result is added",
		"permission error is not a miss",
		"Do not require a separate `service search` command first",
		"scoped additive service mutation",
		"authors local intent only",
		"read-only combined view",
		"available_to_add",
	} {
		if !strings.Contains(content, token) {
			t.Errorf("workspace skill missing workspace-first add guidance %q", token)
		}
	}
}

func TestDevSkillsIncludePermissionAndDenialGuidance(t *testing.T) {
	tests := []struct {
		name     string
		required []string
	}{
		{name: "fused-cli", required: []string{"catalogue.read", "catalogue.import", "access.read", "access.manage", "team access service", "team access workspace"}},
		{name: "fused-workspace", required: []string{"service.read", "service.manage", "workspace.update", "team access service", "team access bucket", "team access workspace"}},
		{name: "fused-sdk", required: []string{"app.create", "app.manage", "app.read", "app.tokens.manage", "service.consume", "bucket.use", "team eligible-owners", "team build-access", "team access app"}},
		{name: "fused-unified-operations", required: []string{"app.create", "app.manage", "app.read", "app.tokens.manage", "service.consume", "bucket.use", "team eligible-owners", "team build-access", "team access app"}},
		{name: "fused-mcp", required: []string{"app.create", "app.manage", "app.read", "app.tokens.manage", "service.consume", "bucket.use", "team eligible-owners", "team build-access", "team access app"}},
		{name: "fused-bucket", required: []string{"bucket.read", "bucket.manage", "credentials.manage", "connection.manage", "service.consume", "team access bucket", "team access service"}},
		{name: "fused-webhook", required: []string{"app.create", "app.manage", "service.consume", "bucket.use", "team eligible-owners", "team build-access", "team access service", "team access bucket"}},
		{name: "fused-config", required: []string{"service.manage", "credentials.manage", "catalogue.import", "connection.manage", "team access service", "team access bucket", "team access workspace"}},
		{name: "fused-notifications", required: []string{"workspace.read", "notification.update", "team access workspace"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec, ok := skillSpecByName(test.name)
			if !ok {
				t.Fatalf("skill spec %q not found", test.name)
			}

			var combined strings.Builder
			for _, manifestPath := range spec.manifest {
				path := filepath.Join("..", "skills", "dev", test.name, filepath.FromSlash(manifestPath))
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read %s: %v", path, err)
				}
				combined.Write(data)
				combined.WriteByte('\n')
			}
			content := combined.String()
			if !strings.Contains(content, "## Permissions and team access") && test.name != "fused-cli" {
				t.Error("missing a Permissions and team access section")
			}
			for _, required := range append(test.required,
				"reference/access-management.md",
				"missing permission",
				"resource",
				"Never self-grant",
			) {
				if !strings.Contains(content, required) {
					t.Errorf("missing permission-guidance token %q", required)
				}
			}
		})
	}
}

// TestFusedUnifiedOperationsSkillDocumentsItsAuthoringContract prevents the
// result-oriented skill from drifting back into a generic SDK overview.
func TestFusedUnifiedOperationsSkillDocumentsItsAuthoringContract(t *testing.T) {
	path := filepath.Join("..", "skills", "dev", "fused-unified-operations", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	content := string(data)
	for _, token := range []string{
		"## Result",
		"## Properties and where they are used",
		"`bindings.<target>.input`",
		"`depends_on`",
		"`rollback.operation`",
		"`${response.github.iid ?? response.linkedin.id}`",
		"`selectors.<service>.endUserRef`",
		"`end_user_ref`",
		"`{results, rollbacks}`",
		"zero physical calls",
		"## Complete example",
	} {
		if !strings.Contains(content, token) {
			t.Errorf("missing Unified Operations guidance %q", token)
		}
	}
	if lines := strings.Count(content, "\n") + 1; lines > 260 {
		t.Errorf("Unified Operations skill is too large for focused loading: %d lines (limit 260)", lines)
	}
}

func TestFusedSDKSkillKeepsIDEAgentWorkflowLocalAndCompact(t *testing.T) {
	path := filepath.Join("..", "skills", "dev", "fused-sdk", "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	content := string(data)
	required := []string{
		"Never run `fused-cli sdk prompt`",
		"Do not delegate the work to another agent",
		"working-facts",
		"load every sibling skill up front",
		"reference/build-sdk-or-mcp.md",
		"fused-workspace",
		"fused-bucket",
		"fused-config",
		"--expires-in <duration>",
		"limits time, not SDK operations",
		"Do not invent or suggest SDK `--allow` or fixed-binding flags",
	}
	for _, token := range required {
		if !strings.Contains(content, token) {
			t.Errorf("missing local SDK workflow token %q", token)
		}
	}
	if lines := strings.Count(content, "\n") + 1; lines > 320 {
		t.Errorf("fused-sdk is too large for progressive loading: %d lines (limit 320)", lines)
	}
}

// TestSDKSkillsDocumentInteractivePlanCredentialBoundary keeps remediation on the shared secret path.
func TestSDKSkillsDocumentInteractivePlanCredentialBoundary(t *testing.T) {
	paths := []string{
		filepath.Join("..", "skills", "dev", "fused-sdk", "SKILL.md"),
		filepath.Join("..", "skills", "dev", "fused-bucket", "SKILL.md"),
	}
	var combined strings.Builder
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		combined.Write(data)
		combined.WriteByte('\n')
	}
	// Installed guidance must preserve the same secret and bucket boundary as the interactive command.
	for _, token := range []string{
		"sdk plan --interactive",
		"exact YAML-resolved bucket",
		"retry once",
		"never creates a bucket",
		"OAuth/OIDC application credential fields",
		"Never collect an end-user provider token",
	} {
		if !strings.Contains(combined.String(), token) {
			t.Errorf("interactive SDK plan guidance is missing %q", token)
		}
	}
}

// TestAppOpenAPIExportIsDiscoverableAcrossCLIReferenceAndSkills keeps the
// exact-version, control-authenticated export distinct from runtime execution.
func TestAppOpenAPIExportIsDiscoverableAcrossCLIReferenceAndSkills(t *testing.T) {
	tests := []struct {
		path     string
		required []string
	}{
		{path: filepath.Join("..", "docs", "COMMANDS.md"), required: []string{
			"## `sdk openapi <sdk-name@version-or-version-id>`", "GET /apps/{app_id}/openapi", "`app.read`",
			"POST /v1/apps/{app_id}/executions", "execution token", "atomically writes", "16 MiB", "`--operation`", "`--format`", "`--out`", "metadata only", "`operation_count`", "`sha256:<64 lowercase hex>`",
		}},
		{path: filepath.Join("..", "skills", "dev", "fused-sdk", "SKILL.md"), required: []string{
			"sdk openapi <sdk-name@version-or-version-id>", "GETs `/apps/{app_id}/openapi`", "`app.read`",
			"POST /v1/apps/{app_id}/executions", "metadata only", "execution-token Bearer", "`operation_count`", "`sha256:<64 lowercase hex>`",
		}},
		{path: filepath.Join("..", "skills", "dev", "fused-cli", "SKILL.md"), required: []string{
			"sdk openapi <sdk-name@version-or-version-id>", "GETs `/apps/{app_id}/openapi`", "`app.read`",
			"POST /v1/apps/{app_id}/executions", "metadata rather than the document", "SDK-wide execution token", "`operation_count`", "`sha256:<64 lowercase hex>`",
		}},
	}
	for _, test := range tests {
		data, err := os.ReadFile(test.path)
		if err != nil {
			t.Fatalf("read %s: %v", test.path, err)
		}
		for _, token := range test.required {
			if !strings.Contains(string(data), token) {
				t.Errorf("%s is missing OpenAPI export contract %q", test.path, token)
			}
		}
	}
}

func TestDevAppSkillsDocumentVersionedLifecycle(t *testing.T) {
	tests := []struct {
		name     string
		required []string
	}{
		{name: "fused-sdk", required: []string{
			"SDK ID", "Version ID", "immutable", "app_version_immutable",
			"FUSED_SDK_TOKEN", "cache miss", "database reset", "no SDK deprecate/deactivate command",
		}},
		{name: "fused-mcp", required: []string{
			"MCP ID", "Version ID", "immutable", "app_version_immutable",
			"hard deactivation", "tombstone", "Engine database", "reapply", "no MCP deprecate",
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join("..", "skills", "dev", test.name, "SKILL.md")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			content := string(data)
			for _, required := range test.required {
				if !strings.Contains(content, required) {
					t.Errorf("missing app-lifecycle guidance %q", required)
				}
			}
			if test.name == "fused-sdk" && strings.Contains(content, "FUSED_LICENSE_KEY") {
				t.Error("SDK runtime guidance must not use the Engine-to-Registry License Key")
			}
		})
	}
}

// TestMCPDevSkillDocumentsInitializedLifecycle keeps installed agents on the protocol-required handshake order.
func TestMCPDevSkillDocumentsInitializedLifecycle(t *testing.T) {
	path := filepath.Join("..", "skills", "dev", "fused-mcp", "SKILL.md")
	data, err := os.ReadFile(path)
	// A missing bundled skill would make the installed guidance unverifiable.
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	content := string(data)
	for _, required := range []string{"notifications/initialized", "Before `tools/list`", "HTTP 202", "MCP-Protocol-Version"} {
		// Every lifecycle marker is needed so an agent cannot skip the notification or its negotiated headers.
		if !strings.Contains(content, required) {
			t.Errorf("MCP skill is missing initialized lifecycle guidance %q", required)
		}
	}
}

// TestMCPDevSkillDocumentsPhysicalPaginationDiscovery prevents installed agents from guessing GET traversal.
func TestMCPDevSkillDocumentsPhysicalPaginationDiscovery(t *testing.T) {
	path := filepath.Join("..", "skills", "dev", "fused-mcp", "SKILL.md")
	data, err := os.ReadFile(path)
	// Missing skill content cannot satisfy the distributed pagination contract.
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	content := strings.Join(strings.Fields(string(data)), " ")
	for _, required := range []string{
		"`supported: true` and `exact_lookup_required: true`", "`usage` offers only an exact `operationId` lookup for full guidance or the safe two-argument `call(operationId, params)`",
		"does not mention or authorize a numeric bound or third argument", "omits `caller_bound_supported` and `engine_max_pages`",
		"Resolve exact detail before adding a physical pagination option, but not solely to make the safe two-argument call", "ranked query result may instead establish `supported: false`",
		"still omits `caller_bound_supported`", "Exact mode alone exposes `caller_bound_supported` and `engine_max_pages`",
		"Exact operation detail exposes `supported`, `caller_bound_supported`, optional `engine_max_pages`", "positive N strictly lower",
		"Evaluate pagination separately for every physical call", "Never reuse pagination support or a page bound from another operation",
		"guidance for `gmail.users.messages.list` cannot authorize a third argument for `gmail.users.messages.get`",
		"Only when exact detail for that same `operationId` reports `caller_bound_supported: true`", "Never derive a numeric bound or third argument from a ranked query result",
		"reports `caller_bound_supported: false`, must use the two-argument form `call(operationId, params)`",
		"Physical-target pagination inside a Unified operation must stay target-keyed", "Never move it into the separate third argument",
		"pre-provider argument correction only when no earlier or concurrent call", "`execute_request: correct_arguments` with `provider_execution: not_started`",
		"`execute_request: do_not_replay` with `provider_execution: unknown`", "pre-provider correction only when isolated",
		"Engine makes one provider request", "Never infer page, cursor, offset",
	} {
		// Every marker changes how a fresh agent formats physical call pagination.
		if !strings.Contains(content, required) {
			t.Errorf("MCP skill is missing physical pagination guidance %q", required)
		}
	}
}

func TestDevAppSkillsDocumentExactSelectionSchemaVersion(t *testing.T) {
	tests := []struct {
		name     string
		required []string
	}{
		{name: "fused-sdk", required: []string{"`schema_version: 3`", "accepts only that current value", "unknown value requires a newer CLI"}},
		{name: "fused-mcp", required: []string{"`schema_version: 3`", "not an `mcp.yaml` field", "substitute another field name"}},
	}
	for _, test := range tests {
		path := filepath.Join("..", "skills", "dev", test.name, "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		content := strings.Join(strings.Fields(string(data)), " ")
		for _, required := range test.required {
			if !strings.Contains(content, required) {
				t.Errorf("%s is missing exact app-selection guidance %q", test.name, required)
			}
		}
		if strings.Contains(string(data), "definition_schema_version") {
			t.Errorf("%s documents a removed app-selection field", test.name)
		}
	}
}

func TestCLISkillsUseCurrentCommandLanguage(t *testing.T) {
	stale := []string{
		"family",
		"fused-cli bucket <",
		"fused-cli secret <",
		"fused-cli connect <",
		"fused-cli workspace service <",
		"sdk-name-or-family",
		"app-family-id",
		"operationId...",
		"webhookId...",
		"<service_name>",
		"name@version-or-app-id",
	}
	err := filepath.WalkDir(filepath.Join("..", "skills"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		content := string(data)
		lower := strings.ToLower(content)
		for _, token := range stale {
			candidate := content
			if token == "family" {
				candidate = lower
			}
			if strings.Contains(candidate, token) {
				t.Errorf("%s contains stale or internal command language %q", path, token)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan CLI skills: %v", err)
	}
}

// --- version folder / URL shape ---------------------------------------------

func TestSkillVersionFolder(t *testing.T) {
	cases := []struct{ version, want string }{
		{"dev", "dev"},
		{"v1.2.3", "1.2.3"},
		{"1.2.3", "1.2.3"},
	}
	for _, c := range cases {
		setTestVersion(t, c.version)
		if got := skillVersionFolder(); got != c.want {
			t.Errorf("skillVersionFolder() with Version=%q = %q, want %q", c.version, got, c.want)
		}
	}
}

func TestSkillRawContentURL(t *testing.T) {
	setTestVersion(t, "v2.0.0")
	spec, _ := skillSpecByName("fused-bucket")
	got := skillRawContentURL("SKILL.md", spec)
	want := skillRawContentBaseURL + "/main/skills/2.0.0/fused-bucket/SKILL.md"
	if got != want {
		t.Errorf("skillRawContentURL() = %q, want %q", got, want)
	}
}

// --- flattening for cursor/windsurf ------------------------------------------

func TestCombineSkillFiles(t *testing.T) {
	manifest := []string{"SKILL.md", "reference/a.md", "reference/b.md"}
	files := map[string]string{
		"SKILL.md":       "---\ndescription: x\n---\nbody",
		"reference/a.md": "content-a",
		"reference/b.md": "content-b",
	}
	combined := combineSkillFiles(files, manifest)
	if !strings.HasPrefix(combined, "---\ndescription: x\n---\nbody") {
		t.Errorf("expected SKILL.md content first, got: %s", combined)
	}
	if strings.Index(combined, "content-a") > strings.Index(combined, "content-b") {
		t.Errorf("expected reference/a.md before reference/b.md in: %s", combined)
	}
	if !strings.Contains(combined, "## Reference: reference/a.md") {
		t.Errorf("expected a reference heading for reference/a.md")
	}
	if strings.Contains(combined, "## Reference: SKILL.md") {
		t.Errorf("SKILL.md itself should not get a Reference heading")
	}
}

func TestCombineSkillFiles_MissingManifestEntrySkipped(t *testing.T) {
	manifest := []string{"SKILL.md", "reference/missing.md"}
	files := map[string]string{"SKILL.md": "body"}
	combined := combineSkillFiles(files, manifest)
	if strings.Contains(combined, "missing.md") {
		t.Errorf("did not expect a missing manifest file to appear: %s", combined)
	}
}

func TestSplitSkillFrontmatter(t *testing.T) {
	content := "---\nname: x\ndescription: \"hello world\"\n---\n\n# Body\n\ntext"
	desc, body := splitSkillFrontmatter(content)
	if desc != `"hello world"` {
		t.Errorf("description = %q, want %q", desc, `"hello world"`)
	}
	if strings.Contains(body, "---") {
		t.Errorf("body should not contain the frontmatter delimiters: %q", body)
	}
	if !strings.Contains(body, "# Body") {
		t.Errorf("body should retain the markdown content: %q", body)
	}
}

func TestSplitSkillFrontmatter_NoFrontmatter(t *testing.T) {
	desc, body := splitSkillFrontmatter("just a plain file")
	if desc != "" {
		t.Errorf("expected no description, got %q", desc)
	}
	if body != "just a plain file" {
		t.Errorf("expected body unchanged, got %q", body)
	}
}

func TestToCursorMDC(t *testing.T) {
	combined := "---\nname: fused-bucket\ndescription: \"desc here\"\n---\n\nbody text"
	out := toCursorMDC(combined, "fused-bucket")
	want := "---\ndescription: \"desc here\"\nglobs:\nalwaysApply: false\n---\n\nbody text"
	if out != want {
		t.Errorf("unexpected cursor mdc:\n got:  %q\n want: %q", out, want)
	}
}

func TestToWindsurfRule(t *testing.T) {
	combined := "---\nname: fused-bucket\ndescription: \"desc\"\n---\n\nbody text"
	out := toWindsurfRule(combined, "fused-bucket")
	if out != "# fused-bucket\n\nbody text" {
		t.Errorf("unexpected windsurf rule: %q", out)
	}
}

// --- install target path resolution -----------------------------------------

func TestSkillTargetsPaths(t *testing.T) {
	targets := skillTargets()
	for _, name := range []string{"claude", "codex", "antigravity", "cursor", "windsurf"} {
		if _, ok := targets[name]; !ok {
			t.Errorf("expected a target named %q", name)
		}
	}

	claude := targets["claude"]
	if got := claude.projectRoot("fused-workspace"); got != filepath.Join(".claude", "skills", "fused-workspace") {
		t.Errorf("claude projectRoot = %q", got)
	}
	if claude.userRoot == nil {
		t.Fatal("claude should support a per-user root")
	}
	home, _ := os.UserHomeDir()
	got, err := claude.userRoot("fused-workspace")
	if err != nil {
		t.Fatalf("claude userRoot: %v", err)
	}
	if got != filepath.Join(home, ".claude", "skills", "fused-workspace") {
		t.Errorf("claude userRoot = %q", got)
	}

	cursor := targets["cursor"]
	if !cursor.isFlat() {
		t.Errorf("cursor target should be flat")
	}
	if got := cursor.flatPath("fused-mcp"); got != filepath.Join(".cursor", "rules", "fused-mcp.mdc") {
		t.Errorf("cursor flatPath = %q", got)
	}
	if cursor.userRoot != nil {
		t.Errorf("cursor should have no per-user root")
	}
}

func TestSkillInstallRoot(t *testing.T) {
	claude := skillTargets()["claude"]

	t.Run("explicit path wins", func(t *testing.T) {
		oldPath, oldScope := skillInstallPath, skillInstallScope
		skillInstallPath, skillInstallScope = "/tmp/explicit", ""
		t.Cleanup(func() { skillInstallPath, skillInstallScope = oldPath, oldScope })

		got, err := skillInstallRoot(claude, "fused-cli")
		if err != nil || got != "/tmp/explicit" {
			t.Errorf("got %q, %v; want /tmp/explicit, nil", got, err)
		}
	})

	t.Run("defaults to user scope when target supports it", func(t *testing.T) {
		oldPath, oldScope := skillInstallPath, skillInstallScope
		skillInstallPath, skillInstallScope = "", ""
		t.Cleanup(func() { skillInstallPath, skillInstallScope = oldPath, oldScope })

		got, err := skillInstallRoot(claude, "fused-cli")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, filepath.Join(".claude", "skills", "fused-cli")) {
			t.Errorf("expected a user-home claude path, got %q", got)
		}
	})

	t.Run("project scope forced", func(t *testing.T) {
		oldPath, oldScope := skillInstallPath, skillInstallScope
		skillInstallPath, skillInstallScope = "", "project"
		t.Cleanup(func() { skillInstallPath, skillInstallScope = oldPath, oldScope })

		got, err := skillInstallRoot(claude, "fused-cli")
		if err != nil || got != filepath.Join(".claude", "skills", "fused-cli") {
			t.Errorf("got %q, %v", got, err)
		}
	})

	t.Run("user scope unsupported by target errors", func(t *testing.T) {
		oldPath, oldScope, oldFor := skillInstallPath, skillInstallScope, skillInstallFor
		skillInstallPath, skillInstallScope, skillInstallFor = "", "user", "cursor"
		t.Cleanup(func() { skillInstallPath, skillInstallScope, skillInstallFor = oldPath, oldScope, oldFor })

		cursor := skillTargets()["cursor"]
		if _, err := skillInstallRoot(cursor, "fused-cli"); err == nil {
			t.Errorf("expected an error for --scope user on a target with no per-user root")
		}
	})
}

func TestWriteSkillFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nested", "SKILL.md")
	if err := writeSkillFile(target, "hello"); err != nil {
		t.Fatalf("writeSkillFile: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want %q", data, "hello")
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("expected the .tmp file to be renamed away, stat err = %v", err)
	}
}

// --- embedded fallback -------------------------------------------------------

func TestEmbeddedSkillFiles(t *testing.T) {
	setTestVersion(t, "dev")
	oldFS := EmbeddedSkillFS
	EmbeddedSkillFS = fstest.MapFS{
		"skills/dev/fused-bucket/SKILL.md": &fstest.MapFile{Data: []byte("bucket skill content")},
	}
	t.Cleanup(func() { EmbeddedSkillFS = oldFS })

	spec, _ := skillSpecByName("fused-bucket")
	files, err := embeddedSkillFiles(spec)
	if err != nil {
		t.Fatalf("embeddedSkillFiles: %v", err)
	}
	if files["SKILL.md"] != "bucket skill content" {
		t.Errorf("got %q", files["SKILL.md"])
	}
}

func TestEmbeddedSkillFiles_NilFS(t *testing.T) {
	oldFS := EmbeddedSkillFS
	EmbeddedSkillFS = nil
	t.Cleanup(func() { EmbeddedSkillFS = oldFS })

	spec, _ := skillSpecByName("fused-cli")
	if _, err := embeddedSkillFiles(spec); err == nil {
		t.Errorf("expected an error when EmbeddedSkillFS is nil")
	}
}

// --- resolveSkillFiles: bundle, fetch, all-or-nothing fallback --------------

func TestResolveSkillFiles_ReleaseBundleWins(t *testing.T) {
	setTestVersion(t, "v2.0.0")
	binDir := t.TempDir()
	setTestExecutablePath(t, filepath.Join(binDir, "fused-cli"))

	spec, _ := skillSpecByName("fused-cli")
	root := filepath.Join(binDir, "skills", "2.0.0", spec.name)
	for _, relPath := range spec.manifest {
		path := filepath.Join(root, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("bundled: "+relPath), 0644); err != nil {
			t.Fatal(err)
		}
	}

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	setTestRawContentBaseURL(t, srv.URL)

	files, source, err := resolveSkillFiles(spec)
	if err != nil {
		t.Fatalf("resolveSkillFiles: %v", err)
	}
	if !strings.HasPrefix(source, "release bundle:") {
		t.Errorf("expected release bundle source, got %q", source)
	}
	if files["SKILL.md"] != "bundled: SKILL.md" {
		t.Errorf("expected bundled content, got %q", files["SKILL.md"])
	}
	if requests != 0 {
		t.Errorf("release bundle should avoid network requests, got %d", requests)
	}
}

func TestResolveSkillFiles_IncompleteReleaseBundleErrors(t *testing.T) {
	setTestVersion(t, "v2.0.0")
	binDir := t.TempDir()
	setTestExecutablePath(t, filepath.Join(binDir, "fused-cli"))

	spec, _ := skillSpecByName("fused-cli")
	root := filepath.Join(binDir, "skills", "2.0.0", spec.name)
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("partial"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := resolveSkillFiles(spec); err == nil || !strings.Contains(err.Error(), "read bundled") {
		t.Fatalf("expected an incomplete bundle error, got %v", err)
	}
}

func TestResolveSkillFiles_GithubSuccess(t *testing.T) {
	spec, _ := skillSpecByName("fused-cli")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fetched: " + r.URL.Path))
	}))
	defer srv.Close()
	setTestRawContentBaseURL(t, srv.URL)
	setTestVersion(t, "dev")

	files, source, err := resolveSkillFiles(spec)
	if err != nil {
		t.Fatalf("resolveSkillFiles: %v", err)
	}
	if !strings.HasPrefix(source, "github:") {
		t.Errorf("expected a github source, got %q", source)
	}
	if !strings.Contains(files["SKILL.md"], "fetched:") {
		t.Errorf("expected fetched content, got %q", files["SKILL.md"])
	}
}

func TestResolveSkillFiles_PartialFetchFailureFallsBackToEmbedded(t *testing.T) {
	spec, _ := skillSpecByName("fused-config") // multi-file manifest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "SKILL.md") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("remote SKILL.md"))
			return
		}
		w.WriteHeader(http.StatusNotFound) // reference files 404
	}))
	defer srv.Close()
	setTestRawContentBaseURL(t, srv.URL)
	setTestVersion(t, "dev")

	oldFS := EmbeddedSkillFS
	EmbeddedSkillFS = fstest.MapFS{
		"skills/dev/fused-config/SKILL.md":                         &fstest.MapFile{Data: []byte("embedded SKILL.md")},
		"skills/dev/fused-config/reference/execution-policies.md":  &fstest.MapFile{Data: []byte("embedded execution-policies")},
		"skills/dev/fused-config/reference/connection-profiles.md": &fstest.MapFile{Data: []byte("embedded connection-profiles")},
		"skills/dev/fused-config/reference/import-overlays.md":     &fstest.MapFile{Data: []byte("embedded import-overlays")},
		"skills/dev/fused-config/reference/openapi-postman.md":     &fstest.MapFile{Data: []byte("embedded openapi-postman")},
	}
	t.Cleanup(func() { EmbeddedSkillFS = oldFS })

	files, source, err := resolveSkillFiles(spec)
	if err != nil {
		t.Fatalf("resolveSkillFiles: %v", err)
	}
	if source != "embedded (offline fallback)" {
		t.Errorf("expected fallback source, got %q", source)
	}
	// All-or-nothing: even though SKILL.md fetched fine, one reference file
	// 404ing must fall the WHOLE skill back to embedded, not mix sources.
	if files["SKILL.md"] != "embedded SKILL.md" {
		t.Errorf("expected the embedded copy (all-or-nothing), got %q", files["SKILL.md"])
	}
}

func TestResolveSkillFiles_NoFetchNoEmbeddedErrors(t *testing.T) {
	spec, _ := skillSpecByName("fused-cli")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	setTestRawContentBaseURL(t, srv.URL)

	oldFS := EmbeddedSkillFS
	EmbeddedSkillFS = nil
	t.Cleanup(func() { EmbeddedSkillFS = oldFS })

	if _, _, err := resolveSkillFiles(spec); err == nil {
		t.Errorf("expected an error when both fetch and embedded fallback are unavailable")
	}
}

// --- command-level wiring ----------------------------------------------------

func TestSkillListCommand(t *testing.T) {
	// Other test files in this package (e.g. skeleton_test.go) call
	// RootCmd.SetOut(buf) and never reset it, so cmd.OutOrStdout() inside
	// skillListCmd.Run may not be the real os.Stdout by the time this test
	// runs -- capture through cobra's own writer, not a stdout pipe, and
	// reset it afterward so this test doesn't leak state either.
	var buf bytes.Buffer
	RootCmd.SetOut(&buf)
	t.Cleanup(func() { RootCmd.SetOut(nil) })

	RootCmd.SetArgs([]string{"skill", "list"})
	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("skill list: %v", err)
	}
	out := buf.String()
	for _, name := range sortedSkillSpecNames() {
		if !strings.Contains(out, name) {
			t.Errorf("expected `skill list` output to mention %q, got:\n%s", name, out)
		}
	}
}

func TestSkillInstallCommand_InstallsFusedSDKForCodingAgent(t *testing.T) {
	oldFor, oldName, oldScope, oldPath := skillInstallFor, skillInstallName, skillInstallScope, skillInstallPath
	skillInstallFor, skillInstallName, skillInstallScope, skillInstallPath = "", "", "", ""
	t.Cleanup(func() {
		skillInstallFor, skillInstallName, skillInstallScope, skillInstallPath = oldFor, oldName, oldScope, oldPath
	})

	setTestVersion(t, "dev")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	setTestRawContentBaseURL(t, srv.URL)

	oldFS := EmbeddedSkillFS
	// Offline installation is intentionally all-or-nothing, so the fixture must
	// contain every manifested file rather than silently mixing skill versions.
	EmbeddedSkillFS = fstest.MapFS{
		"skills/dev/fused-sdk/SKILL.md":                          &fstest.MapFile{Data: []byte("---\nname: fused-sdk\ndescription: SDK workflow\n---\n\nNever run `fused-cli sdk prompt`.\n")},
		"skills/dev/fused-sdk/reference/engine-execution-api.md": &fstest.MapFile{Data: []byte("Use the Engine execution token in the Authorization header.")},
	}
	t.Cleanup(func() { EmbeddedSkillFS = oldFS })

	destination := filepath.Join(t.TempDir(), "fused-sdk")
	var output bytes.Buffer
	RootCmd.SetOut(&output)
	RootCmd.SetArgs([]string{"skill", "install", "--for", "codex", "--skill", "fused-sdk", "--path", destination})
	oldSilenceErrors, oldSilenceUsage := RootCmd.SilenceErrors, RootCmd.SilenceUsage
	RootCmd.SilenceErrors, RootCmd.SilenceUsage = true, true
	t.Cleanup(func() {
		RootCmd.SetOut(nil)
		RootCmd.SetArgs(nil)
		RootCmd.SilenceErrors, RootCmd.SilenceUsage = oldSilenceErrors, oldSilenceUsage
	})

	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("install fused-sdk skill: %v", err)
	}
	installed, err := os.ReadFile(filepath.Join(destination, "SKILL.md"))
	if err != nil {
		t.Fatalf("read installed fused-sdk skill: %v", err)
	}
	if !strings.Contains(string(installed), "Never run `fused-cli sdk prompt`") {
		t.Errorf("installed skill lost the coding-agent routing boundary: %q", installed)
	}
	reference, err := os.ReadFile(filepath.Join(destination, "reference", "engine-execution-api.md"))
	if err != nil {
		t.Fatalf("read installed execution reference: %v", err)
	}
	if !strings.Contains(string(reference), "Authorization header") {
		t.Errorf("installed skill lost its execution reference: %q", reference)
	}
	if !strings.Contains(output.String(), "Installed fused-sdk skill for OpenAI Codex") {
		t.Errorf("unexpected install output: %q", output.String())
	}
}

func TestSkillInstallCommand_MissingForErrors(t *testing.T) {
	oldFor, oldName, oldScope, oldPath := skillInstallFor, skillInstallName, skillInstallScope, skillInstallPath
	skillInstallFor, skillInstallName, skillInstallScope, skillInstallPath = "", "", "", ""
	t.Cleanup(func() {
		skillInstallFor, skillInstallName, skillInstallScope, skillInstallPath = oldFor, oldName, oldScope, oldPath
	})

	RootCmd.SetArgs([]string{"skill", "install"})
	RootCmd.SilenceErrors = true
	RootCmd.SilenceUsage = true
	err := RootCmd.Execute()
	if err == nil {
		t.Fatal("expected an error when --for is omitted")
	}
	if !strings.Contains(err.Error(), "--for is required") {
		t.Errorf("expected the --for is required message, got: %v", err)
	}
}
