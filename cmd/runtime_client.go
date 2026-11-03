package cmd

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"

	"github.com/spf13/cobra"
)

// FusedRuntimeAssets contains Engine-built npm archives, never a second runtime implementation.
var FusedRuntimeAssets fs.FS

var runtimeClientOut string

var runtimeClientCmd = &cobra.Command{
	Use:   "runtime-client",
	Short: "Download the built-in @fused/runtime TypeScript/Node client",
	Long: `Writes the Engine-owned @fused/runtime npm archive and its checksum manifest
into <out>/fused-runtime. Like auth-client, this works without login or an Engine
connection. Install the archive using the printed npm command. Requires Node 24+.`,
	Args: cobra.NoArgs,
	RunE: runRuntimeClient,
}

// init registers the local download beside the other fixed clients before app command relocation.
func init() {
	runtimeClientCmd.Flags().StringVarP(&runtimeClientOut, "out", "o", ".", "Directory to write the fused-runtime package into")
	sdkCmd.AddCommand(runtimeClientCmd)
}

// runtimePackageFilename validates embedded provenance before writing an installable package.
func runtimePackageFilename(assets fs.FS) (string, error) {
	content, err := fs.ReadFile(assets, "assets/fused-runtime/manifest.json")
	// A missing release input must fail instead of producing an empty successful download.
	if err != nil {
		return "", err
	}
	var manifest struct {
		Name     string `json:"name"`
		Version  string `json:"version"`
		Filename string `json:"filename"`
		SHA256   string `json:"sha256"`
	}
	// Malformed metadata cannot select a payload or an output path.
	if err := json.Unmarshal(content, &manifest); err != nil {
		return "", fmt.Errorf("invalid runtime manifest: %w", err)
	}
	// Exact package identity and a simple release version keep the filename inside its asset directory.
	if manifest.Name != "@fused/runtime" || !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(manifest.Version) || manifest.Filename != "fused-runtime-"+manifest.Version+".tgz" {
		return "", fmt.Errorf("invalid runtime package identity")
	}
	archive, err := fs.ReadFile(assets, "assets/fused-runtime/"+manifest.Filename)
	// Do not emit install instructions for a missing archive.
	if err != nil {
		return "", err
	}
	// Checksum verification detects a stale or partially refreshed distribution snapshot.
	if fmt.Sprintf("%x", sha256.Sum256(archive)) != manifest.SHA256 {
		return "", fmt.Errorf("runtime package checksum mismatch")
	}
	return manifest.Filename, nil
}

// runRuntimeClient materializes the same fixed Engine build in every workspace without remote access.
func runRuntimeClient(cmd *cobra.Command, _ []string) error {
	filename, err := runtimePackageFilename(FusedRuntimeAssets)
	// Validate the complete embedded package before creating output files.
	if err != nil {
		return err
	}
	dest := filepath.Join(runtimeClientOut, "fused-runtime")
	_, err = copyEmbeddedClientTree(FusedRuntimeAssets, "assets/fused-runtime", dest)
	// An unsuccessful write must not advertise an installable result.
	if err != nil {
		return err
	}
	archive, err := filepath.Abs(filepath.Join(dest, filename))
	// Absolute paths keep npm from interpreting a bare relative directory as a registry package.
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Wrote @fused/runtime to %s\nInstall: npm install %s\n", archive, shellQuoteSDKInvokeArgument(archive))
	return nil
}
