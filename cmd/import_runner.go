package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/Usefused/cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// defaultImportReceiptPath is deliberately a single fixed path, not one keyed
// per-service the way defaultReceiptPath(configKey) is for declarative
// configs: "import apply" always means "apply the most recent import plan",
// there's no directory of many import specs to disambiguate between.
const defaultImportReceiptPath = ".fused/.state/import.plan.json"

// defaultSpecImportTimeout gives bounded parser and set-based persistence work
// enough time without weakening the one-minute feedback loop for ordinary CLI
// reads. Large reviewed contracts can legitimately contain thousands of rows.
const defaultSpecImportTimeout = 20 * time.Minute

type importSpecPlanOptions struct {
	name               string
	slug               string
	url                string
	version            string
	destinationVersion string
	target             string
	// isPublic is nil when --public was not passed at all, distinct from an
	// explicit --public=false -- see import.go's flag registration.
	isPublic   *bool
	category   string
	overlay    string
	receiptOut string
	jsonOut    bool
	strict     bool
}

type importSpecApplyOptions struct {
	planID      string
	reviewHash  string
	receiptPath string
}

// importApplyOutcomeUnknownError distinguishes a lost response from a proven
// mutation failure. Import plans are one-shot, so callers must never use the
// generic retryable timeout classification to replay the same receipt.
type importApplyOutcomeUnknownError struct {
	cause       error
	timeout     time.Duration
	operationID string
	timedOut    bool
}

// Error explains both sides of the ambiguous boundary without exposing the
// Engine URL, credentials, source, or remote response body.
func (e *importApplyOutcomeUnknownError) Error() string {
	prefix := "import apply outcome is unknown."
	// Only a real deadline should claim elapsed timeout or suggest a larger future budget.
	if e.timedOut {
		prefix = fmt.Sprintf("import apply outcome is unknown after %s.", e.timeout)
	}
	return prefix + " " + e.remediation()
}

// Unwrap preserves deadline inspection for logs while command classification
// remains on the safer, non-retryable import-specific error.
func (e *importApplyOutcomeUnknownError) Unwrap() error {
	return e.cause
}

// remediation directs recovery through the durable read-only operation status
// because a blind mutation retry is unnecessary and may obscure the outcome.
func (e *importApplyOutcomeUnknownError) remediation() string {
	recovery := fmt.Sprintf("Check the durable outcome with `fused-cli import status %s`.", safeImportOperationID(e.operationID))
	// Timeout tuning is relevant only when a deadline, rather than reset or malformed proof, lost the response.
	if e.timedOut {
		recovery += fmt.Sprintf(" For future large imports, set `--timeout` above %s.", e.timeout)
	}
	return recovery
}

// safeImportOperationID prevents a locally edited receipt from injecting
// terminal or shell syntax into the exact recovery command.
func safeImportOperationID(value string) string {
	operationID, err := uuid.Parse(strings.TrimSpace(value))
	// Only canonical UUIDs are safe and useful in the status command.
	if err != nil {
		return "<operation-id>"
	}
	return operationID.String()
}

// importPlanReceipt mirrors planReceipt's shape (config_runner.go) for the
// same reason: a local, on-disk record of what was just planned, so a later
// "import apply" (possibly a different process/CI step) knows what plan_id
// and review_hash to send without re-planning.
type importPlanReceipt struct {
	Slug             string `json:"slug,omitempty"`
	PlanID           string `json:"plan_id"`
	ReviewHash       string `json:"review_hash"`
	SourceHash       string `json:"source_hash,omitempty"`
	OverlayHash      string `json:"overlay_hash,omitempty"`
	SourceBundleHash string `json:"source_bundle_hash,omitempty"`
	EngineURL        string `json:"engine_url,omitempty"`
	CreatedAt        string `json:"created_at,omitempty"`
}

