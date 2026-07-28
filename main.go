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

func main() {
	cmd.ReadmeContent = readmeContent
	cmd.EmbeddedSkillFS = skillFS
	cmd.Execute()
}
