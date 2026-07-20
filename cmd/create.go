package cmd

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/charmbracelet/huh"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var description string
var outputDir string
var sdkName string
var sdkVersion string
var targetType string
var targetLanguage string
var deploy bool
var autoYes bool
var bucketName string

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new SDK",
	Run: func(cmd *cobra.Command, args []string) {
		// cmd is passed through (rather than referencing the package-level
		// createCmd var from inside runCreate) to avoid a var-initialization
		// cycle: createCmd's own initializer captures this closure.
		runCreate(cmd)
	},
}

func init() {
	createCmd.Flags().StringVarP(&sdkName, "name", "n", "", "Name of the generated SDK (e.g., 'stripe-sdk')")
	createCmd.Flags().StringVarP(&sdkVersion, "version", "v", "1.0.0", "Version of the generated SDK")
	createCmd.Flags().StringVarP(&targetType, "type", "t", "sdk", "Target type for the SDK (e.g., 'sdk', 'mcp')")
	createCmd.Flags().StringVarP(&targetLanguage, "language", "l", "typescript", "Target language for the SDK (e.g., 'typescript', 'python')")
	// --deploy is deprecated and now a no-op: MCP creation always deploys (see
	// isMCPTarget/validateCreateFlags below). Kept registered so existing
	// scripts that pass it don't break on flag parsing.
	createCmd.Flags().BoolVar(&deploy, "deploy", false, "Deprecated, no-op: MCP servers are always deployed to the Engine now")
	createCmd.Flags().BoolVarP(&autoYes, "yes", "y", false, "Skip interactive menu and automatically proceed")
	createCmd.Flags().StringVarP(&description, "description", "d", "", "Description of the SDK to create (e.g. 'Create a stripe and plunk sdk')")
	createCmd.Flags().StringVarP(&outputDir, "output", "o", ".", "Directory to save the generated SDK zip")
	createCmd.Flags().StringVarP(&bucketName, "bucket", "b", "", "Secret bucket the deployed MCP server resolves credentials from (mcp target only; defaults to the workspace's default bucket)")

	createCmd.MarkFlagRequired("name")

	RootCmd.AddCommand(createCmd)
}

func searchAndAddEndpoints(client *api.Client, searchString string, currentCart map[string]api.Integration, servicesMap map[string]api.Service) {
	// fmt.Printf("🧠 Parsing intent using AI for: %q...\n", searchString)
	intent, err := client.ParseSDKIntent(searchString)
	if err != nil {
		fmt.Printf("Failed to parse intent: %v\n", err)
		return
	}

	if len(intent.Services) == 0 {
		fmt.Println("No services detected in your query.")
		return
	}

	// Fetch workspace services once to avoid redundant network calls
	var intentNames []string
	for _, svc := range intent.Services {
		intentNames = append(intentNames, svc.Name)
	}

	workspaceServices, err := client.ListWorkspaceServices(intentNames...)
	if err != nil {
		// Non-fatal: if we can't fetch workspace services, we just rely on Registry defaults
		workspaceServices = []api.WorkspaceService{}
	}

	added := 0
	for _, svcIntent := range intent.Services {
		added += processServiceIntent(client, svcIntent, workspaceServices, currentCart, servicesMap)
	}
	fmt.Printf("✅ Added %d new targeted endpoints to the cart.\n", added)
}

// processServiceIntent resolves one parsed intent (a service name + endpoint
// query) to a matched service and its endpoints, merging any endpoints not
// already in the cart. Extracted from searchAndAddEndpoints's per-intent loop
// body so both functions stay under the complexity budget, and so the
// service/endpoint resolution steps are unit-testable on their own.
// Returns the number of endpoints newly added, so the caller can keep a
// running total without needing to know how the merge happened.
func processServiceIntent(client *api.Client, svcIntent api.IntentService, workspaceServices []api.WorkspaceService, cart map[string]api.Integration, servicesMap map[string]api.Service) int {
	fmt.Printf("🔍 Searching for service matching %q...\n", svcIntent.Name)
	services, err := client.SearchServices(svcIntent.Name)
	if err != nil || len(services) == 0 {
		fmt.Printf("   -> Could not find service matching %q\n", svcIntent.Name)
		return 0
	}

	// Take the best match (first one)
	s := services[0]
	servicesMap[s.ID] = s

	version, err := resolveServiceVersion(client, s.ID, workspaceServices)
	if err != nil {
		fmt.Printf("   -> ❌ Service %q is not enabled in your workspace.\n", svcIntent.Name)
		fmt.Printf("      To add it, run: fused workspace service add %q\n", svcIntent.Name)
		fmt.Printf("      Then apply the changes: fused workspace apply\n")
		return 0
	}

	fmt.Printf("   -> Found %q (v%s)! Fetching endpoints (intent: %q)...\n", s.Name, version, svcIntent.EndpointQuery)
	endpoints, err := client.SearchEndpoints(s.ID, version, svcIntent.EndpointQuery)
	if err != nil {
		fmt.Printf("Error fetching endpoints for service %s: %v\n", s.Name, err)
		return 0
	}

	return mergeNewEndpoints(cart, endpoints)
}

