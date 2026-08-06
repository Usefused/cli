package cmd

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// skillSpec describes one fused-cli skill. name doubles as the skill's own
// directory name (both under skills/<version>/ in this repo and at every
// install target) and its SKILL.md frontmatter `name:` field -- keep all
// three in sync when adding a skill.
type skillSpec struct {
	name string
	// summary is shown by `skill list` only. It's a static one-liner, not
	// fetched from SKILL.md's own frontmatter description, so `skill list`
	// never needs network access to be useful.
	summary string
	// manifest lists every file (relative to this skill's own root) that
	// makes up the skill: a SKILL.md plus any progressively loaded reference
	// documents. This is the same SKILL.md +
	// reference/ progressive disclosure shape Claude Code, Codex, and
	// Antigravity skills all document natively (a "references" or
	// "resources" subfolder alongside SKILL.md).
	manifest []string
}

// skillSpecs is every skill this CLI ships. fused-cli is the entry point for
// CLI setup and access management; fused-build-sdk is the compact IDE-agent
// workflow from a business goal to a ready SDK; fused-workspace/fused-sdk/
// fused-mcp/fused-webhook/fused-bucket each own one config kind's commands and
// shape; fused-config holds cross-cutting config owned by no single kind
// (execution policy, connection profiles, and their OpenAPI/Postman
// equivalent); fused-notifications explains plan/apply notices. Domain skills
// link to one another instead of duplicating details.
var skillSpecs = []skillSpec{
	{
		name:    "fused-cli",
		summary: "Setup, credentials, team/user access management, and an index of the other skills",
		manifest: []string{
			"SKILL.md",
			"reference/access-management.md",
			"reference/build-sdk-or-mcp.md",
		},
	},
	{
		name:    "fused-build-sdk",
		summary: "IDE-agent workflow from a business goal to a validated, ready-to-use SDK",
		manifest: []string{
			"SKILL.md",
		},
	},
	{
		name:    "fused-workspace",
		summary: "Workspace service allowlist: enabling services and versions, policy, deprecations",
		manifest: []string{
			"SKILL.md",
		},
	},
	{
		name:    "fused-sdk",
		summary: "Generating a typed SDK package: operation/webhook selection, auth/connect scoping",
		manifest: []string{
			"SKILL.md",
		},
	},
	{
		name:    "fused-mcp",
		summary: "Deploying an Engine-hosted MCP server: operation selection and calling convention",
		manifest: []string{
			"SKILL.md",
		},
	},
	{
		name:    "fused-bucket",
		summary: "Bucket credentials: secrets, values, OAuth connect, connection resources",
		manifest: []string{
			"SKILL.md",
		},
	},
	{
		name:    "fused-config",
		summary: "Cross-cutting config: execution policy, connection profiles, OpenAPI/Postman connect config",
		manifest: []string{
			"SKILL.md",
			"reference/execution-policies.md",
			"reference/connection-profiles.md",
			"reference/openapi-postman.md",
		},
	},
	{
		name:    "fused-webhook",
		summary: "Inbound webhook registration and SDK webhook attachments",
		manifest: []string{
			"SKILL.md",
		},
	},
	{
		name:    "fused-notifications",
		summary: "Interpret Registry and workspace notifications shown by plan and apply",
		manifest: []string{
			"SKILL.md",
		},
	},
}

func skillSpecByName(name string) (skillSpec, bool) {
	for _, spec := range skillSpecs {
		if spec.name == name {
			return spec, true
		}
	}
	return skillSpec{}, false
}

func sortedSkillSpecNames() []string {
	names := make([]string, 0, len(skillSpecs))
	for _, spec := range skillSpecs {
		names = append(names, spec.name)
	}
	sort.Strings(names)
	return names
}

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Print, list, or install a fused-cli skill",
	Long: `Print, list, or install a skill that teaches an agent how to edit
Fused workspace, SDK, MCP, bucket, execution policy, and connection profile
configs using this CLI.

'skill install' always requires --for <agent>: this CLI never assumes which
agent or app you use, so nothing gets installed unless you say so explicitly.
Supported values: claude, codex, antigravity, cursor, windsurf.

Content is fetched from this build's own version folder published on GitHub,
falling back to the copy embedded in this binary if any file fails to fetch,
so a skill-only content fix can ship without a new CLI release -- and an
install is never a mismatched mix of fetched and embedded content.`,
	Args: cobra.NoArgs,
	RunE: requireSubcommand,
}

