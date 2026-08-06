package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/config"
	"github.com/spf13/cobra"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type cliIdentityFixture struct {
	wantIdentity *api.WhoAmIResponse
	whoAmIErr    error
	logoutErr    error
	whoAmICalls  int
	logoutCalls  int
}

func (f *cliIdentityFixture) WhoAmI() (*api.WhoAmIResponse, error) {
	f.whoAmICalls++
	return f.wantIdentity, f.whoAmIErr
}

func (f *cliIdentityFixture) LogoutCLI() error {
	f.logoutCalls++
	return f.logoutErr
}

func preserveAuthCommandGlobals(t *testing.T) {
	t.Helper()
	previousFactory := newCLIIdentityClient
	previousAPIKey, previousEngineURL := APIKey, EngineURL
	t.Cleanup(func() {
		newCLIIdentityClient = previousFactory
		APIKey, EngineURL = previousAPIKey, previousEngineURL
	})
	APIKey, EngineURL = "", ""
}

func TestTopLevelIdentityCommandsAreRegistered(t *testing.T) {
	for _, name := range []string{"whoami", "logout"} {
		command, _, err := RootCmd.Find([]string{name})
		if err != nil || command == nil || command.Name() != name {
			t.Fatalf("find %s = %#v, %v", name, command, err)
		}
	}
}

func TestWhoAmIUsesEffectiveCredentialResolutionWithoutPrintingSecrets(t *testing.T) {
	preserveAuthCommandGlobals(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FUSED_ENGINE_URL", "https://env-engine.example")
	t.Setenv("FUSED_API_KEY", "fsk_environment_secret")
	t.Setenv("FUSED_LICENSE_KEY", "fsk_license_secret")
	if err := config.Set("api-key", "fsk_saved_secret"); err != nil {
		t.Fatalf("set saved key: %v", err)
	}
	fixture := &cliIdentityFixture{wantIdentity: &api.WhoAmIResponse{
		Authenticated: true, AccountID: "account", WorkspaceID: "workspace", SubjectID: "subject", SubjectKind: "user",
		DisplayName: "Martins", Email: "martins@example.com", CredentialID: "credential", CredentialSource: "manual_api_key", AuthenticationMethod: "api_key",
	}}
	var gotURL, gotKey string
	newCLIIdentityClient = func(engineURL, apiKey string, _ api.ClientOptions) cliIdentityAPI {
		gotURL, gotKey = engineURL, apiKey
		return fixture
	}
	output := &bytes.Buffer{}
	command := &cobra.Command{Use: "whoami"}
	command.SetOut(output)
	command.SetContext(t.Context())
	if err := runWhoAmI(command); err != nil {
		t.Fatalf("runWhoAmI: %v", err)
	}
	if gotURL != "https://env-engine.example" || gotKey != "fsk_saved_secret" {
		t.Fatalf("effective target = %q key=%q", gotURL, gotKey)
	}
	for _, secret := range []string{"fsk_saved_secret", "fsk_environment_secret", "fsk_license_secret"} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("whoami output leaked %q: %s", secret, output.String())
		}
	}
	for _, wanted := range []string{"Martins (subject)", "saved login/config / manual_api_key", "no expiry reported"} {
		if !strings.Contains(output.String(), wanted) {
			t.Fatalf("whoami output missing %q: %s", wanted, output.String())
		}
	}
}

