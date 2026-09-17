package cmd

import (
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/spf13/cobra"
)

// FusedAdminAssets is the embedded fixed Fused Admin client tree (the embedding
// lives in main.go's go:embed directive). It lets `admin-client` materialize the
// client without a server or code generation, because the client is identical
// for every workspace.
var FusedAdminAssets fs.FS

var adminClientCmd = &cobra.Command{
	Use:   "admin-client",
	Short: "Download the built-in Fused Admin management client",
	Long: `Writes the built-in Fused Admin management client into a local directory
under the fixed package name "fused-admin" (TypeScript) or import "fused.fused_admin"
(Python). The client lists/deploys MCP servers and mints app tokens using an
OAuth access token, so it needs no fused-cli session. It is identical for every
workspace, so there is no naming or generation step -- only the language is
chosen.`,
	Args: cobra.NoArgs,
	RunE: runAdminClient,
}

var adminClientLanguage string
var adminClientOut string

func init() {
	adminClientCmd.Flags().StringVarP(&adminClientLanguage, "language", "l", "ts", "Client language: ts or python")
	adminClientCmd.Flags().StringVarP(&adminClientOut, "out", "o", ".", "Directory to write the fused-admin package into")
	// Group under `sdk` so the built-in admin client sits beside generated SDKs
	// and the built-in auth client; like those it is local-only and needs no
	// Engine connection.
	sdkCmd.AddCommand(adminClientCmd)
}

func runAdminClient(cmd *cobra.Command, _ []string) error {
	dir, ok := map[string]string{"ts": "typescript", "python": "python"}[adminClientLanguage]
	// Unknown languages fail fast rather than materializing an empty directory.
	if !ok {
		return fmt.Errorf("unsupported language %q (use ts or python)", adminClientLanguage)
	}
	root := "assets/fused-admin/" + dir
	dest := filepath.Join(adminClientOut, "fused-admin")
	written, err := copyEmbeddedClientTree(FusedAdminAssets, root, dest)
	if err != nil {
		return err
	}
	fmt.Printf("Wrote %d files to %s (package: fused-admin)\n", written, dest)
	return nil
}