var skillInstallFor string
var skillInstallName string
var skillPrintName string
var skillInstallScope string
var skillInstallPath string

// skillTarget describes how to install a fused-cli skill for one agent/app.
// userRoot/projectRoot/flatPath all take the skill's own name so one target
// definition serves every skill in skillSpecs. Locations below are each
// agent's own documented convention as of writing this, not guesses:
//
//   - claude:      Claude Code skills directory (SKILL.md + reference/, no
//     manifest needed): https://docs.claude.com/en/docs/claude-code/skills
//   - codex:       OpenAI Codex's user/repo skill scopes (open agent-skills
//     standard, same SKILL.md + references/ shape):
//     https://developers.openai.com/codex/skills
//   - antigravity:  Google Antigravity's global/workspace skill scopes (same
//     open standard, same SKILL.md + resources/ shape):
//     https://antigravity.google/docs/skills
//   - cursor:       Cursor has no SKILL.md/skills concept, and no notion of a
//     rule pointing at sibling reference files read on demand -- only
//     Project Rules (.cursor/rules/*.mdc, its own frontmatter fields) with
//     no stable global/per-user location, so only project scope is
//     supported, and each skill's manifest files are flattened into one
//     document per skill:
//     https://cursor.com/docs/rules
//   - windsurf:     Windsurf/Cascade rules are freeform Markdown in
//     .windsurf/rules/ with no frontmatter convention and no on-demand
//     reference mechanism either; its one global file (global_rules.md) is
//     a single shared file we shouldn't overwrite, so only project scope is
//     supported, also flattened into one document per skill.
type skillTarget struct {
	displayName  string
	supportsUser bool
	// userRoot/projectRoot return the skill's root DIRECTORY for
	// directory-based targets (claude/codex/antigravity): every manifest
	// file is written underneath, preserving its relative path.
	userRoot    func(name string) (string, error)
	projectRoot func(name string) string
	// flatPath is set instead, for targets with no skill-directory concept
	// (cursor/windsurf): one skill's manifest files are combined into one
	// document and transform reshapes it into that target's expected format.
	flatPath  func(name string) string
	transform func(combined, name string) string
}

func (t skillTarget) isFlat() bool { return t.flatPath != nil }

func skillTargets() map[string]skillTarget {
	return map[string]skillTarget{
		"claude": {
			displayName:  "Claude Code",
			supportsUser: true,
			userRoot: func(name string) (string, error) {
				return userHomeSubpath(".claude", "skills", name)
			},
			projectRoot: func(name string) string {
				return filepath.Join(".claude", "skills", name)
			},
		},
		"codex": {
			displayName:  "OpenAI Codex",
			supportsUser: true,
			userRoot: func(name string) (string, error) {
				return userHomeSubpath(".agents", "skills", name)
			},
			projectRoot: func(name string) string {
				return filepath.Join(".agents", "skills", name)
			},
		},
		"antigravity": {
			displayName:  "Google Antigravity",
			supportsUser: true,
			userRoot: func(name string) (string, error) {
				return userHomeSubpath(".gemini", "config", "skills", name)
			},
			// Antigravity's workspace skill scope is the same .agents/skills
			// convention Codex uses -- both follow the open agent-skills
			// standard, so a repo can share one copy for both agents.
			projectRoot: func(name string) string {
				return filepath.Join(".agents", "skills", name)
			},
		},
		"cursor": {
			displayName: "Cursor",
			flatPath: func(name string) string {
				return filepath.Join(".cursor", "rules", name+".mdc")
			},
			transform: toCursorMDC,
		},
		"windsurf": {
			displayName: "Windsurf",
			flatPath: func(name string) string {
				return filepath.Join(".windsurf", "rules", name+".md")
			},
			transform: toWindsurfRule,
		},
	}
}

func sortedSkillTargetNames() []string {
	targets := skillTargets()
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func userHomeSubpath(parts ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, parts...)...), nil
}

// combineSkillFiles flattens one skill's multiple files into one document
// for targets with no skill-directory/on-demand-reference concept (Cursor,
// Windsurf): everything SKILL.md would normally tell an agent to read on
// demand is inlined instead, in manifest order (SKILL.md first, so its
// frontmatter is still the first thing in the combined text).
func combineSkillFiles(files map[string]string, manifest []string) string {
	var b strings.Builder
	for _, relPath := range manifest {
		content, ok := files[relPath]
		if !ok {
			continue
		}
		if relPath != "SKILL.md" {
			fmt.Fprintf(&b, "\n\n## Reference: %s\n\n", relPath)
		}
		b.WriteString(content)
	}
	return b.String()
}

