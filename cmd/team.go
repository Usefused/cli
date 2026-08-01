package cmd

import (
	"fmt"
	"strings"
	"text/tabwriter"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

var teamCmd = &cobra.Command{
	Use:   "team",
	Short: "Manage workspace teams and their access",
}

var teamListFlags listFlags
var teamListSearch string
var teamListIncludeArchived bool
var teamListCmd = &cobra.Command{
	Use:   "list",
	Short: "List teams",
	RunE: WithTelemetry("cli.team.list", func(cmd *cobra.Command, _ []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		page, err := client.ListTeams(teamListSearch, teamListIncludeArchived, teamListFlags.pageOptions())
		if err != nil {
			return err
		}
		if len(page.Items) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No teams found.")
			return nil
		}
		printTeams(cmd, page.Items)
		printPageSummary(cmd.OutOrStdout(), page.Total, teamListFlags)
		return nil
	}),
}

var teamShowCmd = &cobra.Command{
	Use:   "show <team-id>",
	Short: "Show a team and its access",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.team.show", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		team, err := client.GetTeam(args[0])
		if err != nil {
			return err
		}
		printTeam(cmd, *team)
		return nil
	}),
}

var teamEligibleOwnerFlags listFlags
var teamEligibleOwnerSearch string
var teamEligibleOwnersCmd = &cobra.Command{
	Use:   "eligible-owners",
	Short: "List teams you can choose to own a new SDK, MCP server, or webhook",
	RunE: WithTelemetry("cli.team.eligible_owners", func(cmd *cobra.Command, _ []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		page, err := client.ListArtifactOwningTeams(teamEligibleOwnerSearch, teamEligibleOwnerFlags.pageOptions())
		if err != nil {
			return err
		}
		if len(page.Items) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No eligible owning teams found. Join an active team or ask an access administrator to add you.")
			return nil
		}
		printArtifactOwningTeams(cmd, page.Items)
		printPageSummary(cmd.OutOrStdout(), page.Total, teamEligibleOwnerFlags)
		return nil
	}),
}

var teamCreateSlug string
var teamCreateDescription string
var teamCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a team",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.team.create", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		payload, err := client.CreateTeam(cliapi.CreateTeamInput{
			Name: strings.TrimSpace(args[0]), Slug: strings.TrimSpace(teamCreateSlug), Description: strings.TrimSpace(teamCreateDescription),
		})
		if err != nil {
			return err
		}
		recordAppliedChange(cmd.Context(), "team.create", "team")
		fmt.Fprintf(cmd.OutOrStdout(), "Created team %s (%s).\n", payload.Team.Name, payload.Team.ID)
		return nil
	}),
}

var teamUpdateName string
var teamUpdateSlug string
var teamUpdateDescription string
var teamUpdateCmd = &cobra.Command{
	Use:   "update <team-id>",
	Short: "Update a team's details",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.team.update", func(cmd *cobra.Command, args []string) error {
		input, err := teamUpdateInput(cmd)
		if err != nil {
			return err
		}
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		payload, err := client.UpdateTeam(args[0], input)
		if err != nil {
			return err
		}
		recordAppliedChange(cmd.Context(), "team.update", "team")
		fmt.Fprintf(cmd.OutOrStdout(), "Updated team %s (%s).\n", payload.Team.Name, payload.Team.ID)
		return nil
	}),
}

var teamArchiveCmd = &cobra.Command{
	Use:   "archive <team-id>",
	Short: "Archive a team that has no remaining bindings or owned artifacts",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.team.archive", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		payload, err := client.ArchiveTeam(args[0])
		if err != nil {
			return err
		}
		recordAppliedChange(cmd.Context(), "team.archive", "team")
		fmt.Fprintf(cmd.OutOrStdout(), "Archived team %s (%s).\n", payload.Team.Name, payload.Team.ID)
		return nil
	}),
}

