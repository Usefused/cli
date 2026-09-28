package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadBoundedSDKBundleFile keeps local artifact reads within the Engine's immutable bundle limits.
func TestReadBoundedSDKBundleFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bundle.js")
	// A normal compiled file reaches the API client without rewriting its bytes.
	if err := os.WriteFile(path, []byte("compiled exact bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	content, err := readBoundedSDKBundleFile(path, 20)
	if err != nil || string(content) != "compiled exact bytes" {
		t.Fatalf("bounded file = %q, error = %v", content, err)
	}
	// A growing file must fail after a bounded read rather than allocating its full size.
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 21)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBoundedSDKBundleFile(path, 20); err == nil {
		t.Fatal("oversized artifact accepted")
	}
	// An empty artifact cannot be attached as executable code.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBoundedSDKBundleFile(path, 20); err == nil {
		t.Fatal("empty artifact accepted")
	}
}
