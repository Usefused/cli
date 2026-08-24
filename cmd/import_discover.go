package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/charmbracelet/huh"
	charmterm "github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	importDiscoverName             string
	importDiscoverSlug             string
	importDiscoverURL              string
	importDiscoverSessionID        string
	importDiscoverVersion          string
	importDiscoverSourceMode       string
	importDiscoverWorkers          int
	importDiscoverMaxPages         int
	importDiscoverMaxDepth         int
	importDiscoverSelect           []string
	importDiscoverAll              bool
	importDiscoverAcceptProposal   []string
	importDiscoverRejectEnrichment bool
	importDiscoverOverlay          string
	importDiscoverReceiptOut       string
	importDiscoverNoBrowser        bool
	importDiscoverJSON             bool
	importDiscoverTimeout          time.Duration
)

var errDiscoverySnapshotRequired = errors.New("reload discovery snapshot")

const discoveryStreamReconnectDelay = 100 * time.Millisecond

var (
	openDiscoveryBrowser        = openSystemBrowser
	discoveryBrowserInteractive = func() bool { return isTerminal(os.Stdin) }
)

// importDiscoverCmd starts the reviewed source-discovery workflow without creating a service.
var importDiscoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "Discover and review an API contract from a provider URL",
	Long: `Find a machine-readable specification first and otherwise crawl admitted API
documentation pages. Fused shows discovered operations and optional x-fused-*
proposals before producing an ordinary import plan. This command never applies
the plan; run "fused-cli import apply" after review.`,
	Args: cobra.NoArgs,
	RunE: WithTelemetry("cli.import.discover", func(cmd *cobra.Command, _ []string) error {
		return runImportDiscovery(cmd, importDiscoveryOptions{
			name: importDiscoverName, slug: importDiscoverSlug, sourceURL: importDiscoverURL,
			sessionID: importDiscoverSessionID,
			version:   importDiscoverVersion, sourceMode: importDiscoverSourceMode,
			requestedWorkers: importDiscoverWorkers, maxPages: importDiscoverMaxPages,
			maxDepth: importDiscoverMaxDepth, selectors: importDiscoverSelect,
			selectAll: importDiscoverAll, acceptedProposals: importDiscoverAcceptProposal,
			rejectEnrichment: importDiscoverRejectEnrichment, overlayPath: importDiscoverOverlay,
			receiptOut: importDiscoverReceiptOut, noBrowser: importDiscoverNoBrowser,
			jsonOut: importDiscoverJSON,
			timeout: importDiscoverTimeout,
		})
	}),
}

// importDiscoveryOptions captures presentation choices while Registry remains authoritative for admission.
type importDiscoveryOptions struct {
	name              string
	slug              string
	sourceURL         string
	sessionID         string
	version           string
	sourceMode        string
	requestedWorkers  int
	maxPages          int
	maxDepth          int
	selectors         []string
	selectAll         bool
	acceptedProposals []string
	rejectEnrichment  bool
	overlayPath       string
	receiptOut        string
	noBrowser         bool
	jsonOut           bool
	timeout           time.Duration
}

// discoverySessionRunner owns one CLI presentation loop over authoritative snapshots.
type discoverySessionRunner struct {
	ctx                       context.Context
	cmd                       *cobra.Command
	client                    *cliapi.Client
	options                   importDiscoveryOptions
	overlayApplied            bool
	enrichmentDecided         bool
	pendingProposalRejections []string
	reviewLinkPresented       bool
}

// init registers the breaking discovery command and intentionally omits the former docs command.
func init() {
	importCmd.AddCommand(importDiscoverCmd)
	importDiscoverCmd.Flags().StringVar(&importDiscoverName, "name", "", "Service name (required unless --session is used)")
	importDiscoverCmd.Flags().StringVar(&importDiscoverSlug, "slug", "", "Service slug to create or update (required unless --session is used)")
	importDiscoverCmd.Flags().StringVar(&importDiscoverURL, "url", "", "Provider specification or documentation URL (required unless --session is used)")
	importDiscoverCmd.Flags().StringVar(&importDiscoverSessionID, "session", "", "Resume an existing discovery session instead of starting one")
	importDiscoverCmd.Flags().StringVar(&importDiscoverVersion, "version", "", "Provider version when it cannot be discovered")
	importDiscoverCmd.Flags().StringVar(&importDiscoverSourceMode, "source-mode", "auto", "Source admission mode: auto, spec, or docs")
	importDiscoverCmd.Flags().IntVar(&importDiscoverWorkers, "workers", 0, "Requested extraction workers; Registry clamps to its configured pool")
	importDiscoverCmd.Flags().IntVar(&importDiscoverMaxPages, "max-pages", 0, "Requested documentation page ceiling; Registry clamps it")
	importDiscoverCmd.Flags().IntVar(&importDiscoverMaxDepth, "max-depth", 0, "Requested documentation crawl depth; Registry clamps it")
	importDiscoverCmd.Flags().StringArrayVar(&importDiscoverSelect, "select", nil, "Exact operation as METHOD:/path; repeat for multiple operations")
	importDiscoverCmd.Flags().BoolVar(&importDiscoverAll, "all", false, "Select every discovered operation explicitly")
	importDiscoverCmd.Flags().StringArrayVar(&importDiscoverAcceptProposal, "accept-proposal", nil, "Accept an exact enrichment proposal ID; repeat for several")
	importDiscoverCmd.Flags().BoolVar(&importDiscoverRejectEnrichment, "reject-enrichment", false, "Reject every optional enrichment proposal")
	importDiscoverCmd.Flags().StringVar(&importDiscoverOverlay, "overlay", "", "Local JSON Fused overlay to review with the discovered contract")
	importDiscoverCmd.Flags().StringVar(&importDiscoverReceiptOut, "receipt-out", "", "Write the resulting import-plan receipt to a specific path")
	importDiscoverCmd.Flags().BoolVar(&importDiscoverNoBrowser, "no-browser", false, "Print the browser review URL instead of opening it")
	importDiscoverCmd.Flags().BoolVar(&importDiscoverJSON, "json", false, "Print the final plan-ready snapshot as JSON")
	importDiscoverCmd.Flags().DurationVar(&importDiscoverTimeout, "timeout", 20*time.Minute, "Maximum discovery session duration")
}