// runImportPlan validates one immutable source request, records bounded audit
// dimensions, and persists the Registry-reviewed receipt without applying it.
func runImportPlan(cmd *cobra.Command, specArg string, opts importSpecPlanOptions) error {
	req, err := buildSpecImportRequest(specArg, opts)
	if err != nil {
		return err
	}
	trace.SpanFromContext(cmd.Context()).SetAttributes(
		attribute.String("target_type", displayImportTarget(req.TargetType)),
		attribute.Bool("destination_version_present", req.DestinationVersion != ""),
		attribute.Bool("strict_mode", req.Strict),
		attribute.Bool("overlay_present", req.OverlayContent != nil),
	)
	// Import parsing receives its own larger bounded default because source size,
	// not ordinary control-plane latency, determines the legitimate work here.
	client, err := getAPIClientWithTimeout(specImportTimeout(cmd))
	if err != nil {
		return err
	}
	resp, err := client.PlanSpecImport(req)
	if err != nil {
		var strictError *api.SpecImportStrictError
		if errors.As(err, &strictError) && !opts.jsonOut {
			printImportDiagnostics(cmd.ErrOrStderr(), strictError.Diagnostics)
		}
		return err
	}
	// Receipt creation starts only after compatibility and destination invariants are proven.
	if err := normalizeAndValidateImportPlanResponse(req, resp); err != nil {
		return err
	}
	trace.SpanFromContext(cmd.Context()).SetAttributes(
		attribute.String("adapter_version", boundedImportTelemetryValue(resp.AdapterVersion)),
		attribute.String("outcome", importPlanOutcome(resp.Action)),
	)

	// A durable receipt must contain the same reviewed response that the user sees.
	if err := maybeWriteImportPlanReceipt(newImportPlanReceipt(resp), opts); err != nil {
		return err
	}
	// Structured mode returns the complete bounded Registry response for automation.
	if opts.jsonOut {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(resp)
	}
	printImportPlanSummary(cmd.OutOrStdout(), resp)
	return nil
}

// normalizeAndValidateImportPlanResponse applies rollout compatibility only
// after Registry returns a complete reviewed planning response.
func normalizeAndValidateImportPlanResponse(req api.SpecImportPlanRequest, resp *api.SpecImportPlanResponse) error {
	// Why: older Registries already honor target_type but do not echo it.
	// Preserve the reviewed request scope in CLI/JSON output during rollout.
	if strings.TrimSpace(resp.TargetType) == "" {
		resp.TargetType = displayImportTarget(req.TargetType)
	}
	// Explicit-only response fields must match the request's closed attachment shape.
	if err := validateImportDestinationAcknowledgement(req, resp); err != nil {
		return err
	}
	// A review hash is the only authority the later apply step accepts.
	if strings.TrimSpace(resp.ReviewHash) == "" {
		return errors.New("Registry returned an import plan without review_hash; run plan again after upgrading the Registry")
	}
	return nil
}

// validateImportDestinationAcknowledgement rejects both ignored selectors and
// unsolicited explicit-only fields before the CLI persists a receipt.
func validateImportDestinationAcknowledgement(req api.SpecImportPlanRequest, resp *api.SpecImportPlanResponse) error {
	requested := strings.TrimSpace(req.DestinationVersion)
	destination := strings.TrimSpace(resp.DestinationVersion)
	source := strings.TrimSpace(resp.SourceVersion)
	// Ordinary imports must retain the legacy single-version response shape.
	if requested == "" {
		// Either explicit-only marker would represent publication intent the caller never supplied.
		if destination != "" || source != "" {
			return errors.New("Registry returned unsolicited import destination metadata; run plan again after upgrading the Registry")
		}
		return nil
	}
	// An explicit acknowledgement distinguishes a destination-aware Registry
	// from an older one that merely resolved the source to the same version.
	if destination != requested {
		return fmt.Errorf("Registry did not acknowledge the requested destination version %q; upgrade the Registry and run plan again", requested)
	}
	// The planned target must also remain the acknowledged attachment target so
	// a Registry cannot redirect the webhook rows to another service version.
	if strings.TrimSpace(resp.TargetVersion) != requested {
		return fmt.Errorf("Registry did not plan the requested destination version %q (planned %q); run plan again", requested, strings.TrimSpace(resp.TargetVersion))
	}
	// Explicit destination plans must expose the separately reviewed source identity.
	if source == "" {
		return errors.New("Registry returned an import destination without source_version; upgrade the Registry and run plan again")
	}
	return nil
}