// mergeNewEndpoints adds any endpoint not already present in cart (keyed by
// endpoint ID) and reports how many were newly added. Pure aside from the
// cart mutation the caller owns, so it's directly unit-testable with plain
// maps/slices -- no network involved.
func mergeNewEndpoints(cart map[string]api.Integration, endpoints []api.Integration) int {
	added := 0
	for _, ep := range endpoints {
		if _, exists := cart[ep.ID]; !exists {
			cart[ep.ID] = ep
			added++
		}
	}
	return added
}

func resolveServiceVersion(client *api.Client, serviceID string, workspaceServices []api.WorkspaceService) (string, error) {
	// First check workspace map
	for _, wsSvc := range workspaceServices {
		if wsSvc.ServiceID == serviceID {
			return wsSvc.Version, nil
		}
	}

	return "", fmt.Errorf("service is not enabled in your workspace")
}

// isMCPTarget is the single source of truth for "is this create call
// producing an MCP server". MCP creation always deploys to the Engine sandbox
// now (no local artifact download path), so this predicate also drives
// SkipSandbox and the post-generation delivery branch below -- keeping it in
// one place avoids the three call sites drifting out of sync (DRY).
func isMCPTarget(targetType string) bool {
	return targetType == "mcp"
}

// validateCreateFlags fails fast on the one unsupported combination: Python
// MCP servers, which the Engine sandbox can't run (server-side guard lives at
// handlers.go's GenerateSDK handler). Pure function, no I/O, so it's directly
// unit-testable without spinning up the interactive cart flow.
func validateCreateFlags(targetType, targetLanguage string) error {
	if isMCPTarget(targetType) && targetLanguage == "python" {
		return fmt.Errorf("Python MCP servers are not supported -- MCP creation deploys to the Engine sandbox, which only runs TypeScript")
	}
	return nil
}

func runCreate(cmd *cobra.Command) {
	if description == "" {
		fmt.Println("Error: --description is required")
		os.Exit(1)
	}

	if err := validateCreateFlags(targetType, targetLanguage); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	warnIfDeployFlagUsed(cmd)

	client, err := newCreateClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	cart, proceed := buildCart(client, description)
	if !proceed {
		return
	}

	// MCP creation no longer generates an SDK at all -- it never touches
	// Registry's /sdks/generate. It resolves the cart's selections directly
	// against the workspace's already-activated service versions and
	// persists an SDKScope on the Engine via ActivateMCPServer. Plain SDK
	// targets are unaffected and keep going through Registry generation.
	if isMCPTarget(targetType) {
		createMCPServer(client, cart)
		return
	}

	generatedSdkID, err := generateSDK(client, buildGenerateRequest(cart))
	if err != nil {
		fmt.Printf("Failed to generate SDK: %v\n", err)
		return
	}
	if generatedSdkID != "" {
		deliverDownloadedSDK(client, sdkName, outputDir, generatedSdkID)
	}
}

// warnIfDeployFlagUsed prints a one-line deprecation notice for --deploy,
// which is now a no-op (MCP creation always deploys -- see isMCPTarget).
// Kept as its own function so the notice logic doesn't add a branch to
// runCreate's already-tight complexity budget.
func warnIfDeployFlagUsed(cmd *cobra.Command) {
	if cmd.Flags().Changed("deploy") {
		fmt.Println("Note: --deploy is deprecated and no longer needed -- MCP servers are always deployed now.")
	}
}

// newCreateClient wires up the API client from stored credentials/config.
// Not unit-tested directly: it's a thin pass-through to GetAPIKey/GetEngineURL,
// which read from the user's local CLI config -- the meaningful logic those
// two functions have is already covered by their own tests.
func newCreateClient() (*api.Client, error) {
	key := GetAPIKey()
	engineURL, err := GetEngineURL()
	if err != nil {
		return nil, err
	}
	return api.NewClient(engineURL, key), nil
}

