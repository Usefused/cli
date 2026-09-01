package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const (
	defaultSDKTokenEnvironment = "FUSED_SDK_TOKEN"
	maxSDKInvokeInputBytes     = 1 << 20
	maxSDKInvokeSelectorBytes  = 256
	maxSDKInvokeTargets        = 16
	maxSDKInvokeResponseBytes  = (maxSDKInvokeTargets + 1) * maxSDKInvokeInputBytes
)

var (
	sdkInvokeParams         string
	sdkInvokeTokenEnv       string
	sdkInvokeTokenStdin     bool
	sdkInvokeEnvironment    string
	sdkInvokeIdempotencyKey string
	sdkInvokeTargets        []string
	sdkInvokeSelector       string
	sdkInvokeSelectors      string
)

var sdkInvokeCmd = &cobra.Command{
	Use:   "invoke <sdk-name@version-or-version-id> <operation>",
	Short: "Invoke one JSON operation through the Engine execution API",
	Args: func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(2)(cmd, args); err != nil {
			return err
		}
		return validateExactAppReference(args[0], "sdk invoke")
	},
	RunE: WithTelemetry("cli.sdk.invoke", func(cmd *cobra.Command, args []string) error {
		return runSDKInvoke(cmd, downloadTargetFromName(args[0]), args[1])
	}),
}

type sdkInvokeSelectorValue struct {
	Environment string `json:"environment,omitempty"`
	EndUserRef  string `json:"end_user_ref,omitempty"`
	AuthType    string `json:"auth_type,omitempty"`
	AuthName    string `json:"auth_name,omitempty"`
	ResourceID  string `json:"resource_id,omitempty"`
}

type sdkInvokeRequest struct {
	Operation string                            `json:"operation"`
	Input     json.RawMessage                   `json:"input"`
	Targets   []string                          `json:"targets,omitempty"`
	Selector  *sdkInvokeSelectorValue           `json:"selector,omitempty"`
	Selectors map[string]sdkInvokeSelectorValue `json:"selectors,omitempty"`
}

type preparedSDKInvocation struct {
	EngineURL      string
	AppID          string
	Token          string
	IdempotencyKey string
	Request        sdkInvokeRequest
}

type sdkInvokeHTTPResponse struct {
	AppID      string `json:"app_id"`
	Operation  string `json:"operation"`
	Kind       string `json:"kind"`
	StatusCode int    `json:"status_code,omitempty"`
	Results    []any  `json:"results"`
	Rollbacks  []any  `json:"rollbacks,omitempty"`
}

type sdkInvokeOutput struct {
	AppID          string  `json:"app_id"`
	Operation      string  `json:"operation"`
	Kind           string  `json:"kind"`
	StatusCode     int     `json:"status_code,omitempty"`
	Results        []any   `json:"results"`
	Rollbacks      *[]any  `json:"rollbacks,omitempty"`
	ElapsedMS      float64 `json:"elapsed_ms"`
	EngineEndpoint string  `json:"engine_endpoint"`
}

type sdkInvokeError struct {
	code     string
	message  string
	category string
	details  map[string]any
	cause    error
}

type sdkInvokeErrorEnvelope struct {
	Error sdkInvokeErrorResponse `json:"error"`
}

type sdkInvokeErrorResponse struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Error returns the bounded Engine or transport message and reconstructs only
// a validated value-free credential command from structured routing details.
func (err *sdkInvokeError) Error() string {
	command := sdkInvokeCredentialCommand(err.code, err.details)
	// Other failures retain their existing compact message without trusting arbitrary detail as terminal text.
	if command == "" {
		return err.message
	}
	return err.message + "\nRun: " + command
}

// sdkInvokeCredentialCommand validates UUID and auth metadata before producing
// the copyable CLI remediation instead of trusting a server-supplied shell string.
func sdkInvokeCredentialCommand(code string, details map[string]any) string {
	// Only the dedicated runtime code authorizes this command surface.
	if code != "bucket_credentials_missing" {
		return ""
	}
	serviceID, serviceOK := details["service_id"].(string)
	bucketID, bucketOK := details["bucket_id"].(string)
	authType, typeOK := details["auth_type"].(string)
	authName, nameOK := details["auth_name"].(string)
	_, serviceErr := uuid.Parse(serviceID)
	_, bucketErr := uuid.Parse(bucketID)
	// Closed identity and auth-family checks prevent malformed response metadata from becoming shell guidance.
	if !serviceOK || !bucketOK || !typeOK || !nameOK || serviceErr != nil || bucketErr != nil || !sdkInvokeStaticAuthType(authType) || invalidSDKInvokeAuthName(authName) {
		return ""
	}
	return fmt.Sprintf("fused-cli secret set %s --bucket %s --type %s --auth-name %s --interactive",
		serviceID, bucketID, shellQuoteSDKInvokeArgument(authType), shellQuoteSDKInvokeArgument(authName))
}

