package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomicWriteFilePreservesExistingMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := atomicWriteFile(path, []byte("new\n"), 0644, nil); err != nil {
		t.Fatalf("atomic write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new\n" {
		t.Fatalf("unexpected content %q", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected mode 0600, got %o", info.Mode().Perm())
	}
}

func TestAtomicWriteFileValidationFailureLeavesOriginalUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace.yaml")
	original := []byte("kind: workspace\n")
	if err := os.WriteFile(path, original, 0640); err != nil {
		t.Fatal(err)
	}

	err := atomicWriteFile(path, []byte("invalid"), 0644, func([]byte) error {
		return errors.New("validation failed")
	})
	if err == nil {
		t.Fatal("expected validation failure")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != string(original) {
		t.Fatalf("original file changed: %q", data)
	}
	temps, globErr := filepath.Glob(filepath.Join(dir, ".workspace.yaml.tmp-*"))
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(temps) != 0 {
		t.Fatalf("temporary files were not cleaned up: %v", temps)
	}
}

func TestAtomicWriteFileCreatesParentAndUsesDefaultMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "receipt.json")
	if err := atomicWriteFile(path, []byte("{}\n"), 0644, validateJSONContent); err != nil {
		t.Fatalf("atomic write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0644 {
		t.Fatalf("expected mode 0644, got %o", info.Mode().Perm())
	}
}

func TestAtomicCreateFileRefusesExistingTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.yaml")
	if err := os.WriteFile(path, []byte("original\n"), 0600); err != nil {
		t.Fatal(err)
	}

	err := atomicCreateFile(path, []byte("replacement\n"), 0644, nil)
	if err == nil || !strings.Contains(err.Error(), "--extend") {
		t.Fatalf("expected an extend hint, got %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "original\n" {
		t.Fatalf("existing file was replaced: %q", data)
	}
}

func TestAtomicCreateFileValidatesBeforePublishing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sdk.yaml")
	err := atomicCreateFile(path, []byte("invalid"), 0644, func([]byte) error {
		return errors.New("invalid draft")
	})
	if err == nil || !strings.Contains(err.Error(), "invalid draft") {
		t.Fatalf("expected validation error, got %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("invalid target should not exist, got %v", statErr)
	}
}
