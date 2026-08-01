package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	cliapi "github.com/Usefused/cli/internal/api"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var (
	importDocsName    string
	importDocsSlug    string
	importDocsURL     string
	importDocsVersion string
	importDocsReview  bool
	importDocsSelect  []string
	importDocsTimeout time.Duration
	importDocsNoAddWS bool
)

var errImportDocsComplete = errors.New("integration docs extraction complete")

var importDocsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Import endpoints from an API documentation URL",
	Long: `Extract endpoints from a human-readable API documentation URL using the
same agent-backed flow as the web UI. By default every discovered endpoint is
selected. Use --review to deselect noise interactively, or --select for a
non-interactive partial import.`,
	RunE: WithTelemetry("cli.import.docs", func(cmd *cobra.Command, args []string) error {
		return runImportDocs(cmd, importDocsOptions{
			name:             importDocsName,
			slug:             importDocsSlug,
			url:              importDocsURL,
			version:          importDocsVersion,
			review:           importDocsReview,
			selectors:        importDocsSelect,
			timeout:          importDocsTimeout,
			skipWorkspaceAdd: importDocsNoAddWS,
		})
	}),
}

type importDocsOptions struct {
	name             string
	slug             string
	url              string
	version          string
	review           bool
	selectors        []string
	timeout          time.Duration
	skipWorkspaceAdd bool
}

type docsImportResult struct {
	serviceID string
	version   string
	extracted int
}

func init() {
	importCmd.AddCommand(importDocsCmd)
	importDocsCmd.Flags().StringVar(&importDocsName, "name", "", "Service name (required)")
	importDocsCmd.Flags().StringVar(&importDocsSlug, "slug", "", "Service slug to create (required; unique within your account)")
	importDocsCmd.Flags().StringVar(&importDocsURL, "url", "", "Human-readable API documentation URL (required)")
	importDocsCmd.Flags().StringVar(&importDocsVersion, "version", "", "Provider version for the extracted contract (required)")
	importDocsCmd.Flags().BoolVar(&importDocsReview, "review", false, "Review discovered endpoints before extraction; all are selected by default")
	importDocsCmd.Flags().StringArrayVar(&importDocsSelect, "select", nil, "Endpoint to import as METHOD:/path; repeat for multiple endpoints")
	importDocsCmd.Flags().DurationVar(&importDocsTimeout, "timeout", 20*time.Minute, "Maximum time to wait for discovery and extraction")
	importDocsCmd.Flags().BoolVar(&importDocsNoAddWS, "no-workspace-add", false, "Skip adding the extracted service to the current workspace")
	importDocsCmd.MarkFlagRequired("name")
	importDocsCmd.MarkFlagRequired("slug")
	importDocsCmd.MarkFlagRequired("url")
	importDocsCmd.MarkFlagRequired("version")
}