// buildCart runs the initial search plus the interactive cart-building menu
// (proceed/modify/add/cancel) until the user confirms or cancels. Returns the
// final cart and whether the caller should proceed to generation.
func buildCart(client *api.Client, description string) (map[string]api.Integration, bool) {
	cart := make(map[string]api.Integration)
	services := make(map[string]api.Service)
	searchAndAddEndpoints(client, description, cart, services)

	for {
		if len(cart) == 0 {
			fmt.Println("Cart is empty. Aborting.")
			return cart, false
		}

		printCartSummary(cart, services)

		if autoYes {
			fmt.Println("Auto-confirm enabled. Proceeding to generation...")
			return cart, true
		}

		action, err := promptCartAction()
		if err != nil {
			fmt.Printf("Menu error: %v\n", err)
			return cart, false
		}

		switch action {
		case "cancel":
			fmt.Println("Cancelled.")
			return cart, false
		case "proceed":
			return cart, true
		case "modify":
			cart = modifyCartSelection(cart, services)
		case "add":
			promptAndAddMore(client, cart, services)
		}
	}
}

// printCartSummary renders the current cart grouped by service, capping the
// per-service listing at 5 endpoints so a large cart doesn't flood the
// terminal.
func printCartSummary(cart map[string]api.Integration, services map[string]api.Service) {
	fmt.Println("\n📦 --- CURRENT SDK CART ---")
	for svcID, eps := range groupCartByService(cart) {
		svcName := "Unknown Service"
		if s, ok := services[svcID]; ok {
			svcName = s.Name
		}
		fmt.Printf("🔸 %s (%d endpoints)\n", svcName, len(eps))
		printEndpointList(eps)
	}
	fmt.Println("---------------------------")
}

// groupCartByService buckets cart entries by ServiceID. Pure map transform,
// unit-tested directly.
func groupCartByService(cart map[string]api.Integration) map[string][]api.Integration {
	grouped := make(map[string][]api.Integration)
	for _, ep := range cart {
		grouped[ep.ServiceID] = append(grouped[ep.ServiceID], ep)
	}
	return grouped
}

// printEndpointList prints up to 5 endpoints, then a "...and N more" line.
func printEndpointList(eps []api.Integration) {
	for i, ep := range eps {
		if i < 5 {
			fmt.Printf("    - %s %s (%s)\n", ep.Method, ep.Path, ep.Name)
		} else if i == 5 {
			fmt.Printf("    - ... and %d more\n", len(eps)-5)
			break
		}
	}
}

// promptCartAction shows the interactive menu and returns the chosen action.
// Not unit-tested: it's a direct passthrough to an interactive TTY prompt
// (huh), which reads real terminal input and has nothing else to assert on.
func promptCartAction() (string, error) {
	var action string
	err := huh.NewSelect[string]().
		Title("What would you like to do?").
		Options(
			huh.NewOption("🚀 Proceed to Generate SDK", "proceed"),
			huh.NewOption("⚙️  Modify Selection (Select/Deselect endpoints)", "modify"),
			huh.NewOption("➕ Add More Endpoints (Refine search)", "add"),
			huh.NewOption("❌ Cancel", "cancel"),
		).
		Value(&action).
		Run()
	return action, err
}

// modifyCartSelection prompts a multi-select of the current cart and returns
// the rebuilt cart. On prompt error/cancel, the cart is returned unchanged
// (matching the original behavior).
func modifyCartSelection(cart map[string]api.Integration, services map[string]api.Service) map[string]api.Integration {
	var options []huh.Option[string]
	for id, ep := range cart {
		svcName := services[ep.ServiceID].Name
		options = append(options, huh.NewOption(fmt.Sprintf("[%s] %s (%s %s)", svcName, ep.Name, ep.Method, ep.Path), id).Selected(true))
	}

	var selectedIDs []string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Select endpoints to KEEP in the SDK").
				Options(options...).
				Value(&selectedIDs).
				Height(15),
		),
	).WithTheme(huh.ThemeBase()).Run()

	if err != nil {
		return cart
	}
	return rebuildCart(cart, selectedIDs)
}

// rebuildCart keeps only the selected IDs from cart. Pure, unit-tested.
func rebuildCart(cart map[string]api.Integration, selectedIDs []string) map[string]api.Integration {
	newCart := make(map[string]api.Integration)
	for _, id := range selectedIDs {
		newCart[id] = cart[id]
	}
	return newCart
}

// promptAndAddMore prompts for an additional free-text description and, if
// given, searches for and merges more endpoints into the cart.
func promptAndAddMore(client *api.Client, cart map[string]api.Integration, services map[string]api.Service) {
	var newDesc string
	err := huh.NewInput().
		Title("Enter additional description (e.g. 'stripe refunds')").
		Value(&newDesc).
		Run()

	if err != nil || newDesc == "" {
		return
	}
	searchAndAddEndpoints(client, newDesc, cart, services)
}