var teamBuildAccessFlags listFlags
var teamBuildAccessSearch string
var teamBuildAccessResource string
var teamBuildAccessCmd = &cobra.Command{
	Use:   "build-access <team-id>",
	Short: "List services or buckets available to both you and a team",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.team.build_access", func(cmd *cobra.Command, args []string) error {
		resourceType, err := normalizeSelectorResourceType(teamBuildAccessResource)
		if err != nil {
			return err
		}
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		page, err := client.ListArtifactBuildSelectors(args[0], resourceType, teamBuildAccessSearch, teamBuildAccessFlags.pageOptions())
		if err != nil {
			return err
		}
		if len(page.Items) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "No %s available to both you and this team.\n", strings.ToLower(resourceType)+"s")
			return nil
		}
		printArtifactBuildSelectors(cmd, page.Items)
		printPageSummary(cmd.OutOrStdout(), page.Total, teamBuildAccessFlags)
		return nil
	}),
}

var teamAccessCmd = &cobra.Command{Use: "access", Short: "Manage a team's workspace and resource access"}
var teamWorkspaceAccessCmd = &cobra.Command{Use: "workspace", Short: "Manage workspace roles"}
var teamWorkspaceSetCmd = &cobra.Command{
	Use:   "set <team-id> <owner|admin|builder|viewer>",
	Short: "Set a team's workspace role",
	Args:  cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.team.access.workspace.set", func(cmd *cobra.Command, args []string) error {
		role, err := normalizeWorkspaceRole(args[1])
		if err != nil {
			return err
		}
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		if _, err := client.SetTeamWorkspaceRole(args[0], stringPointer(role)); err != nil {
			return err
		}
		recordAppliedChange(cmd.Context(), "team.access.workspace.set", "team_binding")
		fmt.Fprintln(cmd.OutOrStdout(), "Workspace role updated.")
		return nil
	}),
}

var teamWorkspaceClearCmd = &cobra.Command{
	Use:   "clear <team-id>",
	Short: "Clear a team's workspace role",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.team.access.workspace.clear", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		if _, err := client.SetTeamWorkspaceRole(args[0], nil); err != nil {
			return err
		}
		recordAppliedChange(cmd.Context(), "team.access.workspace.clear", "team_binding")
		fmt.Fprintln(cmd.OutOrStdout(), "Workspace role cleared.")
		return nil
	}),
}

var teamServiceAccessCmd = &cobra.Command{Use: "service", Short: "Manage service access"}
var teamServiceGrantCmd = resourceAccessCommand("grant <team-id> <service-id> <use|manage>", "Grant service access", "cli.team.access.service.grant", grantTeamServiceAccess)
var teamServiceRevokeCmd = resourceAccessCommand("revoke <team-id> <service-id> <use|manage>", "Revoke service access", "cli.team.access.service.revoke", revokeTeamServiceAccess)
var teamBucketAccessCmd = &cobra.Command{Use: "bucket", Short: "Manage bucket access"}
var teamBucketGrantCmd = resourceAccessCommand("grant <team-id> <bucket-id> <use|manage>", "Grant bucket access", "cli.team.access.bucket.grant", grantTeamBucketAccess)
var teamBucketRevokeCmd = resourceAccessCommand("revoke <team-id> <bucket-id> <use|manage>", "Revoke bucket access", "cli.team.access.bucket.revoke", revokeTeamBucketAccess)
var teamArtifactAccessCmd = &cobra.Command{Use: "artifact", Short: "Share SDKs and MCP servers with a team"}
var teamArtifactGrantCmd = artifactAccessCommand("grant <team-id> <artifact-id> <read|manage>", "Grant SDK or MCP server access", "cli.team.access.artifact.grant", grantTeamArtifactAccess)
var teamArtifactRevokeCmd = artifactAccessCommand("revoke <team-id> <artifact-id> <read|manage>", "Revoke SDK or MCP server access", "cli.team.access.artifact.revoke", revokeTeamArtifactAccess)

type resourceAccessMutation func(*cliapi.Client, string, string, string) error

func resourceAccessCommand(use, short, spanName string, mutate resourceAccessMutation) *cobra.Command {
	return teamAccessCommand(use, short, spanName, normalizeAccessLevel, mutate)
}

