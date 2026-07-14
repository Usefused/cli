package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	name       string
	slug       string
	isPublic   bool
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
	req := api.SpecImportPlanRequest{
		Name:     opts.name,
		Slug:     opts.slug,
		IsPublic: opts.isPublic,
		Category: opts.category,
	}
	if isURL(specArg) {
		req.SourceURL = specArg
		return req, nil
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
		receipt.EngineURL = engineURL
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
	if resp.IsNewService {
		fmt.Fprintf(out, "New service %q (slug: %s) -- plan ID: %s\n", resp.Name, resp.Slug, resp.PlanID)
	} else {
		fmt.Fprintf(out, "Update to %q (slug: %s, strategy: %s) -- plan ID: %s\n", resp.Name, resp.Slug, resp.ResolvedStrategy, resp.PlanID)
	}
	fmt.Fprintf(out, "Diff: %d added, %d changed, %d removed\n", resp.Diff.Added, resp.Diff.Changed, resp.Diff.Removed)
	for _, name := range resp.Diff.ChangedNames {
		fmt.Fprintf(out, "  ~ %s\n", name)
	}
	for _, name := range resp.Diff.RemovedNames {
		fmt.Fprintf(out, "  - %s\n", name)
	}
	printImportUsageWarning(out, resp.Usage)
	fmt.Fprintf(out, "Run `fused import apply` to commit this plan.\n")
}

// printImportUsageWarning names which SDKs/workspaces are pinned to the
// version a modify_existing apply would touch -- informational only (never
// fails the command, see Task 6's "plan && apply is safe to run unattended
// in CI" requirement), and silent when the usage block is empty or absent
// (a new service or a new_version strategy, neither of which affects an
// existing consumer).
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
	resp, err := client.ApplySpecImport(receipt.PlanID, receipt.SourceHash)
	if err != nil {
		return err
	}
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
		fmt.Fprintf(out, "Created service %s (version %s)\n", resp.ServiceID, resp.Version)
		return
	}
	fmt.Fprintf(out, "Applied %s to service %s (version %s)\n", resp.ResolvedStrategy, resp.ServiceID, resp.Version)
}

func writeImportPlanReceiptFile(path string, receipt importPlanReceipt) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
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