// runImportDiscovery validates local presentation inputs and drives one remote session to plan_ready.
func runImportDiscovery(cmd *cobra.Command, options importDiscoveryOptions) error {
	if err := validateImportDiscoveryOptions(options); err != nil {
		return err
	}
	client, err := getAPIClientWithTimeout(options.timeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), options.timeout)
	defer cancel()
	recordDiscoveryTelemetry(ctx, options)
	snapshot, started, err := loadImportDiscoverySession(ctx, client, options)
	if err != nil {
		return err
	}
	verb := "Resuming"
	if started {
		verb = "Started"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s discovery session %s\n", verb, snapshot.SessionID)
	runner := discoverySessionRunner{ctx: ctx, cmd: cmd, client: client, options: options}
	return runner.run(snapshot)
}

// loadImportDiscoverySession either creates revision one or reloads the exact
// authoritative session named by --session without replaying start inputs.
func loadImportDiscoverySession(ctx context.Context, client *cliapi.Client, options importDiscoveryOptions) (*cliapi.DiscoverySnapshot, bool, error) {
	if options.sessionID != "" {
		snapshot, err := client.GetDiscoverySession(ctx, options.sessionID)
		return snapshot, false, err
	}
	snapshot, err := client.StartDiscovery(ctx, discoveryStartRequest(options))
	return snapshot, true, err
}

// validateImportDiscoveryOptions rejects ambiguous automation before a remote session is created.
func validateImportDiscoveryOptions(options importDiscoveryOptions) error {
	if err := validateDiscoveryIdentityOptions(options); err != nil {
		return err
	}
	if err := validateDiscoveryLimitOptions(options); err != nil {
		return err
	}
	if err := validateDiscoverySelectionOptions(options); err != nil {
		return err
	}
	return validateDiscoveryEnrichmentOptions(options)
}

// validateDiscoveryIdentityOptions requires the service and source identity needed before admission.
func validateDiscoveryIdentityOptions(options importDiscoveryOptions) error {
	if options.sessionID != "" {
		return validateDiscoveryResumeOptions(options)
	}
	if strings.TrimSpace(options.name) == "" || strings.TrimSpace(options.slug) == "" {
		return errors.New("--name and --slug are required")
	}
	if !isURL(strings.TrimSpace(options.sourceURL)) {
		return errors.New("--url must be an http(s) URL")
	}
	if !validDiscoverySourceMode(options.sourceMode) {
		return errors.New("--source-mode must be auto, spec, or docs")
	}
	return nil
}

// validateDiscoveryResumeOptions rejects start-only values so resume never
// appears to reinterpret or silently ignore a second service identity.
func validateDiscoveryResumeOptions(options importDiscoveryOptions) error {
	if err := validateDiscoveryResumeSessionID(options.sessionID); err != nil {
		return err
	}
	if err := validateDiscoveryResumeIdentity(options); err != nil {
		return err
	}
	return validateDiscoveryResumeScheduling(options)
}

// validateDiscoveryResumeSessionID admits one bounded opaque route identity.
func validateDiscoveryResumeSessionID(sessionID string) error {
	if sessionID == "" || sessionID != strings.TrimSpace(sessionID) || len(sessionID) > 128 || strings.ContainsAny(sessionID, " /\\\t\r\n\x00") {
		return errors.New("--session must be a bounded opaque session ID")
	}
	return nil
}

