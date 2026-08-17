package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Usefused/cli/internal/enginegrpc"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	defaultSDKTokenEnvironment = "FUSED_SDK_TOKEN"
	maxSDKInvokeInputBytes     = 16 << 20
)

var (
	sdkInvokeGRPCURL        string
	sdkInvokeParams         string
	sdkInvokeTokenEnv       string
	sdkInvokeTokenStdin     bool
	sdkInvokeEnvironment    string
	sdkInvokeIdempotencyKey string
)

var sdkInvokeCmd = &cobra.Command{
	Use:   "invoke <sdk-name@version-or-version-id> <operation>",
	Short: "Invoke one operation through the SDK execution transport",
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

type sdkInvokeOutput struct {
	AppID        string  `json:"app_id"`
	Operation    string  `json:"operation"`
	StatusCode   int32   `json:"status_code,omitempty"`
	Results      []any   `json:"results"`
	ElapsedMS    float64 `json:"elapsed_ms"`
	GRPCEndpoint string  `json:"grpc_endpoint"`
}

type sdkInvokeError struct {
	code     string
	message  string
	category string
	details  map[string]any
	cause    error
}

// Error returns the bounded Engine or transport message for an SDK invocation.
func (err *sdkInvokeError) Error() string { return err.message }

// Unwrap exposes the underlying gRPC failure when one exists.
func (err *sdkInvokeError) Unwrap() error { return err.cause }

// init registers the exact-version SDK invocation command and runtime flags.
func init() {
	sdkCmd.AddCommand(sdkInvokeCmd)
	addJSONOutputFlag(sdkInvokeCmd)
	sdkInvokeCmd.Flags().StringVar(&sdkInvokeGRPCURL, "grpc-url", "", "SDK execution gRPC URL (or FUSED_ENGINE_GRPC_URL)")
	sdkInvokeCmd.Flags().StringVar(&sdkInvokeParams, "params", "{}", "JSON object, @file, or - for stdin")
	sdkInvokeCmd.Flags().StringVar(&sdkInvokeTokenEnv, "token-env", defaultSDKTokenEnvironment, "Environment variable containing the SDK execution token")
	sdkInvokeCmd.Flags().BoolVar(&sdkInvokeTokenStdin, "token-stdin", false, "Read the SDK execution token from stdin")
	sdkInvokeCmd.Flags().StringVar(&sdkInvokeEnvironment, "environment", "", "Named provider environment selector")
	sdkInvokeCmd.Flags().StringVar(&sdkInvokeIdempotencyKey, "idempotency-key", "", "Stable logical-request idempotency key (generated when omitted)")
}

// runSDKInvoke performs one measured SDK execution and renders its result.
func runSDKInvoke(cmd *cobra.Command, target sdkDownloadTarget, operation string) error {
	operation = strings.TrimSpace(operation)
	if operation == "" {
		return errors.New("operation is required")
	}
	grpcURL, appID, token, params, err := prepareSDKInvocation(cmd, target)
	if err != nil {
		return err
	}
	started := time.Now()
	results, statusCode, err := executeSDKInvocation(cmd.Context(), grpcURL, appID, token, operation, params)
	if err != nil {
		return err
	}
	output := sdkInvokeOutput{
		AppID: appID, Operation: operation, StatusCode: statusCode,
		Results: results, ElapsedMS: float64(time.Since(started).Microseconds()) / 1000,
		GRPCEndpoint: grpcURL,
	}
	return writeSDKInvocationOutput(cmd, output)
}

// prepareSDKInvocation resolves inputs, runtime authentication, transport, and app identity.
func prepareSDKInvocation(cmd *cobra.Command, target sdkDownloadTarget) (string, string, string, []byte, error) {
	params, err := readSDKInvokeParams(sdkInvokeParams, sdkInvokeTokenStdin, cmd.InOrStdin())
	if err != nil {
		return "", "", "", nil, err
	}
	token, err := readSDKInvokeToken(cmd.InOrStdin())
	if err != nil {
		return "", "", "", nil, err
	}
	grpcURL, err := resolveSDKInvokeGRPCURL()
	if err != nil {
		return "", "", "", nil, err
	}
	client, err := getAPIClient()
	if err != nil {
		return "", "", "", nil, err
	}
	appID, err := client.ResolveSDKAppReference(target.Name, target.Version)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("resolve SDK version: %w", err)
	}
	return grpcURL, appID, token, params, nil
}

// writeSDKInvocationOutput renders invocation results without exposing the execution token.
func writeSDKInvocationOutput(cmd *cobra.Command, output sdkInvokeOutput) error {
	if wantsJSON(cmd) {
		return writeJSON(cmd, output)
	}
	for _, result := range output.Results {
		encoded, marshalErr := json.MarshalIndent(result, "", "  ")
		if marshalErr != nil {
			return marshalErr
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Status: %d\nElapsed: %.2f ms\n", output.StatusCode, output.ElapsedMS)
	return nil
}

// readSDKInvokeParams loads and validates exactly one JSON object for execution.
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
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, fmt.Errorf("--params must contain one JSON object: %w", err)
	}
	if object == nil {
		return nil, errors.New("--params must contain one JSON object")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, errors.New("--params must contain exactly one JSON object")
	}
	return data, nil
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
	name := strings.TrimSpace(sdkInvokeTokenEnv)
	if name == "" {
		return "", errors.New("--token-env must name an environment variable")
	}
	if token := strings.TrimSpace(os.Getenv(name)); token != "" {
		return token, nil
	}
	return "", fmt.Errorf("SDK execution token is not set in %s; use --token-env or --token-stdin", name)
}

