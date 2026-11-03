package cmd

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// runtimeClientFixture produces a small deterministic release pair to test validation without npm.
func runtimeClientFixture() fstest.MapFS {
	archive := []byte("runtime archive fixture")
	manifest := fmt.Sprintf(`{"name":"@fused/runtime","version":"0.1.0","filename":"fused-runtime-0.1.0.tgz","sha256":"%x"}`, sha256.Sum256(archive))
	return fstest.MapFS{
		"assets/fused-runtime/manifest.json":           {Data: []byte(manifest)},
		"assets/fused-runtime/fused-runtime-0.1.0.tgz": {Data: archive},
	}
}

// TestRuntimeClientDownload exercises public command routing, offline writes and safe install instructions.
func TestRuntimeClientDownload(t *testing.T) {
	oldAssets, oldOut := FusedRuntimeAssets, runtimeClientOut
	// Restore command globals because other command tests share this process.
	t.Cleanup(func() { FusedRuntimeAssets, runtimeClientOut = oldAssets, oldOut })
	FusedRuntimeAssets = runtimeClientFixture()
	runtimeClientOut = filepath.Join(t.TempDir(), "vendor with 'quotes' and $vars")
	command, _, err := RootCmd.Find([]string{"app", "runtime-client"})
	// The local client must remain discoverable on the shared app surface.
	if err != nil || command != runtimeClientCmd {
		t.Fatalf("runtime-client route: %v, %v", command, err)
	}
	var output bytes.Buffer
	command.SetOut(&output)
	// Restore inherited output for subsequent command executions.
	t.Cleanup(func() { command.SetOut(nil) })
	// Calling the handler requires neither configured Engine URLs nor credentials.
	if err := command.RunE(command, nil); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(runtimeClientOut, "fused-runtime", "fused-runtime-0.1.0.tgz")
	data, err := os.ReadFile(archive)
	// Materialization must preserve the exact checked bytes and print one shell-safe install argument.
	if err != nil || string(data) != "runtime archive fixture" || !strings.Contains(output.String(), "npm install "+shellQuoteSDKInvokeArgument(archive)) {
		t.Fatalf("archive=%q err=%v output=%s", data, err, output.String())
	}
}

// TestRuntimeClientRejectsBrokenAssets prevents corrupted or path-bearing snapshots from appearing installable.
func TestRuntimeClientRejectsBrokenAssets(t *testing.T) {
	for _, mode := range []string{"missing manifest", "invalid json", "wrong identity", "path traversal", "missing archive", "checksum mismatch"} {
		// Each independent corruption must fail before any output is written.
		t.Run(mode, func(t *testing.T) {
			assets := runtimeClientFixture()
			manifest := assets["assets/fused-runtime/manifest.json"]
			// Corrupt exactly one release invariant per case to keep failures attributable.
			switch mode {
			case "missing manifest":
				delete(assets, "assets/fused-runtime/manifest.json")
			case "invalid json":
				manifest.Data = []byte("{")
			case "wrong identity":
				manifest.Data = bytes.ReplaceAll(manifest.Data, []byte("@fused/runtime"), []byte("other"))
			case "path traversal":
				manifest.Data = bytes.ReplaceAll(manifest.Data, []byte("fused-runtime-0.1.0.tgz"), []byte("../escape.tgz"))
			case "missing archive":
				delete(assets, "assets/fused-runtime/fused-runtime-0.1.0.tgz")
			case "checksum mismatch":
				assets["assets/fused-runtime/fused-runtime-0.1.0.tgz"].Data = []byte("corrupt")
			}
			// Every damaged snapshot must be rejected instead of returning a filename.
			if _, err := runtimePackageFilename(assets); err == nil {
				t.Fatal("accepted invalid release assets")
			}
		})
	}
}