// validateDiscoveryResumeIdentity prevents a resumed session from acquiring a
// second client-authored service, source, or version identity.
func validateDiscoveryResumeIdentity(options importDiscoveryOptions) error {
	if strings.TrimSpace(options.name) != "" || strings.TrimSpace(options.slug) != "" || strings.TrimSpace(options.sourceURL) != "" || strings.TrimSpace(options.version) != "" {
		return errors.New("--session cannot be combined with --name, --slug, --url, or --version")
	}
	return nil
}

// validateDiscoveryResumeScheduling prevents client flags from appearing to
// alter worker, crawl, or source decisions already sealed in revision one.
func validateDiscoveryResumeScheduling(options importDiscoveryOptions) error {
	if options.requestedWorkers != 0 || options.maxPages != 0 || options.maxDepth != 0 {
		return errors.New("--session cannot change Registry-owned worker or crawl limits")
	}
	if strings.ToLower(strings.TrimSpace(options.sourceMode)) != "auto" {
		return errors.New("--session cannot change the source admission mode")
	}
	return nil
}

// validateDiscoveryLimitOptions rejects negative or unbounded local wait choices.
func validateDiscoveryLimitOptions(options importDiscoveryOptions) error {
	if options.requestedWorkers < 0 || options.maxPages < 0 || options.maxDepth < 0 {
		return errors.New("--workers, --max-pages, and --max-depth cannot be negative")
	}
	if options.timeout <= 0 {
		return errors.New("--timeout must be greater than zero")
	}
	return nil
}

// validateDiscoverySelectionOptions prevents two competing operation authorization modes.
func validateDiscoverySelectionOptions(options importDiscoveryOptions) error {
	if options.selectAll && len(options.selectors) > 0 {
		return errors.New("--all cannot be combined with --select")
	}
	return nil
}

// validateDiscoveryEnrichmentOptions prevents simultaneous accept and reject instructions.
func validateDiscoveryEnrichmentOptions(options importDiscoveryOptions) error {
	if options.rejectEnrichment && len(options.acceptedProposals) > 0 {
		return errors.New("--reject-enrichment cannot be combined with --accept-proposal")
	}
	return nil
}

// validDiscoverySourceMode admits only the Registry-owned source strategy vocabulary.
func validDiscoverySourceMode(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "auto", "spec", "docs":
		return true
	default:
		return false
	}
}

// recordDiscoveryTelemetry records bounded option metadata without source URLs or contract content.
func recordDiscoveryTelemetry(ctx context.Context, options importDiscoveryOptions) {
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String("source_mode", strings.ToLower(strings.TrimSpace(options.sourceMode))),
		attribute.Int("requested_workers", options.requestedWorkers),
		attribute.Int("requested_max_pages", options.maxPages),
		attribute.Int("requested_max_depth", options.maxDepth),
		attribute.String("selection_mode", discoverySelectionMode(options)),
		attribute.Bool("overlay_present", strings.TrimSpace(options.overlayPath) != ""),
	)
}

// discoveryStartRequest maps CLI flags to the single shared Registry start shape.
func discoveryStartRequest(options importDiscoveryOptions) cliapi.DiscoveryStartRequest {
	return cliapi.DiscoveryStartRequest{
		Name: strings.TrimSpace(options.name), Slug: strings.TrimSpace(options.slug),
		Version: strings.TrimSpace(options.version), SourceURL: strings.TrimSpace(options.sourceURL),
		SourceMode: strings.ToLower(strings.TrimSpace(options.sourceMode)), RequestedWorkers: options.requestedWorkers,
		Crawl: cliapi.DiscoveryCrawlRequest{MaxPages: options.maxPages, MaxDepth: options.maxDepth},
	}
}

// discoverySelectionMode classifies only user intent and never includes operation coordinates.
func discoverySelectionMode(options importDiscoveryOptions) string {
	if options.selectAll {
		return "all"
	}
	if len(options.selectors) > 0 {
		return "explicit"
	}
	return "interactive"
}

// run advances snapshots until Registry emits one exact import plan or a terminal failure.
func (runner *discoverySessionRunner) run(snapshot *cliapi.DiscoverySnapshot) error {
	for {
		next, done, err := runner.advance(snapshot)
		if err != nil {
			return err
		}
		if done {
			return runner.finish(next)
		}
		snapshot = next
	}
}

// advance delegates active, decision, and terminal states without inventing state transitions locally.
func (runner *discoverySessionRunner) advance(snapshot *cliapi.DiscoverySnapshot) (*cliapi.DiscoverySnapshot, bool, error) {
	switch snapshot.State {
	case cliapi.DiscoveryStateResolveSource, cliapi.DiscoveryStateFetchSpec,
		cliapi.DiscoveryStateCrawlDocs, cliapi.DiscoveryStateDiscoverOperations,
		cliapi.DiscoveryStateExtractContract, cliapi.DiscoveryStateEnrichContract:
		next, err := runner.waitForSnapshot(snapshot)
		return next, false, err
	case cliapi.DiscoveryStateAwaitingSelection:
		next, err := runner.selectOperations(snapshot)
		return next, false, err
	case cliapi.DiscoveryStateAwaitingReview:
		return runner.advanceReview(snapshot)
	case cliapi.DiscoveryStatePlanReady:
		return runner.completePlan(snapshot)
	case cliapi.DiscoveryStateError:
		return snapshot, false, discoveryFailure(snapshot)
	case cliapi.DiscoveryStateCancelled:
		return snapshot, false, errors.New("discovery session was cancelled")
	default:
		return snapshot, false, fmt.Errorf("discovery entered unsupported state %q", snapshot.State)
	}
}

