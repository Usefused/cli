package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Usefused/cli/cmd"
)

// TestEmbeddedRuntimePackage verifies the real release payload through the CLI's offline export path.
func TestEmbeddedRuntimePackage(t *testing.T) {
	cmd.FusedRuntimeAssets = fusedRuntimeAssetFS
	command, _, err := cmd.RootCmd.Find([]string{"app", "runtime-client"})
	// The binary must expose the same command tested with synthetic assets.
	if err != nil || command.Name() != "runtime-client" {
		t.Fatalf("find runtime command: %v", err)
	}
	destination := t.TempDir()
	command.SetOut(io.Discard)
	// Invalid flag wiring must fail the test instead of writing into the checkout.
	if err := command.Flags().Set("out", destination); err != nil {
		t.Fatal(err)
	}
	// Real embedded bytes must pass identity and checksum verification before export.
	if err := command.RunE(command, nil); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(destination, "fused-runtime", "manifest.json"))
	// The manifest must travel with the package so downstream consumers can verify provenance.
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct{ Filename string }
	// Read the recorded version instead of coupling tests to a particular runtime release.
	if err := json.Unmarshal(manifest, &metadata); err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(filepath.Join(destination, "fused-runtime", metadata.Filename))
	// A successful export must have materialized its advertised archive.
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(archive))
	// Only a valid npm tarball is installable, regardless of its manifest checksum.
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	files := map[string]bool{}
	tarReader := tar.NewReader(reader)
	for {
		entry, err := tarReader.Next()
		// EOF completes the archive; any other parse failure indicates a broken release.
		if err == io.EOF {
			break
		}
		// Do not accept truncated tar data as an incomplete but valid package.
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name] = true
	}
	for _, name := range []string{"package/package.json", "package/README.md", "package/dist/src/index.js", "package/dist/src/index.d.ts", "package/dist/src/client.js", "package/dist/src/engine.proto"} {
		// Consumers need executable code, TypeScript declarations and the schema without compiling source.
		if !files[name] {
			t.Errorf("missing runtime payload: %s", name)
		}
	}
}