// buildSpecImportRequest resolves the positional spec-path-or-url argument
// into either source_url (the Registry fetches it, same as any other direct
// import) or source_content (a local file the Registry can't reach itself).
func buildSpecImportRequest(specArg string, opts importSpecPlanOptions) (api.SpecImportPlanRequest, error) {
	slug := strings.TrimSpace(opts.slug)
	targetType, err := normalizeImportTarget(opts.target)
	req := api.SpecImportPlanRequest{
		Name:               opts.name,
		Slug:               slug,
		Version:            strings.TrimSpace(opts.version),
		DestinationVersion: strings.TrimSpace(opts.destinationVersion),
		IsPublic:           opts.isPublic,
		TargetType:         targetType,
		Category:           opts.category,
		Strict:             opts.strict,
	}
	if err != nil {
		return req, err
	}
	// Identity must be valid before reading either optional overlay or source bytes.
	if err := validateSpecImportIdentity(req); err != nil {
		return req, err
	}
	overlayContent, err := readImportOverlay(opts.overlay)
	// Overlay failures stop before a partially populated request can leave the CLI.
	if err != nil {
		return req, err
	}
	req.OverlayContent = overlayContent
	return populateSpecImportSource(req, specArg, opts.url)
}

// validateSpecImportIdentity keeps destination scope and canonical local
// identity checks independent from source transport selection.
func validateSpecImportIdentity(req api.SpecImportPlanRequest) error {
	// Destination attachment is deliberately limited to webhook-only imports so
	// endpoint imports retain their established source-version replacement rules.
	if req.DestinationVersion != "" && req.TargetType != "webhooks" {
		return errors.New("--destination-version requires --target webhooks")
	}
	// A missing slug cannot identify either the source owner or destination service.
	if req.Slug == "" {
		return errors.New("--slug is required")
	}
	return nil
}

// populateSpecImportSource selects exactly one URL or local immutable source
// without mixing transport policy into request identity validation.
func populateSpecImportSource(req api.SpecImportPlanRequest, specArg, rawSourceURL string) (api.SpecImportPlanRequest, error) {
	sourceURL := strings.TrimSpace(rawSourceURL)
	// An explicit URL owns online-source selection and excludes a positional path.
	if sourceURL != "" {
		// Two source selectors would make the reviewed bytes ambiguous.
		if specArg != "" {
			return req, errors.New("provide either a local spec path or --url, not both")
		}
		// Registry fetching is deliberately limited to bounded HTTP transports.
		if !isURL(sourceURL) {
			return req, errors.New("--url must be an http(s) URL")
		}
		req.SourceURL = sourceURL
		return req, nil
	}
	// Source content is mandatory when the URL selector is absent.
	if specArg == "" {
		return req, errors.New("a local spec path or --url is required")
	}
	// Positional URLs are rejected so callers cannot accidentally bypass explicit URL intent.
	if isURL(specArg) {
		return req, errors.New("online sources must be passed with --url")
	}
	data, err := os.ReadFile(specArg)
	// Local read failures must stop before a receipt can authorize nonexistent bytes.
	if err != nil {
		return req, fmt.Errorf("failed to read spec file %s: %w", specArg, err)
	}
	req.SourceContent = string(data)
	return req, nil
}

func readImportOverlay(path string) (*string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	if isURL(path) {
		return nil, errors.New("--overlay requires a local file path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read overlay file %s: %w", path, err)
	}
	// Why: Registry owns overlay parsing and canonicalization. Preserving the
	// bytes here prevents the CLI from creating a second review identity.
	content := string(data)
	return &content, nil
}

