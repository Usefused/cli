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
kind: sdk
version: 1
name: security-detection
sdkVersion: "1.2.0"
language: typescript
target: sdk
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
	if cfg.Kind != configfile.KindSDK {
		t.Fatalf("kind: got %q", cfg.Kind)
	}
	if cfg.ConfigKey != "sdk:security-detection" {
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

func TestLoadRun_SingleWorkspaceFile(t *testing.T) {
	path := writeFile(t, t.TempDir(), "workspace.yaml", `
kind: workspace
version: 1
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions:
      - "2026-07-01"
      - "2026-08-01"
    default: "2026-07-01"
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
	cfg := run.Configs[0]
	if cfg.Kind != configfile.KindWorkspace {
		t.Fatalf("kind: got %q", cfg.Kind)
	}
	if cfg.ConfigKey != "workspace" {
		t.Errorf("config key: got %q", cfg.ConfigKey)
	}
	if got := cfg.Workspace.Services["okta"].Default; got != "2026-07-01" {
		t.Errorf("default version: got %q", got)
	}
	if got := cfg.Workspace.Services["okta"].ServiceID; got != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("service_id: got %q", got)
	}
	if got := cfg.Workspace.Deprecations; len(got) != 1 || got[0].EffectiveAt != "2026-10-01" {
		t.Errorf("deprecations not parsed: %+v", got)
	}
}

func TestLoadRun_WorkspaceAllowsSlugOnlyService(t *testing.T) {
	path := writeFile(t, t.TempDir(), "workspace.yaml", `
kind: workspace
version: 1
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

func TestLoadRun_DiscoversFusedFolderInOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".fused/workspace.yaml", `
kind: workspace
version: 1
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    versions: ["2026-07-01"]
`)
	writeFile(t, dir, ".fused/sdks/security.yaml", `
kind: sdk
version: 1
name: security
sdkVersion: "1.0.0"
language: typescript
target: sdk
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

func TestLoadRun_RejectsDuplicateSDKNames(t *testing.T) {
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
	if err == nil || !strings.Contains(err.Error(), "duplicate sdk name") {
		t.Fatalf("expected duplicate sdk name error, got %v", err)
	}
}

func TestLoadRun_RejectsInvalidFiles(t *testing.T) {
	tests := map[string]string{
		"unknown kind": `
kind: service
version: 1
`,
		"invalid language": `
kind: sdk
version: 1
name: security
sdkVersion: "1.0.0"
language: ruby
target: sdk
services:
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`,
		"invalid target": `
kind: sdk
version: 1
name: security
sdkVersion: "1.0.0"
language: typescript
target: mobile
services:
  okta:
    version: "2026-07-01"
    operations: ["listLogEvents"]
`,
		"malformed sdk service": `
kind: sdk
version: 1
name: security
sdkVersion: "1.0.0"
language: typescript
target: sdk
services:
  okta:
    operations: ["listLogEvents"]
`,
		"malformed workspace service": `
kind: workspace
version: 1
services:
  okta:
    service_id: "00000000-0000-0000-0000-000000000001"
    default: "2026-07-01"
`,
		"legacy endpoints key rejected": `
kind: sdk
version: 1
name: security
sdkVersion: "1.0.0"
language: typescript
target: sdk
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
kind: sdk
version: 1
name: `+name+`
sdkVersion: "1.0.0"
language: typescript
target: sdk
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
