package cmd

import (
	"fmt"
	"strings"
	"time"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

var bucketCmd = &cobra.Command{
	Use:   "bucket <name|list> [create|remove]",
	Short: "Manage workspace buckets",
	Args:  validateBucketArgs,
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.bucket", func(cmd *cobra.Command, args []string) error {
		return runBucketAction(cmd, args)
	}),
	ValidArgsFunction: completeBucketArgs,
}

func validateBucketArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if args[0] == "list" {
		if len(args) > 1 {
			return fmt.Errorf("list accepts no additional arguments")
		}
		return nil
	}
	if len(args) < 2 {
		return fmt.Errorf("bucket requires an action (e.g. create, remove)")
	}
	action := args[1]
	if action != "create" && action != "remove" {
		return fmt.Errorf("unknown bucket action %q", action)
	}
	if len(args) > 2 {
		return fmt.Errorf("too many arguments for %s action", action)
	}
	return nil
}

func runBucketAction(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	if args[0] == "list" {
		return runBucketList(cmd, args)
	}

	name := args[0]
	action := args[1]

	switch action {
	case "create":
		return runBucketCreate(cmd, name)
	case "remove":
		return runBucketRemove(cmd, name)
	default:
		return fmt.Errorf("unknown action %s", action)
	}
}

func runBucketCreate(cmd *cobra.Command, name string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	err = client.CreateBucket(name)
	if err != nil {
		return err
	}
	fmt.Printf("Bucket '%s' created successfully.\n", name)
	return nil
}

func runBucketList(cmd *cobra.Command, args []string) error {
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
}

func runBucketRemove(cmd *cobra.Command, name string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	err = client.DeleteBucket(name)
	if err != nil {
		return err
	}
	fmt.Printf("Bucket '%s' removed successfully.\n", name)
	return nil
}

func completeBucketArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		client, err := getAPIClient()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		buckets, err := client.ListBuckets()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var candidates []string
		if strings.HasPrefix("list", toComplete) {
			candidates = append(candidates, "list")
		}
		for _, bucket := range buckets {
			if strings.HasPrefix(bucket.Name, toComplete) {
				candidates = append(candidates, bucket.Name)
			}
		}
		return candidates, cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) == 1 {
		if args[0] == "list" {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		actions := []string{"create", "remove"}
		var matches []string
		for _, a := range actions {
			if strings.HasPrefix(a, toComplete) {
				matches = append(matches, a)
			}
		}
		return matches, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func init() {
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
