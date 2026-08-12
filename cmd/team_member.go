package cmd

import (
	"fmt"
	"strings"
	"text/tabwriter"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

var teamMemberCmd = commandGroup("member", "Manage team members")
var teamMemberListFlags listFlags
var teamMemberListCmd = &cobra.Command{
	Use: "list <team-slug-or-id>", Short: "List team members", Args: cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.team.member.list", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		page, err := client.ListTeamMembers(args[0], teamMemberListFlags.pageOptions())
		if err != nil {
			return err
		}
		if wantsJSON(cmd) {
			return writeJSONPage(cmd, page.Items, page.Total, teamMemberListFlags)
		}
		if len(page.Items) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No team members found.")
			return nil
		}
		printTeamMembers(cmd, page.Items)
		printPageSummary(cmd.OutOrStdout(), page.Total, teamMemberListFlags)
		return nil
	}),
}

var teamMemberRole string
var teamMemberAddCmd = &cobra.Command{
	Use: "add <team-slug-or-id> <email>", Short: "Add a person by email, creating their record when needed", Args: cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.team.member.add", func(cmd *cobra.Command, args []string) error {
		role, err := normalizeMembershipRole(teamMemberRole)
		if err != nil {
			return err
		}
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		payload, err := client.AddTeamMember(args[0], strings.TrimSpace(args[1]), role)
		if err != nil {
			return err
		}
		recordAppliedChangeIf(cmd.Context(), "team.member.add", "team_membership", payload.Changed)
		if payload.Changed && payload.Membership != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Added %s to the team.\n", payload.Membership.DisplayName)
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "Team membership is already up to date.")
		}
		return nil
	}),
}

var teamMemberRemoveCmd = &cobra.Command{
	Use: "remove <team-slug-or-id> <user-email-or-id>", Short: "Remove a person from a team", Args: cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.team.member.remove", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		payload, err := client.RemoveTeamMember(args[0], args[1])
		if err != nil {
			return err
		}
		recordAppliedChangeIf(cmd.Context(), "team.member.remove", "team_membership", payload.Changed)
		printMutationOutcome(cmd, payload.Changed, "Team member removed.", "Team member is already absent.")
		return nil
	}),
}

func normalizeMembershipRole(value string) (string, error) {
	role := strings.ToUpper(strings.TrimSpace(value))
	if role == "MEMBER" || role == "MANAGER" {
		return role, nil
	}
	return "", fmt.Errorf("membership role must be member or manager")
}

func printTeamMembers(cmd *cobra.Command, members []cliapi.TeamMember) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tEMAIL\tROLE\tSTATUS\tUSER_ID")
	for _, member := range members {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", member.DisplayName, member.Email, strings.ToLower(member.MembershipRole), strings.ToLower(member.Status), member.UserID)
	}
	_ = w.Flush()
}

func init() {
	teamCmd.AddCommand(teamMemberCmd)
	teamMemberCmd.AddCommand(teamMemberListCmd, teamMemberAddCmd, teamMemberRemoveCmd)
	addJSONOutputFlag(teamMemberListCmd)
	addListFlags(teamMemberListCmd, &teamMemberListFlags)
	teamMemberAddCmd.Flags().StringVar(&teamMemberRole, "role", "member", "Membership role: member or manager")
}
