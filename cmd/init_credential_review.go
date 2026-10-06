package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/charmbracelet/huh"
)

var chooseInitCredentialAction = promptInitCredentialAction
var openInitCredentialBrowser = openSystemBrowser

// promptInitCredentialAction makes deferral an explicit choice before an otherwise valid app is published.
func promptInitCredentialAction() (string, error) {
	action := "setup"
	err := huh.NewSelect[string]().Title("Credentials are missing. How would you like to continue?").Options(
		huh.NewOption("Set up credentials here", "setup"),
		huh.NewOption("Open bucket setup in the browser", "bucket"),
		huh.NewOption("Recheck credentials and continue", "recheck"),
		huh.NewOption("Proceed anyway", "proceed"),
		huh.NewOption("Cancel app creation", "cancel"),
	).Value(&action).Run()
	return action, err
}

// reviewInitCredentialReadiness shares one review boundary for SDK, MCP and combined app init.
func reviewInitCredentialReadiness(client *api.Client, cfg *configfile.ParsedConfig, engineURL string, plan plannedConfig, opts planOptions) (plannedConfig, error) {
	for plan.credentialReadiness != nil && len(plan.credentialReadiness.MissingCredentials) > 0 {
		targets, err := credentialReadinessTargets(cfg, plan.credentialReadiness)
		// Invalid destinations must never be converted into browser links or secure storage prompts.
		if err != nil {
			return plannedConfig{}, err
		}
		printCredentialReadiness(opts.output, cfg.ConfigKey, plan.credentialReadiness)
		action, err := chooseInitCredentialAction()
		// Interrupting the menu cancels app publication rather than accepting deferred setup.
		if err != nil {
			return plannedConfig{}, fmt.Errorf("credential review cancelled: %w", err)
		}
		switch action {
		case "proceed":
			fmt.Fprintln(opts.output, "Credential setup skipped; creating the app with the warning above.")
			return plan, nil
		case "cancel":
			return plannedConfig{}, errors.New("app creation cancelled before publication; credentials were not changed by this choice")
		case "bucket":
			// Browser setup uses the existing Engine's bucket UI and leaves this candidate in memory.
			if err := openInitCredentialBuckets(engineURL, targets, opts); err != nil {
				return plannedConfig{}, err
			}
			continue
		case "setup":
			// Each secure collector confirms its exact service and bucket before any value is requested.
			err := remediateSDKPlanReadiness(client, cfg, plan.credentialReadiness, opts)
			// Declining a later service may follow earlier successful writes; refresh readiness before the next choice.
			if err != nil && !errors.Is(err, errCredentialStorageDeclined) {
				return plannedConfig{}, err
			}
		case "recheck":
			// Rechecking reads current material; it never invokes provider operations or stores credentials.
		default:
			return plannedConfig{}, errors.New("invalid credential review choice")
		}
		// A fresh receipt replaces the earlier plan only after Engine has revalidated the exact app config.
		plan, err = planOneConfig(client, cfg, engineURL, opts.ownerTeamSlug)
		if err != nil {
			return plannedConfig{}, err
		}
	}
	fmt.Fprintln(opts.output, "Credential requirements are configured. Continuing app creation.")
	return plan, nil
}

// initCredentialBucketURL keeps setup on the configured Engine without leaking API keys into the browser URL.
func initCredentialBucketURL(engineURL, bucketID string) (string, error) {
	base, err := url.Parse(engineURL)
	// Only a configured HTTP origin may receive a value-free bucket navigation target.
	if err != nil || base.Host == "" || base.User != nil || (base.Scheme != "https" && base.Scheme != "http") {
		return "", errors.New("invalid Fused URL for bucket setup")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/integrations/buckets"
	base.RawPath = ""
	base.Fragment = ""
	base.RawQuery = url.Values{"bucket": {bucketID}, "tab": {"secrets"}}.Encode()
	return base.String(), nil
}

// openInitCredentialBuckets opens each missing bucket once while preserving a copyable link if browser launch fails.
func openInitCredentialBuckets(engineURL string, targets []appCredentialTarget, opts planOptions) error {
	seen := make(map[string]bool)
	ctx := opts.auditCtx
	// Tests and standalone planners may omit audit context; browser launch still needs cancellation-safe context.
	if ctx == nil {
		ctx = context.Background()
	}
	for _, target := range targets {
		// Several auth schemes can share one setup page.
		if seen[target.bucket.ID] {
			continue
		}
		seen[target.bucket.ID] = true
		link, err := initCredentialBucketURL(engineURL, target.bucket.ID)
		if err != nil {
			return err
		}
		fmt.Fprintf(opts.output, "Bucket %q: %s\n", target.bucket.Name, link)
		// A missing desktop browser is recoverable through the printed link, not a different Engine.
		if err := openInitCredentialBrowser(ctx, link); err != nil {
			fmt.Fprintln(opts.output, "Could not open the browser; use the link above.")
		}
	}
	fmt.Fprintln(opts.output, "Add credentials in your bucket, then choose Recheck credentials and continue. Bucket permissions still apply.")
	return nil
}