func runImportDocs(cmd *cobra.Command, opts importDocsOptions) error {
	if err := validateImportDocsOptions(opts); err != nil {
		return err
	}
	client, err := getAPIClientWithTimeout(opts.timeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), opts.timeout)
	defer cancel()
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("service_slug", strings.TrimSpace(opts.slug)),
		attribute.String("service_version", strings.TrimSpace(opts.version)),
		attribute.String("selection_mode", docsImportSelectionMode(opts)),
		attribute.Bool("workspace_add_requested", !opts.skipWorkspaceAdd),
	)

	start, err := client.StartIntegrationExtraction(ctx, cliapi.IntegrationExtractionStartRequest{
		Name:         opts.name,
		ServiceSlug:  strings.TrimSpace(opts.slug),
		Version:      strings.TrimSpace(opts.version),
		SourceURL:    strings.TrimSpace(opts.url),
		ImportMethod: "docs",
		TargetType:   "endpoints",
	})
	if err != nil {
		return err
	}
	span.AddEvent("cli.import.docs.session_started", trace.WithAttributes(attribute.String("session_id", start.SessionID)))

	fmt.Fprintf(cmd.OutOrStdout(), "Started docs extraction session %s\n", start.SessionID)
	result, err := runImportDocsSession(ctx, cmd, client, start.SessionID, opts)
	if err != nil {
		return err
	}
	if result.serviceID == "" {
		return errors.New("docs extraction completed without a service ID")
	}
	if !opts.skipWorkspaceAdd {
		if err := addDocsImportToWorkspace(ctx, client, result, opts); err != nil {
			span.AddEvent("cli.import.docs.workspace_add_failed", trace.WithAttributes(attribute.String("service_id", result.serviceID)))
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: extraction succeeded, but workspace registration failed: %v\n", err)
		} else {
			span.AddEvent("cli.import.docs.workspace_added", trace.WithAttributes(attribute.String("service_id", result.serviceID)))
		}
	}
	span.AddEvent("cli.import.docs.completed", trace.WithAttributes(
		attribute.String("service_id", result.serviceID),
		attribute.String("service_version", result.version),
		attribute.Int("extracted_endpoint_count", result.extracted),
	))
	fmt.Fprintf(cmd.OutOrStdout(), "Imported service %s (version %s, %d endpoints)\n", result.serviceID, result.version, result.extracted)
	return nil
}

func docsImportSelectionMode(opts importDocsOptions) string {
	if len(opts.selectors) > 0 {
		return "explicit"
	}
	if opts.review {
		return "review"
	}
	return "all"
}

func validateImportDocsOptions(opts importDocsOptions) error {
	if strings.TrimSpace(opts.name) == "" {
		return errors.New("--name is required")
	}
	if strings.TrimSpace(opts.slug) == "" {
		return errors.New("--slug is required")
	}
	if strings.TrimSpace(opts.version) == "" {
		return errors.New("--version is required for docs extraction")
	}
	if !isURL(strings.TrimSpace(opts.url)) {
		return errors.New("--url must be an http(s) URL")
	}
	if opts.review && len(opts.selectors) > 0 {
		return errors.New("--review and --select cannot be used together")
	}
	if opts.timeout <= 0 {
		return errors.New("--timeout must be greater than zero")
	}
	return nil
}

func runImportDocsSession(ctx context.Context, cmd *cobra.Command, client *cliapi.Client, sessionID string, opts importDocsOptions) (docsImportResult, error) {
	runner := &docsImportSessionRunner{ctx: ctx, cmd: cmd, client: client, sessionID: sessionID, opts: opts}
	err := client.StreamIntegrationExtraction(ctx, sessionID, runner.handleEvent)
	if errors.Is(err, errImportDocsComplete) {
		return runner.result, nil
	}
	return runner.result, err
}

type docsImportSessionRunner struct {
	ctx       context.Context
	cmd       *cobra.Command
	client    *cliapi.Client
	sessionID string
	opts      importDocsOptions
	result    docsImportResult
	responded bool
}

func (r *docsImportSessionRunner) handleEvent(event cliapi.IntegrationExtractionEvent) error {
	switch event.Type {
	case "connected":
		return nil
	case "thinking", "extraction_started":
		printNonEmptyLine(r.cmd.OutOrStdout(), event.Message)
	case "awaiting_input":
		return r.handleAwaitingInput(event)
	case "extracted":
		r.result.extracted++
		printExtractedEndpoint(r.cmd.OutOrStdout(), event)
	case "complete":
		return r.handleComplete(event)
	case "error":
		return docsImportEventError(event)
	}
	return nil
}

func (r *docsImportSessionRunner) handleAwaitingInput(event cliapi.IntegrationExtractionEvent) error {
	if r.responded {
		return nil
	}
	selected, err := resolveDocsImportSelection(r.cmd, event.Questions, r.opts)
	if err != nil {
		return err
	}
	answer, err := buildDocsImportAnswer(selected)
	if err != nil {
		return err
	}
	if err := r.client.RespondIntegrationExtraction(r.ctx, r.sessionID, answer); err != nil {
		return err
	}
	r.responded = true
	trace.SpanFromContext(r.ctx).AddEvent("cli.import.docs.endpoints_selected", trace.WithAttributes(
		attribute.Int("selected_endpoint_count", len(selected)),
		attribute.String("selection_mode", docsImportSelectionMode(r.opts)),
	))
	fmt.Fprintf(r.cmd.OutOrStdout(), "Extracting %d selected endpoints...\n", len(selected))
	return nil
}

