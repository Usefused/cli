package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var workspaceConnectionCmd = &cobra.Command{
	Use:   "connection",
	Short: "Manage connected user resources",
}

var workspaceConnectionResourcesCmd = &cobra.Command{
	Use:   "resources",
	Short: "Inspect and select provider resources",
}

var workspaceConnectionResourcesListCmd = &cobra.Command{
	Use:   "list <connection-id>",
	Short: "List active resources for a connected user",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.workspace.connection_resources.list", func(cmd *cobra.Command, args []string) error {
		return runConnectionResourcesList(cmd, args[0])
	}),
}

var workspaceConnectionResourcesDefaultCmd = &cobra.Command{
	Use:   "set-default <connection-id> <resource-id>",
	Short: "Set the default resource for a connected user",
	Args:  cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.workspace.connection_resources.set_default", func(cmd *cobra.Command, args []string) error {
		return runConnectionResourceSetDefault(cmd, args[0], args[1])
	}),
}

var workspaceConnectionResourcesRediscoverCmd = &cobra.Command{
	Use:   "rediscover <connection-id>",
	Short: "Refresh provider resources for a connected user",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.workspace.connection_resources.rediscover", func(cmd *cobra.Command, args []string) error {
		return runConnectionResourcesRediscover(cmd, args[0])
	}),
}

// runConnectionResourcesList prints stable opaque IDs so users can pass them
// through fused.resourceId without copying provider URLs into configuration.
func runConnectionResourcesList(cmd *cobra.Command, connectionID string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	resources, err := client.ListConnectionResources(connectionID)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tDISPLAY_NAME\tRESOURCE_TYPE\tDEFAULT")
	for _, resource := range resources {
		fmt.Fprintf(w, "%s\t%s\t%s\t%t\n", resource.ID, resource.DisplayName, resource.ResourceType, resource.IsDefault)
	}
	w.Flush()
	return nil
}

// runConnectionResourceSetDefault delegates ownership and resource membership
// checks to Engine before confirming the user-triggered routing change.
func runConnectionResourceSetDefault(cmd *cobra.Command, connectionID, resourceID string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	resource, err := client.SetDefaultConnectionResource(connectionID, resourceID)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Default resource set to %s (%s).\n", resource.DisplayName, resource.ID)
	return nil
}

// runConnectionResourcesRediscover prints only a count because provider URLs
// and identifiers are available through the explicit list command when needed.
func runConnectionResourcesRediscover(cmd *cobra.Command, connectionID string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	resources, err := client.RediscoverConnectionResources(connectionID)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Refreshed %d connection resources.\n", len(resources))
	return nil
}

// init keeps connection-resource commands under workspace because their
// ownership follows the Engine's bucket-backed workspace boundary.
func init() {
	workspaceCmd.AddCommand(workspaceConnectionCmd)
	workspaceConnectionCmd.AddCommand(workspaceConnectionResourcesCmd)
	workspaceConnectionResourcesCmd.AddCommand(workspaceConnectionResourcesListCmd)
	workspaceConnectionResourcesCmd.AddCommand(workspaceConnectionResourcesDefaultCmd)
	workspaceConnectionResourcesCmd.AddCommand(workspaceConnectionResourcesRediscoverCmd)
}
