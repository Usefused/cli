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
	// isPublic is nil when --public was not passed at all, distinct from an
	// explicit --public=false -- see import.go's flag registration.
	isPublic   *bool
	category   string
	receiptOut string
	jsonOut    bool
}

type importSpecApplyOptions struct {
	planID      string
	sourceHash  string
	receiptPath string
}

// importPlanReceipt mirrors planReceipt's shape (config_runner.go) for the
// same reason: a local, on-disk record of what was just planned, so a later
// "import apply" (possibly a different process/CI step) knows what plan_id
// and source_hash to send without re-planning.
type importPlanReceipt struct {
	Slug       string `json:"slug,omitempty"`
	PlanID     string `json:"plan_id"`
	SourceHash string `json:"source_hash"`
	EngineURL  string `json:"engine_url,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
}

func runImportPlan(cmd *cobra.Command, specArg string, opts importSpecPlanOptions) error {
	req, err := buildSpecImportRequest(specArg, opts)
	if err != nil {
		return err
	}
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	resp, err := client.PlanSpecImport(req)
	if err != nil {
		return err
	}

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
	req := api.SpecImportPlanRequest{
		Name:     opts.name,
		Slug:     slug,
		Version:  strings.TrimSpace(opts.version),
		IsPublic: opts.isPublic,
		Category: opts.category,
	}
	if slug == "" {
		return req, errors.New("--slug is required")
	}
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

func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func newImportPlanReceipt(resp *api.SpecImportPlanResponse) importPlanReceipt {
	receipt := importPlanReceipt{
		Slug:       resp.Slug,
		PlanID:     resp.PlanID,
		SourceHash: resp.SourceHash,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if engineURL, err := GetEngineURL(); err == nil {
		receipt.EngineURL = canonicalEngineURLOrRaw(engineURL)
	}
	return receipt
}

func maybeWriteImportPlanReceipt(receipt importPlanReceipt, opts importSpecPlanOptions) error {
	if opts.jsonOut {
		return nil
	}
	path := opts.receiptOut
	if path == "" {
		path = defaultImportReceiptPath
	}
	return writeImportPlanReceiptFile(path, receipt)
}

func printImportPlanSummary(out io.Writer, resp *api.SpecImportPlanResponse) {
	fmt.Fprintf(out, "Plan %s for %q (slug: %s, version: %s) -- plan ID: %s\n", resp.Action, resp.Name, resp.Slug, resp.TargetVersion, resp.PlanID)
	fmt.Fprintf(out, "Diff: %d added, %d changed, %d removed\n", resp.Diff.Added, resp.Diff.Changed, resp.Diff.Removed)
	for _, name := range resp.Diff.ChangedNames {
		fmt.Fprintf(out, "  ~ %s\n", name)
	}
	for _, name := range resp.Diff.RemovedNames {
		fmt.Fprintf(out, "  - %s\n", name)
	}
	printImportUsageWarning(out, resp.Usage)
	fmt.Fprintf(out, "Run `fused-cli import apply` to commit this plan.\n")
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
	resp, err := client.ApplySpecImport(receipt.PlanID, receipt.SourceHash)
	if err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "service_import")
	printImportApplyResult(cmd.OutOrStdout(), resp)
	return nil
}

// resolveImportApplyReceipt mirrors receiptForApply's --plan-id/receipt-file
// split, but --plan-id here must be paired with --source-hash explicitly:
// unlike a declarative config file (whose source_hash config_runner.go
// always recomputes locally regardless of --plan-id), an import's
// source_hash is a hash of arbitrary spec bytes with no other local record
// of it unless a receipt file already has it.
func resolveImportApplyReceipt(opts importSpecApplyOptions) (importPlanReceipt, error) {
	if opts.planID != "" {
		if opts.sourceHash == "" {
			return importPlanReceipt{}, errors.New("--source-hash is required when using --plan-id")
		}
		return importPlanReceipt{PlanID: opts.planID, SourceHash: opts.sourceHash}, nil
	}
	path := opts.receiptPath
	if path == "" {
		path = defaultImportReceiptPath
	}
	return readImportPlanReceiptFile(path)
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
