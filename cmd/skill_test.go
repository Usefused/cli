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

// --- skillSpec / manifest ---------------------------------------------------

func TestSkillSpecByName(t *testing.T) {
	for _, name := range []string{"fused-cli", "fused-workspace", "fused-sdk", "fused-mcp", "fused-bucket", "fused-config", "fused-webhook", "fused-notifications"} {
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

func TestDevSkillsIncludePermissionAndDenialGuidance(t *testing.T) {
	tests := []struct {
		name     string
		required []string
	}{
		{name: "fused-cli", required: []string{"catalogue.read", "catalogue.import", "access.read", "access.manage", "team access service", "team access workspace"}},
		{name: "fused-workspace", required: []string{"service.read", "service.manage", "workspace.update", "team access service", "team access bucket", "team access workspace"}},
		{name: "fused-sdk", required: []string{"app.create", "app.manage", "app.read", "app.tokens.manage", "service.consume", "bucket.use", "team eligible-owners", "team build-access", "team access app"}},
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

// --- resolveSkillFiles: fetch, all-or-nothing fallback ----------------------

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
	EmbeddedSkillFS = fstest.MapFS{
		"skills/dev/fused-sdk/SKILL.md": &fstest.MapFile{Data: []byte("---\nname: fused-sdk\ndescription: SDK workflow\n---\n\nNever run `fused-cli sdk prompt`.\n")},
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
