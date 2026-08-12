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
	Use:   "bucket",
	Short: "Manage workspace buckets",
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var bucketListFlags listFlags
var bucketServicesFlags listFlags
var bucketSecretsFlags listFlags
var bucketValuesFlags listFlags
var bucketConnectionsFlags listFlags
var bucketSDKsFlags listFlags
var bucketConnectionsService string
var bucketConnectionsUser string

var bucketCreateCmd = newBucketCommand("create <bucket-name>", "Create a bucket", "cli.bucket.create", runBucketCreate)
var bucketDeleteCmd = newBucketCommand("delete <bucket-name>", "Delete a bucket", "cli.bucket.delete", runBucketDelete)
var bucketShowCmd = newBucketCommand("show <bucket-name-or-id>", "Show a bucket", "cli.bucket.show", runBucketShow)
var bucketServicesCmd = newBucketCommand("services <bucket-name-or-id>", "List services represented in a bucket", "cli.bucket.services", runBucketServices)
var bucketSecretsCmd = newBucketCommand("secrets <bucket-name-or-id>", "List secret metadata in a bucket", "cli.bucket.secrets", runBucketSecrets)
var bucketValuesCmd = newBucketCommand("values <bucket-name-or-id>", "List non-secret values in a bucket", "cli.bucket.values", runBucketValues)
var bucketConnectionsCmd = newBucketCommand("connections <bucket-name-or-id>", "List connected users in a bucket", "cli.bucket.connections", runBucketConnections)
var bucketSDKsCmd = newBucketCommand("sdks <bucket-name-or-id>", "List SDK scopes using a bucket", "cli.bucket.sdks", runBucketSDKs)

var bucketListCmd = &cobra.Command{
	Use:   "list",
	Short: "List buckets",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.bucket.list", func(cmd *cobra.Command, _ []string) error {
		return runBucketList(cmd)
	}),
}

func newBucketCommand(use, short, spanName string, run func(*cobra.Command, string) error) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Args: cobra.ExactArgs(1),
		RunE: WithTelemetry(spanName, func(cmd *cobra.Command, args []string) error {
			return run(cmd, args[0])
		}),
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
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "bucket")
	fmt.Fprintf(cmd.OutOrStdout(), "Bucket %q created.\n", name)
	return nil
}

func runBucketList(cmd *cobra.Command) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	page, err := client.ListBucketSummariesPage(bucketListFlags.pageOptions())
	if err != nil {
		return err
	}
	if wantsJSON(cmd) {
		return writeJSONPage(cmd, page.Items, page.Total, bucketListFlags)
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

func runBucketDelete(cmd *cobra.Command, name string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	err = client.DeleteBucket(name)
	if err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "bucket")
	fmt.Fprintf(cmd.OutOrStdout(), "Bucket %q deleted.\n", name)
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
	if wantsJSON(cmd) {
		return writeJSON(cmd, bucket)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "name:\t%s\nid:\t%s\nsecrets:\t%d\nvalues:\t%d\ncreated_at:\t%s\n", bucket.Name, bucket.ID, bucket.SecretCount, bucket.ValueCount, bucket.CreatedAt)
	return nil
}

func runBucketServices(cmd *cobra.Command, nameOrID string) error {
	client, bucketID, err := bucketClientAndID(nameOrID)
	if err != nil {
		return err
	}
	page, err := client.ListBucketServicePage(bucketID, bucketServicesFlags.pageOptions())
	if err != nil {
		return err
	}
	if wantsJSON(cmd) {
		return writeJSONPage(cmd, page.Items, page.Total, bucketServicesFlags)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE_NAME\tSERVICE_ID\tSECRETS\tVALUES\tCONNECT_CONFIGS\tUSERS")
	for _, service := range page.Items {
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%d\n", service.ServiceName, service.ServiceID, service.SecretCount, service.ValueCount, service.ConnectConfigCount, service.ConnectedUserCount)
	}
	w.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, bucketServicesFlags)
	return nil
}

func runBucketSecrets(cmd *cobra.Command, nameOrID string) error {
	client, bucketID, err := bucketClientAndID(nameOrID)
	if err != nil {
		return err
	}
	page, err := client.ListSecretMetaPage(bucketID, bucketSecretsFlags.pageOptions())
	if err != nil {
		return err
	}
	if wantsJSON(cmd) {
		return writeJSONPage(cmd, page.Items, page.Total, bucketSecretsFlags)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE_ID\tKEY_NAME\tTYPE\tEXPIRES")
	for _, secret := range page.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", secret.ServiceID, secret.KeyName, secret.CredentialType, formatOptionalTime(secret.ExpiresAt))
	}
	w.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, bucketSecretsFlags)
	return nil
}

