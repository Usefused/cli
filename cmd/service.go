package cmd

import (
	"fmt"
	"io"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

var serviceCmd = &cobra.Command{
	Use:   "service <service-slug>",
	Short: "Inspect Registry services",
	Args:  cobra.MaximumNArgs(1),
	RunE: WithTelemetry("cli.service", func(cmd *cobra.Command, args []string) error {
		if !serviceShowVersions {
			return cmd.Help()
		}
		if len(args) != 1 {
			return fmt.Errorf("service slug is required when using --versions")
		}
		return runServiceVersions(cmd, args[0])
	}),
}

var serviceShowVersions bool

var serviceVersionsCmd = &cobra.Command{
	Use:   "versions <service-slug>",
	Short: "List available versions for a service slug",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.service.versions", func(cmd *cobra.Command, args []string) error {
		return runServiceVersions(cmd, args[0])
	}),
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
}
