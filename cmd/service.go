package cmd

import (
	"fmt"
	"io"
	"strings"

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

func validateServiceArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) > 2 {
		return fmt.Errorf("service accepts at most <service-slug> and one action")
	}
	if len(args) == 2 && args[1] != "versions" && args[1] != "show" {
		return fmt.Errorf("unknown service action %q", args[1])
	}
	return nil
}

func runServiceAction(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	if serviceShowVersions || (len(args) == 2 && args[1] == "versions") {
		return runServiceVersions(cmd, args[0])
	}
	if len(args) == 2 && args[1] == "show" {
		return runServiceShow(cmd, args[0])
	}
	return cmd.Help()
}

func completeServiceArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 1 {
		actions := []string{"versions", "show"}
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
	for _, version := range versions {
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", version.Name, version.ID, version.Status, version.CreatedAt)
	}
}

func init() {
	RootCmd.AddCommand(serviceCmd)
	serviceCmd.Flags().BoolVar(&serviceShowVersions, "versions", false, "List available versions for the service slug; supports @provider/slug")
	serviceCmd.AddCommand(serviceVersionsCmd)
	serviceCmd.AddCommand(serviceShowCmd)
}