// advanceReview hands interactive review to the authenticated Engine UI while
// preserving the flag-driven decision path for CI and other automation.
func (runner *discoverySessionRunner) advanceReview(snapshot *cliapi.DiscoverySnapshot) (*cliapi.DiscoverySnapshot, bool, error) {
	if runner.usesBrowserReview() {
		next, err := runner.waitForBrowserReview(snapshot)
		return next, false, err
	}
	next, err := runner.reviewDraft(snapshot)
	return next, false, err
}

// completePlan presents an interactive plan-ready session once before the
// ordinary receipt boundary is committed locally.
func (runner *discoverySessionRunner) completePlan(snapshot *cliapi.DiscoverySnapshot) (*cliapi.DiscoverySnapshot, bool, error) {
	if runner.usesBrowserReview() {
		if err := runner.presentBrowserReview(snapshot); err != nil {
			return snapshot, false, err
		}
	}
	return snapshot, true, nil
}

// usesBrowserReview requires either an interactive terminal or an explicit
// printed-link request, and never turns CI/--no-input into a waiting workflow.
func (runner *discoverySessionRunner) usesBrowserReview() bool {
	if nonInteractive() || runner.options.jsonOut {
		// Machine-readable and explicit no-input modes must reach plan_ready only
		// through their typed flags and must never receive browser prose.
		return false
	}
	return runner.options.noBrowser || discoveryBrowserInteractive()
}

// waitForBrowserReview presents one stable handoff and then treats Registry
// events only as signals to reload the authoritative session snapshot.
func (runner *discoverySessionRunner) waitForBrowserReview(snapshot *cliapi.DiscoverySnapshot) (*cliapi.DiscoverySnapshot, error) {
	if err := runner.presentBrowserReview(snapshot); err != nil {
		return nil, err
	}
	return runner.waitForSnapshot(snapshot)
}

// presentBrowserReview prints a reusable authenticated URL and opens it only
// for an interactive terminal that did not request --no-browser.
func (runner *discoverySessionRunner) presentBrowserReview(snapshot *cliapi.DiscoverySnapshot) error {
	if runner.reviewLinkPresented {
		return nil
	}
	reviewURL, err := discoveryReviewURL(runner.client.BaseURL, snapshot.SessionID)
	if err != nil {
		return err
	}
	runner.reviewLinkPresented = true
	fmt.Fprintf(runner.cmd.OutOrStdout(), "Review this contract in your browser:\n%s\n", reviewURL)
	if !runner.usesBrowserReview() || runner.options.noBrowser {
		return nil
	}
	if err := openDiscoveryBrowser(runner.ctx, reviewURL); err != nil {
		fmt.Fprintln(runner.cmd.OutOrStdout(), "The browser could not be opened automatically; use the URL above.")
	}
	return nil
}