func runBucketValues(cmd *cobra.Command, nameOrID string) error {
	client, bucketID, err := bucketClientAndID(nameOrID)
	if err != nil {
		return err
	}
	page, err := client.ListBucketValuesPage(bucketID, bucketValuesFlags.pageOptions())
	if err != nil {
		return err
	}
	if wantsJSON(cmd) {
		return writeJSONPage(cmd, page.Items, page.Total, bucketValuesFlags)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "SERVICE_ID\tKEY_NAME\tLOCATION")
	for _, value := range page.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\n", value.ServiceID, value.KeyName, value.Location)
	}
	w.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, bucketValuesFlags)
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
	page, err := client.ListAuthConnectionPage(bucketID, serviceID, bucketConnectionsUser, bucketConnectionsFlags.pageOptions())
	if err != nil {
		return err
	}
	if wantsJSON(cmd) {
		return writeJSONPage(cmd, page.Items, page.Total, bucketConnectionsFlags)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "USER_REF\tSERVICE_ID\tAUTH_TYPE\tREFRESH_STATE\tLAST_FAILURE\tTRACE_ID\tID")
	for _, conn := range page.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", conn.EndUserRef, conn.ServiceID, conn.AuthType, conn.RefreshState, conn.LastFailureCode, conn.LastFailureTraceID, conn.ID)
	}
	w.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, bucketConnectionsFlags)
	return nil
}

func runBucketSDKs(cmd *cobra.Command, nameOrID string) error {
	client, bucketID, err := bucketClientAndID(nameOrID)
	if err != nil {
		return err
	}
	page, err := client.ListBucketSDKPage(bucketID, bucketSDKsFlags.pageOptions())
	if err != nil {
		return err
	}
	if wantsJSON(cmd) {
		return writeJSONPage(cmd, page.Items, page.Total, bucketSDKsFlags)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tID\tKIND\tACTIVE")
	for _, sdk := range page.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%t\n", sdk.Name, sdk.ID, sdk.Kind, sdk.Active)
	}
	w.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, bucketSDKsFlags)
	return nil
}

func bucketClientAndID(nameOrID string) (*cliapi.Client, string, error) {
	client, err := getAPIClient()
	if err != nil {
		return nil, "", err
	}
	reference := strings.TrimSpace(nameOrID)
	bucketID := reference
	if _, parseErr := uuid.Parse(reference); parseErr != nil {
		bucketID, err = client.ResolveBucketReference(reference)
	}
	if err != nil {
		return nil, "", err
	}
	return client, bucketID, nil
}

func resolveExplicitBucketID(value string) (string, error) {
	reference := strings.TrimSpace(value)
	if _, err := uuid.Parse(reference); err == nil {
		return reference, nil
	}
	client, err := getAPIClient()
	if err != nil {
		return "", err
	}
	return client.ResolveBucketReference(reference)
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

func init() {
	RootCmd.AddCommand(bucketCmd)
	bucketCmd.AddCommand(bucketCreateCmd, bucketDeleteCmd, bucketListCmd, bucketShowCmd, bucketServicesCmd, bucketSecretsCmd, bucketValuesCmd, bucketConnectionsCmd, bucketSDKsCmd)
	addJSONOutputFlag(bucketListCmd, bucketShowCmd, bucketServicesCmd, bucketSecretsCmd, bucketValuesCmd, bucketConnectionsCmd, bucketSDKsCmd)
	addListFlags(bucketListCmd, &bucketListFlags)
	addListFlags(bucketServicesCmd, &bucketServicesFlags)
	addListFlags(bucketSecretsCmd, &bucketSecretsFlags)
	addListFlags(bucketValuesCmd, &bucketValuesFlags)
	addListFlags(bucketConnectionsCmd, &bucketConnectionsFlags)
	addListFlags(bucketSDKsCmd, &bucketSDKsFlags)
	bucketConnectionsCmd.Flags().StringVar(&bucketConnectionsService, "service", "", "Service slug")
	bucketConnectionsCmd.Flags().StringVar(&bucketConnectionsUser, "user", "", "End-user reference")
}