func normalizeImportTarget(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "":
		return "endpoints", nil
	case "all":
		// Why: omitted target_type is the established wire representation for
		// importing both endpoints and webhooks, so keep old servers compatible.
		return "", nil
	case "endpoints", "webhooks":
		return normalized, nil
	default:
		return "", errors.New("--target must be one of: all, endpoints, webhooks")
	}
}

func isURL(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// newImportPlanReceipt keeps the informational bundle identity across process
// restarts without turning it into an apply credential or storage locator.
func newImportPlanReceipt(resp *api.SpecImportPlanResponse) importPlanReceipt {
	receipt := importPlanReceipt{
		Slug:             resp.Slug,
		PlanID:           resp.PlanID,
		ReviewHash:       resp.ReviewHash,
		SourceHash:       resp.SourceHash,
		OverlayHash:      resp.OverlayHash,
		SourceBundleHash: resp.SourceBundleHash,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	if engineURL, err := GetEngineURL(); err == nil {
		receipt.EngineURL = canonicalEngineURLOrRaw(engineURL)
	}
	return receipt
}

func maybeWriteImportPlanReceipt(receipt importPlanReceipt, opts importSpecPlanOptions) error {
	if opts.jsonOut && strings.TrimSpace(opts.receiptOut) == "" {
		return nil
	}
	path := opts.receiptOut
	if path == "" {
		path = defaultImportReceiptPath
	}
	return writeImportPlanReceiptFile(path, receipt)
}

// printImportPlanSummary exposes the reviewed bundle digest while keeping
// Registry object-storage implementation details out of the CLI surface.
func printImportPlanSummary(out io.Writer, resp *api.SpecImportPlanResponse) {
	fmt.Fprintf(out, "Plan %s for %q (slug: %s, version: %s, target: %s) -- plan ID: %s\n", resp.Action, resp.Name, resp.Slug, resp.TargetVersion, displayImportTarget(resp.TargetType), resp.PlanID)
	fmt.Fprintf(out, "Source format: %s\n", resp.SourceFormat)
	sourceVersion := strings.TrimSpace(resp.SourceVersion)
	destinationVersion := strings.TrimSpace(resp.DestinationVersion)
	// A separate source label matters only for explicit cross-version attachment;
	// otherwise the headline's target version already conveys the same identity.
	if destinationVersion != "" && sourceVersion != "" && sourceVersion != destinationVersion {
		fmt.Fprintf(out, "Source version: %s\n", sourceVersion)
	}
	fmt.Fprintf(out, "Review hash: %s\n", resp.ReviewHash)
	fmt.Fprintf(out, "Source bundle hash: %s\n", resp.SourceBundleHash)
	if resp.OverlayHash == "" {
		fmt.Fprintln(out, "Overlay: none")
	} else {
		fmt.Fprintln(out, "Overlay: applied")
	}
	fmt.Fprintf(out, "Diff: %d added, %d changed, %d removed\n", resp.Diff.Added, resp.Diff.Changed, resp.Diff.Removed)
	for _, name := range resp.Diff.ChangedNames {
		fmt.Fprintf(out, "  ~ %s\n", name)
	}
	for _, name := range resp.Diff.RemovedNames {
		fmt.Fprintf(out, "  - %s\n", name)
	}
	printImportUsageWarning(out, resp.Usage)
	printImportDiagnostics(out, resp.Diagnostics)
	fmt.Fprintf(out, "Run `fused-cli import apply` to commit this plan.\n")
}

func boundedImportTelemetryValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	if len(value) > 64 {
		return "other"
	}
	for _, char := range value {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-", char) {
			return "other"
		}
	}
	return value
}

func importPlanOutcome(action string) string {
	switch strings.TrimSpace(action) {
	case "create_service", "create_version", "update_version", "no_change":
		return strings.TrimSpace(action)
	default:
		return "unknown"
	}
}

