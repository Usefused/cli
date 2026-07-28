package cmd

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

var serviceCmd = &cobra.Command{
	Use:   "service <service-slug> [versions]",
	Short: "Inspect Registry services",
	Args:  validateServiceArgs,
	RunE: WithTelemetry("cli.service", func(cmd *cobra.Command, args []string) error {
		return runServiceAction(cmd, args)
	}),
	ValidArgsFunction: completeServiceArgs,
}

var serviceShowVersions bool
var serviceListFlags listFlags
var serviceOperationsQuery string
var serviceOperationsVersion string

func validateServiceArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) > 2 {
		return fmt.Errorf("service accepts at most <service-slug> and one action")
	}
	if len(args) == 2 && !isServiceAction(args[1]) {
		return fmt.Errorf("unknown service action %q", args[1])
	}
	return nil
}

func isServiceAction(action string) bool {
	_, ok := serviceActionHandlers()[action]
	return ok
}

func runServiceAction(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	if serviceShowVersions {
		return runServiceVersions(cmd, args[0])
	}
	if len(args) != 2 {
		return cmd.Help()
	}
	action, ok := serviceActionHandlers()[args[1]]
	if !ok {
		return fmt.Errorf("unknown service action %q", args[1])
	}
	return action(cmd, args[0])
}

type serviceActionHandler func(*cobra.Command, string) error

func serviceActionHandlers() map[string]serviceActionHandler {
	return map[string]serviceActionHandler{
		"versions":   runServiceVersions,
		"show":       runServiceShow,
		"operations": runServiceOperations,
		"webhooks":   runServiceWebhooks,
	}
}

func completeServiceArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 1 {
		actions := []string{"versions", "show", "operations", "webhooks"}
		var matches []string
		for _, a := range actions {
			if toComplete == "" || strings.HasPrefix(a, toComplete) {
				matches = append(matches, a)
			}
		}
		if len(matches) > 0 {
			return matches, cobra.ShellCompDirectiveNoFileComp
		}
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func runServiceOperations(cmd *cobra.Command, serviceSlug string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	service, err := client.GetServiceInfo(serviceSlug)
	if err != nil {
		return err
	}
	if service == nil {
		return fmt.Errorf("service %s not found", serviceSlug)
	}
	ops, err := readServiceOperations(cmd, client, service.ID)
	if err != nil {
		return err
	}
	printIntegrations(cmd.OutOrStdout(), ops)
	return nil
}

func readServiceOperations(cmd *cobra.Command, client *cliapi.Client, serviceID string) ([]cliapi.Integration, error) {
	if strings.TrimSpace(serviceOperationsQuery) != "" {
		return client.SearchEndpointsPage(serviceID, serviceOperationsVersion, serviceOperationsQuery, serviceListFlags.pageOptions())
	}
	if cmd.Flags().Changed("limit") || cmd.Flags().Changed("offset") {
		return nil, fmt.Errorf("--limit and --offset require --q for service operations because only endpoint search is paginated server-side")
	}
	// Why: The Registry has a full operation-list field but no paginated
	// operation-list field yet; avoid slicing in the CLI where it would hide
	// database work and drift from UI semantics.
	return client.ServiceOperations(serviceID, serviceOperationsVersion)
}

func printIntegrations(out io.Writer, integrations []cliapi.Integration) {
	w := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tMETHOD\tPATH\tID")
	for _, op := range integrations {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", op.Name, op.Method, op.Path, op.ID)
	}
	w.Flush()
}

func runServiceWebhooks(cmd *cobra.Command, serviceSlug string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	service, err := client.GetServiceInfo(serviceSlug)
	if err != nil {
		return err
	}
	if service == nil {
		return fmt.Errorf("service %s not found", serviceSlug)
	}
	webhooks, err := client.FetchWebhooks(service.ID, serviceOperationsVersion)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tID\tDESCRIPTION")
	for _, webhook := range webhooks {
		fmt.Fprintf(w, "%s\t%s\t%s\n", webhook.Name, webhook.ID, webhook.Description)
	}
	w.Flush()
	return nil
}

var serviceVersionsCmd = &cobra.Command{
	Use:   "versions <service-slug>",
	Short: "List available versions for a service slug",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.service.versions", func(cmd *cobra.Command, args []string) error {
		return runServiceVersions(cmd, args[0])
	}),
}

var serviceShowCmd = &cobra.Command{
	Use:   "show <service-slug>",
	Short: "Show base URL and servers for a service",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.service.show", func(cmd *cobra.Command, args []string) error {
		return runServiceShow(cmd, args[0])
	}),
}

func runServiceShow(cmd *cobra.Command, serviceSlug string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	info, err := client.GetServiceInfo(serviceSlug)
	if err != nil {
		return err
	}
	if info == nil {
		return fmt.Errorf("service %s not found", serviceSlug)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "name:\t%s\n", info.Name)
	fmt.Fprintf(cmd.OutOrStdout(), "slug:\t%s\n", info.DisplaySlug())
	fmt.Fprintf(cmd.OutOrStdout(), "base_url:\t%s\n", info.BaseURL)
	for _, srv := range info.Servers {
		if srv.Description != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "server:\t%s\t%s\n", srv.URL, srv.Description)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "server:\t%s\n", srv.URL)
		}
	}
	if len(info.AuthConfigs) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "\nauth_methods:\n")
		for _, auth := range info.AuthConfigs {
			fmt.Fprintf(cmd.OutOrStdout(), "  - %s (type: %s, scheme: %s)\n", auth.Name, auth.Type, auth.Scheme)
		}
	}
	return nil
}

func runServiceVersions(cmd *cobra.Command, service string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	versions, err := client.ServiceVersions(service)
	if err != nil {
		return err
	}
	printServiceVersions(cmd.OutOrStdout(), service, versions)
	return nil
}

func printServiceVersions(out io.Writer, service string, versions []cliapi.ServiceVersion) {
	if len(versions) == 0 {
		fmt.Fprintf(out, "No versions found for service %s.\n", service)
		return
	}
	w := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tID\tSTATUS\tCREATED_AT")
	for _, version := range versions {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", version.Name, version.ID, version.Status, version.CreatedAt)
	}
	w.Flush()
}

func init() {
	RootCmd.AddCommand(serviceCmd)
	serviceCmd.Flags().BoolVar(&serviceShowVersions, "versions", false, "List available versions for the service slug; supports @provider/slug")
	serviceCmd.Flags().StringVar(&serviceOperationsQuery, "q", "", "Search query for service operations")
	serviceCmd.Flags().StringVar(&serviceOperationsVersion, "version", "", "Service version for operations/webhooks")
	addListFlags(serviceCmd, &serviceListFlags)
	serviceCmd.AddCommand(serviceVersionsCmd)
	serviceCmd.AddCommand(serviceShowCmd)
}
