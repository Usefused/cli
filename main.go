package main

import (
	"embed"

	"github.com/Usefused/cli/cmd"
)

//go:embed README.md
var readmeContent string

// Every fused-cli skill (fused-cli, fused-workspace, fused-sdk, fused-mcp,
// fused-bucket, fused-config) lives under skills/<version>/<skill-name>/, one
// version folder per CLI release -- see cmd/skill.go's skillVersionFolder and
// skillSpecs. Embedding the whole tree here (rather than one version's
// subfolder) means a new release only has to add its own version folder to
// the repo; this line never needs to change again.
//
//go:embed skills
var skillFS embed.FS

func main() {
	cmd.ReadmeContent = readmeContent
	cmd.EmbeddedSkillFS = skillFS
	cmd.Execute()
}
