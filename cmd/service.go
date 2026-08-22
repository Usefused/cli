package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Inspect workspace and Registry services",
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var serviceListFlags listFlags
var serviceSearchQuery string
var serviceOperationsQuery string
var serviceOperationsVersion string
var serviceOperationVersion string
var serviceOperationIncludeRequest bool
var serviceOperationIncludeResponses bool
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
	if wantsJSON(cmd) {
		return writeJSON(cmd, ops)
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
	if wantsJSON(cmd) {
		return writeJSON(cmd, webhooks)
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
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	ServiceID       string `json:"service_id"`
	IsOwner         *bool  `json:"is_owner,omitempty"`
	IsPublic        *bool  `json:"is_public,omitempty"`
	WorkspaceStatus string `json:"workspace_status"`
}

var serviceSearchCmd = &cobra.Command{
	Use:   "search",
	Short: "Search workspace and Registry services when the slug is unknown",
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
		results, err := searchServiceResults(client, query)
		if err != nil {
			return err
		}
		results, err = addWorkspaceStatusToServiceSearch(client, query, results)
		if err != nil {
			return err
		}
		if wantsJSON(cmd) {
			return writeJSON(cmd, results)
		}
		if len(results) == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "No workspace or Registry services found for query %q.\n", query)
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSLUG\tSERVICE_ID\tWORKSPACE")
		for _, result := range results {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", result.Name, result.Slug, result.ServiceID, result.WorkspaceStatus)
		}
		return w.Flush()
	}),
}

// searchServiceResults is shared by read-only Registry discovery and the
// workspace add fallback. Keeping the account-qualified slug projection here
// ensures both commands hand users the exact same reusable service identity.
func searchServiceResults(client *cliapi.Client, query string) ([]serviceSearchResult, error) {
	services, err := client.SearchServices(query)
	if err != nil {
		return nil, err
	}
	results := make([]serviceSearchResult, 0, len(services))
	for _, service := range services {
		isOwner, isPublic := service.IsOwner, service.IsPublic
		results = append(results, serviceSearchResult{
			Name: service.Name, Slug: service.DisplaySlug(), ServiceID: service.ID,
			IsOwner: &isOwner, IsPublic: &isPublic,
		})
	}
	return results, nil
}

var serviceShowCmd = &cobra.Command{
	Use:   "show <service-slug>",
	Short: "Show base URL and servers for a service",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.service.show", func(cmd *cobra.Command, args []string) error {
		return runServiceShow(cmd, args[0])
	}),
}

