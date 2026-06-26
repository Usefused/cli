package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

var updateCh = make(chan string, 1)

func startUpdateCheck() {
	go func() {
		updateCh <- fetchLatestVersion()
	}()
}

func fetchLatestVersion() string {
	if Version == "dev" {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/Usefused/cli/releases/latest", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "fused-cli/"+Version)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return ""
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(Version, "v")

	if latest != "" && latest != current {
		return release.TagName
	}
	return ""
}

func printUpdateNudge() {
	select {
	case latest := <-updateCh:
		if latest != "" {
			fmt.Fprintf(os.Stderr, "\n⚠  fused-cli %s is available (you have v%s)\n", latest, Version)
			fmt.Fprintf(os.Stderr, "   curl -fsSL https://raw.githubusercontent.com/Usefused/cli/main/install.sh | bash\n")
		}
	case <-time.After(300 * time.Millisecond):
		// check didn't finish in time, skip
	}
}