// toCursorMDC reshapes one skill's combined content for Cursor's Project
// Rules format. Cursor only recognizes .mdc files with its own frontmatter
// fields (description, globs, alwaysApply) under .cursor/rules/ -- a bare
// .md file there, or one carrying SKILL.md's name/description frontmatter,
// is silently ignored. Leaving globs empty and alwaysApply: false makes this
// an "Agent Requested" rule, matched against description like a skill would
// be.
func toCursorMDC(combined, _ string) string {
	description, body := splitSkillFrontmatter(combined)
	return fmt.Sprintf("---\ndescription: %s\nglobs:\nalwaysApply: false\n---\n\n%s", description, body)
}

// toWindsurfRule reshapes one skill's combined content for Windsurf/Cascade
// rules, which are freeform Markdown with no YAML frontmatter convention --
// Cascade reads the whole file as instructions, so the frontmatter is
// dropped in favor of a plain heading naming the skill.
func toWindsurfRule(combined, name string) string {
	_, body := splitSkillFrontmatter(combined)
	return fmt.Sprintf("# %s\n\n%s", name, body)
}

// splitSkillFrontmatter pulls the description field's raw YAML value
// (quotes included, so re-emitting it in another file's frontmatter stays
// valid YAML) and the Markdown body out of a SKILL.md's frontmatter block.
// Not a general YAML parser -- just enough for the fixed shape we control in
// every cli/skills/*/SKILL.md.
func splitSkillFrontmatter(content string) (description string, body string) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", content
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
		if strings.HasPrefix(lines[i], "description:") {
			description = strings.TrimSpace(strings.TrimPrefix(lines[i], "description:"))
		}
	}
	if end == -1 {
		return description, content
	}
	// The line right after the closing "---" is always the blank spacer line
	// SKILL.md's own frontmatter convention leaves before its first heading;
	// trimming it here (not just the leading newline) keeps a flattened
	// Cursor/Windsurf file from starting with an extra blank line.
	return description, strings.TrimLeft(strings.Join(lines[end+1:], "\n"), "\n")
}

var skillListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every fused-cli skill",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.skill.list", func(cmd *cobra.Command, _ []string) error {
		w := cmd.OutOrStdout()
		for _, name := range sortedSkillSpecNames() {
			spec, _ := skillSpecByName(name)
			fmt.Fprintf(w, "%-16s %s\n", spec.name, spec.summary)
		}
		return nil
	}),
}

var skillPrintCmd = &cobra.Command{
	Use:   "print [skill-file]",
	Short: "Print a fused-cli skill's content to stdout (default: SKILL.md)",
	Long: fmt.Sprintf(`Print a fused-cli skill's content to stdout.

With no argument, prints SKILL.md (the skill's own index). Pass a reference
file's relative path to print that instead. Use --skill to pick which skill
(default: fused-cli). Run 'fused-cli skill list' to see every skill name.

Available skills: %s`, strings.Join(sortedSkillSpecNames(), ", ")),
	Args: cobra.MaximumNArgs(1),
	RunE: WithTelemetry("cli.skill.print", func(cmd *cobra.Command, args []string) error {
		spec, ok := skillSpecByName(skillPrintName)
		if !ok {
			return fmt.Errorf("unknown --skill %q; available: %s", skillPrintName, strings.Join(sortedSkillSpecNames(), ", "))
		}
		files, _, err := resolveSkillFiles(spec)
		if err != nil {
			return err
		}
		relPath := "SKILL.md"
		if len(args) == 1 {
			relPath = args[0]
		}
		content, ok := files[relPath]
		if !ok {
			return fmt.Errorf("unknown file %q for skill %q; available: %s", relPath, spec.name, strings.Join(spec.manifest, ", "))
		}
		fmt.Fprint(cmd.OutOrStdout(), content)
		return nil
	}),
}

