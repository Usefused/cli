package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Print or install the fused-config Claude Code skill",
	Long: `Print or install a Claude Code skill that teaches an agent how to edit
Fused workspace, connection policy, execution policy, and SDK/MCP configs
using this CLI.

Content is fetched from the version-tagged SKILL.md published alongside this
CLI release, falling back to the copy embedded in this binary if the fetch
fails, so a skill-only update can ship without a new CLI release.`,
}

var skillInstallScope string
var skillInstallPath string

var skillPrintCmd = &cobra.Command{
	Use:   "print",
	Short: "Print the fused-config skill content (SKILL.md) to stdout",
	RunE: func(cmd *cobra.Command, args []string) error {
		content, _, err := resolveSkillContent()
		if err != nil {
			return err
		}
		fmt.Fprint(cmd.OutOrStdout(), content)
		return nil
	},
}

var skillInstallCmd = &cobra.Command{
	Use:   "install",
	Short: `Write the fused-config skill into a Claude Code skills directory (default: ~/.claude/skills)`,
	// Why: Write to OTEL to audit user/agent-triggered writes into another
	// tool's config directory, same as other mutating commands.
	RunE: WithTelemetry("cli.skill.install", func(cmd *cobra.Command, args []string) error {
		content, source, err := resolveSkillContent()
		if err != nil {
			return err
		}
		target, err := skillInstallTarget()
		if err != nil {
			return err
		}
		if err := writeSkillFile(target, content); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Installed fused-config skill to %s (source: %s)\n", target, source)
		return nil
	}),
}

func init() {
	RootCmd.AddCommand(skillCmd)
	skillCmd.AddCommand(skillPrintCmd)
	skillCmd.AddCommand(skillInstallCmd)

	skillInstallCmd.Flags().StringVar(&skillInstallScope, "scope", "user", `Install location: "user" (~/.claude/skills, no trust prompt) or "project" (./.claude/skills, requires accepting Claude Code's project trust dialog)`)
	skillInstallCmd.Flags().StringVar(&skillInstallPath, "path", "", "Exact file path to write SKILL.md to, overriding --scope")
}

// skillRawContentURL mirrors the raw-content convention already used for
// install.sh/update checks (version_check.go): a version-tagged path when
// this is a real release build, otherwise main (Version == "dev", i.e. a
// local/unreleased build with no matching tag to pin to).
func skillRawContentURL() string {
	if Version == "dev" {
		return "https://raw.githubusercontent.com/Usefused/cli/main/SKILL.md"
	}
	tag := Version
	if len(tag) == 0 || tag[0] != 'v' {
		tag = "v" + tag
	}
	return fmt.Sprintf("https://raw.githubusercontent.com/Usefused/cli/%s/SKILL.md", tag)
}

// resolveSkillContent prefers the version-tagged SKILL.md published in the
// repo -- so a skill-only content update doesn't require a new CLI release
// -- falling back to the copy embedded in this binary (main.go's go:embed)
// on any fetch failure: offline, timeout, 404, etc. Returns the content plus
// a short label of where it came from, surfaced by `skill install`.
func resolveSkillContent() (string, string, error) {
	if content, ok := fetchSkillContent(); ok {
		return content, "github:" + skillRawContentURL(), nil
	}
	if EmbeddedSkillContent != "" {
		return EmbeddedSkillContent, "embedded (offline fallback)", nil
	}
	return "", "", fmt.Errorf("could not fetch SKILL.md from %s and no embedded copy is available", skillRawContentURL())
}

func fetchSkillContent() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", skillRawContentURL(), nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("User-Agent", "fused-cli/"+Version)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil || len(body) == 0 {
		return "", false
	}
	return string(body), true
}

func skillInstallTarget() (string, error) {
	if skillInstallPath != "" {
		return skillInstallPath, nil
	}
	switch skillInstallScope {
	case "project":
		return filepath.Join(".claude", "skills", "fused-config", "SKILL.md"), nil
	case "user", "":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".claude", "skills", "fused-config", "SKILL.md"), nil
	default:
		return "", fmt.Errorf("unknown --scope %q (expected \"user\" or \"project\")", skillInstallScope)
	}
}

// writeSkillFile mirrors internal/config.Save's atomic write (temp file then
// rename) so a crash mid-write can't leave a truncated SKILL.md behind.
func writeSkillFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
