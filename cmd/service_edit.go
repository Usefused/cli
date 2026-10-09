package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"strings"
)

// newServiceEditCommand keeps omitted fields unchanged and leaves ownership enforcement to Registry.
func newServiceEditCommand() *cobra.Command {
	var name, description string
	command := &cobra.Command{Use: "edit <service-slug>", Short: "Edit the name or description of a service you own", Args: cobra.ExactArgs(1)}
	// Resolve canonical identity before writing so the slug itself never changes.
	command.RunE = func(cmd *cobra.Command, args []string) error {
		var nextName, nextDescription *string
		// Explicitly supplied empty descriptions clear existing copy; omission preserves it.
		if cmd.Flags().Changed("description") {
			nextDescription = &description
		}
		// Reject empty display names without making a request.
		if cmd.Flags().Changed("name") {
			name = strings.TrimSpace(name)
			// A whitespace label cannot identify a service to users.
			if name == "" {
				return fmt.Errorf("--name must not be empty")
			}
			nextName = &name
		}
		// Avoid sending a mutation with no requested edits.
		if nextName == nil && nextDescription == nil {
			return fmt.Errorf("provide --name or --description")
		}
		client, err := getAPIClient()
		// Configuration errors must not fall back to another account.
		if err != nil {
			return err
		}
		service, err := client.GetServiceInfo(args[0])
		// Lookup failures cannot be treated as permission to create a service.
		if err != nil {
			return err
		}
		// Fail before writing when the service is absent or owned by another account.
		if service == nil || !service.IsOwner {
			return fmt.Errorf("only the service owner can edit its name and description")
		}
		// Registry repeats the ownership check and scopes its database update to the account.
		if err := client.UpdateServiceDetails(service.ID, nextName, nextDescription); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Updated service %s.\n", service.DisplaySlug())
		return nil
	}
	command.Flags().StringVar(&name, "name", "", "New display name (keeps the slug unchanged)")
	command.Flags().StringVar(&description, "description", "", "New description; an empty string clears it")
	return command
}

// init registers the display-metadata command alongside existing service inspection commands.
func init() { serviceCmd.AddCommand(newServiceEditCommand()) }
