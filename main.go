package main

import (
	_ "embed"

	"github.com/Usefused/cli/cmd"
)

//go:embed README.md
var readmeContent string

func main() {
	cmd.ReadmeContent = readmeContent
	cmd.Execute()
}
