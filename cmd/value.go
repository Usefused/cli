package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var valueCmd = &cobra.Command{
	Use:   "value",
	Short: "Manage workspace bucket values",
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var valueListFlags listFlags

var valueSetCmd = &cobra.Command{
	Use:   "set <bucket-name-or-id> <service-slug> <location> <key-name> <value>",
	Short: "Set a non-secret bucket value",
	Args:  cobra.ExactArgs(5),
	RunE: WithTelemetry("cli.value.set", func(cmd *cobra.Command, args []string) error {
		return runValueSet(cmd, args[0], args[1], args[3], args[2], args[4])
	}),
}

var valueListCmd = &cobra.Command{
	Use:   "list <bucket-name-or-id>",
	Short: "List non-secret values in a bucket",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.value.list", func(cmd *cobra.Command, args []string) error {
		return runValueList(cmd, args[0])
	}),
}

var valueDeleteCmd = &cobra.Command{
	Use:   "delete <bucket-name-or-id> <service-slug> <key-name>",
	Short: "Delete a non-secret bucket value",
	Args:  cobra.ExactArgs(3),
	RunE: WithTelemetry("cli.value.delete", func(cmd *cobra.Command, args []string) error {
		return runValueDelete(cmd, args[0], args[1], args[2])
	}),
}

func runValueSet(cmd *cobra.Command, bucketID, serviceSlug, keyName, location, value string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	resolvedBucketID, err := resolveExplicitBucketID(bucketID)
	if err != nil {
		return err
	}

	serviceID, err := resolveServiceIDFromSlug(client, serviceSlug)
	if err != nil {
		return err
	}

	err = client.UpsertBucketValue(resolvedBucketID, serviceID, keyName, location, value)
	if err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "bucket_value")
	fmt.Fprintf(cmd.OutOrStdout(), "Bucket value %q set.\n", keyName)
	return nil
}

func runValueList(cmd *cobra.Command, bucketID string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	resolvedBucketID, err := resolveExplicitBucketID(bucketID)
	if err != nil {
		return err
	}

	page, err := client.ListBucketValuesPage(resolvedBucketID, valueListFlags.pageOptions())
	if err != nil {
		return err
	}
	for _, value := range page.Items {
		fmt.Fprintf(cmd.OutOrStdout(), "Service: %s, Key: %s, Location: %s\n", value.ServiceID, value.KeyName, value.Location)
	}
	printPageSummary(cmd.OutOrStdout(), page.Total, valueListFlags)
	return nil
}

func runValueDelete(cmd *cobra.Command, bucketID, serviceSlug, keyName string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	resolvedBucketID, err := resolveExplicitBucketID(bucketID)
	if err != nil {
		return err
	}

	serviceID, err := resolveServiceIDFromSlug(client, serviceSlug)
	if err != nil {
		return err
	}

	err = client.DeleteBucketValue(resolvedBucketID, serviceID, keyName)
	if err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "bucket_value")
	fmt.Fprintf(cmd.OutOrStdout(), "Bucket value %q deleted.\n", keyName)
	return nil
}

func init() {
	RootCmd.AddCommand(valueCmd)
	valueCmd.AddCommand(valueSetCmd, valueListCmd, valueDeleteCmd)
	addListFlags(valueListCmd, &valueListFlags)
}
