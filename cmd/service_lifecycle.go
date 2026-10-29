package cmd

import (
	"fmt"
	"strings"
	"time"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

type serviceLifecycleReceipt struct {
	OK        bool       `json:"ok"`
	Scope     string     `json:"scope"`
	ServiceID string     `json:"service_id"`
	Action    string     `json:"action"`
	DeleteAt  *time.Time `json:"delete_at"`
}

// newServiceRegistryCommand keeps global deletion separate from workspace removal and advisory deprecation.
func newServiceRegistryCommand() *cobra.Command {
	command := commandGroup("registry", "Manage Registry service deletion across all consuming workspaces (owner only)")
	command.Long = "Read a service's Registry lifecycle, delete it immediately, or deprecate it with an automatic deletion date. Mutations require Registry ownership and Engine service.manage. All versions and consuming workspaces are affected; only small audit identities remain after deletion."
	command.AddCommand(
		newServiceLifecycleCommand("lifecycle", "Read lifecycle status, including a deleted service by its UUID"),
		newServiceLifecycleCommand("delete", "Delete all versions from Registry now; irreversible"),
		newServiceLifecycleCommand("deprecate", "Deprecate all versions and schedule automatic Registry deletion"),
		newServiceLifecycleCommand("cancel-deletion", "Cancel a pending deletion before its deadline"),
	)
	return command
}

// newServiceLifecycleCommand gives each invocation fresh flags while sharing identity resolution and receipts.
func newServiceLifecycleCommand(action, short string) *cobra.Command {
	command := &cobra.Command{Use: action + " <service>", Short: short, Args: cobra.ExactArgs(1)}
	command.Long = short + ". Accepts an exact service UUID, an owned slug, or @provider/slug. This operates on the shared Registry, independently of local workspace membership."
	// Destructive intent must be explicit in automation as well as interactive shells.
	if action == "delete" || action == "deprecate" {
		command.Flags().Bool("yes", false, "Confirm deletion of all versions for every consuming workspace")
	}
	// An unambiguous timestamp distinguishes deletion from the older advisory --at date.
	if action == "deprecate" {
		command.Flags().String("delete-at", "", "Future RFC3339 deletion time with timezone (required)")
	}
	addJSONOutputFlag(command)
	// Capture only immutable action identity; flag values are read for the current invocation.
	command.RunE = WithTelemetry("cli.service.registry."+action, func(cmd *cobra.Command, args []string) error {
		return runServiceLifecycle(cmd, args[0], action)
	})
	return command
}

// serviceDeletionTime rejects ambiguous or elapsed timestamps before resolving credentials or making requests.
func serviceDeletionTime(cmd *cobra.Command, action string) (*time.Time, error) {
	// Reading or cancelling a schedule must never acquire an implicit deletion deadline.
	if action != "deprecate" {
		return nil, nil
	}
	raw, _ := cmd.Flags().GetString("delete-at")
	at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	// Dates without a timezone could delete at a different instant from the user's intention.
	if err != nil || !at.After(time.Now()) {
		return nil, fmt.Errorf("--delete-at must be a future RFC3339 timestamp with timezone, for example 2027-01-01T00:00:00Z")
	}
	at = at.UTC().Truncate(time.Microsecond)
	return &at, nil
}

// runServiceLifecycle resolves exactly one target without depending on its workspace activation state.
func runServiceLifecycle(cmd *cobra.Command, reference, action string) error {
	at, err := serviceDeletionTime(cmd, action)
	// Invalid dates must never turn into immediate deletion or cancellation.
	if err != nil {
		return err
	}
	// Both immediate and scheduled deletion need explicit acknowledgment of their global scope.
	if action == "delete" || action == "deprecate" {
		yes, _ := cmd.Flags().GetBool("yes")
		// Noninteractive execution does not imply permission to destroy shared definitions.
		if !yes {
			return fmt.Errorf("this deletes all service versions for every consuming workspace; pass --yes to confirm")
		}
	}
	client, err := getAPIClient()
	// Authentication failure must not cause a switch of account or Registry.
	if err != nil {
		return err
	}
	id, err := resolveServiceIDFromSlug(client, reference)
	// A failed exact lookup is not an invitation to guess a similarly named service.
	if err != nil {
		return err
	}
	// Tombstone reads use exact UUIDs because normal catalogue lookup excludes deleted content.
	if action == "lifecycle" {
		return printServiceLifecycle(cmd, client, id)
	}
	return mutateServiceLifecycle(cmd, client, id, action, at)
}

// printServiceLifecycle reports authoritative fields without treating an elapsed schedule as confirmed deletion.
func printServiceLifecycle(cmd *cobra.Command, client *cliapi.Client, id string) error {
	state, err := client.GetServiceLifecycle(id)
	// Missing or inaccessible state is an error, not evidence that the service is active.
	if err != nil {
		return err
	}
	// Machine output retains null timestamps so agents can distinguish scheduled and confirmed deletion.
	if wantsJSON(cmd) {
		return writeJSON(cmd, state)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s (%s)\n", state.Name, state.ServiceID)
	// Confirmed deletion takes precedence over its former scheduled date.
	switch {
	case state.DeletedAt != nil:
		fmt.Fprintf(cmd.OutOrStdout(), "Deleted from Registry: %s\n", state.DeletedAt.UTC().Format(time.RFC3339Nano))
	case state.DeleteAt != nil:
		fmt.Fprintf(cmd.OutOrStdout(), "Deprecated; Registry deletion scheduled: %s\n", state.DeleteAt.UTC().Format(time.RFC3339Nano))
	default:
		fmt.Fprintln(cmd.OutOrStdout(), "No Registry deletion scheduled.")
	}
	return nil
}

// mutateServiceLifecycle emits a receipt only after Registry acknowledges the precise requested mutation.
func mutateServiceLifecycle(cmd *cobra.Command, client *cliapi.Client, id, action string, at *time.Time) error {
	var err error
	// Each operation maps to a distinct HTTP contract; cancellation cannot accidentally delete content.
	switch action {
	case "delete":
		err = client.DeleteRegistryService(id)
	case "deprecate":
		err = client.ScheduleServiceDeletion(id, *at)
	case "cancel-deletion":
		err = client.CancelServiceDeletion(id)
	default:
		return fmt.Errorf("unsupported Registry lifecycle action %q", action)
	}
	// Ownership checks remain authoritative server-side, including when an exact UUID bypassed name lookup.
	if err != nil {
		return err
	}
	receipt := serviceLifecycleReceipt{OK: true, Scope: "registry", ServiceID: id, Action: action, DeleteAt: at}
	// JSON reports committed intent without a second read that could fail after a successful write.
	if wantsJSON(cmd) {
		return writeJSON(cmd, receipt)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Registry service %s: %s succeeded.\n", id, action)
	// Make the irreversible cutoff visible in human output as well as the structured receipt.
	if at != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Deletion scheduled for %s.\n", at.Format(time.RFC3339Nano))
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Consuming Engines reconcile Registry changes; offline Engines apply them when they reconnect.")
	return nil
}

// init registers the explicitly Registry-scoped lifecycle commands without changing workspace command behavior.
func init() { serviceCmd.AddCommand(newServiceRegistryCommand()) }
