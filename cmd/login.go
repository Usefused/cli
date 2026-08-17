package cmd

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/config"
	"github.com/spf13/cobra"
)

var (
	loginNoBrowser       bool
	openLoginBrowser     = openSystemBrowser
	cliLoginPollInterval = 1500 * time.Millisecond
	newCLILoginClient    = func(engineURL string, options api.ClientOptions) cliLoginAPI {
		return api.NewClientWithOptions(engineURL, "", options)
	}
)

type cliLoginAPI interface {
	StartCLILogin(api.CLILoginStartRequest) (api.CLILoginStartResponse, error)
	PollCLILogin(string, string) (api.CLILoginPollResponse, error)
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Sign in to a Fused Engine",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.login", func(cmd *cobra.Command, _ []string) error {
		return runCLILogin(cmd)
	}),
}

func init() {
	loginCmd.Flags().BoolVar(&loginNoBrowser, "no-browser", false, "Print the sign-in URL instead of opening a browser")
	RootCmd.AddCommand(loginCmd)
}

func runCLILogin(cmd *cobra.Command) error {
	engineURL, err := normalizedLoginEngineURL()
	if err != nil {
		return err
	}
	rawKey, err := generateCLIControlCredential()
	if err != nil {
		return errors.New("could not create a local CLI credential")
	}
	client := newCLILoginClient(engineURL, api.ClientOptions{
		Context: cmd.Context(), Timeout: RequestTimeout, RequestID: RequestID, DisableProgress: true,
	})
	start, err := client.StartCLILogin(api.CLILoginStartRequest{
		CredentialHash: hashCLICredential(rawKey), CredentialPrefix: rawKey[:8],
	})
	if err != nil {
		return fmt.Errorf("start CLI login: %w", err)
	}
	verificationURL, err := cliVerificationURL(engineURL, start)
	if err != nil {
		return errors.New("Engine returned an invalid CLI login transaction")
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Open this URL to sign in:")
	fmt.Fprintln(cmd.OutOrStdout(), verificationURL)
	if !loginNoBrowser {
		if err := openLoginBrowser(cmd.Context(), verificationURL); err != nil {
			fmt.Fprintln(cmd.OutOrStdout(), "\nThe browser could not be opened automatically; use the URL above.")
		}
	}
	result, err := waitForCLILogin(cmd.Context(), client, start)
	if err != nil {
		return err
	}
	if err := config.SaveLogin(engineURL, rawKey, result.CredentialID, result.ExpiresAt); err != nil {
		return fmt.Errorf("save CLI login: %w", err)
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "cli_login")
	fmt.Fprintln(cmd.OutOrStdout(), "\n✅ Signed in. The CLI credential was saved to your Fused config.")
	return nil
}

func normalizedLoginEngineURL() (string, error) {
	value, err := GetEngineURL()
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return "", errors.New("engine-url must be an HTTP or HTTPS URL without embedded credentials")
	}
	parsed.RawQuery, parsed.Fragment = "", ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func generateCLIControlCredential() (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return "fsk_" + base64.RawURLEncoding.EncodeToString(secret), nil
}

func hashCLICredential(rawKey string) string {
	digest := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(digest[:])
}

func cliVerificationURL(engineURL string, start api.CLILoginStartResponse) (string, error) {
	parsed, err := url.Parse(engineURL + "/login")
	if err != nil {
		return "", err
	}
	parsed.RawQuery = url.Values{"next": {"/cli-login"}}.Encode()
	parsed.Fragment = url.Values{
		"transaction_id": {start.TransactionID}, "browser_token": {start.BrowserToken},
	}.Encode()
	return parsed.String(), nil
}

func waitForCLILogin(ctx context.Context, client cliLoginAPI, start api.CLILoginStartResponse) (api.CLILoginPollResponse, error) {
	deadline := time.NewTimer(time.Until(start.ExpiresAt))
	defer deadline.Stop()
	ticker := time.NewTicker(cliLoginPollInterval)
	defer ticker.Stop()
	for {
		result, err := client.PollCLILogin(start.TransactionID, start.PollToken)
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, api.ErrCLILoginPending) {
			return api.CLILoginPollResponse{}, fmt.Errorf("complete CLI login: %w", err)
		}
		select {
		case <-ctx.Done():
			return api.CLILoginPollResponse{}, ctx.Err()
		case <-deadline.C:
			return api.CLILoginPollResponse{}, errors.New("CLI login expired; run fused-cli login again")
		case <-ticker.C:
		}
	}
}

func openSystemBrowser(ctx context.Context, target string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{target}
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", target}
	default:
		name, args = "xdg-open", []string{target}
	}
	return exec.CommandContext(ctx, name, args...).Run()
}
