package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var description string

var sdkName string
var artifactVersion string
var targetLanguage string
var autoYes bool

var promptCmd = &cobra.Command{
	Use:   "prompt",
	Short: "Use AI to prompt and generate a new SDK config",
	Run: func(cmd *cobra.Command, args []string) {
		runPrompt()
	},
}

func init() {
	promptCmd.Flags().StringVarP(&sdkName, "name", "n", "", "Name of the generated SDK (e.g., 'stripe-sdk')")
	promptCmd.Flags().StringVarP(&artifactVersion, "version", "v", "1.0.0", "Version of the generated SDK")
	promptCmd.Flags().StringVarP(&targetLanguage, "language", "l", "typescript", "Target language for the SDK (e.g., 'typescript', 'python')")
	promptCmd.Flags().BoolVarP(&autoYes, "yes", "y", false, "Skip interactive menu and automatically proceed")
	promptCmd.Flags().StringVarP(&description, "description", "d", "", "Description of the SDK to create (e.g. 'Create a stripe and plunk sdk')")

	promptCmd.MarkFlagRequired("name")

	sdkCmd.AddCommand(promptCmd)
}

func searchAndAddEndpoints(client *api.Client, searchString string, currentCart map[string]api.Integration, servicesMap map[string]api.Service, wsServicesMap map[string]api.WorkspaceService) {
	target := ConfigFile
	if target == "" {
		target = filepath.Join(".fused", "workspace.yaml")
	}

	if _, err := os.Stat(target); os.IsNotExist(err) {
		fmt.Println("No local workspace config found. Syncing from Engine...")
		_, syncErr := PerformWorkspaceSync(context.Background(), client, ConfigFile)
		if syncErr != nil {
			fmt.Printf("Warning: Failed to sync workspace config: %v\n", syncErr)
		}
	}

	wsPath, wsCfg, err := loadWorkspaceConfigForSync(ConfigFile)
	if err != nil {
		fmt.Printf("Failed to load workspace config: %v\n", err)
		return
	}

	intent, err := client.ParseSDKIntent(searchString)
	if err != nil {
		fmt.Printf("Failed to parse intent: %v\n", err)
		return
	}

	if len(intent.Services) == 0 {
		fmt.Println("No services detected in your query.")
		return
	}

	added := 0
	modifiedWorkspace := false
	var newWorkspaceServices []string

	for _, svcIntent := range intent.Services {
		addedEndpoints, svcAdded := processServiceIntent(client, svcIntent, wsCfg, currentCart, servicesMap, wsServicesMap)
		added += addedEndpoints
		if svcAdded {
			modifiedWorkspace = true
			newWorkspaceServices = append(newWorkspaceServices, svcIntent.Name)
		}
	}

	if modifiedWorkspace {
		err = writeWorkspaceConfig(wsPath, wsCfg)
		if err != nil {
			fmt.Printf("Warning: failed to write updated workspace.yaml: %v\n", err)
		} else {
			fmt.Printf("✅ Automatically added %s to your workspace config.\n", strings.Join(newWorkspaceServices, ", "))
		}
	}

	fmt.Printf("✅ Added %d new targeted endpoints to the cart.\n", added)
}