// sdkInvokeStaticAuthType admits only app-configurable static families whose
// values can be collected by the shared secret set command.
func sdkInvokeStaticAuthType(value string) bool {
	switch value {
	case "api_key", "bearer", "basic", "mtls":
		return true
	default:
		return false
	}
}

// invalidSDKInvokeAuthName rejects control text and excessive metadata before
// the remaining value is safely quoted as one shell word.
func invalidSDKInvokeAuthName(value string) bool {
	trimmed := strings.TrimSpace(value)
	// Empty, changed-by-trimming, or control-bearing names cannot identify the exact imported family safely.
	return trimmed == "" || trimmed != value || len(value) > 256 || strings.ContainsAny(value, "\r\n\x00")
}

// shellQuoteSDKInvokeArgument produces one POSIX shell word without permitting
// substitutions, separators, or additional flags from imported metadata.
func shellQuoteSDKInvokeArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// Unwrap exposes the underlying HTTP or context failure when one exists.
func (err *sdkInvokeError) Unwrap() error { return err.cause }

// init registers exact-version REST execution and its credential-safe flags.
func init() {
	sdkCmd.AddCommand(sdkInvokeCmd)
	addJSONOutputFlag(sdkInvokeCmd)
	sdkInvokeCmd.Flags().StringVar(&sdkInvokeParams, "params", "{}", "JSON value, @file, or - for stdin (physical operations require an object)")
	sdkInvokeCmd.Flags().StringVar(&sdkInvokeTokenEnv, "token-env", defaultSDKTokenEnvironment, "Environment variable containing the SDK execution token")
	sdkInvokeCmd.Flags().BoolVar(&sdkInvokeTokenStdin, "token-stdin", false, "Read the SDK execution token from stdin")
	sdkInvokeCmd.Flags().StringVar(&sdkInvokeEnvironment, "environment", "", "Physical operation environment selector")
	sdkInvokeCmd.Flags().StringVar(&sdkInvokeIdempotencyKey, "idempotency-key", "", "Stable logical-request idempotency key (generated when omitted)")
	sdkInvokeCmd.Flags().StringArrayVar(&sdkInvokeTargets, "target", nil, "Unified target to execute (required 1-16 times; values must be unique)")
	sdkInvokeCmd.Flags().StringVar(&sdkInvokeSelector, "selector", "", "Physical execution selector as a strict JSON object or @file")
	sdkInvokeCmd.Flags().StringVar(&sdkInvokeSelectors, "selectors", "", "Unified service selectors as a strict target-keyed JSON object or @file")
}

// runSDKInvoke performs one measured REST execution and renders its inferred shape.
func runSDKInvoke(cmd *cobra.Command, target sdkDownloadTarget, operation string) error {
	operation = strings.TrimSpace(operation)
	if operation == "" {
		return errors.New("operation is required")
	}
	prepared, err := prepareSDKInvocation(cmd, target, operation)
	if err != nil {
		return err
	}
	started := time.Now()
	response, endpoint, err := executeSDKInvocation(cmd.Context(), prepared)
	if err != nil {
		return err
	}
	var rollbacks *[]any
	if response.Kind == "unified" {
		// Why: Unified output must preserve an explicit empty rollback array while physical output omits the field.
		rollbacks = &response.Rollbacks
	}
	output := sdkInvokeOutput{
		AppID: prepared.AppID, Operation: operation, Kind: response.Kind,
		StatusCode: response.StatusCode, Results: response.Results, Rollbacks: rollbacks,
		ElapsedMS: float64(time.Since(started).Microseconds()) / 1000, EngineEndpoint: endpoint,
	}
	return writeSDKInvocationOutput(cmd, output)
}

