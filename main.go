package main

import (
	"embed"

	"github.com/Usefused/cli/cmd"
)

//go:embed README.md
var readmeContent string

// Every fused-cli skill lives under skills/<version>/<skill-name>/. Embedding
// the tree keeps `skill print`/`skill install` usable offline; the release prep
// target snapshots skills/dev into that version folder before tagging.
//
//go:embed skills
var skillFS embed.FS

// The built-in Fused Auth OAuth client is fixed and identical for every
// workspace, so it is embedded rather than generated. `fused-cli auth-client`
// materializes it under the fixed "fused-auth" package name.
//
//go:embed assets/fused-auth
var fusedAuthAssetFS embed.FS

// The built-in Fused Admin management client mirrors the auth client: fixed and
// embedded rather than generated. `fused-cli admin-client` materializes it
// under the fixed "fused-admin" package name.
//
//go:embed assets/fused-admin
var fusedAdminAssetFS embed.FS

// Engine owns this compiled runtime snapshot; the CLI distributes it offline like the auth client.
//
//go:embed assets/fused-runtime
var fusedRuntimeAssetFS embed.FS

// main supplies embedded release assets before dispatching a CLI command.
func main() {
	// Keep --readme aligned with the intentionally short onboarding document;
	// detailed command help remains available through --help and docs/.
	cmd.ReadmeContent = readmeContent
	cmd.EmbeddedSkillFS = skillFS
	cmd.FusedAuthAssets = fusedAuthAssetFS
	cmd.FusedAdminAssets = fusedAdminAssetFS
	cmd.FusedRuntimeAssets = fusedRuntimeAssetFS
	cmd.Execute()
}