func processServiceIntent(client *api.Client, svcIntent api.IntentService, wsCfg *configfile.WorkspaceConfig, cart map[string]api.Integration, servicesMap map[string]api.Service, wsServicesMap map[string]api.WorkspaceService) (int, bool) {
	fmt.Printf("🔍 Searching for service matching %q...\n", svcIntent.Name)
	services, err := client.SearchServices(svcIntent.Name)
	if err != nil || len(services) == 0 {
		fmt.Printf("   -> Could not find service matching %q\n", svcIntent.Name)
		return 0, false
	}

	s := services[0]
	servicesMap[s.ID] = s

	visMap, err := client.ServiceVisibilities([]string{s.ID})
	if err != nil {
		fmt.Printf("   -> Could not fetch visibility for %q\n", s.Name)
		return 0, false
	}
	vis := visMap[s.ID]
	key := serviceIntentConfigKey(vis)

	svcAdded := false
	var version string
	// Prefer whatever version is already enabled locally over fetching the
	// latest from Registry -- if the user already added this service to
	// their workspace config, the Copilot should target that version rather
	// than silently pinning a different (possibly newer) one underneath it.
	if existing, ok := wsCfg.Services[key]; ok {
		if len(existing.Versions) > 0 {
			version = existing.Versions[0].Version
		}
	}

	if version == "" {
		// Not enabled locally yet -- fall back to Registry's latest version
		// so intent-based discovery can add a brand-new service on its own.
		versions, err := client.ServiceVersions(key)
		if err != nil || len(versions) == 0 {
			fmt.Printf("   -> Could not find any versions for %q\n", s.Name)
			return 0, false
		}
		version = versions[0].Name

		wsCfg.Services[key] = configfile.WorkspaceService{
			ServiceID: s.ID,
			Versions:  []configfile.WorkspaceServiceVersion{{Version: version}},
		}
		svcAdded = true
		fmt.Printf("   -> 🌟 Automatically added %s (v%s) to workspace config\n", key, version)
	}

	wsServicesMap[s.ID] = api.WorkspaceService{
		ServiceID: s.ID,
		Version:   version,
	}

	fmt.Printf("   -> Found %q (v%s)! Fetching endpoints (intent: %q)...\n", s.Name, version, svcIntent.EndpointQuery)
	endpoints, err := client.SearchEndpoints(s.ID, version, svcIntent.EndpointQuery)
	if err != nil {
		fmt.Printf("Error fetching endpoints for service %s: %v\n", s.Name, err)
		return 0, svcAdded
	}

	return mergeNewEndpoints(cart, endpoints), svcAdded
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
func runPrompt() {
	if description == "" {
		fmt.Println("Error: --description is required")
		os.Exit(1)
	}

	client, err := newPromptClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	cart, wsServicesMap, proceed := buildCart(client, description)
	if !proceed {
		return
	}

	cfg := &configfile.SDKConfig{
		BaseConfig: configfile.BaseConfig{
			APIVersion: configfile.APIVersionV1,
			Kind:       configfile.KindSDK,
		},
		Name:     sdkName,
		Version:  artifactVersion,
		Language: targetLanguage,
		Services: make(map[string]configfile.ArtifactService),
	}

	for _, ep := range cart {
		wsSvc := wsServicesMap[ep.ServiceID]
		svcSlug := wsSvc.ServiceSlug

		svcCfg, exists := cfg.Services[svcSlug]
		if !exists {
			svcCfg = configfile.ArtifactService{
				Version:    wsSvc.Version,
				Operations: []string{},
			}
		}
		// Avoid duplicates
		found := false
		for _, op := range svcCfg.Operations {
			if op == ep.Name {
				found = true
				break
			}
		}
		if !found {
			svcCfg.Operations = append(svcCfg.Operations, ep.Name)
		}
		cfg.Services[svcSlug] = svcCfg
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		fmt.Printf("Failed to marshal config: %v\n", err)
		return
	}

	err = os.MkdirAll(".fused/sdks", 0755)
	if err != nil {
		fmt.Printf("Failed to create .fused/sdks directory: %v\n", err)
		return
	}

	path := fmt.Sprintf(".fused/sdks/%s.yaml", sdkName)
	err = os.WriteFile(path, data, 0644)
	if err != nil {
		fmt.Printf("Failed to write config: %v\n", err)
		return
	}

	fmt.Printf("✅ SDK config generated at %s\n", path)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  1. Preview changes: fused-cli sdk plan\n")
	fmt.Printf("  2. Apply changes:   fused-cli sdk apply\n")
}

// newPromptClient wires up the API client from stored credentials/config.
// Not unit-tested directly: it's a thin pass-through to GetAPIKey/GetEngineURL,
// which read from the user's local CLI config -- the meaningful logic those
// two functions have is already covered by their own tests.
func newPromptClient() (*api.Client, error) {
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
func buildCart(client *api.Client, description string) (map[string]api.Integration, map[string]api.WorkspaceService, bool) {
	cart := make(map[string]api.Integration)
	services := make(map[string]api.Service)
	wsServicesMap := make(map[string]api.WorkspaceService)
	searchAndAddEndpoints(client, description, cart, services, wsServicesMap)

	for {
		if len(cart) == 0 {
			fmt.Println("Cart is empty. Aborting.")
			return cart, wsServicesMap, false
		}

		printCartSummary(cart, services)

		if autoYes {
			fmt.Println("Auto-confirm enabled. Proceeding to generation...")
			return cart, wsServicesMap, true
		}

		action, err := promptCartAction()
		if err != nil {
			fmt.Printf("Menu error: %v\n", err)
			return cart, wsServicesMap, false
		}

		switch action {
		case "cancel":
			fmt.Println("Cancelled.")
			return cart, wsServicesMap, false
		case "proceed":
			return cart, wsServicesMap, true
		case "modify":
			cart = modifyCartSelection(cart, services)
		case "add":
			promptAndAddMore(client, cart, services, wsServicesMap)
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
func promptAndAddMore(client *api.Client, cart map[string]api.Integration, services map[string]api.Service, wsServicesMap map[string]api.WorkspaceService) {
	var newDesc string
	err := huh.NewInput().
		Title("Enter additional description (e.g. 'stripe refunds')").
		Value(&newDesc).
		Run()

	if err != nil || newDesc == "" {
		return
	}
	searchAndAddEndpoints(client, newDesc, cart, services, wsServicesMap)
}
