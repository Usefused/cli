package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	defaultSDKOpenAPIFormat     = "yaml"
	maxSDKOpenAPIOperationBytes = 512
	maxSDKOpenAPIYAMLDepth      = 128
	maxSDKOpenAPINameBytes      = 120
	maxSDKOpenAPIVersionBytes   = 80
)

var (
	sdkOpenAPIOperation string
	sdkOpenAPIOut       string
	sdkOpenAPIFormat    string
	apiOpenAPIOperation string
	apiOpenAPIOut       string
	apiOpenAPIFormat    string
)

var apiCmd = &cobra.Command{
	Use:   "api",
	Short: "Manage direct REST APIs",
	Args:  cobra.NoArgs,
	RunE:  requireSubcommand,
}

var sdkOpenAPICmd = &cobra.Command{
	Use:   "openapi <sdk-name@version-or-version-id>",
	Short: "Export one exact SDK version as OpenAPI",
	Args:  validateSDKOpenAPIArgs,
	RunE:  WithTelemetry("cli.sdk.openapi", runSDKOpenAPICommand),
}

var apiOpenAPICmd = &cobra.Command{
	Use:   "openapi <api-name@version-or-version-id>",
	Short: "Export one exact REST API version as OpenAPI",
	Args:  validateAPIOpenAPIArgs,
	RunE:  WithTelemetry("cli.api.openapi", runAPIOpenAPICommand),
}

type sdkOpenAPIOptions struct {
	Operation string
	Format    string
	Out       string
	JSON      bool
}

type sdkOpenAPIOutput struct {
	SDK        string `json:"sdk,omitempty"`
	API        string `json:"api,omitempty"`
	Version    string `json:"version,omitempty"`
	VersionID  string `json:"version_id"`
	Operation  string `json:"operation,omitempty"`
	Operations int    `json:"operation_count"`
	Format     string `json:"format"`
	Path       string `json:"path"`
	Bytes      int    `json:"bytes"`
	SHA256     string `json:"sha256"`
	ServerURL  string `json:"server_url"`
	Status     string `json:"status"`
}

// init registers the exact-version OpenAPI export and its file/output controls.
func init() {
	RootCmd.AddCommand(apiCmd)
	sdkCmd.AddCommand(sdkOpenAPICmd)
	apiCmd.AddCommand(apiOpenAPICmd)
	sdkOpenAPICmd.Flags().StringVar(&sdkOpenAPIOperation, "operation", "", "Export only this exact operation ID")
	sdkOpenAPICmd.Flags().StringVarP(&sdkOpenAPIOut, "out", "o", "", "Output file (defaults to an SDK/version OpenAPI filename)")
	sdkOpenAPICmd.Flags().StringVar(&sdkOpenAPIFormat, "format", defaultSDKOpenAPIFormat, "Document format: yaml or json")
	sdkOpenAPICmd.Flags().Bool(jsonOutputFlag, false, "Print export metadata as JSON")
	apiOpenAPICmd.Flags().StringVar(&apiOpenAPIOperation, "operation", "", "Export only this exact operation ID")
	apiOpenAPICmd.Flags().StringVarP(&apiOpenAPIOut, "out", "o", "", "Output file (defaults to an API/version OpenAPI filename)")
	apiOpenAPICmd.Flags().StringVar(&apiOpenAPIFormat, "format", defaultSDKOpenAPIFormat, "Document format: yaml or json")
	apiOpenAPICmd.Flags().Bool(jsonOutputFlag, false, "Print export metadata as JSON")
}

// validateSDKOpenAPIArgs requires one immutable SDK version reference.
func validateSDKOpenAPIArgs(cmd *cobra.Command, args []string) error {
	// Exact arity prevents a second reference from being mistaken for an output path.
	if err := cobra.ExactArgs(1)(cmd, args); err != nil {
		return err
	}
	return validateExactAppReference(args[0], "sdk openapi")
}

// validateAPIOpenAPIArgs requires one immutable REST API version reference.
func validateAPIOpenAPIArgs(cmd *cobra.Command, args []string) error {
	// Exact arity prevents a second reference from being mistaken for an output path.
	if err := cobra.ExactArgs(1)(cmd, args); err != nil {
		return err
	}
	return validateExactAppReference(args[0], "api openapi")
}

