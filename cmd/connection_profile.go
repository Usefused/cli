package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

var (
	providerProfileVersion  string
	providerProfileAuthType string
	providerProfileFile     string
)

var serviceConnectionProfileCmd = &cobra.Command{
	Use:   "connection-profile",
	Short: "Manage provider connection profiles",
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var serviceConnectionProfileSetCmd = &cobra.Command{
	Use:   "set <service-slug-or-id>",
	Short: "Publish an immutable provider connection-profile revision",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.service.connection_profile.set", func(cmd *cobra.Command, args []string) error {
		return runSetProviderConnectionProfile(cmd, args[0])
	}),
}

func runSetProviderConnectionProfile(cmd *cobra.Command, serviceRef string) error {
	profile, err := loadProviderConnectionProfile()
	if err != nil {
		return err
	}
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	serviceID, versionID, serviceName, err := resolveProviderProfileTarget(client, serviceRef, providerProfileVersion)
	if err != nil {
		return err
	}
	revision, err := client.SetConnectionProfile(serviceID, versionID, serviceName, profile)
	if err != nil {
		return err
	}
	if revision == nil {
		return fmt.Errorf("Registry returned no connection-profile revision")
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "connection_profile")
	fmt.Fprintf(cmd.OutOrStdout(), "profile_id:\t%s\nrevision:\t%d\nprofile_hash:\t%s\nprovenance:\t%s\n", revision.ProfileID, revision.Revision, revision.ProfileHash, revision.Provenance)
	return nil
}

func loadProviderConnectionProfile() (map[string]interface{}, error) {
	if strings.TrimSpace(providerProfileVersion) == "" || strings.TrimSpace(providerProfileAuthType) == "" || strings.TrimSpace(providerProfileFile) == "" {
		return nil, fmt.Errorf("--version, --auth-type, and --file are required")
	}
	raw, err := os.ReadFile(providerProfileFile)
	if err != nil {
		return nil, fmt.Errorf("read connection profile: %w", err)
	}
	var profile map[string]interface{}
	if err := json.Unmarshal(raw, &profile); err != nil {
		return nil, fmt.Errorf("connection profile must be a JSON object: %w", err)
	}
	if fmt.Sprint(profile["auth_type"]) != providerProfileAuthType {
		return nil, fmt.Errorf("connection profile auth_type must match --auth-type")
	}
	return profile, nil
}

func resolveProviderProfileTarget(client interface {
	GetServiceInfo(string) (*api.ServiceInfo, error)
	ServiceVersions(string) ([]api.ServiceVersion, error)
}, serviceRef, requestedVersion string) (string, string, string, error) {
	service, err := client.GetServiceInfo(serviceRef)
	if err != nil {
		return "", "", "", err
	}
	if service == nil {
		return "", "", "", fmt.Errorf("service %s not found", serviceRef)
	}
	versions, err := client.ServiceVersions(serviceRef)
	if err != nil {
		return "", "", "", err
	}
	for _, version := range versions {
		if version.Name == requestedVersion {
			return service.ID, version.ID, service.Name, nil
		}
	}
	return "", "", "", fmt.Errorf("service %s has no version %s", serviceRef, requestedVersion)
}

func init() {
	serviceConnectionProfileSetCmd.Flags().StringVar(&providerProfileVersion, "version", "", "Service version")
	serviceConnectionProfileSetCmd.Flags().StringVar(&providerProfileAuthType, "auth-type", "", "Authentication protocol (oauth or oidc)")
	serviceConnectionProfileSetCmd.Flags().StringVar(&providerProfileFile, "file", "", "JSON connection-profile file")
	serviceConnectionProfileCmd.AddCommand(serviceConnectionProfileSetCmd)
	serviceCmd.AddCommand(serviceConnectionProfileCmd)
}