func (r *docsImportSessionRunner) handleComplete(event cliapi.IntegrationExtractionEvent) error {
	r.result.serviceID = event.IntegrationID
	r.result.version = event.Version
	if r.result.version == "" {
		r.result.version = r.opts.version
	}
	return errImportDocsComplete
}

func docsImportEventError(event cliapi.IntegrationExtractionEvent) error {
	if event.Message == "" {
		return errors.New("docs extraction failed")
	}
	return errors.New(event.Message)
}

func printNonEmptyLine(out io.Writer, line string) {
	if strings.TrimSpace(line) != "" {
		fmt.Fprintln(out, line)
	}
}

func printExtractedEndpoint(out io.Writer, event cliapi.IntegrationExtractionEvent) {
	if event.Payload == nil {
		printNonEmptyLine(out, event.Message)
		return
	}
	fmt.Fprintf(out, "Extracted %s %s\n", strings.ToUpper(event.Payload.Method), event.Payload.Path)
}

func resolveDocsImportSelection(cmd *cobra.Command, questions []cliapi.IntegrationExtractionQuestion, opts importDocsOptions) ([]cliapi.IntegrationEndpointIdentifier, error) {
	endpoints := discoveredDocsEndpoints(questions)
	if len(endpoints) == 0 {
		return nil, errors.New("no endpoints were discovered from the documentation URL")
	}
	// Explicit selections are a CI-friendly allowlist, so they must match
	// discovered endpoints exactly instead of quietly turning into a smaller import.
	if len(opts.selectors) > 0 {
		return selectedDocsEndpointsByFlag(endpoints, opts.selectors)
	}
	if opts.review {
		return reviewDocsEndpoints(cmd, endpoints)
	}
	// The docs importer treats the discovered set as the contract by default;
	// review is opt-in so omitted endpoints never become accidental deletion signals.
	return allDocsEndpoints(endpoints), nil
}

func discoveredDocsEndpoints(questions []cliapi.IntegrationExtractionQuestion) []cliapi.Integration {
	for _, q := range questions {
		if len(q.Endpoints) > 0 {
			return q.Endpoints
		}
	}
	return nil
}

func allDocsEndpoints(endpoints []cliapi.Integration) []cliapi.IntegrationEndpointIdentifier {
	selected := make([]cliapi.IntegrationEndpointIdentifier, 0, len(endpoints))
	for _, endpoint := range endpoints {
		selected = append(selected, docsEndpointIdentifier(endpoint))
	}
	return selected
}

