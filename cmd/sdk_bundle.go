package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const maxCLIExecutionBundleBytes = 2 << 20
const maxCLIExecutionManifestBytes = 1 << 20

var sdkBundleCmd = commandGroup("bundle", "Upload code compiled outside Engine")

var sdkBundleAttachCmd = &cobra.Command{
	Use:   "attach <app-version-id>",
	Short: "Upload code compiled outside Engine to one Unified App version",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.unified.bundle.attach", func(cmd *cobra.Command, args []string) error {
		return runSDKBundleAttach(cmd, args[0])
	}),
}

// runSDKBundleAttach validates exact local artifacts before making the audited Engine control mutation.
func runSDKBundleAttach(cmd *cobra.Command, appID string) error {
	// The attach route has no family-wide or latest-version selector.
	parsedID, err := uuid.Parse(appID)
	if err != nil || parsedID.String() != appID {
		return errors.New("attach requires an exact App version ID")
	}
	sourceHash, _ := cmd.Flags().GetString("source-hash")
	bundlePath, _ := cmd.Flags().GetString("bundle")
	manifestPath, _ := cmd.Flags().GetString("manifest")
	// All three values are required; the source hash must match the applied plan receipt.
	if strings.TrimSpace(sourceHash) == "" || strings.TrimSpace(bundlePath) == "" || strings.TrimSpace(manifestPath) == "" {
		return errors.New("--source-hash, --bundle, and --manifest are required")
	}
	bundle, err := readBoundedSDKBundleFile(bundlePath, maxCLIExecutionBundleBytes)
	if err != nil {
		return fmt.Errorf("read bundle: %w", err)
	}
	manifest, err := readBoundedSDKBundleFile(manifestPath, maxCLIExecutionManifestBytes)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	client, err := getAPIClient()
	// Local validation precedes credential lookup and network mutation.
	if err != nil {
		return err
	}
	result, err := client.AttachExecutionBundle(appID, sourceHash, bundle, json.RawMessage(manifest))
	if err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), "unified.bundle.attach", "unified_app")
	// The receipt contains metadata only; compiled code and saved credentials never enter output.
	if wantsJSON(cmd) {
		return writeJSON(cmd, result)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Uploaded compiled code to App version %s.\n", result.AppID)
	return nil
}

// readBoundedSDKBundleFile caps allocation even when a local file changes between stat and read.
func readBoundedSDKBundleFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open file")
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	// Empty or growing files are invalid deployment artifacts.
	if err != nil || len(content) == 0 || int64(len(content)) > limit {
		return nil, errors.New("file is empty, unreadable, or too large")
	}
	return content, nil
}

// init registers the explicit immutable-code attachment after SDK apply.
func init() {
	unifiedCmd.AddCommand(sdkBundleCmd)
	sdkBundleCmd.AddCommand(sdkBundleAttachCmd)
	sdkBundleAttachCmd.Flags().String("source-hash", "", "Exact source hash from the plan receipt")
	sdkBundleAttachCmd.Flags().String("bundle", "", "Compiled JavaScript file")
	sdkBundleAttachCmd.Flags().String("manifest", "", "Compiler metadata JSON file")
	sdkBundleAttachCmd.Flags().Bool(jsonOutputFlag, false, "Print upload result as JSON")
}
