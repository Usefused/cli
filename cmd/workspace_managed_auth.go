package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var workspaceManagedAuthCmd = &cobra.Command{
	Use:   "managed-auth",
	Short: "Manage this workspace's enrollment with Fused's managed-auth broker",
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var workspaceManagedAuthStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show this workspace's managed-auth enrollment status",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.workspace.managed_auth.status", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		status, err := client.ManagedAuthStatus()
		if err != nil {
			return err
		}
		if wantsJSON(cmd) {
			return writeJSON(cmd, status)
		}
		fmt.Fprintln(cmd.OutOrStdout(), status.Status)
		return nil
	}),
}

var workspaceManagedAuthEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enroll this workspace with Fused's managed-auth broker",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.workspace.managed_auth.enable", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		status, err := client.EnableManagedAuth()
		if err != nil {
			return err
		}
		if wantsJSON(cmd) {
			return writeJSON(cmd, status)
		}
		fmt.Fprintln(cmd.OutOrStdout(), status.Status)
		return nil
	}),
}

// workspaceManagedAuthDisableCmd exposes durable opt-out through the ordinary workspace controls.
var workspaceManagedAuthDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disable Fused Managed Auth and revoke this Engine's broker credential",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.workspace.managed_auth.disable", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		// A missing Engine client cannot acknowledge a saved preference.
		if err != nil {
			return err
		}
		status, err := client.DisableManagedAuth()
		// Local persistence failure must remain visible to automation.
		if err != nil {
			return err
		}
		// JSON retains pending-revocation detail for scripts.
		if wantsJSON(cmd) {
			return writeJSON(cmd, status)
		}
		fmt.Fprintln(cmd.OutOrStdout(), status.Status)
		// Broker outages do not undo opt-out; explain remaining cleanup without claiming it finished.
		if status.RevocationPending {
			fmt.Fprintln(cmd.OutOrStdout(), "Broker revocation pending; this Engine will retry automatically.")
		}
		return nil
	}),
}

// init attaches enrollment lifecycle actions to the existing workspace command tree.
func init() {
	workspaceManagedAuthCmd.AddCommand(workspaceManagedAuthStatusCmd, workspaceManagedAuthEnableCmd, workspaceManagedAuthDisableCmd)
	workspaceCmd.AddCommand(workspaceManagedAuthCmd)
}
