package cmd

import (
	"strings"
	"testing"
)

// TestServiceEditRequiresAnExplicitChange rejects empty edits before resolving credentials.
func TestServiceEditRequiresAnExplicitChange(t *testing.T) {
	for _, args := range [][]string{{"demo"}, {"demo", "--name", "  "}} {
		command := newServiceEditCommand()
		command.SetArgs(args)
		err := command.Execute()
		// No-op or invalid labels must fail without making a request.
		if err == nil || (!strings.Contains(err.Error(), "provide --name") && !strings.Contains(err.Error(), "must not be empty")) {
			t.Fatalf("args=%v err=%v", args, err)
		}
	}
}
