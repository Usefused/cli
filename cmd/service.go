package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Inspect Registry services",
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var serviceListFlags listFlags
var serviceSearchQuery string
var serviceSearchJSON bool
var serviceOperationsQuery string
var serviceOperationsVersion string
var serviceWebhooksVersion string

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
	fmt.Fprintln(w, "NAME\tMETHOD\tPATH\tSECURITY\tID")
	for _, op := range integrations {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", op.Name, op.Method, op.Path, formatSecurityRequirements(op.SecurityRequirements), op.ID)
	}
	w.Flush()
}

func formatSecurityRequirements(requirements cliapi.SecurityRequirements) string {
	if len(requirements) == 0 {
		return "-"
	}
	alternatives := make([]string, 0, len(requirements))
	for _, alternative := range requirements {
		if len(alternative.Schemes) == 0 {
			alternatives = append(alternatives, "anonymous")
			continue
		}
		schemes := make([]string, 0, len(alternative.Schemes))
		for _, requirement := range alternative.Schemes {
			schemes = append(schemes, formatSecurityRequirement(requirement))
		}
		alternatives = append(alternatives, strings.Join(schemes, " + "))
	}
	return strings.Join(alternatives, " OR ")
}

func formatSecurityRequirement(requirement cliapi.SecurityRequirement) string {
	if len(requirement.Scopes) == 0 {
		return requirement.Scheme
	}
	return requirement.Scheme + "[" + strings.Join(requirement.Scopes, ",") + "]"
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
	webhooks, err := client.FetchWebhooks(service.ID, serviceWebhooksVersion)
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

type serviceSearchResult struct {
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	ServiceID string `json:"service_id"`
	IsOwner   bool   `json:"is_owner"`
	IsPublic  bool   `json:"is_public"`
}

var serviceSearchCmd = &cobra.Command{
	Use:   "search",
	Short: "Search Registry services when the slug is unknown",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.service.search", func(cmd *cobra.Command, _ []string) error {
		query := strings.TrimSpace(serviceSearchQuery)
		if query == "" {
			return fmt.Errorf("--q must not be empty")
		}
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		services, err := client.SearchServices(query)
		if err != nil {
			return err
		}
		results := make([]serviceSearchResult, 0, len(services))
		for _, service := range services {
			results = append(results, serviceSearchResult{
				Name:      service.Name,
				Slug:      service.DisplaySlug(),
				ServiceID: service.ID,
				IsOwner:   service.IsOwner,
				IsPublic:  service.IsPublic,
			})
		}
		if serviceSearchJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(results)
		}
		if len(results) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "No Registry services found for query %q.\n", query)
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSLUG\tSERVICE_ID")
		for _, result := range results {
			fmt.Fprintf(w, "%s\t%s\t%s\n", result.Name, result.Slug, result.ServiceID)
		}
		return w.Flush()
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

var serviceOperationsCmd = &cobra.Command{
	Use:   "operations <service-slug>",
	Short: "List or search service operations",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.service.operations", func(cmd *cobra.Command, args []string) error {
		return runServiceOperations(cmd, args[0])
	}),
}

var serviceWebhooksCmd = &cobra.Command{
	Use:   "webhooks <service-slug>",
	Short: "List service webhooks",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.service.webhooks", func(cmd *cobra.Command, args []string) error {
		return runServiceWebhooks(cmd, args[0])
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
		for _, variable := range srv.Variables {
			fmt.Fprintf(cmd.OutOrStdout(), "  variable:\t%s\trequired: %t%s\n", variable.Name, variable.Required, formatServerVariableOptions(variable))
		}
	}
	if len(info.AuthConfigs) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "\nauth_methods:\n")
		for _, auth := range info.AuthConfigs {
			fmt.Fprintf(cmd.OutOrStdout(), "  - %s (type: %s, scheme: %s%s)\n", auth.Name, auth.Type, auth.Scheme, formatBasicPasswordMode(auth.BasicPasswordMode))
		}
	}
	return nil
}

func formatServerVariableOptions(variable cliapi.ServerVariable) string {
	parts := make([]string, 0, 2)
	if variable.Default != nil {
		parts = append(parts, "default: "+*variable.Default)
	}
	if len(variable.Enum) > 0 {
		parts = append(parts, "enum: "+strings.Join(variable.Enum, ","))
	}
	if len(parts) == 0 {
		return ""
	}
	return "\t" + strings.Join(parts, "\t")
}

func formatBasicPasswordMode(mode cliapi.BasicPasswordMode) string {
	if mode == "" {
		return ""
	}
	return ", basic_password_mode: " + string(mode)
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
	serviceCmd.AddCommand(serviceSearchCmd, serviceVersionsCmd, serviceShowCmd, serviceOperationsCmd, serviceWebhooksCmd)
	serviceSearchCmd.Flags().StringVar(&serviceSearchQuery, "q", "", "Service name or capability to search for")
	serviceSearchCmd.Flags().BoolVar(&serviceSearchJSON, "json", false, "Print results as JSON")
	serviceOperationsCmd.Flags().StringVar(&serviceOperationsQuery, "q", "", "Search query")
	serviceOperationsCmd.Flags().StringVar(&serviceOperationsVersion, "version", "", "Service version")
	addListFlags(serviceOperationsCmd, &serviceListFlags)
	serviceWebhooksCmd.Flags().StringVar(&serviceWebhooksVersion, "version", "", "Service version")
}
