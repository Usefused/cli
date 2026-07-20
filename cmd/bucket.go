package cmd

import (
	"fmt"
	"time"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

var bucketCmd = &cobra.Command{
	Use:   "bucket",
	Short: "Manage workspace buckets",
}

var bucketCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new bucket",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.bucket.create", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		name := args[0]
		err = client.CreateBucket(name)
		if err != nil {
			return err
		}
		fmt.Printf("Bucket '%s' created successfully.\n", name)
		return nil
	}),
}

var bucketListCmd = &cobra.Command{
	Use:   "list",
	Short: "List workspace buckets",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.bucket.list", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		buckets, err := client.ListBuckets()
		if err != nil {
			return err
		}
		for _, b := range buckets {
			defStr := ""
			if b.IsDefault {
				defStr = " (default)"
			}
			fmt.Printf("Name: %s%s, Created: %s\n", b.Name, defStr, b.CreatedAt.Format(time.RFC3339))
		}
		return nil
	}),
}

var bucketRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a workspace bucket",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.bucket.remove", func(cmd *cobra.Command, args []string) error {
		client, err := getAPIClient()
		if err != nil {
			return err
		}
		name := args[0]
		err = client.DeleteBucket(name)
		if err != nil {
			return err
		}
		fmt.Printf("Bucket '%s' removed successfully.\n", name)
		return nil
	}),
}

func init() {
	bucketCmd.AddCommand(bucketCreateCmd)
	bucketCmd.AddCommand(bucketListCmd)
	bucketCmd.AddCommand(bucketRemoveCmd)
	RootCmd.AddCommand(bucketCmd)
}

func resolveBucketID(client *cliapi.Client, nameOrID string) (string, error) {
	if nameOrID == "" {
		return "", nil
	}
	buckets, err := client.ListBuckets()
	if err != nil {
		return "", err
	}
	for _, b := range buckets {
		if b.ID == nameOrID || b.Name == nameOrID {
			return b.ID, nil
		}
	}
	return "", fmt.Errorf("bucket %s not found", nameOrID)
}

func resolveBucketIDPrompt(client *cliapi.Client, nameOrID string) (string, error) {
	if nameOrID == "" {
		return "", nil
	}
	buckets, err := client.ListBuckets()
	if err != nil {
		return "", err
	}
	for _, b := range buckets {
		if b.ID == nameOrID || b.Name == nameOrID {
			return b.ID, nil
		}
	}
	fmt.Printf("Bucket '%s' doesn't exist. Create it? [y/N] ", nameOrID)
	var ans string
	fmt.Scanln(&ans)
	if ans == "y" || ans == "Y" {
		if err := client.CreateBucket(nameOrID); err != nil {
			return "", fmt.Errorf("failed to create bucket: %w", err)
		}
		fmt.Printf("Bucket '%s' created.\n", nameOrID)
		buckets, _ := client.ListBuckets()
		for _, b := range buckets {
			if b.Name == nameOrID {
				return b.ID, nil
			}
		}
	} else {
		return "", fmt.Errorf("aborted")
	}
	return "", fmt.Errorf("bucket %s not found after creation", nameOrID)
}
