package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/config"
	"github.com/spf13/cobra"
)

type cliIdentityAPI interface {
	WhoAmI() (*api.WhoAmIResponse, error)
	LogoutCLI() error
}

var newCLIIdentityClient = func(engineURL, apiKey string, options api.ClientOptions) cliIdentityAPI {
	return api.NewClientWithOptions(engineURL, apiKey, options)
}

var whoAmICmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show the identity used for Engine requests",
	Args:  cobra.NoArgs,
	RunE: WithTelemetry("cli.whoami", func(cmd *cobra.Command, _ []string) error {
		return runWhoAmI(cmd)
	}),
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Revoke and remove the saved CLI login",
	Long: `Revoke and remove the managed credential created by fused-cli login.

Logout always uses the Engine URL and credential stored by login. Inherited
--key, --engine-url, and credential environment variables cannot retarget it.`,
	Args: cobra.NoArgs,
	RunE: WithTelemetry("cli.logout", func(cmd *cobra.Command, _ []string) error {
		return runCLILogout(cmd)
	}),
}

func init() {
	RootCmd.AddCommand(whoAmICmd, logoutCmd)
	addJSONOutputFlag(whoAmICmd)
}

func runWhoAmI(cmd *cobra.Command) error {
	engineURL, err := GetEngineURL()
	if err != nil {
		return err
	}
	credential := resolveAPIKey()
	if credential.value == "" {
		return errors.New("api-key is not configured; run fused-cli login or set FUSED_API_KEY")
	}
	client := newCLIIdentityClient(engineURL, credential.value, api.ClientOptions{
		Context: cmd.Context(), Timeout: RequestTimeout, RequestID: RequestID, DisableProgress: nonInteractive(),
	})
	identity, err := client.WhoAmI()
	if err != nil {
		return fmt.Errorf("identify current CLI credential: %w", err)
	}
	if wantsJSON(cmd) {
		return writeJSON(cmd, whoAmIResult{
			Authenticated: true, Engine: engineURL, LocalCredentialSource: credential.source, Identity: identity,
		})
	}
	writeWhoAmI(cmd, engineURL, credential.source, identity)
	return nil
}

type whoAmIResult struct {
	Authenticated         bool                `json:"authenticated"`
	Engine                string              `json:"engine"`
	LocalCredentialSource string              `json:"local_credential_source"`
	Identity              *api.WhoAmIResponse `json:"identity"`
}

func writeWhoAmI(cmd *cobra.Command, engineURL, localSource string, identity *api.WhoAmIResponse) {
	output := cmd.OutOrStdout()
	fmt.Fprintln(output, "Authenticated: yes")
	fmt.Fprintln(output, "Engine:", printableIdentityValue(engineURL))
	fmt.Fprintln(output, "Identity:", identityLabel(identity))
	if identity.Email != "" {
		fmt.Fprintln(output, "Email:", printableIdentityValue(identity.Email))
	}
	fmt.Fprintln(output, "Account:", printableIdentityValue(identity.AccountID))
	fmt.Fprintln(output, "Workspace:", printableIdentityValue(identity.WorkspaceID))
	fmt.Fprintln(output, "Credential source:", credentialSourceLabel(localSource, identity.CredentialSource))
	fmt.Fprintln(output, "Authentication:", printableIdentityValue(identity.AuthenticationMethod))
	fmt.Fprintln(output, "Expires:", expiryLabel(identity.ExpiresAt))
}

func identityLabel(identity *api.WhoAmIResponse) string {
	name := printableIdentityValue(identity.DisplayName)
	if name == "unknown" {
		name = printableIdentityValue(identity.SubjectKind)
	}
	return name + " (" + printableIdentityValue(identity.SubjectID) + ")"
}

func credentialSourceLabel(local, remote string) string {
	local = printableIdentityValue(local)
	remote = printableIdentityValue(remote)
	if remote == "unknown" || remote == local {
		return local
	}
	return local + " / " + remote
}

func expiryLabel(expiresAt *time.Time) string {
	if expiresAt == nil || expiresAt.IsZero() {
		return "no expiry reported"
	}
	return expiresAt.UTC().Format(time.RFC3339)
}

func printableIdentityValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 {
		return "unknown"
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return "unknown"
		}
	}
	return value
}

func runCLILogout(cmd *cobra.Command) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load saved CLI login: %w", err)
	}
	if cfg.APIKey == "" {
		return errors.New("no saved managed CLI login was found; run fused-cli login to sign in")
	}
	if cfg.CredentialSource != config.ManagedCLILoginSource {
		return errors.New("no saved managed CLI login was found; the manually configured api-key was left unchanged")
	}
	if err := revokeSavedCLILogin(cmd, cfg); err != nil && !errors.Is(err, api.ErrCLILogoutAlreadyInactive) {
		return err
	}
	changed, err := config.ClearCredential()
	if err != nil {
		return fmt.Errorf("remove saved CLI credential: %w", err)
	}
	recordAppliedChangeIf(cmd.Context(), cmd.CommandPath(), "cli_login", changed)
	fmt.Fprintln(cmd.OutOrStdout(), "✅ Logged out. The saved CLI credential was removed; the Engine URL was preserved.")
	writeEnvironmentCredentialWarning(cmd)
	return nil
}

func revokeSavedCLILogin(cmd *cobra.Command, cfg *config.Config) error {
	if cfg.EngineURL == "" || cfg.CredentialID == "" {
		return errors.New("saved managed login metadata is incomplete; sign in again before logging out")
	}
	client := newCLIIdentityClient(cfg.EngineURL, cfg.APIKey, api.ClientOptions{
		Context: cmd.Context(), Timeout: RequestTimeout, RequestID: RequestID, DisableProgress: true,
	})
	if err := client.LogoutCLI(); err != nil {
		return fmt.Errorf("revoke saved CLI login: %w", err)
	}
	return nil
}

func writeEnvironmentCredentialWarning(cmd *cobra.Command) {
	variables := make([]string, 0, 2)
	for _, name := range []string{"FUSED_API_KEY", "FUSED_LICENSE_KEY"} {
		if os.Getenv(name) != "" {
			variables = append(variables, name)
		}
	}
	if len(variables) == 0 {
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), "⚠️  %s remains set; future commands can still authenticate through the environment.\n", strings.Join(variables, " and "))
}