// prepareSDKInvocation resolves exact app identity separately from the execution-token REST request.
func prepareSDKInvocation(cmd *cobra.Command, target sdkDownloadTarget, operation string) (preparedSDKInvocation, error) {
	request, err := buildSDKInvokeRequest(operation, cmd.InOrStdin())
	if err != nil {
		return preparedSDKInvocation{}, err
	}
	token, err := readSDKInvokeToken(cmd.InOrStdin())
	if err != nil {
		return preparedSDKInvocation{}, err
	}
	engineURL, err := resolveSDKInvokeEngineURL()
	if err != nil {
		return preparedSDKInvocation{}, err
	}
	client, err := getAPIClient()
	if err != nil {
		return preparedSDKInvocation{}, err
	}
	appID, err := client.ResolveSDKAppReference(target.Name, target.Version)
	if err != nil {
		return preparedSDKInvocation{}, fmt.Errorf("resolve SDK version: %w", err)
	}
	return preparedSDKInvocation{
		EngineURL: engineURL, AppID: appID, Token: token,
		IdempotencyKey: effectiveSDKInvokeIdempotencyKey(), Request: request,
	}, nil
}

// buildSDKInvokeRequest admits only the public execution fields supported by Engine REST.
func buildSDKInvokeRequest(operation string, stdin io.Reader) (sdkInvokeRequest, error) {
	input, err := readSDKInvokeParams(sdkInvokeParams, sdkInvokeTokenStdin, stdin)
	if err != nil {
		return sdkInvokeRequest{}, err
	}
	targets, err := normalizedSDKInvokeTargets(sdkInvokeTargets)
	if err != nil {
		return sdkInvokeRequest{}, err
	}
	selector, err := readSDKInvokeSelector(sdkInvokeSelector)
	if err != nil {
		return sdkInvokeRequest{}, err
	}
	selectors, err := readSDKInvokeSelectors(sdkInvokeSelectors)
	if err != nil {
		return sdkInvokeRequest{}, err
	}
	selector, err = mergeSDKInvokeEnvironment(selector, selectors, sdkInvokeEnvironment)
	if err != nil {
		return sdkInvokeRequest{}, err
	}
	if selector != nil && len(selectors) > 0 {
		// Why: physical and Unified selectors are disjoint namespaces; sending both would make kind inference ambiguous.
		return sdkInvokeRequest{}, errors.New("--selector cannot be combined with --selectors")
	}
	return sdkInvokeRequest{Operation: operation, Input: input, Targets: targets, Selector: selector, Selectors: selectors}, nil
}