// runSDKOpenAPICommand translates the exact CLI reference before export.
func runSDKOpenAPICommand(cmd *cobra.Command, args []string) error {
	return runSDKOpenAPI(cmd, downloadTargetFromName(args[0]))
}

// runAPIOpenAPICommand translates the exact REST API reference before export.
func runAPIOpenAPICommand(cmd *cobra.Command, args []string) error {
	return runAPIOpenAPI(cmd, downloadTargetFromName(args[0]))
}

// runSDKOpenAPI resolves one Version ID, renders the document, and atomically writes the selected file.
func runSDKOpenAPI(cmd *cobra.Command, target sdkDownloadTarget) error {
	options, err := readSDKOpenAPIOptions(cmd)
	if err != nil {
		return err
	}
	return runAppOpenAPI(cmd, target, options, "SDK", false)
}

// runAPIOpenAPI resolves one direct REST API version and uses the shared immutable app export lifecycle.
func runAPIOpenAPI(cmd *cobra.Command, target sdkDownloadTarget) error {
	options, err := readAPIOpenAPIOptions(cmd)
	// Invalid local flags must stop before app identity resolution or Engine access.
	if err != nil {
		return err
	}
	return runAppOpenAPI(cmd, target, options, "REST API", true)
}

// runAppOpenAPI resolves one immutable app, renders its document, and atomically writes the selected file.
func runAppOpenAPI(cmd *cobra.Command, target sdkDownloadTarget, options sdkOpenAPIOptions, resourceLabel string, directAPI bool) error {
	client, err := getAPIClient()
	// Missing Engine configuration prevents both identity resolution and export.
	if err != nil {
		return err
	}
	appID, err := client.ResolveSDKAppReference(target.Name, target.Version)
	// REST APIs deliberately reuse SDK-kind immutable storage while retaining their own public command surface.
	if err != nil {
		return fmt.Errorf("resolve %s version: %w", resourceLabel, err)
	}
	payload, err := client.ExportSDKOpenAPI(appID, options.Operation)
	// Repair guidance is limited to the stable schema-snapshot failure returned by current and older Engines.
	if err != nil {
		return actionableAppOpenAPIExportError(target, options, directAPI, err)
	}
	serverURL, err := normalizedSDKOpenAPIServerURL(client.BaseURL)
	// An unsafe Engine URL must never be embedded into a generated contract.
	if err != nil {
		return err
	}
	document, operationCount, err := renderSDKOpenAPIDocument(payload, serverURL, appID, options.Format)
	// Remote identity or schema inconsistencies are rejected before touching the output path.
	if err != nil {
		return err
	}
	path := sdkOpenAPIOutputPath(options.Out, target, appID, options.Format)
	// Atomic publication preserves any earlier document when the new write fails.
	if err := atomicWriteFile(path, document, 0o644, nil); err != nil {
		return fmt.Errorf("write OpenAPI document: %w", err)
	}
	output := newSDKOpenAPIOutput(target, appID, options, path, serverURL, document, operationCount, directAPI)
	// The file is the command's durable result, so a closed metadata stream cannot turn a completed export into a retryable failure.
	_ = writeSDKOpenAPIOutput(cmd, output)
	return nil
}

// readSDKOpenAPIOptions validates local flags before any Engine request occurs.
func readSDKOpenAPIOptions(cmd *cobra.Command) (sdkOpenAPIOptions, error) {
	return readAppOpenAPIOptions(cmd, sdkOpenAPIOperation, sdkOpenAPIOut, sdkOpenAPIFormat)
}

// readAPIOpenAPIOptions validates direct REST API flags before any Engine request occurs.
func readAPIOpenAPIOptions(cmd *cobra.Command) (sdkOpenAPIOptions, error) {
	return readAppOpenAPIOptions(cmd, apiOpenAPIOperation, apiOpenAPIOut, apiOpenAPIFormat)
}

