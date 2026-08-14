package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageReleaseSkillsCreatesVersionedSnapshot(t *testing.T) {
	temp := t.TempDir()
	devRoot := filepath.Join(temp, "skills", "dev", "fused-cli")
	if err := os.MkdirAll(devRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devRoot, "SKILL.md"), []byte("release skill"), 0644); err != nil {
		t.Fatal(err)
	}

	script, err := filepath.Abs(filepath.Join("scripts", "stage-release-skills.sh"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", script, "v1.2.3")
	command.Dir = temp
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("stage release skills: %v\n%s", err, output)
	}

	data, err := os.ReadFile(filepath.Join(temp, "skills", "1.2.3", "fused-cli", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "release skill" {
		t.Fatalf("staged content = %q", data)
	}
}

func TestReleaseConfigPackagesVersionedSkills(t *testing.T) {
	data, err := os.ReadFile(".goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `skills/{{ .Version }}/**/*`) {
		t.Fatal("GoReleaser archive must package the staged versioned skills")
	}
}
