package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/config"
	"github.com/spf13/cobra"
)

type cliLoginAPIFixture struct {
	startInput api.CLILoginStartRequest
	start      api.CLILoginStartResponse
	poll       api.CLILoginPollResponse
}

func (f *cliLoginAPIFixture) StartCLILogin(input api.CLILoginStartRequest) (api.CLILoginStartResponse, error) {
	f.startInput = input
	return f.start, nil
}

func (f *cliLoginAPIFixture) PollCLILogin(string, string) (api.CLILoginPollResponse, error) {
	return f.poll, nil
}

// TestRunCLILoginAllowsNoInputBrowserApproval verifies browser login needs no terminal prompt.
func TestRunCLILoginAllowsNoInputBrowserApproval(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FUSED_API_KEY", "")
	t.Setenv("FUSED_LICENSE_KEY", "")
	fixture := &cliLoginAPIFixture{
		start: api.CLILoginStartResponse{
			TransactionID: "transaction", PollToken: "poll", BrowserToken: "browser",
			ExpiresAt: time.Now().Add(time.Minute),
		},
		poll: api.CLILoginPollResponse{
			Status: "authenticated", CredentialID: "credential", ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
		},
	}
	previousFactory, previousOpen := newCLILoginClient, openLoginBrowser
	previousURL, previousNoInput, previousNoBrowser := EngineURL, NoInput, loginNoBrowser
	t.Cleanup(func() {
		newCLILoginClient, openLoginBrowser = previousFactory, previousOpen
		EngineURL, NoInput, loginNoBrowser = previousURL, previousNoInput, previousNoBrowser
	})
	newCLILoginClient = func(string, api.ClientOptions) cliLoginAPI { return fixture }
	var opened string
	openLoginBrowser = func(_ context.Context, target string) error { opened = target; return nil }
	EngineURL, NoInput, loginNoBrowser = "https://engine.example", true, false
	output := &bytes.Buffer{}
	command := &cobra.Command{Use: "login"}
	command.SetOut(output)
	command.SetContext(t.Context())
	if err := runCLILogin(command); err != nil {
		t.Fatalf("runCLILogin: %v", err)
	}
	cfg, err := config.Load()
	if err != nil || !strings.HasPrefix(cfg.APIKey, "fsk_") || cfg.EngineURL != EngineURL {
		t.Fatalf("saved config = %#v, %v", cfg, err)
	}
	if fixture.startInput.CredentialHash == "" || fixture.startInput.CredentialPrefix != cfg.APIKey[:8] {
		t.Fatalf("credential commitment = %#v", fixture.startInput)
	}
	if !strings.Contains(opened, "/login?next=%2Fcli-login") || !strings.Contains(opened, "browser_token=browser") {
		t.Fatalf("verification URL = %q", opened)
	}
	if strings.Contains(output.String(), cfg.APIKey) {
		t.Fatal("CLI output leaked the saved credential")
	}
}

// TestRunCLILoginAllowsNonInteractiveNoBrowserEnrollment verifies remote approval enrollment.
func TestRunCLILoginAllowsNonInteractiveNoBrowserEnrollment(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FUSED_API_KEY", "")
	t.Setenv("FUSED_LICENSE_KEY", "")
	fixture := &cliLoginAPIFixture{
		start: api.CLILoginStartResponse{
			TransactionID: "transaction", PollToken: "poll", BrowserToken: "browser",
			ExpiresAt: time.Now().Add(time.Minute),
		},
		poll: api.CLILoginPollResponse{
			Status: "authenticated", CredentialID: "credential", ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
		},
	}
	previousFactory, previousOpen := newCLILoginClient, openLoginBrowser
	previousURL, previousNoInput, previousNoBrowser := EngineURL, NoInput, loginNoBrowser
	t.Cleanup(func() {
		newCLILoginClient, openLoginBrowser = previousFactory, previousOpen
		EngineURL, NoInput, loginNoBrowser = previousURL, previousNoInput, previousNoBrowser
	})
	newCLILoginClient = func(string, api.ClientOptions) cliLoginAPI { return fixture }
	openLoginBrowser = func(context.Context, string) error {
		t.Fatal("browser should not be opened")
		return nil
	}
	EngineURL, NoInput, loginNoBrowser = "https://engine.example", true, true
	output := &bytes.Buffer{}
	command := &cobra.Command{Use: "login"}
	command.SetOut(output)
	command.SetContext(t.Context())
	if err := runCLILogin(command); err != nil {
		t.Fatalf("runCLILogin: %v", err)
	}
	if !strings.Contains(output.String(), "Open this URL to sign in:") || !strings.Contains(output.String(), "browser_token=browser") {
		t.Fatalf("output = %q", output.String())
	}
}
