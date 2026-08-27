package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

var description string

var sdkName string
var appVersion string
var targetLanguage string
var autoYes bool

// promptCartActionRunner keeps the interactive boundary replaceable in tests
// so cancellation and terminal failures can be verified without a real TTY.
var promptCartActionRunner = promptCartAction

var promptCmd = &cobra.Command{
	Use:   "prompt",
	Short: "Use AI to prompt and generate a new SDK config",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.sdk.prompt", func(cmd *cobra.Command, args []string) error {
		if !autoYes {
			if err := requireInteractive("pass --yes to accept the generated SDK selection without prompts"); err != nil {
				return err
			}
		}
		return runPrompt(cmd)
	}),
}

func init() {
	promptCmd.Flags().StringVarP(&sdkName, "name", "n", "", "Name of the generated SDK (e.g., 'stripe-sdk')")
	promptCmd.Flags().StringVarP(&appVersion, "version", "v", "1.0.0", "Version of the generated SDK")
	promptCmd.Flags().StringVarP(&targetLanguage, "language", "l", "typescript", "Target language for the SDK (e.g., 'typescript', 'python')")
	promptCmd.Flags().BoolVarP(&autoYes, "yes", "y", false, "Skip interactive menu and automatically proceed")
	promptCmd.Flags().StringVarP(&description, "description", "d", "", "Description of the SDK to create (e.g. 'Create a stripe and plunk sdk')")

	promptCmd.MarkFlagRequired("name")

	sdkCmd.AddCommand(promptCmd)
}

// searchAndAddEndpoints resolves one natural-language request and persists any
// workspace additions, returning every operational failure to the command.
func searchAndAddEndpoints(client *api.Client, searchString string, currentCart map[string]api.Integration, servicesMap map[string]api.Service, wsServicesMap map[string]api.WorkspaceService) error {
	// Prompt generation depends on the workspace snapshot, so a failed
	// prerequisite sync must stop the command instead of using partial state.
	if err := ensurePromptWorkspace(client, promptWorkspaceTarget()); err != nil {
		return err
	}

	wsPath, wsCfg, err := loadWorkspaceConfigForSync(ConfigFile)
	// Preserve the filesystem error so callers can identify the unreadable
	// workspace path instead of seeing an empty-cart message.
	if err != nil {
		return fmt.Errorf("loading workspace config: %w", err)
	}

	intent, err := client.ParseSDKIntent(searchString)
	// Engine error metadata remains available through wrapping for telemetry and
	// human rendering at the command boundary.
	if err != nil {
		return fmt.Errorf("parsing SDK intent: %w", err)
	}

	// A query with no recognized service cannot produce an SDK and is a
	// validation failure, not a successful cancellation.
	if len(intent.Services) == 0 {
		return fmt.Errorf("no services detected in query %q", searchString)
	}

	added, newWorkspaceServices, err := processPromptServiceIntents(client, intent.Services, wsCfg, currentCart, servicesMap, wsServicesMap)
	// Partial intent results are not persisted because the generated SDK would
	// otherwise conceal whichever service lookup failed.
	if err != nil {
		return err
	}
	// Workspace persistence is part of prompt generation and must be observable
	// as a command failure when the local file cannot be replaced.
	if err := persistPromptWorkspace(wsPath, wsCfg, newWorkspaceServices); err != nil {
		return err
	}

	fmt.Printf("✅ Added %d new targeted endpoints to the cart.\n", added)
	return nil
}

// promptWorkspaceTarget returns the explicit workspace path or the standard
// project-local location used by prerequisite synchronization.
func promptWorkspaceTarget() string {
	// An explicit config path must also be the path checked before syncing.
	if ConfigFile != "" {
		return ConfigFile
	}
	return filepath.Join(".fused", "workspace.yaml")
}

// ensurePromptWorkspace synchronizes only when the requested workspace file
// is absent and exposes prerequisite or filesystem failures to the caller.
func ensurePromptWorkspace(client *api.Client, target string) error {
	_, err := os.Stat(target)
	// An existing workspace needs no network prerequisite.
	if err == nil {
		return nil
	}
	// A stat failure other than absence can hide permission or path problems and
	// should not be reinterpreted as a missing workspace.
	if !os.IsNotExist(err) {
		return fmt.Errorf("checking workspace config %s: %w", target, err)
	}
	fmt.Println("No local workspace config found. Syncing from Engine...")
	// The process execution context lets SIGINT stop the prerequisite
	// sync instead of leaving an agent waiting on abandoned network work.
	if _, err := PerformWorkspaceSync(executionContext, client, ConfigFile); err != nil {
		return fmt.Errorf("syncing workspace config from Engine: %w", err)
	}
	return nil
}