// readAppOpenAPIOptions applies one local validation contract to both public OpenAPI command surfaces.
func readAppOpenAPIOptions(cmd *cobra.Command, operation, out, requestedFormat string) (sdkOpenAPIOptions, error) {
	format := strings.ToLower(strings.TrimSpace(requestedFormat))
	// Only the two deterministic encodings implemented below are accepted.
	if format != "yaml" && format != "json" {
		return sdkOpenAPIOptions{}, errors.New("--format must be yaml or json")
	}
	validatedOperation, err := exactSDKOpenAPIOperation(cmd, operation)
	// An explicitly supplied operation must retain exact identity without whitespace normalization.
	if err != nil {
		return sdkOpenAPIOptions{}, err
	}
	// An explicit output flag cannot silently fall back to a generated filename.
	if cmd.Flags().Changed("out") && strings.TrimSpace(out) == "" {
		return sdkOpenAPIOptions{}, errors.New("--out cannot be empty")
	}
	return sdkOpenAPIOptions{Operation: validatedOperation, Format: format, Out: out, JSON: wantsJSON(cmd)}, nil
}

// actionableAppOpenAPIExportError adds a bounded repair path only for immutable schema-snapshot conflicts.
func actionableAppOpenAPIExportError(target sdkDownloadTarget, options sdkOpenAPIOptions, directAPI bool, cause error) error {
	var apiErr *api.APIError
	// Transport, authorization, lookup, and ordinary configuration failures keep their original diagnostics.
	if !errors.As(cause, &apiErr) || !repairableAppOpenAPISchemaError(apiErr) {
		return cause
	}
	surface := "sdk"
	// Direct REST APIs must be retried through their own public command even though identity lookup is shared internally.
	if directAPI {
		surface = "api"
	}
	retry := "fused-cli " + surface + " openapi " + shellQuoteWorkspaceServiceArg(appOpenAPIReference(target))
	// Preserve an explicit output path in the suggested retry so recovery writes where the user requested.
	if strings.TrimSpace(options.Out) != "" {
		retry += " --out " + shellQuoteWorkspaceServiceArg(options.Out)
	}
	// Exact operation filtering must survive repair so recovery does not widen the exported surface.
	if options.Operation != "" {
		retry += " --operation " + shellQuoteWorkspaceServiceArg(options.Operation)
	}
	// Non-default encoding must survive repair so the output extension and document bytes remain consistent.
	if options.Format != defaultSDKOpenAPIFormat {
		retry += " --format " + shellQuoteWorkspaceServiceArg(options.Format)
	}
	// Structured callers need the retry to retain machine-readable metadata output.
	if options.JSON {
		retry += " --json"
	}
	repair := "fused-cli workspace services refresh-missing-contracts --limit 100"
	// Structured recovery stays machine-readable when the failed export requested JSON.
	if options.JSON {
		repair += " --json"
	}
	recoveryError := *apiErr
	// The exact bounded repair command must survive shared JSON classification instead of remaining only in wrapped prose.
	recoveryError.Recovery = repair
	recoveryError.Remediation = "Run the recovery command repeatedly until it reports 0 missing, then retry `" + retry + "`."
	return &recoveryError
}

// repairableAppOpenAPISchemaError recognizes both the stable code and the bounded legacy 409 emitted before that code existed.
func repairableAppOpenAPISchemaError(apiErr *api.APIError) bool {
	// Only a conflict represents immutable snapshot state that an explicit refresh can repair.
	if apiErr == nil || apiErr.HTTPStatus != http.StatusConflict {
		return false
	}
	// Current Engines expose a dedicated code that avoids parsing human text.
	if apiErr.Code == "app_openapi_schema_unavailable" {
		return true
	}
	// Older Engines used configuration_conflict only for this exact bounded message.
	return apiErr.Code == "configuration_conflict" && apiErr.Message == "immutable operation schemas are unavailable or inconsistent"
}

// appOpenAPIReference reconstructs the exact user-facing immutable reference for recovery output.
func appOpenAPIReference(target sdkDownloadTarget) string {
	// A Version ID is already complete and must not gain an empty version suffix.
	if target.Version == "" {
		return target.Name
	}
	return target.Name + "@" + target.Version
}