func artifactAccessCommand(use, short, spanName string, mutate resourceAccessMutation) *cobra.Command {
	return teamAccessCommand(use, short, spanName, normalizeArtifactAccessLevel, mutate)
}

type accessLevelNormalizer func(string) (string, error)

func teamAccessCommand(use, short, spanName string, normalize accessLevelNormalizer, mutate resourceAccessMutation) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Args: cobra.ExactArgs(3),
		RunE: WithTelemetry(spanName, func(cmd *cobra.Command, args []string) error {
			level, err := normalize(args[2])
			if err != nil {
				return err
			}
			client, err := getAPIClient()
			if err != nil {
				return err
			}
			if err := mutate(client, args[0], args[1], level); err != nil {
				return err
			}
			recordAppliedChange(cmd.Context(), spanName, "team_binding")
			fmt.Fprintln(cmd.OutOrStdout(), "Team access updated.")
			return nil
		}),
	}
}

func grantTeamServiceAccess(client *cliapi.Client, teamID, resourceID, level string) error {
	_, err := client.GrantTeamServiceAccess(teamID, resourceID, level)
	return err
}

func revokeTeamServiceAccess(client *cliapi.Client, teamID, resourceID, level string) error {
	_, err := client.RevokeTeamServiceAccess(teamID, resourceID, level)
	return err
}

func grantTeamBucketAccess(client *cliapi.Client, teamID, resourceID, level string) error {
	_, err := client.GrantTeamBucketAccess(teamID, resourceID, level)
	return err
}

func revokeTeamBucketAccess(client *cliapi.Client, teamID, resourceID, level string) error {
	_, err := client.RevokeTeamBucketAccess(teamID, resourceID, level)
	return err
}

func grantTeamArtifactAccess(client *cliapi.Client, teamID, resourceID, level string) error {
	_, err := client.GrantTeamArtifactAccess(teamID, resourceID, level)
	return err
}

func revokeTeamArtifactAccess(client *cliapi.Client, teamID, resourceID, level string) error {
	_, err := client.RevokeTeamArtifactAccess(teamID, resourceID, level)
	return err
}

func teamUpdateInput(cmd *cobra.Command) (cliapi.UpdateTeamInput, error) {
	input := cliapi.UpdateTeamInput{}
	if cmd.Flags().Changed("name") {
		input.Name = stringPointer(strings.TrimSpace(teamUpdateName))
	}
	if cmd.Flags().Changed("slug") {
		input.Slug = stringPointer(strings.TrimSpace(teamUpdateSlug))
	}
	if cmd.Flags().Changed("description") {
		input.Description = stringPointer(strings.TrimSpace(teamUpdateDescription))
	}
	if input.Name == nil && input.Slug == nil && input.Description == nil {
		return input, fmt.Errorf("provide at least one of --name, --slug, or --description")
	}
	return input, nil
}

func stringPointer(value string) *string { return &value }

func normalizeWorkspaceRole(value string) (string, error) {
	role := strings.ToUpper(strings.TrimSpace(value))
	switch role {
	case "OWNER", "ADMIN", "BUILDER", "VIEWER":
		return role, nil
	default:
		return "", fmt.Errorf("workspace role must be owner, admin, builder, or viewer")
	}
}

func normalizeAccessLevel(value string) (string, error) {
	// Product language is Use/Manage while the GraphQL enum uses USER/MANAGER
	// to match the seeded resource-role slugs.
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "use", "user":
		return "USER", nil
	case "manage", "manager":
		return "MANAGER", nil
	default:
		return "", fmt.Errorf("access level must be use or manage")
	}
}

func normalizeArtifactAccessLevel(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "read", "reader":
		return "READER", nil
	case "manage", "manager":
		return "MANAGER", nil
	default:
		return "", fmt.Errorf("artifact access level must be read or manage")
	}
}

func normalizeSelectorResourceType(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "service", "services":
		return "SERVICE", nil
	case "bucket", "buckets":
		return "BUCKET", nil
	default:
		return "", fmt.Errorf("resource must be service or bucket")
	}
}