// processPromptServiceIntents resolves intents in order and stops before
// persistence when any service cannot be resolved completely.
func processPromptServiceIntents(client *api.Client, intents []api.IntentService, wsCfg *configfile.WorkspaceConfig, cart map[string]api.Integration, servicesMap map[string]api.Service, wsServicesMap map[string]api.WorkspaceService) (int, []string, error) {
	added := 0
	var newWorkspaceServices []string
	for _, intent := range intents {
		addedEndpoints, serviceAdded, err := processServiceIntent(client, intent, wsCfg, cart, servicesMap, wsServicesMap)
		// A partially-resolved intent set would make the generated config differ
		// silently from the user's request, so propagate the first exact failure.
		if err != nil {
			return added, newWorkspaceServices, err
		}
		added += addedEndpoints
		// Only newly-enabled services require a workspace file write.
		if serviceAdded {
			newWorkspaceServices = append(newWorkspaceServices, intent.Name)
		}
	}
	return added, newWorkspaceServices, nil
}

// persistPromptWorkspace writes newly-enabled services and returns replacement
// failures so prompt never reports a generated SDK against stale workspace state.
func persistPromptWorkspace(path string, cfg *configfile.WorkspaceConfig, addedServices []string) error {
	// No workspace mutation means no file replacement is necessary.
	if len(addedServices) == 0 {
		return nil
	}
	// Atomic write failures are command failures because the SDK will depend on
	// the services that were just selected.
	if err := writeWorkspaceConfig(path, cfg); err != nil {
		return fmt.Errorf("writing updated workspace config %s: %w", path, err)
	}
	fmt.Printf("✅ Automatically added %s to your workspace config.\n", strings.Join(addedServices, ", "))
	return nil
}

// processServiceIntent resolves one service, version, and endpoint query while
// preserving the underlying API error at every remote boundary.
func processServiceIntent(client *api.Client, svcIntent api.IntentService, wsCfg *configfile.WorkspaceConfig, cart map[string]api.Integration, servicesMap map[string]api.Service, wsServicesMap map[string]api.WorkspaceService) (int, bool, error) {
	fmt.Printf("🔍 Searching for service matching %q...\n", svcIntent.Name)
	services, err := client.SearchServices(svcIntent.Name)
	// Keep Engine error codes/details intact instead of coalescing them with an
	// empty search result.
	if err != nil {
		return 0, false, fmt.Errorf("searching for service %q: %w", svcIntent.Name, err)
	}
	// A valid empty response is actionable input feedback rather than an API
	// outage and therefore gets its own specific error.
	if len(services) == 0 {
		return 0, false, fmt.Errorf("no service matched %q", svcIntent.Name)
	}

	s := services[0]
	servicesMap[s.ID] = s

	visMap, err := client.ServiceVisibilities([]string{s.ID})
	// Visibility determines the canonical workspace key, so generation cannot
	// safely continue when that lookup fails.
	if err != nil {
		return 0, false, fmt.Errorf("fetching visibility for service %q: %w", s.Name, err)
	}
	vis, found := visMap[s.ID]
	// A missing canonical reference is a malformed dependency response, not evidence that the service has no published versions.
	if !found || strings.TrimSpace(vis.Slug) == "" {
		return 0, false, fmt.Errorf("visibility response omitted the canonical reference for service %q", s.Name)
	}
	key := serviceIntentConfigKey(vis)

	version, svcAdded, err := resolveServiceIntentVersion(client, wsCfg, key, s)
	// Version resolution failures retain the service-specific API or validation cause.
	if err != nil {
		return 0, false, err
	}

	wsServicesMap[s.ID] = api.WorkspaceService{
		ServiceID:   s.ID,
		ServiceSlug: key,
		Version:     version,
	}

	fmt.Printf("   -> Found %q (v%s)! Fetching endpoints (intent: %q)...\n", s.Name, version, svcIntent.EndpointQuery)
	endpoints, err := client.SearchEndpoints(s.ID, version, svcIntent.EndpointQuery)
	// Endpoint search failures must retain their Engine diagnostics at the CLI
	// boundary instead of being printed and converted to success.
	if err != nil {
		return 0, svcAdded, fmt.Errorf("searching endpoints for service %q: %w", s.Name, err)
	}
	// A completed search with no matching operations cannot satisfy this intent.
	if len(endpoints) == 0 {
		return 0, svcAdded, fmt.Errorf("no endpoints matched intent %q for service %q", svcIntent.EndpointQuery, s.Name)
	}

	return mergeNewEndpoints(cart, endpoints), svcAdded, nil
}