// normalizedSDKInvokeTargets trims repeatable target flags and rejects duplicate graph steps.
func normalizedSDKInvokeTargets(values []string) ([]string, error) {
	if len(values) > maxSDKInvokeTargets {
		return nil, fmt.Errorf("at most %d --target values are allowed", maxSDKInvokeTargets)
	}
	seen := make(map[string]struct{}, len(values))
	targets := make([]string, 0, len(values))
	for _, value := range values {
		target := strings.TrimSpace(value)
		if target == "" {
			return nil, errors.New("--target cannot be empty")
		}
		if _, exists := seen[target]; exists {
			return nil, fmt.Errorf("--target %q is duplicated", target)
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}
	return targets, nil
}

// readSDKInvokeSelector decodes the closed physical selector vocabulary.
func readSDKInvokeSelector(raw string) (*sdkInvokeSelectorValue, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	data, err := loadSDKInvokeJSONOption(raw)
	if err != nil {
		return nil, fmt.Errorf("read --selector: %w", err)
	}
	var selector sdkInvokeSelectorValue
	if err := decodeStrictSDKInvokeJSON(data, &selector); err != nil {
		return nil, fmt.Errorf("--selector must contain one safe selector object: %w", err)
	}
	if err := validateSDKInvokeSelector(selector); err != nil {
		return nil, err
	}
	return &selector, nil
}

// readSDKInvokeSelectors decodes Unified selectors keyed only by public service target.
func readSDKInvokeSelectors(raw string) (map[string]sdkInvokeSelectorValue, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	data, err := loadSDKInvokeJSONOption(raw)
	if err != nil {
		return nil, fmt.Errorf("read --selectors: %w", err)
	}
	var selectors map[string]sdkInvokeSelectorValue
	if err := decodeStrictSDKInvokeJSON(data, &selectors); err != nil || selectors == nil {
		return nil, errors.New("--selectors must contain one strict target-keyed selector object")
	}
	for target, selector := range selectors {
		if strings.TrimSpace(target) == "" {
			return nil, errors.New("--selectors target cannot be empty")
		}
		if err := validateSDKInvokeSelector(selector); err != nil {
			return nil, fmt.Errorf("--selectors target %q: %w", target, err)
		}
	}
	return selectors, nil
}

// validateSDKInvokeSelector bounds every non-secret routing selector before transport.
func validateSDKInvokeSelector(selector sdkInvokeSelectorValue) error {
	values := []string{selector.Environment, selector.EndUserRef, selector.AuthName, selector.ResourceID}
	for _, value := range values {
		if len(value) > maxSDKInvokeSelectorBytes {
			return fmt.Errorf("selector values cannot exceed %d bytes", maxSDKInvokeSelectorBytes)
		}
	}
	if selector.AuthType == "" {
		return nil
	}
	for _, allowed := range []string{"basic", "bearer", "api_key", "oauth", "oidc", "mtls"} {
		if selector.AuthType == allowed {
			return nil
		}
	}
	return errors.New("selector auth_type is unsupported")
}

// mergeSDKInvokeEnvironment preserves --environment as physical selector sugar without overriding JSON.
func mergeSDKInvokeEnvironment(selector *sdkInvokeSelectorValue, selectors map[string]sdkInvokeSelectorValue, raw string) (*sdkInvokeSelectorValue, error) {
	environment := strings.TrimSpace(raw)
	if environment == "" {
		return selector, nil
	}
	if len(selectors) > 0 {
		return nil, errors.New("--environment is physical selector sugar and cannot be combined with --selectors")
	}
	if len(environment) > maxSDKInvokeSelectorBytes {
		return nil, fmt.Errorf("--environment cannot exceed %d bytes", maxSDKInvokeSelectorBytes)
	}
	if selector == nil {
		return &sdkInvokeSelectorValue{Environment: environment}, nil
	}
	if selector.Environment != "" && selector.Environment != environment {
		return nil, errors.New("--environment conflicts with --selector environment")
	}
	selector.Environment = environment
	return selector, nil
}

// loadSDKInvokeJSONOption reads an inline object or bounded @file without claiming stdin.
func loadSDKInvokeJSONOption(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "@") {
		return []byte(raw), nil
	}
	file, err := os.Open(strings.TrimPrefix(raw, "@"))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readBoundedSDKInvokeInput(file)
}

// decodeStrictSDKInvokeJSON rejects unknown fields and trailing JSON values.
func decodeStrictSDKInvokeJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	// Why: provider identifiers and counters may exceed IEEE-754 precision and must survive CLI JSON round trips.
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

// effectiveSDKInvokeIdempotencyKey generates one header value when the caller omitted it.
func effectiveSDKInvokeIdempotencyKey() string {
	if value := strings.TrimSpace(sdkInvokeIdempotencyKey); value != "" {
		return value
	}
	return uuid.NewString()
}

// resolveSDKInvokeEngineURL selects and validates the global Engine REST endpoint.
func resolveSDKInvokeEngineURL() (string, error) {
	raw, err := GetEngineURL()
	if err != nil {
		return "", err
	}
	return validateSDKInvokeEngineURL(raw)
}

// validateSDKInvokeEngineURL rejects authority credentials and request-specific URL components.
func validateSDKInvokeEngineURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("Engine URL must be an absolute http or https URL without credentials, query, or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
	return strings.TrimRight(parsed.String(), "/"), nil
}

// sdkInvokeEndpoint appends the exact app execution route to the configured Engine base.
func sdkInvokeEndpoint(engineURL, appID string) string {
	return strings.TrimRight(engineURL, "/") + "/v1/apps/" + url.PathEscape(appID) + "/executions"
}

// executeSDKInvocation sends the runtime token only as Bearer authorization to Engine REST.
func executeSDKInvocation(ctx context.Context, prepared preparedSDKInvocation) (sdkInvokeHTTPResponse, string, error) {
	if ctx == nil {
		// Why: direct command helpers and tests may not have passed through Cobra's Execute context initialization.
		ctx = context.Background()
	}
	request, endpoint, err := newSDKInvokeHTTPRequest(ctx, prepared)
	if err != nil {
		return sdkInvokeHTTPResponse{}, endpoint, err
	}
	client := &http.Client{Timeout: RequestTimeout, CheckRedirect: rejectSDKInvokeRedirect}
	response, err := client.Do(request)
	if err != nil {
		return sdkInvokeHTTPResponse{}, endpoint, sdkInvokeTransportError(err)
	}
	defer response.Body.Close()
	responseBody, err := readBoundedSDKInvokeResponse(response.Body)
	if err != nil {
		return sdkInvokeHTTPResponse{}, endpoint, err
	}
	decoded, err := decodeSDKInvokeHTTPResult(response.StatusCode, responseBody, prepared)
	return decoded, endpoint, err
}

