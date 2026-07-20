package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var valueCmd = &cobra.Command{
	Use:   "value",
	Short: "Manage workspace bucket values",
}

var valueSetCmd = &cobra.Command{
	Use:   "set <bucket-id> <service-slug> <key-name> <location> <value>",
	Short: "Set a bucket value",
	Args:  cobra.ExactArgs(5),
	RunE: WithTelemetry("cli.value.set", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		bucketID := args[0]
		serviceSlug := args[1]
		keyName := args[2]
		location := args[3]
		value := args[4]

		resolvedBucketID, err := resolveBucketIDPrompt(client, bucketID)
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
		fmt.Printf("Bucket value '%s' set successfully.\n", keyName)
		return nil
	}),
}

var valueListCmd = &cobra.Command{
	Use:   "list <bucket-id>",
	Short: "List bucket values",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.value.list", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		bucketID := args[0]
		
		resolvedBucketID, err := resolveBucketID(client, bucketID)
		if err != nil {
			return err
		}

		values, err := client.ListBucketValues(resolvedBucketID)
		if err != nil {
			return err
		}
		for _, v := range values {
			fmt.Printf("Service: %s, Key: %s, Location: %s\n", v.ServiceID, v.KeyName, v.Location)
		}
		return nil
	}),
}

var valueRemoveCmd = &cobra.Command{
	Use:   "remove <bucket-id> <service-slug> <key-name>",
	Short: "Remove a bucket value",
	Args:  cobra.ExactArgs(3),
	RunE: WithTelemetry("cli.value.remove", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		bucketID := args[0]
		serviceSlug := args[1]
		keyName := args[2]

		resolvedBucketID, err := resolveBucketID(client, bucketID)
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
		fmt.Printf("Bucket value '%s' removed successfully.\n", keyName)
		return nil
	}),
}

func init() {
	valueCmd.AddCommand(valueSetCmd)
	valueCmd.AddCommand(valueListCmd)
	valueCmd.AddCommand(valueRemoveCmd)
	RootCmd.AddCommand(valueCmd)
}