// resolveServiceIntentVersion reuses an existing local pin or fetches one
// bounded latest-version summary before adding a new workspace service.
func resolveServiceIntentVersion(client *api.Client, wsCfg *configfile.WorkspaceConfig, key string, service api.Service) (string, bool, error) {
	var version string
	// Existing pins take precedence so prompting never silently upgrades a declared service.
	if existing, ok := wsCfg.Services[key]; ok && len(existing.Versions) > 0 {
		version = existing.Versions[0].Version
	}
	// A local pin avoids a Registry lookup and leaves the workspace unchanged.
	if version != "" {
		return version, false, nil
	}
	// Intent discovery fetches only the newest summary, not every version's large policy payload.
	versions, err := client.ServiceVersionSummaries(key)
	// Preserve API diagnostics separately from a valid empty version list.
	if err != nil {
		return "", false, fmt.Errorf("fetching versions for service %q: %w", service.Name, err)
	}
	// A service without a published version cannot back a generated operation.
	if len(versions) == 0 {
		return "", false, fmt.Errorf("no versions are available for service %q", service.Name)
	}
	version = versions[0].Name
	wsCfg.Services[key] = configfile.WorkspaceService{
		ServiceID: service.ID,
		Versions:  []configfile.WorkspaceServiceVersion{{Version: version}},
	}
	fmt.Printf("   -> 🌟 Automatically added %s (v%s) to workspace config\n", key, version)
	return version, true, nil
}

func serviceIntentConfigKey(vis api.ServiceVisibility) string {
	if vis.IsOwner {
		return vis.Slug
	}
	return serviceConfigRef(vis.Slug, providerHandle(vis.Provider))
}

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

// runPrompt retains the interactive endpoint cart for native SDKs. MCP
// runtimes use the declarative `mcp plan` and `mcp apply` commands instead.
func runPrompt(cmd *cobra.Command) error {
	// The natural-language request is mandatory even though Cobra cannot mark a
	// shared package variable as a positional argument.
	if description == "" {
		return fmt.Errorf("--description is required")
	}

	client, err := newPromptClient()
	// Credential and Engine URL failures already carry their actionable detail.
	if err != nil {
		return err
	}

	cart, wsServicesMap, proceed, err := buildCart(client, description)
	// Operational failures are distinct from the explicit menu cancellation.
	if err != nil {
		return err
	}
	// The cancel action is the sole successful path that skips generation.
	if !proceed {
		return nil
	}
	cfg := promptSDKConfig(cart, wsServicesMap)
	path, err := writePromptSDKConfig(cfg)
	// Config persistence must complete before an applied change is recorded.
	if err != nil {
		return err
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "sdk_config")
	fmt.Printf("✅ SDK config generated at %s\n", path)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  1. Preview changes: fused-cli sdk plan\n")
	fmt.Printf("  2. Apply changes:   fused-cli sdk apply\n")
	return nil
}

func promptSDKConfig(cart map[string]api.Integration, wsServices map[string]api.WorkspaceService) *configfile.SDKConfig {
	cfg := &configfile.SDKConfig{
		BaseConfig: configfile.BaseConfig{
			APIVersion: configfile.APIVersionV1,
			Kind:       configfile.KindSDK,
		},
		Name:     sdkName,
		Version:  appVersion,
		Language: targetLanguage,
		Services: make(map[string]configfile.AppService),
	}

	for _, ep := range cart {
		addPromptSDKOperation(cfg, wsServices[ep.ServiceID], ep.Name)
	}
	return cfg
}

func addPromptSDKOperation(cfg *configfile.SDKConfig, service api.WorkspaceService, operation string) {
	serviceCfg, exists := cfg.Services[service.ServiceSlug]
	if !exists {
		serviceCfg = configfile.AppService{Version: service.Version, Operations: []string{}}
	}
	for _, existing := range serviceCfg.Operations {
		if existing == operation {
			return
		}
	}
	serviceCfg.Operations = append(serviceCfg.Operations, operation)
	cfg.Services[service.ServiceSlug] = serviceCfg
}

func writePromptSDKConfig(cfg *configfile.SDKConfig) (string, error) {
	path := fmt.Sprintf(".fused/sdks/%s.yaml", sdkName)
	if err := writeSDKConfig(path, cfg); err != nil {
		return "", fmt.Errorf("failed to write config: %w", err)
	}
	return path, nil
}

// newPromptClient wires up the API client from stored credentials/config.
// Not unit-tested directly: it's a thin pass-through to GetAPIKey/GetEngineURL,
// which read from the user's local CLI config -- the meaningful logic those
// two functions have is already covered by their own tests.
func newPromptClient() (*api.Client, error) {
	return getAPIClient()
}