// exactSDKOpenAPIOperation preserves operation identity while rejecting an explicitly empty filter.
func exactSDKOpenAPIOperation(cmd *cobra.Command, operation string) (string, error) {
	if operation == "" && !cmd.Flags().Changed("operation") {
		return "", nil
	}
	if operation == "" || operation != strings.TrimSpace(operation) || len(operation) > maxSDKOpenAPIOperationBytes {
		return "", fmt.Errorf("--operation must be a non-empty exact operation ID of at most %d bytes", maxSDKOpenAPIOperationBytes)
	}
	return operation, nil
}

// normalizedSDKOpenAPIServerURL validates the public Engine base embedded in the exported document.
func normalizedSDKOpenAPIServerURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("Engine URL must be an absolute http or https URL without credentials, query, or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
	return strings.TrimRight(parsed.String(), "/"), nil
}

// renderSDKOpenAPIDocument validates exact app identity and injects the configured Engine server before encoding.
func renderSDKOpenAPIDocument(payload []byte, serverURL, appID, format string) ([]byte, int, error) {
	document, operationCount, err := decodeSDKOpenAPIDocument(payload, appID)
	if err != nil {
		return nil, 0, err
	}
	document["servers"] = []any{map[string]any{"url": serverURL}}
	if format == "json" {
		encoded, marshalErr := json.MarshalIndent(document, "", "  ")
		if marshalErr != nil {
			return nil, 0, marshalErr
		}
		return append(encoded, '\n'), operationCount, nil
	}
	encoded, err := marshalSDKOpenAPIYAML(document)
	return encoded, operationCount, err
}

// decodeSDKOpenAPIDocument retains exact numbers and validates the exact app-specific OpenAPI contract.
func decodeSDKOpenAPIDocument(payload []byte, appID string) (map[string]any, int, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil || document == nil {
		return nil, 0, errors.New("Engine returned an invalid OpenAPI document")
	}
	if err := ensureSDKOpenAPIJSONEOF(decoder); err != nil {
		return nil, 0, errors.New("Engine returned trailing OpenAPI data")
	}
	execution, err := validateSDKOpenAPIDocumentIdentity(document, appID)
	if err != nil {
		return nil, 0, err
	}
	operationCount, err := sdkOpenAPIOperationCount(document["x-fused-operation-count"])
	if err != nil {
		return nil, 0, err
	}
	branchCount, err := sdkOpenAPIRequestBranchCount(execution)
	if err != nil || operationCount != branchCount {
		return nil, 0, errors.New("Engine returned an inconsistent OpenAPI operation count")
	}
	return document, operationCount, nil
}

// validateSDKOpenAPIDocumentIdentity binds the supported document version and execution path to the resolved app.
func validateSDKOpenAPIDocumentIdentity(document map[string]any, appID string) (map[string]any, error) {
	if version, ok := document["openapi"].(string); !ok || !supportedSDKOpenAPIVersion(version) {
		return nil, errors.New("Engine returned an unsupported OpenAPI version")
	}
	if document["x-fused-app-id"] != appID {
		return nil, errors.New("Engine returned OpenAPI for a different SDK Version ID")
	}
	return validateSDKOpenAPIExecutionPath(document, appID)
}

