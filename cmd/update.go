package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update [sdk_id]",
	Short: "Update an existing SDK",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runUpdate(args[0])
	},
}

func init() {
	updateCmd.Flags().StringVarP(&outputDir, "output", "o", ".", "Directory to save the updated SDK zip")
	rootCmd.AddCommand(updateCmd)
}

func runUpdate(sdkID string) {
	key := GetAPIKey()
	client := api.NewClient(apiURL, key)

	fmt.Printf("🚀 Requesting update for SDK: %s...\n", sdkID)
	req := api.GenerateSDKRequest{
		Name:        "Updated SDK",
		TargetType:  "typescript",
		UpgradeFrom: sdkID,
	}

	resp, err := client.GenerateSDK(req)
	if err != nil {
		fmt.Printf("Failed to initiate SDK update: %v\n", err)
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
		fmt.Printf("✅ SDK Update Complete. Downloading SDK %s...\n", generatedSdkID)
		zipData, err := client.DownloadSDK(generatedSdkID)
		if err != nil {
			fmt.Printf("Error downloading SDK: %v\n", err)
			return
		}

		outPath := fmt.Sprintf("%s/sdk_%s.zip", strings.TrimRight(outputDir, "/"), generatedSdkID)
		
		if err := os.MkdirAll(strings.TrimRight(outputDir, "/"), 0755); err != nil {
			fmt.Printf("Error creating output directory: %v\n", err)
			return
		}

		if err := os.WriteFile(outPath, zipData, 0644); err != nil {
			fmt.Printf("Error writing zip file: %v\n", err)
			return
		}
		fmt.Printf("🎉 Updated SDK saved to %s\n", outPath)
	}
}
