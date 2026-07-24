package main

import (
	_ "embed"

	"github.com/Usefused/cli/cmd"
)

//go:embed README.md
var readmeContent string

//go:embed SKILL.md
var skillContent string

func main() {
	cmd.ReadmeContent = readmeContent
	cmd.EmbeddedSkillContent = skillContent
	cmd.Execute()
}
