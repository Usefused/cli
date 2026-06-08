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
	"github.com/spf13/cobra"
)

var description string
var outputDir string
var autoConfirm bool
var sdkName string
var sdkVersion string
var targetType string

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new SDK",
	Run: func(cmd *cobra.Command, args []string) {
		runCreate()
	},
}

func init() {
	createCmd.Flags().StringVarP(&sdkName, "name", "n", "", "Name of the generated SDK (e.g., 'stripe-sdk')")
	createCmd.Flags().StringVarP(&sdkVersion, "version", "v", "1.0.0", "Version of the generated SDK")
	createCmd.Flags().StringVarP(&targetType, "target", "t", "typescript", "Target language for the SDK (e.g., 'typescript')")
	createCmd.Flags().StringVarP(&description, "description", "d", "", "Description of the SDK to create (e.g. 'Create a stripe and plunk sdk')")
	createCmd.Flags().StringVarP(&outputDir, "output", "o", ".", "Directory to save the generated SDK zip")
	createCmd.Flags().BoolVarP(&autoConfirm, "auto-confirm", "y", false, "Automatically confirm and select all endpoints")
	
	createCmd.MarkFlagRequired("name")
	
	rootCmd.AddCommand(createCmd)
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

	added := 0
	for _, svcIntent := range intent.Services {
		fmt.Printf("🔍 Searching for service matching %q...\n", svcIntent.Name)
		services, err := client.SearchServices(svcIntent.Name)
		if err != nil || len(services) == 0 {
			fmt.Printf("   -> Could not find service matching %q\n", svcIntent.Name)
			continue
		}

		// Take the best match (first one)
		s := services[0]
		servicesMap[s.ID] = s

		fmt.Printf("   -> Found %q! Fetching endpoints (intent: %q)...\n", s.Name, svcIntent.EndpointQuery)
		endpoints, err := client.SearchEndpoints(s.ID, svcIntent.EndpointQuery)
		if err != nil {
			fmt.Printf("Error fetching endpoints for service %s: %v\n", s.Name, err)
			continue
		}
		for _, ep := range endpoints {
			if _, exists := currentCart[ep.ID]; !exists {
				currentCart[ep.ID] = ep
				added++
			}
		}
	}
	fmt.Printf("✅ Added %d new targeted endpoints to the cart.\n", added)
}

