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
	"github.com/spf13/cobra"
)

var updateTargetType string
var updateTargetLanguage string
var updateDeploy bool

var updateCmd = &cobra.Command{
	Use:   "update [sdk_id_or_name]",
	Short: "Update an existing SDK",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runUpdate(args[0])
	},
}

func init() {
	updateCmd.Flags().StringVarP(&outputDir, "output", "o", ".", "Directory to save the updated SDK zip")
	updateCmd.Flags().StringVarP(&updateTargetType, "type", "t", "sdk", "Target type for the SDK (e.g., 'sdk', 'mcp')")
	updateCmd.Flags().StringVarP(&updateTargetLanguage, "language", "l", "typescript", "Target language for the SDK (e.g., 'typescript', 'python')")
	updateCmd.Flags().BoolVarP(&updateDeploy, "deploy", "", false, "Deploy the generated MCP server to Fused systems (applies only if --type=mcp)")
	rootCmd.AddCommand(updateCmd)
}

func runUpdate(sdkID string) {
	if updateDeploy && updateTargetType != "mcp" {
		fmt.Println("Error: --deploy can only be used with --type=mcp")
		os.Exit(1)
	}

	if updateDeploy && updateTargetLanguage == "python" {
		fmt.Println("Error: Python MCP servers cannot be deployed to Fused systems.")
		os.Exit(1)
	}

	key := GetAPIKey()
	client := api.NewClient(apiURL, key)

	var generatedSdkID string
	if len(sdkID) == 36 && sdkID[8] == '-' && sdkID[13] == '-' && sdkID[18] == '-' && sdkID[23] == '-' {
		// Looks like a UUID, assume it's the SDK ID
		generatedSdkID = sdkID
	} else {
		// It's a name, find the ID by name
		namePart := sdkID
		versionPart := ""
		if lastIdx := strings.LastIndex(sdkID, "@"); lastIdx > 0 {
			namePart = sdkID[:lastIdx]
			versionPart = sdkID[lastIdx+1:]
		}

		if versionPart != "" {
			fmt.Printf("🔍 Looking up SDK by name '%s' (version '%s')...\n", namePart, versionPart)
		} else {
			fmt.Printf("🔍 Looking up SDK by name '%s'...\n", namePart)
		}
		
		sdkDetails, err := client.GetSDKByName(namePart, versionPart)
		if err != nil || sdkDetails == nil || sdkDetails.ID == "" {
			if versionPart != "" {
				fmt.Printf("Error: could not find an SDK with the name '%s' and version '%s'\n", namePart, versionPart)
			} else {
				fmt.Printf("Error: could not find an SDK with the name '%s'\n", namePart)
			}
			os.Exit(1)
		}
		generatedSdkID = sdkDetails.ID
		fmt.Printf("✅ Found SDK ID: %s\n", generatedSdkID)
	}

	fmt.Printf("🚀 Requesting SDK Update for %s...\n", generatedSdkID)

	req := api.GenerateSDKRequest{
		Name:           "Updated SDK",
		TargetType:     updateTargetType,
		TargetLanguage: updateTargetLanguage,
		SkipSandbox:    !updateDeploy,
		UpgradeFrom:    generatedSdkID,
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

	var finalGeneratedSdkID string
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
				finalGeneratedSdkID = ev.IntegrationID
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

	if finalGeneratedSdkID != "" {
		if updateDeploy {
			fmt.Println("✅ MCP Server Deployment Complete.")
			sdkDetails, err := client.GetSDK(finalGeneratedSdkID)
			if err != nil {
				fmt.Printf("Error fetching MCP details: %v\n", err)
				return
			}
			if sdkDetails != nil && sdkDetails.SandboxURL != "" {
				fmt.Printf("\n🌐 Sandbox URL: %s\n", sdkDetails.SandboxURL)
				fmt.Println("\nTo use this MCP Server, configure your client to connect to the above SSE Sandbox URL.")
				fmt.Println("Authentication credentials should be passed as HTTP headers prefixed with 'X-Env-' when establishing the connection.")
			} else {
				fmt.Println("Sandbox URL is not available.")
			}
		} else {
			fmt.Printf("✅ SDK Update Complete. Downloading SDK %s...\n", finalGeneratedSdkID)
			zipData, err := client.DownloadSDK(finalGeneratedSdkID)
		if err != nil {
			fmt.Printf("Error downloading SDK: %v\n", err)
			return
		}

		zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
		if err != nil {
			fmt.Printf("Error reading zip archive: %v\n", err)
			return
		}

		extractDir := strings.TrimRight(outputDir, "/")
		if extractDir == "" {
			extractDir = "."
		}

		for _, f := range zipReader.File {
			fpath := filepath.Join(extractDir, f.Name)

			// Safely check if fpath is inside extractDir to prevent zip slip
			rel, err := filepath.Rel(extractDir, fpath)
			if err != nil || strings.HasPrefix(rel, "..") {
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

		fmt.Printf("🎉 Updated SDK automatically extracted to %s/\n", extractDir)
		}
	}
}