// newSDKInvokeHTTPRequest builds the approved route and its execution-only headers.
func newSDKInvokeHTTPRequest(ctx context.Context, prepared preparedSDKInvocation) (*http.Request, string, error) {
	body, err := json.Marshal(prepared.Request)
	if err != nil {
		return nil, "", err
	}
	endpoint := sdkInvokeEndpoint(prepared.EngineURL, prepared.AppID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, endpoint, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+prepared.Token)
	request.Header.Set("Idempotency-Key", prepared.IdempotencyKey)
	if RequestID != "" {
		request.Header.Set("X-Request-ID", RequestID)
	}
	return request, endpoint, nil
}

// sdkInvokeTransportError hides transport implementation detail behind one stable CLI error.
func sdkInvokeTransportError(cause error) error {
	return &sdkInvokeError{
		code: "sdk_execution_request_failed", message: "could not reach the Engine execution endpoint",
		category: "dependency", cause: cause, details: map[string]any{"stage": "execute"},
	}
}

// decodeSDKInvokeHTTPResult selects the reviewed success or error decoder by status.
func decodeSDKInvokeHTTPResult(statusCode int, body []byte, prepared preparedSDKInvocation) (sdkInvokeHTTPResponse, error) {
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return sdkInvokeHTTPResponse{}, decodeSDKInvokeHTTPError(statusCode, body)
	}
	decoded, err := decodeSDKInvokeHTTPResponse(body)
	if err != nil {
		return sdkInvokeHTTPResponse{}, err
	}
	if decoded.AppID != prepared.AppID || decoded.Operation != prepared.Request.Operation {
		return sdkInvokeHTTPResponse{}, invalidSDKInvokeHTTPResponse("Engine returned mismatched execution identity", nil)
	}
	return decoded, nil
}

// rejectSDKInvokeRedirect prevents a family execution token from crossing to another route or origin.
func rejectSDKInvokeRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