// discoveryReviewURL preserves an Engine base path while binding the UI to one
// opaque session; authentication and tenant authorization remain browser-side.
func discoveryReviewURL(engineURL, sessionID string) (string, error) {
	if err := validateDiscoveryResumeSessionID(sessionID); err != nil {
		return "", err
	}
	parsed, err := url.Parse(strings.TrimSpace(engineURL))
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Opaque != "" {
		return "", errors.New("Engine returned an invalid browser review base URL")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/integrations"
	parsed.RawPath, parsed.RawQuery, parsed.Fragment = "", "", ""
	parsed.RawQuery = url.Values{"handoff": {"cli"}, "session": {sessionID}, "tab": {"pending"}}.Encode()
	return parsed.String(), nil
}

// waitForSnapshot uses SSE for progress and reloads GET because the stream is never the source of truth.
func (runner *discoverySessionRunner) waitForSnapshot(current *cliapi.DiscoverySnapshot) (*cliapi.DiscoverySnapshot, error) {
	for {
		streamErr := runner.client.StreamDiscovery(runner.ctx, current.SessionID, func(event cliapi.DiscoveryEvent) error {
			return runner.handleEvent(current.Revision, event)
		})
		next, reloadErr := runner.client.GetDiscoverySession(runner.ctx, current.SessionID)
		if reloadErr != nil {
			return nil, reloadErr
		}
		if next.Revision != current.Revision || next.State != current.State {
			return next, nil
		}
		// The Engine proxy can close a healthy, idle browser-review stream at its
		// own timeout. Only that exact transport EOF is safe to reconnect.
		if errors.Is(streamErr, io.ErrUnexpectedEOF) {
			if err := waitForDiscoveryStreamReconnect(runner.ctx); err != nil {
				return nil, err
			}
			continue
		}
		if streamErr != nil && !errors.Is(streamErr, errDiscoverySnapshotRequired) {
			return nil, streamErr
		}
		return nil, errors.New("discovery stream ended without an authoritative state change")
	}
}

// waitForDiscoveryStreamReconnect prevents proxy EOF recovery from becoming a
// busy loop and remains bounded by the command's discovery context.
func waitForDiscoveryStreamReconnect(ctx context.Context) error {
	timer := time.NewTimer(discoveryStreamReconnectDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// handleEvent prints bounded diagnostics and reloads only after Registry
// announces a revision newer than the authoritative snapshot already held.
func (runner *discoverySessionRunner) handleEvent(currentRevision uint64, event cliapi.DiscoveryEvent) error {
	if payload, err := cliapi.DecodeDiscoveryPayload(event.Payload); err == nil {
		printDiscoveryDiagnostics(runner.cmd.ErrOrStderr(), payload.Diagnostics)
	}
	// Registry replays the current envelope on every SSE connection. Its state
	// may be decision-owned, but an equal revision contains no new authority.
	if event.Revision > currentRevision {
		return errDiscoverySnapshotRequired
	}
	return nil
}

// selectOperations resolves an exact allowlist and sends the typed selection action.
func (runner *discoverySessionRunner) selectOperations(snapshot *cliapi.DiscoverySnapshot) (*cliapi.DiscoverySnapshot, error) {
	payload, err := cliapi.DecodeDiscoveryPayload(snapshot.Payload)
	if err != nil {
		return nil, err
	}
	selected, err := resolveDiscoverySelection(runner.cmd, payload, runner.options)
	if err != nil {
		return nil, err
	}
	actionPayload, err := json.Marshal(struct {
		Operations []discoveryOperationSelection `json:"operations"`
	}{Operations: discoveryOperationSelections(selected)})
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(runner.cmd.OutOrStdout(), "Selected %d operations for extraction\n", len(selected))
	return runner.applyAction(snapshot, cliapi.DiscoveryActionSelectOperations, actionPayload)
}

// discoveryOperationSelection is the exact action shape accepted by Registry;
// preview-only summaries and occurrence counts must never enter authorization.
type discoveryOperationSelection struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// discoveryOperationSelections strips presentation metadata before the strict
// action decoder authorizes the selected method/path coordinates.
func discoveryOperationSelections(operations []cliapi.DiscoveryOperation) []discoveryOperationSelection {
	result := make([]discoveryOperationSelection, len(operations))
	for index, operation := range operations {
		result[index] = discoveryOperationSelection{Method: operation.Method, Path: operation.Path}
	}
	return result
}

// resolveDiscoverySelection requires explicit automation and gives interactive users a real preview.
func resolveDiscoverySelection(cmd *cobra.Command, payload cliapi.DiscoveryPayload, options importDiscoveryOptions) ([]cliapi.DiscoveryOperation, error) {
	if len(payload.Operations) == 0 || payload.MaxSelections <= 0 {
		return nil, errors.New("Registry did not provide selectable operations and an authoritative selection limit")
	}
	if options.selectAll {
		return validateDiscoverySelection(payload.Operations, payload.MaxSelections)
	}
	if len(options.selectors) > 0 {
		return selectedDiscoveryOperations(payload.Operations, options.selectors, payload.MaxSelections)
	}
	return reviewDiscoveryOperations(cmd, payload.Operations, payload.MaxSelections)
}

// validateDiscoverySelection rejects empty or oversized batches instead of truncating a contract silently.
func validateDiscoverySelection(operations []cliapi.DiscoveryOperation, maximum int) ([]cliapi.DiscoveryOperation, error) {
	if len(operations) == 0 {
		return nil, errors.New("at least one operation must be selected")
	}
	if len(operations) > maximum {
		return nil, fmt.Errorf("selected %d operations, but Registry permits at most %d", len(operations), maximum)
	}
	return exactOperationProjection(operations), nil
}

// selectedDiscoveryOperations matches every flag against the complete discovered set exactly once.
func selectedDiscoveryOperations(operations []cliapi.DiscoveryOperation, selectors []string, maximum int) ([]cliapi.DiscoveryOperation, error) {
	byKey := make(map[string]cliapi.DiscoveryOperation, len(operations))
	for _, operation := range operations {
		byKey[discoveryOperationKey(operation.Method, operation.Path)] = operation
	}
	selected := make([]cliapi.DiscoveryOperation, 0, len(selectors))
	seen := make(map[string]struct{}, len(selectors))
	for _, selector := range selectors {
		key, err := parseDiscoverySelector(selector)
		if err != nil {
			return nil, err
		}
		operation, found := byKey[key]
		if !found {
			return nil, fmt.Errorf("selected operation was not discovered: %s", key)
		}
		if _, duplicate := seen[key]; !duplicate {
			selected = append(selected, operation)
			seen[key] = struct{}{}
		}
	}
	return validateDiscoverySelection(selected, maximum)
}

// parseDiscoverySelector canonicalizes the CLI METHOD:/path notation without accepting fuzzy matches.
func parseDiscoverySelector(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	method, path, found := strings.Cut(value, ":")
	if !found {
		fields := strings.Fields(value)
		if len(fields) != 2 {
			return "", fmt.Errorf("invalid --select %q; expected METHOD:/path", raw)
		}
		method, path = fields[0], fields[1]
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	path = strings.TrimSpace(path)
	if method == "" || !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("invalid --select %q; expected METHOD:/path", raw)
	}
	return discoveryOperationKey(method, path), nil
}

// reviewDiscoveryOperations presents every operation while keeping the submitted value an exact coordinate.
func reviewDiscoveryOperations(cmd *cobra.Command, operations []cliapi.DiscoveryOperation, maximum int) ([]cliapi.DiscoveryOperation, error) {
	if err := requireInteractive("pass --all or repeat --select METHOD:/path"); err != nil {
		return nil, err
	}
	if !isTerminal(os.Stdin) {
		return nil, errors.New("operation review requires an interactive terminal")
	}
	selectedKeys := make([]string, 0, min(len(operations), maximum))
	options := make([]huh.Option[string], 0, len(operations))
	byKey := make(map[string]cliapi.DiscoveryOperation, len(operations))
	for _, operation := range operations {
		key := discoveryOperationKey(operation.Method, operation.Path)
		byKey[key] = operation
		preselected := len(selectedKeys) < maximum
		if preselected {
			selectedKeys = append(selectedKeys, key)
		}
		options = append(options, huh.NewOption(discoveryOperationLabel(operation), key).Selected(preselected))
	}
	err := huh.NewForm(huh.NewGroup(huh.NewMultiSelect[string]().
		Title("Select operations to extract").Options(options...).Value(&selectedKeys))).
		WithInput(os.Stdin).WithOutput(cmd.ErrOrStderr()).Run()
	if err != nil {
		return nil, err
	}
	selected := make([]cliapi.DiscoveryOperation, 0, len(selectedKeys))
	for _, key := range selectedKeys {
		selected = append(selected, byKey[key])
	}
	return validateDiscoverySelection(selected, maximum)
}

// exactOperationProjection removes presentation fields from the authorization-bearing action payload.
func exactOperationProjection(operations []cliapi.DiscoveryOperation) []cliapi.DiscoveryOperation {
	result := make([]cliapi.DiscoveryOperation, len(operations))
	for index, operation := range operations {
		result[index] = cliapi.DiscoveryOperation{Method: strings.ToUpper(strings.TrimSpace(operation.Method)), Path: strings.TrimSpace(operation.Path)}
	}
	sort.Slice(result, func(left, right int) bool {
		return discoveryOperationKey(result[left].Method, result[left].Path) < discoveryOperationKey(result[right].Method, result[right].Path)
	})
	return result
}

// discoveryOperationKey creates the exact identity shared by flags, preview options, and action payloads.
func discoveryOperationKey(method, path string) string {
	return strings.ToUpper(strings.TrimSpace(method)) + " " + strings.TrimSpace(path)
}

// discoveryOperationLabel keeps descriptive text separate from the exact submitted coordinate.
func discoveryOperationLabel(operation cliapi.DiscoveryOperation) string {
	label := discoveryOperationKey(operation.Method, operation.Path)
	if strings.TrimSpace(operation.Summary) != "" {
		label += " - " + strings.TrimSpace(operation.Summary)
	}
	return label
}

// reviewDraft shows diagnostics, applies reviewed changes, and then requests an immutable plan.
func (runner *discoverySessionRunner) reviewDraft(snapshot *cliapi.DiscoverySnapshot) (*cliapi.DiscoverySnapshot, error) {
	payload, err := cliapi.DecodeDiscoveryPayload(snapshot.Payload)
	if err != nil {
		return nil, err
	}
	printDiscoveryDiagnostics(runner.cmd.ErrOrStderr(), payload.Diagnostics)
	if !runner.overlayApplied && strings.TrimSpace(runner.options.overlayPath) != "" {
		return runner.applyOverlay(snapshot)
	}
	if len(runner.pendingProposalRejections) > 0 {
		pending := runner.pendingProposalRejections
		runner.pendingProposalRejections = nil
		runner.enrichmentDecided = true
		return runner.applyProposalAction(snapshot, cliapi.DiscoveryActionRejectEnrichment, pending)
	}
	if !runner.enrichmentDecided && len(payload.Proposals) > 0 {
		return runner.decideEnrichment(snapshot, payload.Proposals)
	}
	runner.enrichmentDecided = true
	return runner.applyAction(snapshot, cliapi.DiscoveryActionRequestPlan, nil)
}

// applyOverlay submits exact local JSON bytes inside the typed overlay action.
func (runner *discoverySessionRunner) applyOverlay(snapshot *cliapi.DiscoverySnapshot) (*cliapi.DiscoverySnapshot, error) {
	content, err := readDiscoveryOverlay(runner.options.overlayPath)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(struct {
		Overlay json.RawMessage `json:"overlay"`
	}{Overlay: content})
	if err != nil {
		return nil, err
	}
	runner.overlayApplied = true
	return runner.applyAction(snapshot, cliapi.DiscoveryActionUpdateOverlay, payload)
}

// readDiscoveryOverlay requires a local JSON object because the action protocol has one representation.
func readDiscoveryOverlay(path string) (json.RawMessage, error) {
	if isURL(strings.TrimSpace(path)) {
		return nil, errors.New("--overlay requires a local file path")
	}
	content, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("failed to read overlay file %s: %w", path, err)
	}
	trimmed := strings.TrimSpace(string(content))
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' || !json.Valid([]byte(trimmed)) {
		return nil, errors.New("discovery --overlay must contain one JSON object")
	}
	return json.RawMessage(trimmed), nil
}

// decideEnrichment resolves accepted and rejected proposal IDs without silently choosing optional behavior.
func (runner *discoverySessionRunner) decideEnrichment(snapshot *cliapi.DiscoverySnapshot, proposals []cliapi.DiscoveryProposal) (*cliapi.DiscoverySnapshot, error) {
	accepted, rejected, err := resolveEnrichmentDecision(runner.cmd, proposals, runner.options)
	if err != nil {
		return nil, err
	}
	if len(accepted) > 0 {
		runner.pendingProposalRejections = rejected
		runner.enrichmentDecided = len(rejected) == 0
		return runner.applyProposalAction(snapshot, cliapi.DiscoveryActionAcceptEnrichment, accepted)
	}
	runner.enrichmentDecided = true
	return runner.applyProposalAction(snapshot, cliapi.DiscoveryActionRejectEnrichment, rejected)
}

// resolveEnrichmentDecision validates flags against proposal identities or opens an interactive selector.
func resolveEnrichmentDecision(cmd *cobra.Command, proposals []cliapi.DiscoveryProposal, options importDiscoveryOptions) ([]string, []string, error) {
	allIDs, byID, err := proposalIdentitySet(proposals)
	if err != nil {
		return nil, nil, err
	}
	if options.rejectEnrichment {
		return nil, allIDs, nil
	}
	if len(options.acceptedProposals) > 0 {
		accepted, err := exactProposalIDs(options.acceptedProposals, byID)
		if err != nil {
			return nil, nil, err
		}
		return accepted, differenceStrings(allIDs, accepted), nil
	}
	return reviewEnrichmentProposals(cmd, proposals, allIDs)
}

// proposalIdentitySet rejects duplicate or empty proposal IDs before they can drive actions.
func proposalIdentitySet(proposals []cliapi.DiscoveryProposal) ([]string, map[string]cliapi.DiscoveryProposal, error) {
	byID := make(map[string]cliapi.DiscoveryProposal, len(proposals))
	for _, proposal := range proposals {
		if strings.TrimSpace(proposal.ID) == "" || proposal.ID != strings.TrimSpace(proposal.ID) {
			return nil, nil, errors.New("Registry returned an invalid enrichment proposal ID")
		}
		if _, duplicate := byID[proposal.ID]; duplicate {
			return nil, nil, errors.New("Registry returned duplicate enrichment proposal IDs")
		}
		byID[proposal.ID] = proposal
	}
	identifiers := make([]string, 0, len(byID))
	for identifier := range byID {
		identifiers = append(identifiers, identifier)
	}
	sort.Strings(identifiers)
	return identifiers, byID, nil
}

// exactProposalIDs requires every automation flag to reference an offered proposal exactly.
func exactProposalIDs(requested []string, available map[string]cliapi.DiscoveryProposal) ([]string, error) {
	seen := make(map[string]struct{}, len(requested))
	for _, identifier := range requested {
		if _, found := available[identifier]; !found {
			return nil, fmt.Errorf("enrichment proposal was not offered: %s", identifier)
		}
		seen[identifier] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for identifier := range seen {
		result = append(result, identifier)
	}
	sort.Strings(result)
	return result, nil
}

// reviewEnrichmentProposals requires a choice rather than accepting agent proposals by default.
func reviewEnrichmentProposals(cmd *cobra.Command, proposals []cliapi.DiscoveryProposal, allIDs []string) ([]string, []string, error) {
	if err := requireInteractive("pass --reject-enrichment or repeat --accept-proposal <id>"); err != nil {
		return nil, nil, err
	}
	if !isTerminal(os.Stdin) {
		return nil, nil, errors.New("enrichment review requires an interactive terminal")
	}
	selected := []string{}
	options := make([]huh.Option[string], 0, len(proposals))
	for _, proposal := range proposals {
		label := proposal.ID + " - " + proposal.Extension + " at " + proposal.Pointer
		options = append(options, huh.NewOption(label, proposal.ID))
	}
	err := huh.NewForm(huh.NewGroup(huh.NewMultiSelect[string]().
		Title("Accept optional Fused enrichment proposals").Options(options...).Value(&selected))).
		WithInput(os.Stdin).WithOutput(cmd.ErrOrStderr()).Run()
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(selected)
	return selected, differenceStrings(allIDs, selected), nil
}

// differenceStrings returns stable proposal IDs not present in the accepted set.
func differenceStrings(all, accepted []string) []string {
	set := make(map[string]struct{}, len(accepted))
	for _, identifier := range accepted {
		set[identifier] = struct{}{}
	}
	result := make([]string, 0, len(all))
	for _, identifier := range all {
		if _, found := set[identifier]; !found {
			result = append(result, identifier)
		}
	}
	return result
}

// applyProposalAction encodes one exact accepted or rejected proposal set.
func (runner *discoverySessionRunner) applyProposalAction(snapshot *cliapi.DiscoverySnapshot, action cliapi.DiscoveryAction, identifiers []string) (*cliapi.DiscoverySnapshot, error) {
	if len(identifiers) == 0 {
		return snapshot, nil
	}
	payload, err := json.Marshal(struct {
		ProposalIDs []string `json:"proposal_ids"`
	}{ProposalIDs: identifiers})
	if err != nil {
		return nil, err
	}
	return runner.applyAction(snapshot, action, payload)
}

// applyAction binds every mutation to the current session and draft revisions.
func (runner *discoverySessionRunner) applyAction(snapshot *cliapi.DiscoverySnapshot, action cliapi.DiscoveryAction, payload json.RawMessage) (*cliapi.DiscoverySnapshot, error) {
	request := cliapi.DiscoveryActionRequest{
		Version: cliapi.DiscoveryProtocolVersion, SessionID: snapshot.SessionID,
		ExpectedRevision: snapshot.Revision, DraftRevision: snapshot.DraftRevision,
		Action: action, Payload: payload,
	}
	return runner.client.ApplyDiscoveryAction(runner.ctx, snapshot.SessionID, request)
}

// discoveryFailure returns the Registry's bounded code without exposing source or provider content.
func discoveryFailure(snapshot *cliapi.DiscoverySnapshot) error {
	payload, err := cliapi.DecodeDiscoveryPayload(snapshot.Payload)
	if err != nil || strings.TrimSpace(payload.FailureCode) == "" {
		return errors.New("discovery session failed")
	}
	return fmt.Errorf("discovery session failed: %s", payload.FailureCode)
}

// finish writes the ordinary import receipt and never calls the apply endpoint implicitly.
func (runner *discoverySessionRunner) finish(snapshot *cliapi.DiscoverySnapshot) error {
	payload, err := cliapi.DecodeDiscoveryPayload(snapshot.Payload)
	if err != nil {
		return err
	}
	if payload.Plan == nil || strings.TrimSpace(payload.Plan.PlanID) == "" || strings.TrimSpace(payload.Plan.ReviewHash) == "" {
		return errors.New("plan-ready discovery snapshot omitted plan_id or review_hash")
	}
	receipt := importPlanReceipt{
		Slug: strings.TrimSpace(runner.options.slug), PlanID: payload.Plan.PlanID,
		ReviewHash: payload.Plan.ReviewHash, EngineURL: canonicalEngineURLOrRaw(runner.client.BaseURL),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	path := strings.TrimSpace(runner.options.receiptOut)
	if path == "" {
		path = defaultImportReceiptPath
	}
	if err := writeImportPlanReceiptFile(path, receipt); err != nil {
		return err
	}
	if runner.options.jsonOut {
		return json.NewEncoder(runner.cmd.OutOrStdout()).Encode(snapshot)
	}
	fmt.Fprintf(runner.cmd.OutOrStdout(), "Import plan ready: %s\nReview hash: %s\nReceipt: %s\nRun `fused-cli import apply` to create or update the service.\n", payload.Plan.PlanID, payload.Plan.ReviewHash, path)
	return nil
}

// printDiscoveryDiagnostics renders fixed Registry review messages without interpreting their codes.
func printDiscoveryDiagnostics(output io.Writer, diagnostics []cliapi.DiscoveryDiagnostic) {
	for _, diagnostic := range diagnostics {
		fmt.Fprintf(output, "%s [%s] %s\n", strings.ToUpper(diagnostic.Severity), diagnostic.Code, diagnostic.Message)
	}
}

// isTerminal reports whether a file is an interactive character device.
func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	// A mode-only test misclassifies /dev/null as interactive; the terminal
	// probe checks the actual descriptor capabilities used by prompt libraries.
	return charmterm.IsTerminal(file.Fd())
}
