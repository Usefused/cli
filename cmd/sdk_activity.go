package cmd

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

var (
	sdkActivityListFlags   listFlags
	sdkActivityAllVersions bool
	sdkActivityStatus      string
	sdkActivityStart       string
	sdkActivityEnd         string
)

var sdkActivityCmd = &cobra.Command{
	Use:   "activity <sdk-name@version-or-version-id>",
	Short: "List SDK execution receipts",
	Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(1)(cmd, args); err != nil {
			return err
		}
		return validateExactAppReference(args[0], "sdk activity")
	},
	RunE: WithTelemetry("cli.sdk.activity", func(cmd *cobra.Command, args []string) error {
		return runSDKActivity(cmd, downloadTargetFromName(args[0]))
	}),
}

// init registers the SDK activity command and its bounded receipt filters.
func init() {
	sdkCmd.AddCommand(sdkActivityCmd)
	addJSONOutputFlag(sdkActivityCmd)
	addListFlags(sdkActivityCmd, &sdkActivityListFlags)
	sdkActivityCmd.Flags().BoolVar(&sdkActivityAllVersions, "all-versions", false, "Include receipts from every version of this SDK")
	sdkActivityCmd.Flags().StringVar(&sdkActivityStatus, "status", "", "Filter by success or failed")
	sdkActivityCmd.Flags().StringVar(&sdkActivityStart, "start", "", "Inclusive RFC3339 start time")
	sdkActivityCmd.Flags().StringVar(&sdkActivityEnd, "end", "", "Inclusive RFC3339 end time")
}

// runSDKActivity resolves an exact SDK and reads its canonical Engine receipts.
func runSDKActivity(cmd *cobra.Command, target sdkDownloadTarget) error {
	if err := validateSDKActivityFilters(); err != nil {
		return err
	}
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	appID, err := client.ResolveSDKAppReference(target.Name, target.Version)
	if err != nil {
		return fmt.Errorf("resolve SDK version: %w", err)
	}
	page, err := client.ListSDKExecutionEvents(appID, api.AppExecutionEventOptions{
		IncludeAllVersions: sdkActivityAllVersions,
		Status:             strings.TrimSpace(sdkActivityStatus),
		StartDate:          strings.TrimSpace(sdkActivityStart),
		EndDate:            strings.TrimSpace(sdkActivityEnd),
		PageOptions:        sdkActivityListFlags.pageOptions(),
	})
	if err != nil {
		return fmt.Errorf("list SDK execution activity: %w", err)
	}
	if wantsJSON(cmd) {
		return writeJSONPage(cmd, page.Items, page.Total, sdkActivityListFlags)
	}
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 2, ' ', 0)
	fmt.Fprintln(writer, "STARTED\tSTATUS\tOPERATION\tPROVIDER_STATUS\tTOTAL_MS\tPROVIDER_MS\tRECEIPT_ID\tTRACE_ID")
	for _, event := range page.Items {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			event.StartedAt, event.Status, event.Operation, optionalInt(event.ProviderHTTPStatus), event.LatencyMS,
			optionalInt64(event.ProviderLatencyMS), event.ID, event.TraceID)
	}
	_ = writer.Flush()
	printPageSummary(cmd.OutOrStdout(), page.Total, sdkActivityListFlags)
	return nil
}

// validateSDKActivityFilters rejects ambiguous statuses and non-RFC3339 bounds.
func validateSDKActivityFilters() error {
	status := strings.TrimSpace(sdkActivityStatus)
	if status != "" && status != "success" && status != "failed" {
		return errors.New("--status must be success or failed")
	}
	for name, value := range map[string]string{"--start": sdkActivityStart, "--end": sdkActivityEnd} {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return fmt.Errorf("%s must be RFC3339", name)
		}
	}
	return nil
}

// optionalInt formats a nullable integer for the human activity table.
func optionalInt(value *int) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprint(*value)
}

// optionalInt64 formats a nullable 64-bit integer for the human activity table.
func optionalInt64(value *int64) string {
	if value == nil {
		return "-"
	}
	return fmt.Sprint(*value)
}