func printImportDiagnostics(out io.Writer, diagnostics []api.SpecImportDiagnostic) {
	if len(diagnostics) == 0 {
		return
	}
	fmt.Fprintf(out, "Diagnostics (%d):\n", len(diagnostics))
	for _, diagnostic := range diagnostics {
		severity := strings.ToUpper(strings.TrimSpace(diagnostic.Severity))
		if severity == "" {
			severity = "INFO"
		}
		fmt.Fprintf(out, "  - %s %s [%s]: %s\n", severity, compactDiagnosticText(diagnostic.Code), importDiagnosticLocation(diagnostic), compactDiagnosticText(diagnostic.Message))
		if recommendation := compactDiagnosticText(diagnostic.Recommendation); recommendation != "" {
			fmt.Fprintf(out, "    Recommendation: %s\n", recommendation)
		}
	}
}

func importDiagnosticLocation(diagnostic api.SpecImportDiagnostic) string {
	method := strings.ToUpper(strings.TrimSpace(diagnostic.Method))
	path := strings.TrimSpace(diagnostic.Path)
	if method != "" || path != "" {
		return strings.TrimSpace(method + " " + path)
	}
	if operationID := strings.TrimSpace(diagnostic.OperationID); operationID != "" {
		return operationID
	}
	return compactDiagnosticText(diagnostic.Scope)
}

