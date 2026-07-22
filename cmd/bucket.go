package cmd

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var bucketCmd = &cobra.Command{
	Use:   "bucket <name-or-id|list> [create|remove|show|services|secrets|values|connections|sdks]",
	Short: "Manage workspace buckets",
	Args:  validateBucketArgs,
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.bucket", func(cmd *cobra.Command, args []string) error {
		return runBucketAction(cmd, args)
	}),
	ValidArgsFunction: completeBucketArgs,
}

var bucketListFlags listFlags
var bucketConnectionsService string
var bucketConnectionsUser string

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
	if !isBucketAction(action) {
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
	action, ok := bucketActionHandlers()[args[1]]
	if !ok {
		return fmt.Errorf("unknown action %s", args[1])
	}
	return action(cmd, args[0])
}

type bucketActionHandler func(*cobra.Command, string) error

func bucketActionHandlers() map[string]bucketActionHandler {
	return map[string]bucketActionHandler{
		"create":      runBucketCreate,
		"remove":      runBucketRemove,
		"show":        runBucketShow,
		"services":    runBucketServices,
		"secrets":     runBucketSecrets,
		"values":      runBucketValues,
		"connections": runBucketConnections,
		"sdks":        runBucketSDKs,
	}
}

func isBucketAction(action string) bool {
	_, ok := bucketActionHandlers()[action]
	return ok
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
	page, err := client.ListBucketSummariesPage(bucketListFlags.pageOptions())
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tID\tSECRETS\tVALUES")
	for _, b := range page.Items {
		defStr := ""
		if b.IsDefault {
			defStr = " (default)"
		}
		fmt.Fprintf(w, "%s%s\t%s\t%d\t%d\n", b.Name, defStr, b.ID, b.SecretCount, b.ValueCount)
	}
	w.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, bucketListFlags)
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

func runBucketShow(cmd *cobra.Command, nameOrID string) error {
	client, bucketID, err := bucketClientAndID(nameOrID)
	if err != nil {
		return err
	}
	bucket, err := client.GetBucketSummary(bucketID)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "name:\t%s\nid:\t%s\nsecrets:\t%d\nvalues:\t%d\ncreated_at:\t%s\n", bucket.Name, bucket.ID, bucket.SecretCount, bucket.ValueCount, bucket.CreatedAt)
	return nil
}

func runBucketServices(cmd *cobra.Command, nameOrID string) error {
	client, bucketID, err := bucketClientAndID(nameOrID)
	if err != nil {
		return err
	}
	page, err := client.ListBucketServicePage(bucketID, bucketListFlags.pageOptions())
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE_NAME\tSERVICE_ID\tSECRETS\tVALUES\tCONNECT_CONFIGS\tUSERS")
	for _, service := range page.Items {
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%d\n", service.ServiceName, service.ServiceID, service.SecretCount, service.ValueCount, service.ConnectConfigCount, service.ConnectedUserCount)
	}
	w.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, bucketListFlags)
	return nil
}

func runBucketSecrets(cmd *cobra.Command, nameOrID string) error {
	client, bucketID, err := bucketClientAndID(nameOrID)
	if err != nil {
		return err
	}
	page, err := client.ListSecretMetaPage(bucketID, bucketListFlags.pageOptions())
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE_ID\tKEY_NAME\tTYPE\tEXPIRES")
	for _, secret := range page.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", secret.ServiceID, secret.KeyName, secret.CredentialType, formatOptionalTime(secret.ExpiresAt))
	}
	w.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, bucketListFlags)
	return nil
}

func runBucketValues(cmd *cobra.Command, nameOrID string) error {
	client, bucketID, err := bucketClientAndID(nameOrID)
	if err != nil {
		return err
	}
	page, err := client.ListBucketValuesPage(bucketID, bucketListFlags.pageOptions())
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE_ID\tKEY_NAME\tLOCATION")
	for _, value := range page.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\n", value.ServiceID, value.KeyName, value.Location)
	}
	w.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, bucketListFlags)
	return nil
}

func runBucketConnections(cmd *cobra.Command, nameOrID string) error {
	client, bucketID, err := bucketClientAndID(nameOrID)
	if err != nil {
		return err
	}
	serviceID, err := resolveOptionalServiceID(client, bucketConnectionsService)
	if err != nil {
		return err
	}
	page, err := client.ListAuthConnectionPage(bucketID, serviceID, bucketConnectionsUser, bucketListFlags.pageOptions())
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "USER_REF\tSERVICE_ID\tAUTH_TYPE\tREFRESH_STATE\tLAST_FAILURE\tTRACE_ID\tID")
	for _, conn := range page.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", conn.EndUserRef, conn.ServiceID, conn.AuthType, conn.RefreshState, conn.LastFailureCode, conn.LastFailureTraceID, conn.ID)
	}
	w.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, bucketListFlags)
	return nil
}

func runBucketSDKs(cmd *cobra.Command, nameOrID string) error {
	client, bucketID, err := bucketClientAndID(nameOrID)
	if err != nil {
		return err
	}
	page, err := client.ListBucketSDKPage(bucketID, bucketListFlags.pageOptions())
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tID\tKIND\tACTIVE")
	for _, sdk := range page.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%t\n", sdk.Name, sdk.ID, sdk.Kind, sdk.Active)
	}
	w.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, bucketListFlags)
	return nil
}

func bucketClientAndID(nameOrID string) (*cliapi.Client, string, error) {
	client, err := getAPIClient()
	if err != nil {
		return nil, "", err
	}
	bucketID, err := resolveBucketID(client, nameOrID)
	if err != nil {
		return nil, "", err
	}
	return client, bucketID, nil
}

func resolveOptionalServiceID(client *cliapi.Client, serviceSlug string) (string, error) {
	if strings.TrimSpace(serviceSlug) == "" {
		return "", nil
	}
	return resolveServiceIDFromSlug(client, serviceSlug)
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return "never"
	}
	return value.Format(time.RFC3339)
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
		return matchingBucketActions(toComplete), cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func matchingBucketActions(toComplete string) []string {
	actions := []string{"create", "remove", "show", "services", "secrets", "values", "connections", "sdks"}
	var matches []string
	for _, action := range actions {
		if strings.HasPrefix(action, toComplete) {
			matches = append(matches, action)
		}
	}
	return matches
}

func init() {
	addListFlags(bucketCmd, &bucketListFlags)
	bucketCmd.Flags().StringVar(&bucketConnectionsService, "service", "", "Service slug for connections")
	bucketCmd.Flags().StringVar(&bucketConnectionsUser, "user", "", "End-user reference for connections")
	RootCmd.AddCommand(bucketCmd)
}

func resolveBucketID(client *cliapi.Client, nameOrID string) (string, error) {
	if nameOrID == "" {
		return "", nil
	}
	if _, err := uuid.Parse(nameOrID); err == nil {
		return nameOrID, nil
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
	if _, err := uuid.Parse(nameOrID); err == nil {
		return nameOrID, nil
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
	return createMissingBucketPrompt(client, nameOrID)
}

func createMissingBucketPrompt(client *cliapi.Client, nameOrID string) (string, error) {
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