func runCreate() {
	if description == "" {
		fmt.Println("Error: --description is required")
		os.Exit(1)
	}

	key := GetAPIKey()
	client := api.NewClient(apiURL, key)

	// Cart state
	cart := make(map[string]api.Integration)
	services := make(map[string]api.Service)

	// Initial Search
	searchAndAddEndpoints(client, description, cart, services)

	if len(cart) == 0 {
		fmt.Println("No endpoints matched your description. Aborting.")
		return
	}

	// Interactive Loop
	for {
		if len(cart) == 0 {
			fmt.Println("Your cart is empty. Aborting.")
			return
		}

		// Print Cart summary
		fmt.Println("\n📦 --- CURRENT SDK CART ---")
		endpointsByService := make(map[string][]api.Integration)
		for _, ep := range cart {
			endpointsByService[ep.ServiceID] = append(endpointsByService[ep.ServiceID], ep)
		}

		for svcID, eps := range endpointsByService {
			svcName := "Unknown Service"
			if s, ok := services[svcID]; ok {
				svcName = s.Name
			}
			fmt.Printf("🔸 %s (%d endpoints)\n", svcName, len(eps))
			for i, ep := range eps {
				// Show max 5 per service to avoid spam, plus count
				if i < 5 {
					fmt.Printf("    - %s %s (%s)\n", ep.Method, ep.Path, ep.Name)
				} else if i == 5 {
					fmt.Printf("    - ... and %d more\n", len(eps)-5)
					break
				}
			}
		}
		fmt.Println("---------------------------")

		if autoConfirm {
			fmt.Println("Auto-confirm enabled. Proceeding to generation...")
			break
		}

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

		if err != nil {
			fmt.Printf("Menu error: %v\n", err)
			return
		}

		if action == "cancel" {
			fmt.Println("Cancelled.")
			return
		} else if action == "proceed" {
			break
		} else if action == "modify" {
			// Multi-select from current cart
			var options []huh.Option[string]
			for id, ep := range cart {
				svcName := services[ep.ServiceID].Name
				options = append(options, huh.NewOption(fmt.Sprintf("[%s] %s (%s %s)", svcName, ep.Name, ep.Method, ep.Path), id).Selected(true))
			}

			var selectedIDs []string
			err := huh.NewMultiSelect[string]().
				Title("Select endpoints to KEEP in the SDK").
				Options(options...).
				Value(&selectedIDs).
				Height(15).
				Run()

			if err == nil {
				// Rebuild cart
				newCart := make(map[string]api.Integration)
				for _, id := range selectedIDs {
					newCart[id] = cart[id]
				}
				cart = newCart
			}
		} else if action == "add" {
			var newDesc string
			err := huh.NewInput().
				Title("Enter additional description (e.g. 'stripe refunds')").
				Value(&newDesc).
				Run()

			if err == nil && newDesc != "" {
				searchAndAddEndpoints(client, newDesc, cart, services)
			}
		}
	}

	// Group selections by service for generation
	selectionsMap := make(map[string][]string)
	for id, ep := range cart {
		selectionsMap[ep.ServiceID] = append(selectionsMap[ep.ServiceID], id)
	}

	var selections []api.SDKSelection
	for sid, eids := range selectionsMap {
		selections = append(selections, api.SDKSelection{
			ServiceID:   sid,
			EndpointIDs: eids,
		})
	}

	fmt.Println("\n🚀 Generating SDK...")
	req := api.GenerateSDKRequest{
		Name:        sdkName,
		Description: description,
		Version:     sdkVersion,
		TargetType:  targetType,
		Selections:  selections,
	}

	resp, err := client.GenerateSDK(req)
	if err != nil {
		fmt.Printf("Failed to generate SDK: %v\n", err)
		return
	}

	fmt.Printf("Job ID: %s. Streaming progress...\n", resp.JobID)

	eventChan := make(chan api.SDKEvent)
	errChan := make(chan error)
	go client.StreamSDKGenerationEvents(resp.JobID, eventChan, errChan)

	var generatedSdkID string
Loop:
	for {
		select {
		case ev, ok := <-eventChan:
			if !ok {
				eventChan = nil
				continue
			}
			fmt.Printf("[%s] %s\n", ev.Type, ev.Message)
			if ev.Type == "complete" {
				generatedSdkID = ev.IntegrationID
				break Loop
			}
			if ev.Type == "error" {
				break Loop
			}
		case errVal, ok := <-errChan:
			if !ok {
				errChan = nil
				continue
			}
			if errVal != nil {
				fmt.Printf("Stream error: %v\n", errVal)
				break Loop
			}
		}
		if eventChan == nil && errChan == nil {
			break Loop
		}
	}

	if generatedSdkID != "" {
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
			fpath := filepath.Join(extractDir, f.Name)

			// Check for ZipSlip vulnerability
			if !strings.HasPrefix(fpath, filepath.Clean(extractDir)+string(os.PathSeparator)) {
				continue
			}

			if f.FileInfo().IsDir() {
				os.MkdirAll(fpath, os.ModePerm)
				continue
			}

			if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
				continue
			}

			outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
			if err != nil {
				continue
			}

			rc, err := f.Open()
			if err != nil {
				outFile.Close()
				continue
			}

			io.Copy(outFile, rc)
			outFile.Close()
			rc.Close()
		}

		fmt.Printf("🎉 SDK automatically extracted to %s/\n", extractDir)
	}
}
