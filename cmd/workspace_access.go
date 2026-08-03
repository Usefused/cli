package cmd

import (
	"fmt"
	"strings"
	"text/tabwriter"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

var workspaceAccessCmd = &cobra.Command{
	Use:   "access",
	Short: "Share selected buckets, SDKs, and MCP servers across the workspace",
}

var workspaceAccessListFlags listFlags
var workspaceAccessListResource string
var workspaceAccessListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workspace-wide resource access",
	RunE: WithTelemetry("cli.workspace.access.list", func(cmd *cobra.Command, _ []string) error {
		resourceType, err := normalizeWorkspaceShareResource(workspaceAccessListResource)
		if err != nil {
			return err
		}
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		page, err := client.ListWorkspaceShares(resourceType, workspaceAccessListFlags.pageOptions())
		if err != nil {
			return err
		}
		if len(page.Items) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No workspace-wide resource access found.")
			return nil
		}
		printWorkspaceShares(cmd, page.Items)
		printPageSummary(cmd.OutOrStdout(), page.Total, workspaceAccessListFlags)
		return nil
	}),
}

var workspaceBucketAccessCmd = &cobra.Command{Use: "bucket", Short: "Share a bucket for workspace-wide use"}
var workspaceBucketAccessGrantCmd = workspaceShareCommand("grant <bucket-name-or-id>", "Share a bucket for workspace-wide use", "cli.workspace.access.bucket.grant", grantWorkspaceBucketAccess)
var workspaceBucketAccessRevokeCmd = workspaceShareCommand("revoke <bucket-name-or-id>", "Stop workspace-wide bucket use", "cli.workspace.access.bucket.revoke", revokeWorkspaceBucketAccess)
var workspaceArtifactAccessCmd = &cobra.Command{Use: "artifact", Short: "Share an SDK or MCP server across the workspace"}
var workspaceArtifactAccessGrantCmd = workspaceShareCommand("grant <artifact-name[@version]-or-id>", "Share an SDK or MCP server for workspace-wide use", "cli.workspace.access.artifact.grant", grantWorkspaceArtifactAccess)
var workspaceArtifactAccessRevokeCmd = workspaceShareCommand("revoke <artifact-name[@version]-or-id>", "Stop workspace-wide SDK or MCP server use", "cli.workspace.access.artifact.revoke", revokeWorkspaceArtifactAccess)

type workspaceShareMutation func(*cliapi.Client, string) (*cliapi.WorkspaceShareMutationPayload, error)

func workspaceShareCommand(use, short, spanName string, mutate workspaceShareMutation) *cobra.Command {
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
			recordAppliedChangeIf(cmd.Context(), spanName, "workspace_share", payload.Changed)
			printMutationOutcome(cmd, payload.Changed, "Workspace access updated.", "Workspace access is already up to date.")
			return nil
		}),
	}
}

func grantWorkspaceBucketAccess(client *cliapi.Client, id string) (*cliapi.WorkspaceShareMutationPayload, error) {
	return client.GrantWorkspaceBucketAccess(id)
}

func revokeWorkspaceBucketAccess(client *cliapi.Client, id string) (*cliapi.WorkspaceShareMutationPayload, error) {
	return client.RevokeWorkspaceBucketAccess(id)
}

func grantWorkspaceArtifactAccess(client *cliapi.Client, id string) (*cliapi.WorkspaceShareMutationPayload, error) {
	return client.GrantWorkspaceArtifactAccess(id)
}

func revokeWorkspaceArtifactAccess(client *cliapi.Client, id string) (*cliapi.WorkspaceShareMutationPayload, error) {
	return client.RevokeWorkspaceArtifactAccess(id)
}

func normalizeWorkspaceShareResource(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "", nil
	case "bucket":
		return "BUCKET", nil
	case "artifact":
		return "ARTIFACT", nil
	default:
		return "", fmt.Errorf("resource must be bucket or artifact")
	}
}

func printWorkspaceShares(cmd *cobra.Command, shares []cliapi.WorkspaceShare) {
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "TYPE\tNAME\tID\tACCESS")
	for _, share := range shares {
		fmt.Fprintf(writer, "%s\t%s\t%s\tuse\n", share.ResourceType, share.ResourceDisplayName, share.ResourceID)
	}
	_ = writer.Flush()
}

func init() {
	workspaceCmd.AddCommand(workspaceAccessCmd)
	workspaceAccessCmd.AddCommand(workspaceAccessListCmd, workspaceBucketAccessCmd, workspaceArtifactAccessCmd)
	workspaceBucketAccessCmd.AddCommand(workspaceBucketAccessGrantCmd, workspaceBucketAccessRevokeCmd)
	workspaceArtifactAccessCmd.AddCommand(workspaceArtifactAccessGrantCmd, workspaceArtifactAccessRevokeCmd)
	workspaceAccessListCmd.Flags().StringVar(&workspaceAccessListResource, "resource", "", "Filter by bucket or artifact")
	addListFlags(workspaceAccessListCmd, &workspaceAccessListFlags)
}
