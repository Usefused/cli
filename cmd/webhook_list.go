package cmd

import (
	"fmt"
	"text/tabwriter"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

var webhookListFlags listFlags
var webhookListQuery, webhookListServiceID string
var workspaceWebhookListFlags listFlags
var workspaceWebhookListQuery string

var webhookListCmd = &cobra.Command{
	Use: "list", Short: "List or search registered workspace webhooks", Args: cobra.NoArgs,
	// Listing is read-only and retains the command's existing audit and cancellation context.
	RunE: WithTelemetry("cli.webhook.list", func(cmd *cobra.Command, _ []string) error {
		return runWebhookList(cmd)
	}),
}

// init exposes the same filters and bounded pages for global and service-scoped registration reads.
func init() {
	webhookCmd.AddCommand(webhookListCmd)
	webhookListCmd.Flags().StringVar(&webhookListQuery, "q", "", "Search registrations by label, URL, or service")
	webhookListCmd.Flags().StringVar(&webhookListServiceID, "service-id", "", "Filter registrations by exact workspace service UUID")
	addListFlags(webhookListCmd, &webhookListFlags)
	addJSONOutputFlag(webhookListCmd)
	workspaceServiceWebhooksCmd.Flags().StringVar(&workspaceWebhookListQuery, "q", "", "Search this service's webhook registrations")
	addListFlags(workspaceServiceWebhooksCmd, &workspaceWebhookListFlags)
}

// runWebhookList reads one server-filtered page without fetching services or reconstructing public URLs.
func runWebhookList(cmd *cobra.Command) error {
	client, err := getAPIClient()
	// Missing local connection configuration must fail before any Engine request.
	if err != nil {
		return err
	}
	page, err := client.ListWorkspaceWebhookPage(webhookListServiceID, webhookListQuery, webhookListFlags.pageOptions())
	// Authorization and transport failures must not appear as empty search results.
	if err != nil {
		return err
	}
	// Structured output preserves the total for agents choosing subsequent offsets.
	if wantsJSON(cmd) {
		return writeJSON(cmd, page)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE\tLABEL\tURL\tDELIVERY\tSIGNATURE\tCREATED_AT")
	for _, webhook := range page.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", webhook.ServiceRef, webhook.Label, webhook.CallbackURL, webhook.DeliveryMode, webhook.Signature, webhook.CreatedAt)
	}
	// A failed output stream cannot be reported as a successfully printed page.
	if err := w.Flush(); err != nil {
		return err
	}
	printWebhookPageSummary(cmd, page, webhookListFlags.Offset)
	return nil
}

// printWebhookPageSummary uses returned row counts so final, empty, and out-of-range pages remain truthful.
func printWebhookPageSummary(cmd *cobra.Command, page *cliapi.WorkspaceWebhookPage, offset int) {
	// Empty pages still report the matching total, which distinguishes no matches from an offset past the end.
	if len(page.Items) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No webhook registrations on this page (%d matching total).\n", page.Total)
		return
	}
	end := offset + len(page.Items)
	fmt.Fprintf(cmd.OutOrStdout(), "Showing %d-%d of %d.\n", offset+1, end, page.Total)
	// Only advertise another page when Engine's filtered total contains additional registrations.
	if end < page.Total {
		fmt.Fprintf(cmd.OutOrStdout(), "Use --offset %d for the next page.\n", end)
	}
}