var skillInstallCmd = &cobra.Command{
	Use:   "install --for <agent-name>",
	Short: `Write fused-cli skill(s) into a target agent's skills/rules directory (e.g. --for claude)`,
	Long: `Write fused-cli skill(s) into a target agent's skills/rules directory.

With no --skill, installs every skill in skillSpecs. Pass --skill to install
just one. --path (an explicit destination) requires --skill, since it names
a single location and there's no single sensible place to put every skill.`,
	// Why: Write to OTEL to audit user/agent-triggered writes into another
	// tool's config directory, same as other mutating commands.
	Args: cobra.NoArgs,
	RunE: WithTelemetry("cli.skill.install", func(cmd *cobra.Command, args []string) error {
		target, ok := skillTargets()[skillInstallFor]
		if !ok {
			return fmt.Errorf("--for is required and must be one of: %s (got %q)",
				strings.Join(sortedSkillTargetNames(), ", "), skillInstallFor)
		}

		var specs []skillSpec
		if skillInstallName == "" {
			if skillInstallPath != "" {
				return fmt.Errorf("--path requires --skill (it names a single destination, not one per skill)")
			}
			specs = skillSpecs
		} else {
			spec, ok := skillSpecByName(skillInstallName)
			if !ok {
				return fmt.Errorf("unknown --skill %q; available: %s", skillInstallName, strings.Join(sortedSkillSpecNames(), ", "))
			}
			specs = []skillSpec{spec}
		}

		for _, spec := range specs {
			files, source, err := resolveSkillFiles(spec)
			if err != nil {
				return err
			}
			if target.isFlat() {
				if err := installFlatSkill(cmd, target, spec, files, source); err != nil {
					return err
				}
				recordAppliedChange(cmd.Context(), cmd.CommandPath(), "agent_skill")
				continue
			}
			if err := installDirectorySkill(cmd, target, spec, files, source); err != nil {
				return err
			}
			recordAppliedChange(cmd.Context(), cmd.CommandPath(), "agent_skill")
		}
		return nil
	}),
}

func installFlatSkill(cmd *cobra.Command, target skillTarget, spec skillSpec, files map[string]string, source string) error {
	if skillInstallScope == "user" {
		return fmt.Errorf("--for %s has no per-user install location; use --scope project (or --path)", skillInstallFor)
	}
	dest := skillInstallPath
	if dest == "" {
		dest = target.flatPath(spec.name)
	}
	content := target.transform(combineSkillFiles(files, spec.manifest), spec.name)
	if err := writeSkillFile(dest, content); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Installed %s skill for %s to %s (source: %s)\n", spec.name, target.displayName, dest, source)
	return nil
}