// supportedSDKOpenAPIVersion accepts only the numeric patch releases in the OpenAPI 3.1 line.
func supportedSDKOpenAPIVersion(version string) bool {
	patch := strings.TrimPrefix(version, "3.1.")
	if patch == version || patch == "" {
		return false
	}
	for _, character := range patch {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// validateSDKOpenAPIExecutionPath requires the real exact-app execution POST rather than a synthetic route.
func validateSDKOpenAPIExecutionPath(document map[string]any, appID string) (map[string]any, error) {
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		return nil, errors.New("Engine returned an OpenAPI document without paths")
	}
	execution, ok := paths["/v1/apps/{app_id}/executions"].(map[string]any)
	if !ok {
		return nil, errors.New("Engine returned OpenAPI without the app execution path")
	}
	post, ok := execution["post"].(map[string]any)
	if !ok {
		return nil, errors.New("Engine returned OpenAPI without the app execution operation")
	}
	if !sdkOpenAPIPathBindsAppID(post, appID) {
		return nil, errors.New("Engine returned OpenAPI without the exact Version ID path binding")
	}
	return post, nil
}

// sdkOpenAPIPathBindsAppID verifies the path parameter enum contains only the resolved Version ID.
func sdkOpenAPIPathBindsAppID(post map[string]any, appID string) bool {
	parameters, ok := post["parameters"].([]any)
	if !ok {
		return false
	}
	for _, value := range parameters {
		parameter, isObject := value.(map[string]any)
		if !isObject || parameter["name"] != "app_id" || parameter["in"] != "path" {
			continue
		}
		schema, schemaOK := parameter["schema"].(map[string]any)
		values, enumOK := schema["enum"].([]any)
		return schemaOK && enumOK && len(values) == 1 && values[0] == appID
	}
	return false
}

// sdkOpenAPIRequestBranchCount reads the discriminator's exact request oneOf branch count.
func sdkOpenAPIRequestBranchCount(post map[string]any) (int, error) {
	requestBody, ok := post["requestBody"].(map[string]any)
	if !ok {
		return 0, errors.New("OpenAPI execution request body is unavailable")
	}
	content, ok := requestBody["content"].(map[string]any)
	media, mediaOK := content["application/json"].(map[string]any)
	schema, schemaOK := media["schema"].(map[string]any)
	branches, branchesOK := schema["oneOf"].([]any)
	if !ok || !mediaOK || !schemaOK || !branchesOK || len(branches) == 0 {
		return 0, errors.New("OpenAPI execution request branches are unavailable")
	}
	return len(branches), nil
}

// sdkOpenAPIOperationCount admits the positive exact integer published by the Engine document.
func sdkOpenAPIOperationCount(value any) (int, error) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, errors.New("Engine returned OpenAPI without an operation count")
	}
	count, err := strconv.ParseInt(number.String(), 10, 32)
	if err != nil || count < 1 {
		return 0, errors.New("Engine returned an invalid OpenAPI operation count")
	}
	return int(count), nil
}

// ensureSDKOpenAPIJSONEOF proves the Engine returned exactly one JSON document.
func ensureSDKOpenAPIJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON data")
	}
	return nil
}

// marshalSDKOpenAPIYAML converts the exact-number JSON tree without a float64 round trip.
func marshalSDKOpenAPIYAML(document map[string]any) ([]byte, error) {
	root, err := sdkOpenAPIYAMLNode(document, 0)
	if err != nil {
		return nil, err
	}
	return yaml.Marshal(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}})
}

// sdkOpenAPIYAMLNode maps bounded JSON values to explicit YAML tags while preserving numeric spelling.
func sdkOpenAPIYAMLNode(value any, depth int) (*yaml.Node, error) {
	if depth > maxSDKOpenAPIYAMLDepth {
		// Why: the Engine response is bounded by bytes, but recursive YAML projection also needs a stack bound.
		return nil, errors.New("OpenAPI document exceeds the supported nesting depth")
	}
	switch typed := value.(type) {
	case map[string]any:
		return sdkOpenAPIYAMLMapping(typed, depth+1)
	case []any:
		return sdkOpenAPIYAMLSequence(typed, depth+1)
	case json.Number:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: sdkOpenAPIYAMLNumberTag(typed), Value: typed.String()}, nil
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: typed}, nil
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprintf("%t", typed)}, nil
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	default:
		return nil, fmt.Errorf("unsupported OpenAPI JSON value %T", value)
	}
}

// sdkOpenAPIYAMLMapping sorts JSON object keys for deterministic OpenAPI files.
func sdkOpenAPIYAMLMapping(values map[string]any, depth int) (*yaml.Node, error) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, key := range keys {
		value, err := sdkOpenAPIYAMLNode(values[key], depth)
		if err != nil {
			return nil, err
		}
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
	}
	return node, nil
}