func printArtifactBuildSelectors(cmd *cobra.Command, selectors []cliapi.ArtifactBuildSelector) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "TYPE\tNAME\tRESOURCE_ID")
	for _, selector := range selectors {
		fmt.Fprintf(w, "%s\t%s\t%s\n", strings.ToLower(selector.ResourceType), selector.DisplayName, selector.ResourceID)
	}
	_ = w.Flush()
}

func printTeams(cmd *cobra.Command, teams []cliapi.Team) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSLUG\tSTATUS\tTEAM_ID")
	for _, team := range teams {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", team.Name, team.Slug, team.Status, team.ID)
	}
	_ = w.Flush()
}

func printArtifactOwningTeams(cmd *cobra.Command, teams []cliapi.ArtifactOwningTeam) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSLUG\tTEAM_ID")
	for _, team := range teams {
		fmt.Fprintf(w, "%s\t%s\t%s\n", team.Name, team.Slug, team.ID)
	}
	_ = w.Flush()
}

func printTeam(cmd *cobra.Command, team cliapi.Team) {
	fmt.Fprintf(cmd.OutOrStdout(), "Name: %s\nSlug: %s\nStatus: %s\nTeam ID: %s\nDescription: %s\n", team.Name, team.Slug, team.Status, team.ID, team.Description)
	if len(team.Bindings) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Access: none")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "ACCESS\tRESOURCE\tRESOURCE_ID")
	for _, binding := range team.Bindings {
		resource := binding.ResourceDisplayName
		if resource == "" {
			resource = binding.ResourceType
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", binding.RoleDisplayName, resource, binding.ResourceID)
	}
	_ = w.Flush()
}

func init() {
	RootCmd.AddCommand(teamCmd)
	teamCmd.AddCommand(teamListCmd, teamEligibleOwnersCmd, teamShowCmd, teamCreateCmd, teamUpdateCmd, teamArchiveCmd, teamBuildAccessCmd, teamAccessCmd)
	addListFlags(teamListCmd, &teamListFlags)
	teamListCmd.Flags().StringVar(&teamListSearch, "search", "", "Search team names and slugs")
	teamListCmd.Flags().BoolVar(&teamListIncludeArchived, "include-archived", false, "Include archived teams")
	addListFlags(teamEligibleOwnersCmd, &teamEligibleOwnerFlags)
	teamEligibleOwnersCmd.Flags().StringVar(&teamEligibleOwnerSearch, "search", "", "Search eligible team names and slugs")
	teamCreateCmd.Flags().StringVar(&teamCreateSlug, "slug", "", "Optional stable team slug; omitted derives it from the name")
	teamCreateCmd.Flags().StringVar(&teamCreateDescription, "description", "", "Team description")
	teamUpdateCmd.Flags().StringVar(&teamUpdateName, "name", "", "New team name")
	teamUpdateCmd.Flags().StringVar(&teamUpdateSlug, "slug", "", "New team slug")
	teamUpdateCmd.Flags().StringVar(&teamUpdateDescription, "description", "", "New description; pass an empty value to clear")
	addListFlags(teamBuildAccessCmd, &teamBuildAccessFlags)
	teamBuildAccessCmd.Flags().StringVar(&teamBuildAccessResource, "resource", "service", "Resource to list: service or bucket")
	teamBuildAccessCmd.Flags().StringVar(&teamBuildAccessSearch, "search", "", "Search available resource names")
	teamAccessCmd.AddCommand(teamWorkspaceAccessCmd, teamServiceAccessCmd, teamBucketAccessCmd, teamArtifactAccessCmd)
	teamWorkspaceAccessCmd.AddCommand(teamWorkspaceSetCmd, teamWorkspaceClearCmd)
	teamServiceAccessCmd.AddCommand(teamServiceGrantCmd, teamServiceRevokeCmd)
	teamBucketAccessCmd.AddCommand(teamBucketGrantCmd, teamBucketRevokeCmd)
	teamArtifactAccessCmd.AddCommand(teamArtifactGrantCmd, teamArtifactRevokeCmd)
}