func installDirectorySkill(cmd *cobra.Command, target skillTarget, spec skillSpec, files map[string]string, source string) error {
	root, err := skillInstallRoot(target, spec.name)
	if err != nil {
		return err
	}
	for _, relPath := range spec.manifest {
		content, ok := files[relPath]
		if !ok {
			continue
		}
		if err := writeSkillFile(filepath.Join(root, filepath.FromSlash(relPath)), content); err != nil {
			return err
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Installed %s skill for %s to %s/ (%d files, source: %s)\n", spec.name, target.displayName, root, len(files), source)
	return nil
}

func init() {
	RootCmd.AddCommand(skillCmd)
	skillCmd.AddCommand(skillListCmd)
	skillCmd.AddCommand(skillPrintCmd)
	skillCmd.AddCommand(skillInstallCmd)

	skillPrintCmd.Flags().StringVar(&skillPrintName, "skill", "fused-cli",
		fmt.Sprintf(`Which skill to print (default: fused-cli). Supported: %s`, strings.Join(sortedSkillSpecNames(), ", ")))

	skillInstallCmd.Flags().StringVar(&skillInstallFor, "for", "",
		fmt.Sprintf(`Agent/app to install the skill for (required). Supported: %s`, strings.Join(sortedSkillTargetNames(), ", ")))
	skillInstallCmd.Flags().StringVar(&skillInstallName, "skill", "",
		fmt.Sprintf(`Which skill to install (default: all of them). Supported: %s`, strings.Join(sortedSkillSpecNames(), ", ")))
	skillInstallCmd.Flags().StringVar(&skillInstallScope, "scope", "", `Install location: "user" or "project" (default: the most common choice for --for; cursor/windsurf only support "project")`)
	skillInstallCmd.Flags().StringVar(&skillInstallPath, "path", "", "Exact path to write to (a directory for claude/codex/antigravity, a file for cursor/windsurf), overriding --scope. Requires --skill.")
}

// skillFetchBranch is the git ref every skill file is fetched from. Unlike
// version_check.go's update nudge, this is deliberately not pinned to the
// release tag: pinning to a version folder lets docs-only skill fixes land on
// main for an already released CLI version.
const skillFetchBranch = "main"

// skillRawContentBaseURL is the GitHub raw-content host + repo path every
// skill file is fetched from. It's a var (not folded into skillRawContentURL
// directly) so tests can point it at a local httptest server instead of
// making a real network call.
var skillRawContentBaseURL = "https://raw.githubusercontent.com/Usefused/cli"

// skillVersionFolder is the "skills/<version>/" path segment this build reads
// from, for both the GitHub fetch and embedded fallback. Dev builds read the
// moving skills/dev folder; release builds read the folder generated by
// `make bundle-skills VERSION=<tag>`.
func skillVersionFolder() string {
	if Version == "dev" {
		return "dev"
	}
	return strings.TrimPrefix(Version, "v")
}

func skillRawContentURL(relPath string, spec skillSpec) string {
	return fmt.Sprintf("%s/%s/skills/%s/%s/%s",
		skillRawContentBaseURL, skillFetchBranch, skillVersionFolder(), spec.name, relPath)
}

// resolveSkillFiles fetches every file in spec.manifest from this build's own
// version folder published on GitHub's main branch -- so a skill-only content
// fix doesn't require a new CLI release -- falling back to the copy embedded
// in this binary (main.go's go:embed) if ANY file fails to fetch: offline,
// timeout, one 404, etc. This is deliberately all-or-nothing so an install is
// never a mismatched mix of fetched and embedded content. Returns relative
// path -> content, plus a short label of where it came from, surfaced by
// `skill install`.
func resolveSkillFiles(spec skillSpec) (map[string]string, string, error) {
	fetched := make(map[string]string, len(spec.manifest))
	allFetched := true
	for _, relPath := range spec.manifest {
		content, ok := fetchSkillFile(relPath, spec)
		if !ok {
			allFetched = false
			break
		}
		fetched[relPath] = content
	}
	if allFetched {
		return fetched, fmt.Sprintf("github:%s/skills/%s/%s", skillFetchBranch, skillVersionFolder(), spec.name), nil
	}

	embedded, err := embeddedSkillFiles(spec)
	if err != nil {
		return nil, "", fmt.Errorf("could not fetch %s skill files from GitHub (skills/%s/%s) and no embedded copy is available: %w",
			spec.name, skillVersionFolder(), spec.name, err)
	}
	return embedded, "embedded (offline fallback)", nil
}

func fetchSkillFile(relPath string, spec skillSpec) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", skillRawContentURL(relPath, spec), nil)
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

func embeddedSkillFiles(spec skillSpec) (map[string]string, error) {
	if EmbeddedSkillFS == nil {
		return nil, fmt.Errorf("no embedded skill content available (EmbeddedSkillFS was never set)")
	}
	root := path.Join("skills", skillVersionFolder(), spec.name)
	files := make(map[string]string, len(spec.manifest))
	for _, relPath := range spec.manifest {
		data, err := fs.ReadFile(EmbeddedSkillFS, path.Join(root, relPath))
		if err != nil {
			return nil, err
		}
		files[relPath] = string(data)
	}
	return files, nil
}

// skillInstallRoot resolves the root directory to write a skill's files
// under, for directory-based targets (claude/codex/antigravity) only --
// flat targets (cursor/windsurf) are handled separately in
// installFlatSkill, since they write one file, not a tree.
func skillInstallRoot(target skillTarget, name string) (string, error) {
	if skillInstallPath != "" {
		return skillInstallPath, nil
	}
	scope := skillInstallScope
	if scope == "" {
		if target.supportsUser {
			scope = "user"
		} else {
			scope = "project"
		}
	}
	switch scope {
	case "project":
		return target.projectRoot(name), nil
	case "user":
		if target.userRoot == nil {
			return "", fmt.Errorf("--for %s has no per-user install location; use --scope project (or --path)", skillInstallFor)
		}
		return target.userRoot(name)
	default:
		return "", fmt.Errorf(`unknown --scope %q (expected "user" or "project")`, scope)
	}
}

// writeSkillFile mirrors internal/config.Save's atomic write (temp file then
// rename) so a crash mid-write can't leave a truncated file behind.
func writeSkillFile(path, content string) error {
	return atomicWriteFile(path, []byte(content), 0644, nil)
}