func compactDiagnosticText(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func displayImportTarget(value string) string {
	if strings.TrimSpace(value) == "" {
		return "all"
	}
	return value
}

// printImportUsageWarning names which SDKs/workspaces are pinned to the
// provider version an update_version apply would touch -- informational only (never
// fails the command, see Task 6's "plan && apply is safe to run unattended
// in CI" requirement), and silent when the usage block is empty or absent.
func printImportUsageWarning(out io.Writer, usage *api.SpecImportUsage) {
	if usage == nil || (len(usage.SDKs) == 0 && len(usage.Workspaces) == 0) {
		return
	}
	fmt.Fprintf(out, "%d SDKs / %d workspaces use this version:\n", len(usage.SDKs), len(usage.Workspaces))
	for _, sdk := range usage.SDKs {
		touches := ""
		if sdk.UsesChangedEndpoint {
			touches = " (uses a changed/removed endpoint)"
		}
		fmt.Fprintf(out, "  - SDK %s%s\n", sdk.Name, touches)
	}
	for _, ws := range usage.Workspaces {
		fmt.Fprintf(out, "  - workspace %s\n", ws.Name)
	}
}

// runImportApply validates the reviewed receipt, submits one mutation attempt,
// and records success only after the client proves the exact committed result.
func runImportApply(cmd *cobra.Command, opts importSpecApplyOptions) error {
	receipt, err := resolveImportApplyReceipt(opts)
	if err != nil {
		return err
	}
	timeout := specImportTimeout(cmd)
	// Apply uses the same import-specific budget as plan so reviewed large
	// contracts are not cut off while their bounded SQL batches are committing.
	client, err := getAPIClientWithTimeout(timeout)
	if err != nil {
		return err
	}
	if opts.planID != "" {
		receipt.EngineURL = canonicalEngineURLOrRaw(client.BaseURL)
	}
	if err := validateReceiptEngineURL(receipt.EngineURL, client.BaseURL); err != nil {
		return fmt.Errorf("import receipt target invalid: %w", err)
	}
	resp, err := client.ApplySpecImport(receipt.PlanID, receipt.ReviewHash)
	if err != nil {
		var unknown *api.SpecImportApplyOutcomeUnknownError
		// Structured safe HTTP errors are authoritative; every marked transport or proof failure recovers by status.
		if errors.As(err, &unknown) {
			return newImportApplyOutcomeUnknownError(cmd, err, timeout, receipt.PlanID)
		}
		var apiError *api.APIError
		// A structured committed partial failure is still mutation evidence even
		// though the requested composite outcome did not fully complete.
		if errors.As(err, &apiError) && apiError.CommitState == "committed" {
			recordAppliedChange(cmd.Context(), cmd.CommandPath(), "service_import")
			trace.SpanFromContext(cmd.Context()).SetAttributes(
				attribute.String("outcome", "partial"),
				attribute.String("failure_phase", safeImportFailurePhase(apiError.Phase)),
			)
		}
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "service_import")
	printImportApplyResult(cmd.OutOrStdout(), resp)
	return nil
}

// safeImportFailurePhase keeps remote phase text out of telemetry while retaining the reviewed partial-apply stage.
func safeImportFailurePhase(value string) string {
	// Workspace activation is the only Engine-local post-commit phase currently
	// returned through import apply; every other value stays a bounded unknown.
	if value == "workspace_activation" {
		return value
	}
	return "unknown"
}

// newImportApplyOutcomeUnknownError records one bounded unknown outcome and
// preserves timeout tuning only when the underlying transport hit its deadline.
func newImportApplyOutcomeUnknownError(cmd *cobra.Command, cause error, timeout time.Duration, operationID string) error {
	timedOut := isRequestTimeout(cause)
	attributes := []attribute.KeyValue{attribute.String("outcome", "unknown")}
	// Deadline duration is useful only for actual timeout recovery, not resets or malformed bodies.
	if timedOut {
		attributes = append(attributes, attribute.Int64("timeout_ms", timeout.Milliseconds()))
	}
	trace.SpanFromContext(cmd.Context()).SetAttributes(attributes...)
	return &importApplyOutcomeUnknownError{cause: cause, timeout: timeout, operationID: operationID, timedOut: timedOut}
}

// runImportStatus reads one durable operation and renders either stable JSON
// or a compact human recovery summary.
func runImportStatus(cmd *cobra.Command, operationID string) error {
	canonicalOperationID := strings.TrimSpace(operationID)
	parsedOperationID, err := uuid.Parse(canonicalOperationID)
	// Local UUID validation keeps invalid path text away from the HTTP client.
	if err != nil {
		return errors.New("operation ID must be a UUID")
	}
	canonicalOperationID = parsedOperationID.String()
	client, err := getAPIClient()
	// Context/auth configuration must succeed before the read-only request.
	if err != nil {
		return err
	}
	status, err := client.GetSpecImportStatus(canonicalOperationID)
	// Shared API errors preserve stable recovery fields for automation.
	if err != nil {
		return err
	}
	// JSON mode is a direct stable projection for scripts and agents.
	if wantsJSON(cmd) {
		return writeJSON(cmd, status)
	}
	printImportStatus(cmd.OutOrStdout(), status)
	return nil
}

// printImportStatus renders the compact human recovery view separately from
// request validation so neither concern accumulates command complexity.
func printImportStatus(out io.Writer, status *api.SpecImportStatusResponse) {
	fmt.Fprintf(out, "Import %s: %s (%s, %s)\n", status.OperationID, status.Status, status.Phase, status.CommitState)
	// A complete committed operation carries the exact durable mutation result.
	if status.CommitState == "committed" && status.ServiceID != "" && status.Version != "" && status.Revision > 0 {
		fmt.Fprintf(out, "Service %s · version %s · revision %d\n", status.ServiceID, status.Version, status.Revision)
	}
	// Terminal failures include one stable selector for scripts and operators.
	if status.Code != "" {
		fmt.Fprintf(out, "Code: %s\n", status.Code)
	}
	// In-progress guidance is informational and deliberately carries no shell recovery command.
	if status.Guidance != "" {
		fmt.Fprintf(out, "Guidance: %s\n", status.Guidance)
	}
	// Recovery is printed only when the server has a non-looping next command.
	if status.Recovery != "" {
		fmt.Fprintf(out, "Recovery: `%s`\n", status.Recovery)
	}
}

// specImportTimeout honors an explicit global flag while giving import plan
// and apply a larger default than short control-plane requests.
func specImportTimeout(cmd *cobra.Command) time.Duration {
	if timeoutFlagChanged(cmd) {
		return RequestTimeout
	}
	return defaultSpecImportTimeout
}

// timeoutFlagChanged checks both local and inherited flag sets because Cobra
// permits a persistent flag before or after the import subcommands.
func timeoutFlagChanged(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	flag := cmd.Flag("timeout")
	return flag != nil && flag.Changed
}

// isRequestTimeout recognizes both context deadlines and transport timeout
// implementations without misclassifying an operator cancellation as unknown.
func isRequestTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var timeoutError net.Error
	return errors.As(err, &timeoutError) && timeoutError.Timeout()
}