// groupEndpointIDsByService buckets the cart's endpoint IDs by ServiceID.
// Shared by buildSelections (Registry SDK generation) and
// resolveMCPSelections (direct Engine activation) -- both need the same
// grouping, just packaged into a different selection type afterward.
func groupEndpointIDsByService(cart map[string]api.Integration) map[string][]string {
	grouped := make(map[string][]string)
	for id, ep := range cart {
		grouped[ep.ServiceID] = append(grouped[ep.ServiceID], id)
	}
	return grouped
}

// buildSelections groups the cart's endpoint IDs by service for the
// generation request payload. Pure, unit-tested.
func buildSelections(cart map[string]api.Integration) []api.SDKSelection {
	var selections []api.SDKSelection
	for sid, eids := range groupEndpointIDsByService(cart) {
		selections = append(selections, api.SDKSelection{
			ServiceID:   sid,
			EndpointIDs: eids,
		})
	}
	return selections
}

// buildGenerateRequest assembles the GenerateSDK request from the confirmed
// cart and the create command's flags. Only reached for plain "sdk" targets
// now -- mcp targets never call Registry generation (see createMCPServer).
func buildGenerateRequest(cart map[string]api.Integration) api.GenerateSDKRequest {
	return api.GenerateSDKRequest{
		Name:           sdkName,
		Description:    description,
		Version:        sdkVersion,
		TargetType:     targetType,
		TargetLanguage: targetLanguage,
		Selections:     buildSelections(cart),
	}
}

// resolveMCPSelections turns the confirmed cart into Engine-native
// selections, resolving each referenced service's ServiceVersionID from the
// workspace's currently-activated services. Unlike Registry SDK generation
// (which resolves versions itself during codegen), the direct-to-Engine
// activate call has no codegen step to do that resolution for us, so the CLI
// does it with one extra read -- not a generation call, just a lookup.
func resolveMCPSelections(client *api.Client, cart map[string]api.Integration) ([]api.MCPSelection, error) {
	workspaceServices, err := client.ListWorkspaceServices()
	if err != nil {
		return nil, fmt.Errorf("failed to list workspace services: %w", err)
	}
	versionIDs := make(map[string]string, len(workspaceServices))
	for _, ws := range workspaceServices {
		versionIDs[ws.ServiceID] = ws.ServiceVersionID
	}

	grouped := groupEndpointIDsByService(cart)
	selections := make([]api.MCPSelection, 0, len(grouped))
	for sid, eids := range grouped {
		versionID := versionIDs[sid]
		if versionID == "" {
			return nil, fmt.Errorf("service %s has no resolved service_version_id in this workspace -- run 'fused-cli workspace apply' first", sid)
		}
		selections = append(selections, api.MCPSelection{
			ServiceID:        sid,
			ServiceVersionID: versionID,
			EndpointIDs:      eids,
		})
	}
	return selections, nil
}

// generateSDK kicks off generation and streams progress until a terminal
// event arrives, returning the generated SDK ID (empty if generation didn't
// complete successfully -- matching the original behavior of relying on the
// streamed messages, not a returned error, to explain a stream-side failure).
func generateSDK(client *api.Client, req api.GenerateSDKRequest) (string, error) {
	fmt.Println("\n🚀 Generating SDK...")
	resp, err := client.GenerateSDK(req)
	if err != nil {
		return "", err
	}
	fmt.Printf("Job ID: %s. Streaming progress...\n", resp.JobID)

	eventChan := make(chan api.SDKEvent)
	errChan := make(chan error)
	go client.StreamSDKGenerationEvents(resp.JobID, eventChan, errChan)

	return collectGenerationResult(eventChan, errChan), nil
}

// collectGenerationResult consumes the event/error streams from
// StreamSDKGenerationEvents until a terminal state is reached, returning the
// generated SDK ID on a "complete" event (empty otherwise). Extracted as a
// standalone function -- decoupled from any real HTTP client -- specifically
// so the state machine can be unit-tested by feeding it fake channels.
//
// Both case bodies fall through to the bottom of the loop (no `continue`)
// rather than skipping straight to the next select: a `continue` here would
// let both channels turn nil across two different iterations without the
// "both nil" check ever running in between, which then blocks forever on a
// select with only nil channels -- Go never wakes a select whose every case
// is on a nil channel. Falling through means the check runs every iteration.
func collectGenerationResult(eventChan <-chan api.SDKEvent, errChan <-chan error) string {
	for {
		select {
		case ev, ok := <-eventChan:
			if ok {
				fmt.Printf("[%s] %s\n", ev.Type, ev.Message)
				if result, terminal := terminalEventResult(ev); terminal {
					return result
				}
			} else {
				eventChan = nil
			}
		case errVal, ok := <-errChan:
			if ok {
				if errVal != nil {
					fmt.Printf("Stream error: %v\n", errVal)
					return ""
				}
			} else {
				errChan = nil
			}
		}
		if eventChan == nil && errChan == nil {
			return ""
		}
	}
}

