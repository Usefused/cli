package main

import (
	"embed"

	"github.com/Usefused/cli/cmd"
)

//go:embed README.md
var readmeContent string

// The Command Reference used to live inside README.md itself; it's now split
// out to docs/COMMANDS.md so the README stays a short landing page on GitHub.
// `--readme` still needs to print one complete, stitched document, so both
// files are embedded separately and concatenated below rather than moving
// the split into cmd/root.go.
//
//go:embed docs/COMMANDS.md
var commandsContent string

// Every fused-cli skill lives under skills/<version>/<skill-name>/. Embedding
// the tree keeps `skill print`/`skill install` usable offline; the release prep
// target snapshots skills/dev into that version folder before tagging.
//
//go:embed skills
var skillFS embed.FS

func main() {
	cmd.ReadmeContent = readmeContent + "\n---\n\n" + commandsContent
	cmd.EmbeddedSkillFS = skillFS
	cmd.Execute()
}