// resolveImportApplyReceipt mirrors receiptForApply's --plan-id/receipt-file
// split, but direct flags carry the Registry's opaque combined review hash.
// Why: apply must authorize the source and overlay reviewed by Registry; the
// CLI cannot safely reconstruct that identity from either local file.
func resolveImportApplyReceipt(opts importSpecApplyOptions) (importPlanReceipt, error) {
	planID := strings.TrimSpace(opts.planID)
	reviewHash := strings.TrimSpace(opts.reviewHash)
	receiptPath := strings.TrimSpace(opts.receiptPath)
	if err := validateImportApplyOptions(planID, reviewHash, receiptPath); err != nil {
		return importPlanReceipt{}, err
	}
	if planID != "" {
		return importPlanReceipt{PlanID: planID, ReviewHash: reviewHash}, nil
	}
	path := receiptPath
	if path == "" {
		path = defaultImportReceiptPath
	}
	return readValidImportPlanReceipt(path)
}

func validateImportApplyOptions(planID, reviewHash, receiptPath string) error {
	if receiptPath != "" {
		if planID != "" || reviewHash != "" {
			return errors.New("--receipt cannot be combined with --plan-id or --review-hash")
		}
	}
	if planID == "" && reviewHash != "" {
		return errors.New("--plan-id is required when using --review-hash")
	}
	if planID != "" && reviewHash == "" {
		return errors.New("--review-hash is required when using --plan-id")
	}
	return nil
}

func readValidImportPlanReceipt(path string) (importPlanReceipt, error) {
	receipt, err := readImportPlanReceiptFile(path)
	if err != nil {
		return receipt, err
	}
	if strings.TrimSpace(receipt.PlanID) == "" {
		return receipt, errors.New("plan receipt has no plan_id; run import plan again")
	}
	if strings.TrimSpace(receipt.ReviewHash) == "" {
		return receipt, errors.New("plan receipt has no review_hash; run import plan again")
	}
	return receipt, nil
}

func printImportApplyResult(out io.Writer, resp *api.SpecImportApplyResponse) {
	if resp.IsNewService {
		fmt.Fprintf(out, "Created service %s (version %s, revision %d)\n", resp.ServiceID, resp.Version, resp.Revision)
		fmt.Fprintf(out, "Slug: %s\n", resp.Slug)
		return
	}
	fmt.Fprintf(out, "Applied %s to service %s (version %s, revision %d)\n", resp.Action, resp.ServiceID, resp.Version, resp.Revision)
}

func writeImportPlanReceiptFile(path string, receipt importPlanReceipt) error {
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(path, append(data, '\n'), 0644, validateJSONContent)
}

func validateJSONContent(data []byte) error {
	if !json.Valid(data) {
		return errors.New("invalid JSON")
	}
	return nil
}

func readImportPlanReceiptFile(path string) (importPlanReceipt, error) {
	var receipt importPlanReceipt
	data, err := os.ReadFile(path)
	if err != nil {
		return receipt, fmt.Errorf("failed to read plan receipt %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		return receipt, fmt.Errorf("failed to parse plan receipt %s: %w", path, err)
	}
	return receipt, nil
}
