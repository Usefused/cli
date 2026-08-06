package cmd

import (
	"fmt"
	"strings"
	"text/tabwriter"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

var userCmd = commandGroup("user", "Manage workspace people and personal credentials")

var userListFlags listFlags
var userListSearch string
var userListIncludeSuspended bool
var userListCmd = &cobra.Command{
	Use: "list", Short: "List people", Args: cobra.NoArgs,
	RunE: WithTelemetry("cli.user.list", func(cmd *cobra.Command, _ []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		page, err := client.ListUsers(userListSearch, userListIncludeSuspended, userListFlags.pageOptions())
		if err != nil {
			return err
		}
		if len(page.Items) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No people found.")
			return nil
		}
		printUsers(cmd, page.Items)
		printPageSummary(cmd.OutOrStdout(), page.Total, userListFlags)
		return nil
	}),
}

var userShowCmd = &cobra.Command{
	Use: "show <email-or-id>", Short: "Show a person, team memberships, and credential metadata", Args: cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.user.show", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		user, err := client.GetUser(args[0])
		if err != nil {
			return err
		}
		printUser(cmd, *user)
		return nil
	}),
}

var userCreateName string
var userCreateCmd = &cobra.Command{
	Use: "create <email>", Short: "Add a person without sending an email", Args: cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.user.create", func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(userCreateName) == "" {
			return fmt.Errorf("flag --name is required")
		}
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		payload, err := client.CreateUser(cliapi.CreateUserInput{Email: strings.TrimSpace(args[0]), DisplayName: strings.TrimSpace(userCreateName)})
		if err != nil {
			return err
		}
		recordAppliedChangeIf(cmd.Context(), "user.create", "user", payload.Changed)
		if !payload.Changed {
			fmt.Fprintf(cmd.OutOrStdout(), "%s (%s) already exists.\n", payload.User.DisplayName, payload.User.ID)
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Added %s (%s). Create a personal credential when they need to sign in.\n", payload.User.DisplayName, payload.User.ID)
		return nil
	}),
}

var userUpdateEmail string
var userUpdateName string
var userUpdateCmd = &cobra.Command{
	Use: "update <email-or-id>", Short: "Update a person's details", Args: cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.user.update", func(cmd *cobra.Command, args []string) error {
		input, err := userUpdateInput(cmd)
		if err != nil {
			return err
		}
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		payload, err := client.UpdateUser(args[0], input)
		if err != nil {
			return err
		}
		recordAppliedChangeIf(cmd.Context(), "user.update", "user", payload.Changed)
		if !payload.Changed {
			fmt.Fprintln(cmd.OutOrStdout(), "Person is already up to date.")
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Updated %s (%s).\n", payload.User.DisplayName, payload.User.ID)
		return nil
	}),
}

var userSuspendCmd = userStatusCommand("suspend <email-or-id>", "Suspend a person and stop their credentials", "cli.user.suspend", func(client *cliapi.Client, id string) (*cliapi.UserMutationPayload, error) {
	return client.SuspendUser(id)
})
var userReactivateCmd = userStatusCommand("reactivate <email-or-id>", "Reactivate a suspended person", "cli.user.reactivate", func(client *cliapi.Client, id string) (*cliapi.UserMutationPayload, error) {
	return client.ReactivateUser(id)
})

type userStatusMutation func(*cliapi.Client, string) (*cliapi.UserMutationPayload, error)

func userStatusCommand(use, short, spanName string, mutate userStatusMutation) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Args: cobra.ExactArgs(1),
		RunE: WithTelemetry(spanName, func(cmd *cobra.Command, args []string) error {
			client, err := getAPIClient()
			if err != nil {
				return err
			}
			payload, err := mutate(client, args[0])
			if err != nil {
				return err
			}
			recordAppliedChangeIf(cmd.Context(), spanName, "user", payload.Changed)
			if !payload.Changed {
				fmt.Fprintf(cmd.OutOrStdout(), "%s is already %s.\n", payload.User.DisplayName, strings.ToLower(payload.User.Status))
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is now %s.\n", payload.User.DisplayName, strings.ToLower(payload.User.Status))
			return nil
		}),
	}
}

var userCredentialCmd = commandGroup("credential", "Issue or revoke personal credentials")
var userCredentialName string
var userCredentialIssueCmd = &cobra.Command{
	Use: "issue <email-or-id>", Short: "Issue a personal credential and show it once", Args: cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.user.credential.issue", func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(userCredentialName)
		if name == "" {
			return fmt.Errorf("flag --name is required")
		}
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		payload, err := client.IssueUserCredential(args[0], name)
		if err != nil {
			return err
		}
		recordAppliedChangeIf(cmd.Context(), "user.credential.issue", "control_credential", payload.Changed)
		if !payload.Changed {
			fmt.Fprintln(cmd.OutOrStdout(), "Credential was not issued; no state changed.")
			return nil
		}
		return printIssuedCredential(cmd, payload)
	}),
}

