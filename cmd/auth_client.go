package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// FusedAuthAssets is the embedded fixed Fused Auth client tree (the embedding
// lives in main.go's go:embed directive). It lets `auth-client` materialize the
// client without a server or code generation, because the client is identical
// for every workspace.
var FusedAuthAssets fs.FS

var authClientCmd = &cobra.Command{
	Use:   "auth-client",
	Short: "Download the built-in Fused Auth OAuth client",
	Long: `Writes the built-in Fused Auth OAuth client into a local directory
under the fixed package name "fused-auth" (TypeScript) or import "fused.fused_auth"
(Python). The client is identical for every workspace, so there is no naming or
generation step -- only the language is chosen.`,
	Args: cobra.NoArgs,
	RunE: runAuthClient,
}

var authClientLanguage string
var authClientOut string

func init() {
	authClientCmd.Flags().StringVarP(&authClientLanguage, "language", "l", "ts", "Client language: ts or python")
	authClientCmd.Flags().StringVarP(&authClientOut, "out", "o", ".", "Directory to write the fused-auth package into")
	// Group under `sdk` so the built-in auth client sits beside generated SDKs;
	// unlike other sdk commands it is local-only and needs no Engine connection.
	sdkCmd.AddCommand(authClientCmd)
}

func runAuthClient(cmd *cobra.Command, _ []string) error {
	dir, ok := map[string]string{"ts": "typescript", "python": "python"}[authClientLanguage]
	// Unknown languages fail fast rather than materializing an empty directory.
	if !ok {
		return fmt.Errorf("unsupported language %q (use ts or python)", authClientLanguage)
	}
	root := "assets/fused-auth/" + dir
	dest := filepath.Join(authClientOut, "fused-auth")
	written, err := copyEmbeddedClientTree(FusedAuthAssets, root, dest)
	if err != nil {
		return err
	}
	fmt.Printf("Wrote %d files to %s (package: fused-auth)\n", written, dest)
	return nil
}

// copyEmbeddedClientTree writes every embedded file under root into dest,
// preserving relative paths. The Python `init.py` asset is renamed to
// `__init__.py` on write because go:embed excludes files whose names start with
// an underscore. It is shared by the auth and admin built-in clients.
func copyEmbeddedClientTree(efs fs.FS, root, dest string) (int, error) {
	written := 0
	err := fs.WalkDir(efs, root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// Directories are materialized lazily via MkdirAll for each file.
		if d.IsDir() {
			return nil
		}
		content, err := fs.ReadFile(efs, path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// Restore the underscore-prefixed Python package marker on disk.
		if filepath.Base(rel) == "init.py" {
			rel = strings.TrimSuffix(rel, "init.py") + "__init__.py"
		}
		target := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return err
		}
		written++
		return nil
	})
	if err != nil {
		return 0, err
	}
	// An empty embed would otherwise report success while producing no package.
	if written == 0 {
		return 0, fmt.Errorf("no embedded files found for %q", root)
	}
	return written, nil
}