// buildCart runs the initial search plus the interactive cart-building menu
// (proceed/modify/add/cancel) until the user confirms or cancels. Returns the
// final cart, whether the caller should proceed to generation, and any
// operational failure distinct from an intentional cancellation.
func buildCart(client *api.Client, description string) (map[string]api.Integration, map[string]api.WorkspaceService, bool, error) {
	cart := make(map[string]api.Integration)
	services := make(map[string]api.Service)
	wsServicesMap := make(map[string]api.WorkspaceService)
	// The initial request must resolve completely before the interactive cart is
	// shown, otherwise users could approve an incomplete SDK unknowingly.
	if err := searchAndAddEndpoints(client, description, cart, services, wsServicesMap); err != nil {
		return cart, wsServicesMap, false, err
	}
	// A successful request that adds nothing cannot generate a useful SDK and is
	// reported as validation rather than a successful no-op.
	if len(cart) == 0 {
		return cart, wsServicesMap, false, fmt.Errorf("no endpoints were added for query %q", description)
	}

	for {
		// Removing every endpoint in the interactive modifier is an intentional
		// abort, equivalent to choosing cancel after reviewing the cart.
		if len(cart) == 0 {
			fmt.Println("Cart is empty. Aborting.")
			return cart, wsServicesMap, false, nil
		}

		printCartSummary(cart, services)

		// Non-interactive confirmation deliberately bypasses only the menu, not
		// any prerequisite or API work above.
		if autoYes {
			fmt.Println("Auto-confirm enabled. Proceeding to generation...")
			return cart, wsServicesMap, true, nil
		}

		action, err := promptCartActionRunner()
		// Terminal/menu failures are operational errors, not user cancellation.
		if err != nil {
			return cart, wsServicesMap, false, fmt.Errorf("opening SDK cart menu: %w", err)
		}
		cart, proceed, done, err := handlePromptCartAction(action, client, cart, services, wsServicesMap)
		// Action handlers preserve operational failures instead of treating them as cancellation.
		if err != nil {
			return cart, wsServicesMap, false, err
		}
		// Only cancel and proceed terminate the cart loop; edit actions return for another review.
		if done {
			return cart, wsServicesMap, proceed, nil
		}
	}
}

// handlePromptCartAction applies one reviewed menu choice and reports whether
// the surrounding cart loop should terminate.
func handlePromptCartAction(action string, client *api.Client, cart map[string]api.Integration, services map[string]api.Service, wsServicesMap map[string]api.WorkspaceService) (map[string]api.Integration, bool, bool, error) {
	switch action {
	// Explicit cancellation remains a successful exit that skips SDK output.
	case "cancel":
		fmt.Println("Cancelled.")
		return cart, false, true, nil
	// Confirmation hands the selected cart to config generation.
	case "proceed":
		return cart, true, true, nil
	// Selection UI errors must not silently keep the old cart and continue.
	case "modify":
		modified, err := modifyCartSelection(cart, services)
		// Failed selection leaves the caller with the last known cart and an explicit error.
		if err != nil {
			return cart, false, false, err
		}
		return modified, false, false, nil
	// Additional searches use the same failure propagation as the initial natural-language request.
	case "add":
		if err := promptAndAddMore(client, cart, services, wsServicesMap); err != nil {
			return cart, false, false, err
		}
		return cart, false, false, nil
	default:
		// Unexpected menu values fail instead of keeping the loop alive indefinitely.
		return cart, false, false, fmt.Errorf("unsupported SDK cart action %q", action)
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
// the rebuilt cart while exposing terminal failures to the command.
func modifyCartSelection(cart map[string]api.Integration, services map[string]api.Service) (map[string]api.Integration, error) {
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

	// A failed selection prompt cannot be treated as approval of the old cart.
	if err != nil {
		return cart, fmt.Errorf("modifying SDK cart selection: %w", err)
	}
	return rebuildCart(cart, selectedIDs), nil
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
// given, searches for and merges more endpoints into the cart while returning
// terminal, API, and workspace persistence failures.
func promptAndAddMore(client *api.Client, cart map[string]api.Integration, services map[string]api.Service, wsServicesMap map[string]api.WorkspaceService) error {
	var newDesc string
	err := huh.NewInput().
		Title("Enter additional description (e.g. 'stripe refunds')").
		Value(&newDesc).
		Run()

	// Input UI failures are operational and should produce a non-zero exit.
	if err != nil {
		return fmt.Errorf("reading additional SDK description: %w", err)
	}
	// An intentionally blank additional query leaves the current cart intact.
	if newDesc == "" {
		return nil
	}
	return searchAndAddEndpoints(client, newDesc, cart, services, wsServicesMap)
}