var serviceOperationCmd = &cobra.Command{
	Use:   "operation",
	Short: "Inspect one Registry operation",
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var serviceOperationShowCmd = &cobra.Command{
	Use:   "show <service-slug> <operation-name>",
	Short: "Show one operation from an exact service version",
	Args:  cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.service.operation.show", func(cmd *cobra.Command, args []string) error {
		return runServiceOperationShow(cmd, args[0], args[1])
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
	if wantsJSON(cmd) {
		return writeJSON(cmd, newServiceShowResult(info))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "name:\t%s\n", info.Name)
	fmt.Fprintf(cmd.OutOrStdout(), "description:\t%s\n", info.Description)
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

type serviceShowResult struct {
	ID          string                 `json:"service_id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Slug        string                 `json:"slug"`
	BaseURL     string                 `json:"base_url"`
	Servers     []cliapi.ServiceServer `json:"servers"`
	AuthConfigs []cliapi.AuthConfig    `json:"auth_configs"`
}

func newServiceShowResult(info *cliapi.ServiceInfo) serviceShowResult {
	return serviceShowResult{
		ID: info.ID, Name: info.Name, Description: info.Description,
		Slug: info.DisplaySlug(), BaseURL: info.BaseURL,
		Servers: info.Servers, AuthConfigs: info.AuthConfigs,
	}
}

func runServiceOperationShow(cmd *cobra.Command, serviceSlug, operationName string) error {
	version := strings.TrimSpace(serviceOperationVersion)
	if version == "" {
		return errors.New("--version is required")
	}
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
	detail, err := client.GetServiceOperation(service.ID, version, operationName, cliapi.ServiceOperationDetailOptions{
		IncludeRequest: serviceOperationIncludeRequest, IncludeResponses: serviceOperationIncludeResponses,
	})
	if err != nil {
		return err
	}
	return renderServiceOperation(cmd, detail)
}

func renderServiceOperation(cmd *cobra.Command, detail *cliapi.ServiceOperationDetail) error {
	if wantsJSON(cmd) {
		return writeJSON(cmd, detail)
	}
	out := cmd.OutOrStdout()
	printServiceOperationSummary(out, detail)
	if serviceOperationIncludeRequest {
		if err := printJSONSection(out, "request_content", detail.RequestContent); err != nil {
			return err
		}
	}
	if serviceOperationIncludeResponses {
		return printJSONSection(out, "responses", detail.Responses)
	}
	return nil
}

func printServiceOperationSummary(out io.Writer, detail *cliapi.ServiceOperationDetail) {
	fmt.Fprintf(out, "name:\t%s\n", detail.Name)
	fmt.Fprintf(out, "id:\t%s\n", detail.ID)
	fmt.Fprintf(out, "description:\t%s\n", detail.Description)
	fmt.Fprintf(out, "method:\t%s\n", detail.Method)
	fmt.Fprintf(out, "path:\t%s\n", detail.Path)
	fmt.Fprintf(out, "security:\t%s\n", formatSecurityRequirements(detail.SecurityRequirements))
	for _, parameter := range detail.Parameters {
		fmt.Fprintf(out, "parameter:\t%s\tin: %s\trequired: %t\ttype: %s\n", parameter.Name, parameter.In, parameter.Required, parameter.Type)
	}
}

func printJSONSection(out io.Writer, label string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s:\n%s\n", label, encoded)
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
	// Version browsing needs identity and compatibility only. The full policy
	// projection is reserved for workspace sync, which actually round-trips it.
	versions, err := client.ServiceVersionSummaries(service)
	if err != nil {
		return err
	}
	if wantsJSON(cmd) {
		return writeJSON(cmd, versions)
	}
	printServiceVersions(cmd.OutOrStdout(), service, versions)
	return nil
}

// printServiceVersions renders the lean server projection without rebuilding a
// second list from full version objects in CLI memory.
func printServiceVersions(out io.Writer, service string, versions []cliapi.ServiceVersionSummary) {
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
	serviceCmd.AddCommand(serviceSearchCmd, serviceVersionsCmd, serviceShowCmd, serviceOperationsCmd, serviceOperationCmd, serviceWebhooksCmd)
	serviceOperationCmd.AddCommand(serviceOperationShowCmd)
	serviceSearchCmd.Flags().StringVar(&serviceSearchQuery, "q", "", "Service name or capability to search for")
	addJSONOutputFlag(serviceSearchCmd, serviceVersionsCmd, serviceShowCmd, serviceOperationsCmd, serviceOperationShowCmd, serviceWebhooksCmd)
	serviceOperationsCmd.Flags().StringVar(&serviceOperationsQuery, "q", "", "Search query")
	serviceOperationsCmd.Flags().StringVar(&serviceOperationsVersion, "version", "", "Service version")
	addListFlags(serviceOperationsCmd, &serviceListFlags)
	serviceOperationShowCmd.Flags().StringVar(&serviceOperationVersion, "version", "", "Exact service version (required)")
	serviceOperationShowCmd.Flags().BoolVar(&serviceOperationIncludeRequest, "include-request", false, "Include the request content contract")
	serviceOperationShowCmd.Flags().BoolVar(&serviceOperationIncludeResponses, "include-responses", false, "Include response contracts")
	serviceOperationShowCmd.MarkFlagRequired("version")
	serviceWebhooksCmd.Flags().StringVar(&serviceWebhooksVersion, "version", "", "Service version")
}