func selectedDocsEndpointsByFlag(endpoints []cliapi.Integration, selectors []string) ([]cliapi.IntegrationEndpointIdentifier, error) {
	byKey := make(map[string]cliapi.IntegrationEndpointIdentifier, len(endpoints))
	for _, endpoint := range endpoints {
		id := docsEndpointIdentifier(endpoint)
		byKey[docsEndpointKey(id.Method, id.Path)] = id
	}
	var selected []cliapi.IntegrationEndpointIdentifier
	var missing []string
	seen := map[string]bool{}
	for _, raw := range selectors {
		method, path, err := parseDocsEndpointSelector(raw)
		if err != nil {
			return nil, err
		}
		key := docsEndpointKey(method, path)
		if seen[key] {
			continue
		}
		seen[key] = true
		if endpoint, ok := byKey[key]; ok {
			selected = append(selected, endpoint)
		} else {
			missing = append(missing, strings.ToUpper(method)+" "+path)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("selected endpoints were not discovered: %s", strings.Join(missing, ", "))
	}
	if len(selected) == 0 {
		return nil, errors.New("at least one endpoint must be selected")
	}
	return selected, nil
}

func parseDocsEndpointSelector(raw string) (string, string, error) {
	value := strings.TrimSpace(raw)
	var method, path string
	if left, right, ok := strings.Cut(value, ":"); ok {
		method, path = left, right
	} else if fields := strings.Fields(value); len(fields) == 2 {
		method, path = fields[0], fields[1]
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	path = strings.TrimSpace(path)
	if method == "" || path == "" || !strings.HasPrefix(path, "/") {
		return "", "", fmt.Errorf("invalid --select %q; expected METHOD:/path", raw)
	}
	return method, path, nil
}

func reviewDocsEndpoints(cmd *cobra.Command, endpoints []cliapi.Integration) ([]cliapi.IntegrationEndpointIdentifier, error) {
	if err := requireInteractive("replace --review with one or more --select METHOD:/path flags"); err != nil {
		return nil, err
	}
	if !isTerminal(os.Stdin) {
		return nil, errors.New("--review requires an interactive terminal")
	}
	selectedKeys := make([]string, 0, len(endpoints))
	options := make([]huh.Option[string], 0, len(endpoints))
	byKey := make(map[string]cliapi.IntegrationEndpointIdentifier, len(endpoints))
	for _, endpoint := range endpoints {
		id := docsEndpointIdentifier(endpoint)
		key := docsEndpointKey(id.Method, id.Path)
		byKey[key] = id
		selectedKeys = append(selectedKeys, key)
		options = append(options, huh.NewOption(docsEndpointLabel(endpoint), key).Selected(true))
	}
	err := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Select endpoints to extract").
			Options(options...).
			Value(&selectedKeys),
	)).WithTheme(huh.ThemeBase()).Run()
	if err != nil {
		return nil, err
	}
	if len(selectedKeys) == 0 {
		return nil, errors.New("at least one endpoint must be selected")
	}
	selected := make([]cliapi.IntegrationEndpointIdentifier, 0, len(selectedKeys))
	for _, key := range selectedKeys {
		if endpoint, ok := byKey[key]; ok {
			selected = append(selected, endpoint)
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Selected %d of %d discovered endpoints\n", len(selected), len(endpoints))
	return selected, nil
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

func docsEndpointIdentifier(endpoint cliapi.Integration) cliapi.IntegrationEndpointIdentifier {
	return cliapi.IntegrationEndpointIdentifier{
		Method: strings.ToUpper(strings.TrimSpace(endpoint.Method)),
		Path:   strings.TrimSpace(endpoint.Path),
		Name:   strings.TrimSpace(endpoint.Name),
	}
}

func docsEndpointKey(method, path string) string {
	return strings.ToUpper(strings.TrimSpace(method)) + " " + strings.TrimSpace(path)
}

func docsEndpointLabel(endpoint cliapi.Integration) string {
	label := docsEndpointKey(endpoint.Method, endpoint.Path)
	if strings.TrimSpace(endpoint.Name) != "" {
		label += " - " + strings.TrimSpace(endpoint.Name)
	}
	return label
}

func buildDocsImportAnswer(selected []cliapi.IntegrationEndpointIdentifier) (string, error) {
	payload, err := json.Marshal(struct {
		SelectedItems []cliapi.IntegrationEndpointIdentifier `json:"selected_items"`
	}{SelectedItems: selected})
	if err != nil {
		return "", err
	}
	wrapped, err := json.Marshal(map[string]string{"preview": string(payload)})
	if err != nil {
		return "", err
	}
	return string(wrapped), nil
}

func addDocsImportToWorkspace(ctx context.Context, client *cliapi.Client, result docsImportResult, opts importDocsOptions) error {
	return client.AddWorkspaceService(ctx, cliapi.WorkspaceServiceAddRequest{
		ServiceID:   result.serviceID,
		ServiceName: opts.name,
		VersionTag:  result.version,
	})
}
