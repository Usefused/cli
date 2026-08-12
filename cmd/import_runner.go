package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// defaultImportReceiptPath is deliberately a single fixed path, not one keyed
// per-service the way defaultReceiptPath(configKey) is for declarative
// configs: "import apply" always means "apply the most recent import plan",
// there's no directory of many import specs to disambiguate between.
const defaultImportReceiptPath = ".fused/.state/import.plan.json"

type importSpecPlanOptions struct {
	name    string
	slug    string
	url     string
	version string
	target  string
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

func runImportPlan(cmd *cobra.Command, specArg string, opts importSpecPlanOptions) error {
	req, err := buildSpecImportRequest(specArg, opts)
	if err != nil {
		return err
	}
	trace.SpanFromContext(cmd.Context()).SetAttributes(
		attribute.String("target_type", displayImportTarget(req.TargetType)),
		attribute.Bool("strict_mode", req.Strict),
		attribute.Bool("overlay_present", req.OverlayContent != nil),
	)
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	resp, err := client.PlanSpecImport(req)
	if err != nil {
		var strictError *api.SpecImportStrictError
		if errors.As(err, &strictError) {
			printImportDiagnostics(cmd.ErrOrStderr(), strictError.Diagnostics)
		}
		return err
	}
	// Why: older Registries already honor target_type but do not echo it.
	// Preserve the reviewed request scope in CLI/JSON output during rollout.
	if strings.TrimSpace(resp.TargetType) == "" {
		resp.TargetType = displayImportTarget(req.TargetType)
	}
	if strings.TrimSpace(resp.ReviewHash) == "" {
		return errors.New("Registry returned an import plan without review_hash; run plan again after upgrading the Registry")
	}
	trace.SpanFromContext(cmd.Context()).SetAttributes(
		attribute.String("adapter_version", boundedImportTelemetryValue(resp.AdapterVersion)),
		attribute.String("outcome", importPlanOutcome(resp.Action)),
	)

	if err := maybeWriteImportPlanReceipt(newImportPlanReceipt(resp), opts); err != nil {
		return err
	}
	if opts.jsonOut {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(resp)
	}
	printImportPlanSummary(cmd.OutOrStdout(), resp)
	return nil
}

// buildSpecImportRequest resolves the positional spec-path-or-url argument
// into either source_url (the Registry fetches it, same as any other direct
// import) or source_content (a local file the Registry can't reach itself).
func buildSpecImportRequest(specArg string, opts importSpecPlanOptions) (api.SpecImportPlanRequest, error) {
	slug := strings.TrimSpace(opts.slug)
	targetType, err := normalizeImportTarget(opts.target)
	req := api.SpecImportPlanRequest{
		Name:       opts.name,
		Slug:       slug,
		Version:    strings.TrimSpace(opts.version),
		IsPublic:   opts.isPublic,
		TargetType: targetType,
		Category:   opts.category,
		Strict:     opts.strict,
	}
	if err != nil {
		return req, err
	}
	if slug == "" {
		return req, errors.New("--slug is required")
	}
	overlayContent, err := readImportOverlay(opts.overlay)
	if err != nil {
		return req, err
	}
	req.OverlayContent = overlayContent
	sourceURL := strings.TrimSpace(opts.url)
	if sourceURL != "" {
		if specArg != "" {
			return req, errors.New("provide either a local spec path or --url, not both")
		}
		if !isURL(sourceURL) {
			return req, errors.New("--url must be an http(s) URL")
		}
		req.SourceURL = sourceURL
		return req, nil
	}
	if specArg == "" {
		return req, errors.New("a local spec path or --url is required")
	}
	if isURL(specArg) {
		return req, errors.New("online sources must be passed with --url")
	}
	data, err := os.ReadFile(specArg)
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

func runImportApply(cmd *cobra.Command, opts importSpecApplyOptions) error {
	receipt, err := resolveImportApplyReceipt(opts)
	if err != nil {
		return err
	}
	client, err := getAPIClient()
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
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "service_import")
	printImportApplyResult(cmd.OutOrStdout(), resp)
	return nil
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