func TestLogoutUsesSavedLoginDespiteOverridesAndPreservesEngineURL(t *testing.T) {
	preserveAuthCommandGlobals(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FUSED_API_KEY", "fsk_environment_secret")
	t.Setenv("FUSED_LICENSE_KEY", "fsk_license_secret")
	APIKey, EngineURL = "fsk_flag_secret", "https://flag-engine.example"
	if err := config.SaveLogin("https://saved-engine.example", "fsk_saved_secret", "credential-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("SaveLogin: %v", err)
	}
	fixture := &cliIdentityFixture{}
	var gotURL, gotKey string
	newCLIIdentityClient = func(engineURL, apiKey string, _ api.ClientOptions) cliIdentityAPI {
		gotURL, gotKey = engineURL, apiKey
		return fixture
	}
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	ctx, span := provider.Tracer("test").Start(t.Context(), "logout")
	output := &bytes.Buffer{}
	command := &cobra.Command{Use: "logout"}
	command.SetOut(output)
	command.SetContext(ctx)
	if err := runCLILogout(command); err != nil {
		t.Fatalf("runCLILogout: %v", err)
	}
	span.End()
	cfg, err := config.Load()
	if err != nil || gotURL != "https://saved-engine.example" || gotKey != "fsk_saved_secret" || fixture.logoutCalls != 1 {
		t.Fatalf("logout target = %q key=%q calls=%d config=%#v err=%v", gotURL, gotKey, fixture.logoutCalls, cfg, err)
	}
	if cfg.EngineURL != "https://saved-engine.example" || cfg.APIKey != "" || cfg.APIKeyExpiresAt != "" || cfg.CredentialID != "" || cfg.CredentialSource != "" {
		t.Fatalf("config after logout = %#v", cfg)
	}
	if !strings.Contains(output.String(), "FUSED_API_KEY and FUSED_LICENSE_KEY") || strings.Contains(output.String(), "fsk_") {
		t.Fatalf("logout output = %s", output.String())
	}
	if countAppliedChangeEvents(exporter.GetSpans()) != 1 {
		t.Fatalf("expected one applied-change event: %#v", exporter.GetSpans())
	}
}

func TestLogoutFailurePreservesSavedCredentialForRetry(t *testing.T) {
	preserveAuthCommandGlobals(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.SaveLogin("https://saved-engine.example", "fsk_saved_secret", "credential-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("SaveLogin: %v", err)
	}
	fixture := &cliIdentityFixture{logoutErr: errors.New("network unavailable")}
	newCLIIdentityClient = func(string, string, api.ClientOptions) cliIdentityAPI { return fixture }
	command := &cobra.Command{Use: "logout"}
	command.SetContext(t.Context())
	if err := runCLILogout(command); err == nil || strings.Contains(err.Error(), "fsk_saved_secret") {
		t.Fatalf("logout error = %v", err)
	}
	cfg, err := config.Load()
	if err != nil || cfg.APIKey != "fsk_saved_secret" || cfg.CredentialID != "credential-1" {
		t.Fatalf("saved credential was not preserved: %#v, %v", cfg, err)
	}
}

func TestLogoutAlreadyInactiveClearsSavedCredential(t *testing.T) {
	preserveAuthCommandGlobals(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.SaveLogin("https://saved-engine.example", "fsk_saved_secret", "credential-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("SaveLogin: %v", err)
	}
	newCLIIdentityClient = func(string, string, api.ClientOptions) cliIdentityAPI {
		return &cliIdentityFixture{logoutErr: api.ErrCLILogoutAlreadyInactive}
	}
	command := &cobra.Command{Use: "logout"}
	command.SetContext(t.Context())
	if err := runCLILogout(command); err != nil {
		t.Fatalf("runCLILogout: %v", err)
	}
	cfg, _ := config.Load()
	if cfg.APIKey != "" || cfg.CredentialSource != "" {
		t.Fatalf("inactive credential remains: %#v", cfg)
	}
}

func TestLogoutWithoutSavedCredentialIsActionable(t *testing.T) {
	preserveAuthCommandGlobals(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	command := &cobra.Command{Use: "logout"}
	if err := runCLILogout(command); err == nil || !strings.Contains(err.Error(), "fused-cli login") {
		t.Fatalf("logout error = %v", err)
	}
}

func TestLogoutLeavesManuallySavedAPIKeyUnchanged(t *testing.T) {
	preserveAuthCommandGlobals(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := config.Set("api-key", "fsk_manual_secret"); err != nil {
		t.Fatalf("set manual key: %v", err)
	}
	command := &cobra.Command{Use: "logout"}
	if err := runCLILogout(command); err == nil || !strings.Contains(err.Error(), "left unchanged") || strings.Contains(err.Error(), "fsk_manual_secret") {
		t.Fatalf("logout error = %v", err)
	}
	cfg, _ := config.Load()
	if cfg.APIKey != "fsk_manual_secret" {
		t.Fatalf("manual key was changed: %#v", cfg)
	}
}