// terminalEventResult reports whether ev ends the generation stream, and if
// so, what SDK ID (if any) to report. Split out of collectGenerationResult so
// the terminal/non-terminal decision -- the part most likely to grow new
// event types over time -- is independently testable and doesn't count
// against the state machine's own complexity budget.
func terminalEventResult(ev api.SDKEvent) (string, bool) {
	switch ev.Type {
	case "complete":
		return ev.IntegrationID, true
	case "error":
		return "", true
	default:
		return "", false
	}
}

// createMCPServer is the entire mcp-target creation path: resolve the cart
// into Engine-native selections, mint a client-side sdkID (there's no
// Registry-issued one anymore -- nothing generated it), and activate
// directly on the Engine. No SDK is generated, downloaded, or built for this
// target type at all.
func createMCPServer(client *api.Client, cart map[string]api.Integration) {
	if len(cart) == 0 {
		fmt.Println("Cart is empty. Aborting.")
		return
	}

	selections, err := resolveMCPSelections(client, cart)
	if err != nil {
		fmt.Printf("Error resolving service versions: %v\n", err)
		return
	}

	sdkID := uuid.NewString()
	fmt.Println("\n🚀 Deploying MCP server...")
	res, err := client.ActivateMCPServer(sdkID, api.MCPActivateRequest{
		Bucket:     bucketName,
		Selections: selections,
		Kind:       "mcp",
		Name:       sdkName,
	})
	if err != nil {
		fmt.Printf("Error activating MCP server on Engine: %v\n", err)
		return
	}

	fmt.Println("✅ MCP Server Deployment Complete.")
	if res == nil || res.MCPURL == "" {
		fmt.Println("MCP URL is not available.")
		return
	}
	fmt.Printf("\n🌐 Engine MCP URL: %s\n", res.MCPURL)
	fmt.Println("\nTo use this MCP Server, configure your client to connect to the above SSE URL.")
	if res.AuthToken != "" {
		fmt.Printf("🔑 Auth token (save this now, it will not be shown again): %s\n", res.AuthToken)
		fmt.Println("Configure your MCP client to send it as: Authorization: Bearer <token>")
	}
	fmt.Println("Per-provider credentials still come from the workspace bucket you deployed against -- nothing else to configure client-side for those.")
}

func deliverDownloadedSDK(client *api.Client, sdkName, outputDir, generatedSdkID string) {
	fmt.Printf("✅ SDK Generation Complete. Downloading SDK %s...\n", generatedSdkID)
	zipData, err := client.DownloadSDK(generatedSdkID)
	if err != nil {
		fmt.Printf("Error downloading SDK: %v\n", err)
		return
	}

	if err := os.MkdirAll(strings.TrimRight(outputDir, "/"), 0755); err != nil {
		fmt.Printf("Error creating output directory: %v\n", err)
		return
	}

	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		fmt.Printf("Error reading zip archive: %v\n", err)
		return
	}

	extractDir := filepath.Join(strings.TrimRight(outputDir, "/"), sdkName)
	if extractDir == "" {
		extractDir = "."
	}

	for _, f := range zipReader.File {
		extractZipEntry(f, extractDir)
	}

	fmt.Printf("🎉 SDK automatically extracted to %s/\n", extractDir)
}

// extractZipEntry writes one zip entry to disk under extractDir. Errors on
// individual entries are swallowed (matching the pre-existing best-effort
// extraction behavior) since one malformed/unwritable entry in a generated
// SDK zip shouldn't abort extraction of the rest.
func extractZipEntry(f *zip.File, extractDir string) {
	fpath := filepath.Join(extractDir, f.Name)

	// Check for ZipSlip vulnerability
	if !strings.HasPrefix(fpath, filepath.Clean(extractDir)+string(os.PathSeparator)) {
		return
	}

	if f.FileInfo().IsDir() {
		os.MkdirAll(fpath, os.ModePerm)
		return
	}

	if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
		return
	}

	outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
	if err != nil {
		return
	}
	defer outFile.Close()

	rc, err := f.Open()
	if err != nil {
		return
	}
	defer rc.Close()

	io.Copy(outFile, rc)
}