// sdkOpenAPIYAMLSequence preserves JSON array order in the exported contract.
func sdkOpenAPIYAMLSequence(values []any, depth int) (*yaml.Node, error) {
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, value := range values {
		child, err := sdkOpenAPIYAMLNode(value, depth)
		if err != nil {
			return nil, err
		}
		node.Content = append(node.Content, child)
	}
	return node, nil
}

// sdkOpenAPIYAMLNumberTag distinguishes exact JSON integers from decimals and exponents.
func sdkOpenAPIYAMLNumberTag(number json.Number) string {
	if strings.ContainsAny(number.String(), ".eE") {
		return "!!float"
	}
	return "!!int"
}

// sdkOpenAPIOutputPath selects the explicit path or a safe deterministic filename.
func sdkOpenAPIOutputPath(out string, target sdkDownloadTarget, appID, format string) string {
	if out != "" {
		return out
	}
	stem := appID
	if target.Version != "" {
		name := safeSDKOpenAPIFileSegment(target.Name, maxSDKOpenAPINameBytes)
		version := safeSDKOpenAPIFileSegment(target.Version, maxSDKOpenAPIVersionBytes)
		if name != "" && version != "" {
			stem = name + "-" + version
		}
	}
	return stem + ".openapi." + format
}

// safeSDKOpenAPIFileSegment removes path controls and bounds one default filename component.
func safeSDKOpenAPIFileSegment(value string, maxBytes int) string {
	var builder strings.Builder
	lastSeparator := false
	for _, character := range strings.TrimSpace(value) {
		allowed := unicode.IsLetter(character) || unicode.IsDigit(character) || strings.ContainsRune("-_.", character)
		if allowed {
			builder.WriteRune(character)
			lastSeparator = false
		} else if !lastSeparator {
			builder.WriteByte('-')
			lastSeparator = true
		}
	}
	safe := strings.Trim(builder.String(), ".-_")
	return boundedSDKOpenAPIFileSegment(safe, value, maxBytes)
}

// boundedSDKOpenAPIFileSegment truncates on a rune boundary and adds a stable collision-resistant suffix.
func boundedSDKOpenAPIFileSegment(safe, original string, maxBytes int) string {
	if len(safe) <= maxBytes {
		return safe
	}
	digest := sha256.Sum256([]byte(original))
	suffix := fmt.Sprintf("-%x", digest[:4])
	limit := maxBytes - len(suffix)
	for limit > 0 && !utf8.ValidString(safe[:limit]) {
		limit--
	}
	return strings.Trim(safe[:limit], ".-_") + suffix
}

// newSDKOpenAPIOutput creates secret-free metadata and hashes the final rendered document bytes.
func newSDKOpenAPIOutput(target sdkDownloadTarget, appID string, options sdkOpenAPIOptions, path, serverURL string, document []byte, operationCount int, directAPI bool) sdkOpenAPIOutput {
	output := sdkOpenAPIOutput{
		VersionID: appID, Operation: options.Operation, Format: options.Format,
		Operations: operationCount, Path: filepath.Clean(path), Bytes: len(document),
		SHA256: sdkOpenAPIDocumentHash(document), ServerURL: serverURL, Status: "completed",
	}
	// Name/version metadata uses the public API noun for direct REST exports.
	if target.Version != "" {
		// Direct API structured output must use its public resource noun.
		if directAPI {
			output.API = target.Name
		} else {
			// Generated SDK compatibility retains the existing sdk metadata field.
			output.SDK = target.Name
		}
		output.Version = target.Version
	}
	return output
}

// sdkOpenAPIDocumentHash returns the canonical lowercase digest label for the exact written bytes.
func sdkOpenAPIDocumentHash(document []byte) string {
	digest := sha256.Sum256(document)
	return fmt.Sprintf("sha256:%x", digest[:])
}

// writeSDKOpenAPIOutput reports only metadata because the document is always file-backed.
func writeSDKOpenAPIOutput(cmd *cobra.Command, output sdkOpenAPIOutput) error {
	if wantsJSON(cmd) {
		return writeJSON(cmd, output)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s OpenAPI document to %s (%d operations, %d bytes, %s)\n", output.Format, output.Path, output.Operations, output.Bytes, output.SHA256)
	return err
}