// readBoundedSDKInvokeResponse prevents an Engine or proxy response from exhausting CLI memory.
func readBoundedSDKInvokeResponse(reader io.Reader) ([]byte, error) {
	// Why: Unified can aggregate one bounded JSON result per target plus its envelope, while input remains capped at 1 MiB.
	data, err := io.ReadAll(io.LimitReader(reader, maxSDKInvokeResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSDKInvokeResponseBytes {
		return nil, errors.New("Engine execution response is too large")
	}
	return data, nil
}

// decodeSDKInvokeHTTPResponse validates the inferred physical or Unified success envelope.
func decodeSDKInvokeHTTPResponse(data []byte) (sdkInvokeHTTPResponse, error) {
	var response sdkInvokeHTTPResponse
	if err := decodeStrictSDKInvokeJSON(data, &response); err != nil {
		return sdkInvokeHTTPResponse{}, invalidSDKInvokeHTTPResponse("Engine returned an invalid execution response", err)
	}
	if _, err := uuid.Parse(response.AppID); err != nil || strings.TrimSpace(response.Operation) == "" || response.Results == nil {
		return sdkInvokeHTTPResponse{}, invalidSDKInvokeHTTPResponse("Engine returned an incomplete execution response", nil)
	}
	switch response.Kind {
	case "physical":
		return response, validateSDKInvokePhysicalResponse(response)
	case "unified":
		return response, validateSDKInvokeUnifiedResponse(response)
	default:
		return sdkInvokeHTTPResponse{}, invalidSDKInvokeHTTPResponse("Engine returned an unknown execution kind", nil)
	}
}

// validateSDKInvokePhysicalResponse requires one successful JSON provider document and no rollback field.
func validateSDKInvokePhysicalResponse(response sdkInvokeHTTPResponse) error {
	if response.StatusCode == 0 || len(response.Results) != 1 || response.Rollbacks != nil {
		return invalidSDKInvokeHTTPResponse("Engine returned an invalid physical execution response", nil)
	}
	return nil
}

// validateSDKInvokeUnifiedResponse requires ordered results and an explicit rollback array without physical status.
func validateSDKInvokeUnifiedResponse(response sdkInvokeHTTPResponse) error {
	if response.Rollbacks == nil || response.StatusCode != 0 {
		return invalidSDKInvokeHTTPResponse("Engine returned an invalid Unified execution response", nil)
	}
	return nil
}

// invalidSDKInvokeHTTPResponse creates one stable error for every malformed success shape.
func invalidSDKInvokeHTTPResponse(message string, cause error) error {
	return &sdkInvokeError{
		code: "sdk_execution_response_invalid", message: message,
		category: "dependency", cause: cause, details: map[string]any{"stage": "decode"},
	}
}

// decodeSDKInvokeHTTPError preserves only the reviewed structured Engine error contract.
func decodeSDKInvokeHTTPError(statusCode int, data []byte) error {
	if statusCode == http.StatusUnauthorized {
		// Why: auth failures stay indistinguishable even if an intermediary returns app-specific text.
		return genericSDKInvokeHTTPError(statusCode)
	}
	var envelope sdkInvokeErrorEnvelope
	if decodeStrictSDKInvokeJSON(data, &envelope) != nil || strings.TrimSpace(envelope.Error.Code) == "" || strings.TrimSpace(envelope.Error.Message) == "" {
		return genericSDKInvokeHTTPError(statusCode)
	}
	details := copySDKInvokeErrorDetails(envelope.Error.Details)
	details["http_status"] = statusCode
	return &sdkInvokeError{
		code: envelope.Error.Code, message: envelope.Error.Message,
		category: sdkInvokeHTTPErrorCategory(statusCode), details: details,
	}
}

// sdkInvokeHTTPErrorCategory derives the stable CLI category from the authoritative HTTP status.
func sdkInvokeHTTPErrorCategory(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return "validation"
	case http.StatusUnauthorized:
		return "authentication"
	case http.StatusForbidden:
		return "authorization"
	case http.StatusConflict:
		return "conflict"
	default:
		if statusCode >= http.StatusInternalServerError {
			return "dependency"
		}
		return "execution"
	}
}

// copySDKInvokeErrorDetails prevents later decoder reuse from mutating the surfaced details map.
func copySDKInvokeErrorDetails(source map[string]any) map[string]any {
	copied := make(map[string]any, len(source)+1)
	for key, value := range source {
		copied[key] = value
	}
	return copied
}

// genericSDKInvokeHTTPError maps untrusted or malformed error bodies to local stable diagnostics.
func genericSDKInvokeHTTPError(statusCode int) error {
	code, message, category := "sdk_execution_failed", "Engine rejected the SDK execution", "execution"
	if statusCode == http.StatusUnauthorized {
		code, message, category = "sdk_authentication_failed", "SDK execution token was rejected", "authentication"
	}
	if statusCode == http.StatusForbidden {
		code, message, category = "sdk_authorization_failed", "SDK execution is not allowed", "authorization"
	}
	if statusCode >= http.StatusInternalServerError {
		code, message, category = "sdk_engine_failed", "Engine could not complete the SDK execution", "dependency"
	}
	return &sdkInvokeError{code: code, message: message, category: category, details: map[string]any{"http_status": statusCode}}
}

// writeSDKInvocationOutput renders physical status or Unified rollbacks without exposing the execution token.
func writeSDKInvocationOutput(cmd *cobra.Command, output sdkInvokeOutput) error {
	if wantsJSON(cmd) {
		return writeJSON(cmd, output)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Kind: %s\nEngine: %s\n", output.Kind, output.EngineEndpoint)
	for _, result := range output.Results {
		if err := writeSDKInvokeValue(cmd.OutOrStdout(), result); err != nil {
			return err
		}
	}
	if output.Kind == "physical" {
		fmt.Fprintf(cmd.OutOrStdout(), "Status: %d\n", output.StatusCode)
	}
	if output.Rollbacks != nil {
		for _, rollback := range *output.Rollbacks {
			fmt.Fprint(cmd.OutOrStdout(), "Rollback: ")
			if err := writeSDKInvokeValue(cmd.OutOrStdout(), rollback); err != nil {
				return err
			}
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Elapsed: %.2f ms\n", output.ElapsedMS)
	return nil
}

// writeSDKInvokeValue renders one response value with deterministic JSON indentation.
func writeSDKInvokeValue(writer io.Writer, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(writer, string(encoded))
	return err
}

// readSDKInvokeParams loads one bounded, duplicate-free JSON value for execution.
func readSDKInvokeParams(raw string, tokenFromStdin bool, stdin io.Reader) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "-" && tokenFromStdin {
		return nil, errors.New("--params - cannot be combined with --token-stdin")
	}
	data, err := loadSDKInvokeParams(raw, stdin)
	if err != nil {
		return nil, fmt.Errorf("read invocation params: %w", err)
	}
	if len(data) > maxSDKInvokeInputBytes {
		return nil, fmt.Errorf("invocation params exceed %d bytes", maxSDKInvokeInputBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := validateSDKInvokeJSONValue(decoder); err != nil {
		return nil, fmt.Errorf("--params must contain one duplicate-free JSON value: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, errors.New("--params must contain exactly one JSON value")
	}
	return data, nil
}

// validateSDKInvokeJSONValue walks one JSON value so duplicate object keys cannot be hidden by map decoding.
func validateSDKInvokeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		return validateSDKInvokeJSONObject(decoder)
	case '[':
		return validateSDKInvokeJSONArray(decoder)
	default:
		return errors.New("unexpected JSON delimiter")
	}
}

// validateSDKInvokeJSONObject rejects duplicate keys while recursively validating nested values.
func validateSDKInvokeJSONObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("JSON object key must be a string")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate JSON object key %q", key)
		}
		seen[key] = struct{}{}
		if err := validateSDKInvokeJSONValue(decoder); err != nil {
			return err
		}
	}
	return consumeSDKInvokeJSONDelimiter(decoder, '}')
}

// validateSDKInvokeJSONArray recursively validates every array element without coercing its shape.
func validateSDKInvokeJSONArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := validateSDKInvokeJSONValue(decoder); err != nil {
			return err
		}
	}
	return consumeSDKInvokeJSONDelimiter(decoder, ']')
}

