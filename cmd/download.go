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

var downloadCmd = &cobra.Command{
	Use:   "download [sdk_id_or_name]",
	Short: "Download an already built SDK",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runDownload(args[0])
	},
}

func init() {
	downloadCmd.Flags().StringVarP(&outputDir, "output", "o", ".", "Directory to save the downloaded SDK zip")
	rootCmd.AddCommand(downloadCmd)
}

func runDownload(sdkArg string) {
	key := GetAPIKey()
	client := api.NewClient(apiURL, key)

	var generatedSdkID string
	if len(sdkArg) == 36 && sdkArg[8] == '-' && sdkArg[13] == '-' && sdkArg[18] == '-' && sdkArg[23] == '-' {
		// Looks like a UUID, assume it's the SDK ID
		generatedSdkID = sdkArg
	} else {
		// It's a name, find the ID by name
		namePart := sdkArg
		versionPart := ""
		if lastIdx := strings.LastIndex(sdkArg, "@"); lastIdx > 0 {
			namePart = sdkArg[:lastIdx]
			versionPart = sdkArg[lastIdx+1:]
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

	fmt.Printf("✅ Downloading SDK %s...\n", generatedSdkID)
	zipData, err := client.DownloadSDK(generatedSdkID)
	if err != nil {
		fmt.Printf("Error downloading SDK: %v\n", err)
		return
	}

	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		fmt.Printf("Error reading downloaded zip: %v\n", err)
		return
	}

	extractDir := outputDir
	if extractDir == "." {
		// If output is default (.), extract to a folder named after the first directory in the zip
		// or just use "sdk"
		if len(zipReader.File) > 0 {
			parts := strings.Split(zipReader.File[0].Name, "/")
			if len(parts) > 0 {
				extractDir = parts[0]
			}
		}
	}

	// Ensure the directory exists
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		fmt.Printf("Error creating extraction directory: %v\n", err)
		return
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

		if err = os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			fmt.Printf("Error creating directory for file %s: %v\n", fpath, err)
			continue
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			fmt.Printf("Error creating file %s: %v\n", fpath, err)
			continue
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			continue
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
	}

	fmt.Printf("🎉 Downloaded SDK automatically extracted to %s/\n", extractDir)
}
