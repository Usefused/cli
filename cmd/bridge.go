package cmd

import "github.com/spf13/cobra"

// newBridgeControlCommand keeps management actions explicit and emits credential-free catalogue JSON.
func newBridgeControlCommand(action, use, description string) *cobra.Command {
	count := 1
	// List has no pairing selector; all other actions require an exact pairing ID.
	if action == "list" {
		count = 0
	}
	return &cobra.Command{Use: use, Short: description, Args: cobra.ExactArgs(count), RunE: func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		// Saved Engine access must resolve before any control request.
		if err != nil {
			return err
		}
		id := ""
		// Only commands that declare a pairing argument may read it.
		if len(args) > 0 {
			id = args[0]
		}
		result, err := client.BridgeRequest(action, id)
		// Failed mutations cannot print a success receipt.
		if err != nil {
			return err
		}
		return writeJSON(cmd, result)
	}}
}

// init registers Bridge catalogue and revocation alongside other Engine controls.
func init() {
	command := &cobra.Command{Use: "bridge", Short: "Inspect paired local Bridges and their selected capabilities", Args: cobra.NoArgs, RunE: requireSubcommand}
	command.AddCommand(newBridgeControlCommand("list", "list", "List paired Bridges"), newBridgeControlCommand("manifests", "show <pairing-id>", "Show current operations with exact manifest hashes"), newBridgeControlCommand("revoke", "revoke <pairing-id>", "Revoke a Bridge installation credential"))
	RootCmd.AddCommand(command)
}
