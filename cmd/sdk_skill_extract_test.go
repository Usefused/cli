package cmd

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractSDKZipInstallsRootSkillCentrally(t *testing.T) {
	root := t.TempDir()
	sdkDir := filepath.Join(root, "fused-sdks", "issue-tracker")
	wantSkillPath := filepath.Join(root, "fused-sdks", ".agents", "skills", "issue-tracker-sdk", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(wantSkillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wantSkillPath, []byte("obsolete skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	skill := "---\nname: issue-tracker-sdk\ndescription: Complete issue tracking tasks with the generated SDK.\n---\n\n## Outcomes\n\nReturn the requested issues.\n"
	archive := sdkTestZip(t, []sdkTestZipFile{
		{name: "README.md", content: "# Issue Tracker\n"},
		{name: "SKILL.md", content: skill},
		{name: "src/index.ts", content: "export class FusedSDK {}\n"},
	})

	if err := extractSDKZip(archive, sdkDir); err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(wantSkillPath)
	if err != nil {
		t.Fatalf("read centrally installed skill: %v", err)
	}
	if string(installed) != skill {
		t.Fatalf("installed skill = %q, want %q", installed, skill)
	}
	if _, err := os.Stat(filepath.Join(sdkDir, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("package-root SKILL.md was retained (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(sdkDir, "src", "index.ts")); err != nil {
		t.Fatalf("ordinary SDK content was not extracted: %v", err)
	}
}

func TestExtractSDKZipWithoutRootSkillIsUnchanged(t *testing.T) {
	root := t.TempDir()
	sdkDir := filepath.Join(root, "fused-sdks", "legacy-sdk")
	archive := sdkTestZip(t, []sdkTestZipFile{{name: "README.md", content: "# Legacy SDK\n"}})

	if err := extractSDKZip(archive, sdkDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sdkDir, "README.md")); err != nil {
		t.Fatalf("legacy SDK was not extracted normally: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "fused-sdks", ".agents")); !os.IsNotExist(err) {
		t.Fatalf("archive without root SKILL.md unexpectedly created a central skill directory (err=%v)", err)
	}
}

func TestExtractSDKZipRejectsMalformedOrUnsafeSkillNamesBeforeExtraction(t *testing.T) {
	tests := map[string]string{
		"missing frontmatter": "# Instructions\n",
		"missing name":        "---\ndescription: Missing name.\n---\n",
		"path traversal":      "---\nname: ../../outside\ndescription: Unsafe.\n---\n",
		"uppercase":           "---\nname: Issue-Tracker\ndescription: Unsafe.\n---\n",
		"underscore":          "---\nname: issue_tracker\ndescription: Unsafe.\n---\n",
		"empty segment":       "---\nname: issue--tracker\ndescription: Unsafe.\n---\n",
		"too long":            "---\nname: " + strings.Repeat("a", 65) + "\ndescription: Too long.\n---\n",
	}
	for name, skill := range tests {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			sdkDir := filepath.Join(root, "fused-sdks", "unsafe-sdk")
			archive := sdkTestZip(t, []sdkTestZipFile{
				{name: "README.md", content: "must not be extracted"},
				{name: "SKILL.md", content: skill},
			})

			if err := extractSDKZip(archive, sdkDir); err == nil {
				t.Fatal("expected invalid generated Agent Skill to reject extraction")
			}
			if _, err := os.Stat(sdkDir); !os.IsNotExist(err) {
				t.Fatalf("invalid Agent Skill allowed partial extraction (err=%v)", err)
			}
			if _, err := os.Stat(filepath.Join(root, "outside")); !os.IsNotExist(err) {
				t.Fatalf("invalid Agent Skill escaped the SDK output root (err=%v)", err)
			}
		})
	}
}

func TestExtractSDKZipRejectsDuplicateRootSkills(t *testing.T) {
	root := t.TempDir()
	sdkDir := filepath.Join(root, "fused-sdks", "duplicate-sdk")
	skill := "---\nname: duplicate-sdk\ndescription: Duplicate.\n---\n"
	archive := sdkTestZip(t, []sdkTestZipFile{
		{name: "SKILL.md", content: skill},
		{name: "./SKILL.md", content: skill},
	})

	if err := extractSDKZip(archive, sdkDir); err == nil {
		t.Fatal("expected duplicate root SKILL.md entries to be rejected")
	}
	if _, err := os.Stat(sdkDir); !os.IsNotExist(err) {
		t.Fatalf("duplicate Agent Skills allowed partial extraction (err=%v)", err)
	}
}

type sdkTestZipFile struct {
	name    string
	content string
}

func sdkTestZip(t *testing.T, files []sdkTestZipFile) []byte {
	t.Helper()
	var data bytes.Buffer
	w := zip.NewWriter(&data)
	for _, file := range files {
		entry, err := w.Create(file.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(file.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