// consumeSDKInvokeJSONDelimiter proves a composite value ended with its expected delimiter.
func consumeSDKInvokeJSONDelimiter(decoder *json.Decoder, expected json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != expected {
		return errors.New("JSON composite ended unexpectedly")
	}
	return nil
}

// loadSDKInvokeParams reads inline, file-backed, or stdin-backed parameter bytes.
func loadSDKInvokeParams(raw string, stdin io.Reader) ([]byte, error) {
	var data []byte
	var err error
	switch {
	case raw == "-":
		data, err = readBoundedSDKInvokeInput(stdin)
	case strings.HasPrefix(raw, "@"):
		var file *os.File
		file, err = os.Open(strings.TrimPrefix(raw, "@"))
		if err == nil {
			defer file.Close()
			data, err = readBoundedSDKInvokeInput(file)
		}
	default:
		data = []byte(raw)
	}
	return data, err
}

// ensureJSONEOF rejects trailing values after a decoded JSON value.
func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON data")
	}
	return nil
}

// readBoundedSDKInvokeInput reads invocation input with a strict memory limit.
func readBoundedSDKInvokeInput(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxSDKInvokeInputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSDKInvokeInputBytes {
		return nil, fmt.Errorf("input exceeds %d bytes", maxSDKInvokeInputBytes)
	}
	return data, nil
}

// readSDKInvokeToken reads only the explicitly selected runtime token source.
func readSDKInvokeToken(stdin io.Reader) (string, error) {
	if sdkInvokeTokenStdin {
		return readSDKInvokeTokenFromStdin(stdin)
	}
	name := strings.TrimSpace(sdkInvokeTokenEnv)
	if name == "" {
		return "", errors.New("--token-env must name an environment variable")
	}
	if token := strings.TrimSpace(os.Getenv(name)); token != "" {
		return token, nil
	}
	return "", fmt.Errorf("SDK execution token is not set in %s; use --token-env or --token-stdin", name)
}

// readSDKInvokeTokenFromStdin bounds and validates one stdin execution token.
func readSDKInvokeTokenFromStdin(stdin io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(stdin, 4097))
	if err != nil {
		return "", fmt.Errorf("read SDK token: %w", err)
	}
	if len(data) > 4096 {
		return "", errors.New("SDK token input is too large")
	}
	if token := strings.TrimSpace(string(data)); token != "" {
		return token, nil
	}
	return "", errors.New("SDK execution token is empty")
}
