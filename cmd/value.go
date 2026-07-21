package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var valueCmd = &cobra.Command{
	Use:   "value <bucket-id> [set|list|remove] [args...]",
	Short: "Manage workspace bucket values",
	Args:  validateValueArgs,
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.value", func(cmd *cobra.Command, args []string) error {
		return runValueAction(cmd, args)
	}),
	ValidArgsFunction: completeValueArgs,
}

func validateValueArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) < 2 {
		return fmt.Errorf("value requires an action (e.g. set, list, remove)")
	}
	action := args[1]
	if action != "set" && action != "list" && action != "remove" {
		return fmt.Errorf("unknown value action %q", action)
	}
	if action == "set" {
		if len(args) != 6 {
			return fmt.Errorf("set accepts exactly 4 args after action (service-slug, key-name, location, value)")
		}
	} else if action == "list" {
		if len(args) != 2 {
			return fmt.Errorf("list accepts exactly 0 args after action")
		}
	} else if action == "remove" {
		if len(args) != 4 {
			return fmt.Errorf("remove accepts exactly 2 args after action (service-slug, key-name)")
		}
	}
	return nil
}

func runValueAction(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}

	bucketID := args[0]
	action := args[1]

	switch action {
	case "set":
		serviceSlug := args[2]
		keyName := args[3]
		location := args[4]
		value := args[5]
		return runValueSet(cmd, bucketID, serviceSlug, keyName, location, value)
	case "list":
		return runValueList(cmd, bucketID)
	case "remove":
		serviceSlug := args[2]
		keyName := args[3]
		return runValueRemove(cmd, bucketID, serviceSlug, keyName)
	default:
		return fmt.Errorf("unknown action %s", action)
	}
}

func runValueSet(cmd *cobra.Command, bucketID, serviceSlug, keyName, location, value string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
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
}

func runValueList(cmd *cobra.Command, bucketID string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
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
}

func runValueRemove(cmd *cobra.Command, bucketID, serviceSlug, keyName string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
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
}

func completeValueArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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
		for _, bucket := range buckets {
			if strings.HasPrefix(bucket.Name, toComplete) {
				candidates = append(candidates, bucket.Name)
			}
			if strings.HasPrefix(bucket.ID, toComplete) {
				candidates = append(candidates, bucket.ID)
			}
		}
		return candidates, cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) == 1 {
		actions := []string{"set", "list", "remove"}
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
	RootCmd.AddCommand(valueCmd)
}