var userCredentialRevokeCmd = &cobra.Command{
	Use: "revoke <user-email-or-id> <credential-name-or-id>", Short: "Revoke a personal credential", Args: cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.user.credential.revoke", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		payload, err := client.RevokeUserCredential(args[0], args[1])
		if err != nil {
			return err
		}
		recordAppliedChangeIf(cmd.Context(), "user.credential.revoke", "control_credential", payload.Changed)
		printMutationOutcome(cmd, payload.Changed, "Credential revoked.", "Credential is already revoked.")
		return nil
	}),
}

func userUpdateInput(cmd *cobra.Command) (cliapi.UpdateUserInput, error) {
	input := cliapi.UpdateUserInput{}
	if cmd.Flags().Changed("email") {
		input.Email = stringPointer(strings.TrimSpace(userUpdateEmail))
	}
	if cmd.Flags().Changed("name") {
		input.DisplayName = stringPointer(strings.TrimSpace(userUpdateName))
	}
	if input.Email == nil && input.DisplayName == nil {
		return input, fmt.Errorf("provide --email or --name")
	}
	return input, nil
}

func printUsers(cmd *cobra.Command, users []cliapi.User) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tEMAIL\tSTATUS\tUSER_ID")
	for _, user := range users {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", user.DisplayName, user.Email, strings.ToLower(user.Status), user.ID)
	}
	_ = w.Flush()
}

func printUser(cmd *cobra.Command, user cliapi.User) {
	fmt.Fprintf(cmd.OutOrStdout(), "Name: %s\nEmail: %s\nStatus: %s\nUser ID: %s\n", user.DisplayName, user.Email, strings.ToLower(user.Status), user.ID)
	printUserMemberships(cmd, user.Memberships)
	printUserCredentials(cmd, user.Credentials)
}

func printUserMemberships(cmd *cobra.Command, memberships []cliapi.UserTeamMembership) {
	if len(memberships) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Teams: none")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "TEAM\tROLE\tTEAM_ID")
	for _, membership := range memberships {
		fmt.Fprintf(w, "%s\t%s\t%s\n", membership.TeamName, strings.ToLower(membership.MembershipRole), membership.TeamID)
	}
	_ = w.Flush()
}

func printUserCredentials(cmd *cobra.Command, credentials []cliapi.ControlCredential) {
	if len(credentials) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Credentials: none")
		return
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "CREDENTIAL\tPREFIX\tSTATUS\tCREDENTIAL_ID")
	for _, credential := range credentials {
		status := "active"
		if credential.RevokedAt != "" {
			status = "revoked"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", credential.Name, credential.KeyPrefix, status, credential.ID)
	}
	_ = w.Flush()
}

func printIssuedCredential(cmd *cobra.Command, payload *cliapi.IssuedCredentialPayload) error {
	if payload.Secret == "" {
		return fmt.Errorf("Engine reported an issued credential without returning its one-time secret")
	}
	// The raw key is intentionally written only to the command's requested
	// output. It is never attached to OTEL, logs, config, or command state.
	fmt.Fprintln(cmd.OutOrStdout(), "WARNING: Copy this personal key now. It will not be shown again.")
	fmt.Fprintln(cmd.OutOrStdout(), "Keep it secret; anyone with this key can act as this person.")
	fmt.Fprintln(cmd.OutOrStdout(), payload.Secret)
	fmt.Fprintf(cmd.OutOrStdout(), "Credential ID: %s\n", payload.Credential.ID)
	return nil
}

func init() {
	RootCmd.AddCommand(userCmd)
	userCmd.AddCommand(userListCmd, userShowCmd, userCreateCmd, userUpdateCmd, userSuspendCmd, userReactivateCmd, userCredentialCmd)
	addListFlags(userListCmd, &userListFlags)
	userListCmd.Flags().StringVar(&userListSearch, "search", "", "Search names and email addresses")
	userListCmd.Flags().BoolVar(&userListIncludeSuspended, "include-suspended", false, "Include suspended people")
	userCreateCmd.Flags().StringVar(&userCreateName, "name", "", "Display name")
	userUpdateCmd.Flags().StringVar(&userUpdateEmail, "email", "", "New email address")
	userUpdateCmd.Flags().StringVar(&userUpdateName, "name", "", "New display name")
	userCredentialCmd.AddCommand(userCredentialIssueCmd, userCredentialRevokeCmd)
	userCredentialIssueCmd.Flags().StringVar(&userCredentialName, "name", "personal", "Credential name")
}