// resolveSDKInvokeGRPCURL selects the explicit CLI or environment gRPC endpoint.
func resolveSDKInvokeGRPCURL() (string, error) {
	raw := strings.TrimSpace(sdkInvokeGRPCURL)
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("FUSED_ENGINE_GRPC_URL"))
	}
	if raw == "" {
		return "", errors.New("SDK gRPC URL is not configured; pass --grpc-url or set FUSED_ENGINE_GRPC_URL")
	}
	return validateSDKInvokeGRPCURL(raw)
}

// validateSDKInvokeGRPCURL validates a credential-free root gRPC URL.
func validateSDKInvokeGRPCURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("SDK gRPC URL must be an absolute http or https URL without credentials, query, or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("SDK gRPC URL must not contain a path")
	}
	parsed.Path = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

// executeSDKInvocation opens the canonical SDK session and performs one Execute call.
func executeSDKInvocation(ctx context.Context, grpcURL, appID, token, operation string, params []byte) ([]any, int32, error) {
	target, transportCredentials, err := sdkGRPCTarget(grpcURL)
	if err != nil {
		return nil, 0, err
	}
	connection, err := grpc.NewClient(target, grpc.WithTransportCredentials(transportCredentials))
	if err != nil {
		return nil, 0, &sdkInvokeError{code: "sdk_connect_failed", message: "could not create the SDK gRPC client", category: "dependency", cause: err}
	}
	defer connection.Close()
	client := enginegrpc.NewEngineServiceClient(connection)
	authContext := metadata.AppendToOutgoingContext(ctx, "x-app-id", appID, "x-api-key", token)
	if _, err := client.Connect(authContext, &enginegrpc.ConnectRequest{AppId: appID, Token: token}); err != nil {
		return nil, 0, sdkGRPCError("connect", err)
	}
	defer disconnectSDKInvocation(client, appID, token)
	hash := sha256.Sum256(params)
	idempotencyKey := strings.TrimSpace(sdkInvokeIdempotencyKey)
	if idempotencyKey == "" {
		idempotencyKey = uuid.NewString()
	}
	stream, err := client.Execute(authContext, &enginegrpc.ExecuteRequest{
		AppId: appID, Token: token, EndpointName: operation, Params: params,
		Environment: strings.TrimSpace(sdkInvokeEnvironment), IdempotencyKey: idempotencyKey,
		RequestBodyHash: hex.EncodeToString(hash[:]),
	})
	if err != nil {
		return nil, 0, sdkGRPCError("execute", err)
	}
	return receiveSDKInvocation(stream)
}

// receiveSDKInvocation collects streamed result frames and the provider status.
func receiveSDKInvocation(stream enginegrpc.EngineService_ExecuteClient) ([]any, int32, error) {
	results := make([]any, 0, 1)
	var statusCode int32
	for {
		frame, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			return nil, statusCode, sdkGRPCError("execute", recvErr)
		}
		if frame.StatusCode != 0 {
			statusCode = frame.StatusCode
		}
		if frame.Error != "" {
			return nil, statusCode, sdkRuntimeFrameError(frame.Error, statusCode)
		}
		if len(frame.Result) > 0 {
			results = append(results, decodeSDKInvokeResult(frame.Result))
		}
	}
	return results, statusCode, nil
}

// sdkGRPCTarget converts the configured URL into a gRPC target and transport policy.
func sdkGRPCTarget(raw string) (string, credentials.TransportCredentials, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", nil, err
	}
	if parsed.Scheme == "http" {
		return parsed.Host, insecure.NewCredentials(), nil
	}
	return parsed.Host, credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: parsed.Hostname()}), nil
}

// disconnectSDKInvocation releases Engine session cache references on command exit.
func disconnectSDKInvocation(client enginegrpc.EngineServiceClient, appID, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "x-app-id", appID, "x-api-key", token)
	_, _ = client.Disconnect(ctx, &enginegrpc.DisconnectRequest{AppId: appID})
}

// sdkGRPCError converts a gRPC status into the stable CLI execution error contract.
func sdkGRPCError(stage string, err error) error {
	grpcStatus, _ := status.FromError(err)
	return &sdkInvokeError{
		code: "sdk_" + stage + "_failed", message: grpcStatus.Message(), category: "execution", cause: err,
		details: map[string]any{"stage": stage, "grpc_code": grpcStatus.Code().String()},
	}
}

// sdkRuntimeFrameError preserves a structured Engine runtime error when available.
func sdkRuntimeFrameError(raw string, statusCode int32) error {
	code, message := "sdk_execution_failed", strings.TrimSpace(raw)
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(raw), &payload) == nil {
		if strings.TrimSpace(payload.Code) != "" {
			code = payload.Code
		}
		if strings.TrimSpace(payload.Message) != "" {
			message = payload.Message
		}
	}
	if message == "" {
		message = "SDK execution failed"
	}
	return &sdkInvokeError{
		code: code, message: message, category: "execution",
		details: map[string]any{"stage": "execute", "provider_http_status": statusCode},
	}
}

// decodeSDKInvokeResult decodes JSON result frames and preserves non-JSON bytes as text.
func decodeSDKInvokeResult(raw []byte) any {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) == nil && ensureJSONEOF(decoder) == nil {
		return value
	}
	return string(raw)
}
