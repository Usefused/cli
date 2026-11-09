package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/spf13/cobra"
)

type temporaryOAuthClientOptions struct {
	name, redirectURI string
	scopes            []string
}

// newTemporaryOAuthClientCommand exposes Engine's one-hour local-client registration without requiring a prior login.
func newTemporaryOAuthClientCommand() *cobra.Command {
	options := &temporaryOAuthClientOptions{}
	command := &cobra.Command{
		Use: "register", Short: "Register a one-hour OAuth client for a local application", Args: cobra.NoArgs,
		// The command uses one mutation path so no other OAuth client state is changed.
		RunE: WithTelemetry("cli.oauth.client.register", func(cmd *cobra.Command, _ []string) error {
			return runTemporaryOAuthClientRegistration(cmd, *options)
		}),
	}
	command.Flags().StringVar(&options.name, "name", "", "Name shown during user consent")
	command.Flags().StringVar(&options.redirectURI, "redirect-uri", "", "Local HTTP callback URL, such as http://localhost:4321/callback")
	command.Flags().StringArrayVar(&options.scopes, "scope", nil, "Exact Fused permission to request; repeat for each scope")
	addJSONOutputFlag(command)
	return command
}

// runTemporaryOAuthClientRegistration prints the one-time secret only after Engine confirms registration.
func runTemporaryOAuthClientRegistration(cmd *cobra.Command, options temporaryOAuthClientOptions) error {
	// Invalid local metadata must fail before an anonymous registration consumes Engine capacity.
	if err := validateTemporaryOAuthClientOptions(options); err != nil {
		return err
	}
	engineURL, err := normalizedLoginEngineURL()
	if err != nil {
		return err
	}
	// This endpoint is public; ambient API keys and saved login credentials must not be sent.
	client := api.NewClientWithOptions(engineURL, "", api.ClientOptions{
		Context: cmd.Context(), Timeout: RequestTimeout, RequestID: RequestID, DisableProgress: true,
	})
	registration, err := client.RegisterTemporaryOAuthClient(api.TemporaryOAuthClientRequest{
		Name: strings.TrimSpace(options.name), RedirectURI: options.redirectURI, Scopes: options.scopes,
	})
	// An uncertain registration cannot be reported as successful or retried automatically.
	if err != nil {
		return fmt.Errorf("register temporary OAuth client: %w", err)
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "oauth_client")
	// Structured output keeps all three one-time fields machine-readable without saving them locally.
	if wantsJSON(cmd) {
		return writeJSON(cmd, registration)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "OAuth client registered. Save the secret now; Fused shows it once.\nClient ID: %s\nClient secret: %s\nExpires: %s\n", registration.ClientID, registration.ClientSecret, registration.ClientIDExpiresAt.Format("2006-01-02T15:04:05Z07:00"))
	return err
}

// validateTemporaryOAuthClientOptions mirrors Engine's local callback boundary before making a mutation.
func validateTemporaryOAuthClientOptions(options temporaryOAuthClientOptions) error {
	parsed, err := url.Parse(options.redirectURI)
	// Anonymous registration must return authorization codes only to an HTTP loopback listener.
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("--redirect-uri must be an HTTP loopback URL")
	}
	host := parsed.Hostname()
	// A loopback hostname is required even when an otherwise valid URL uses HTTP.
	if !strings.EqualFold(host, "localhost") && host != "127.0.0.1" && host != "::1" {
		return errors.New("--redirect-uri must use localhost or a loopback IP address")
	}
	// Engine bounds names and requires a finite, explicit permission set.
	if len(options.name) > 200 || len(options.scopes) == 0 || len(options.scopes) > 64 {
		return errors.New("--name must be at most 200 bytes and --scope must appear 1 to 64 times")
	}
	seen := make(map[string]struct{}, len(options.scopes))
	for _, scope := range options.scopes {
		// Whitespace and duplicates would change or ambiguously repeat the consent ceiling.
		if scope == "" || strings.TrimSpace(scope) != scope {
			return errors.New("--scope values must be nonempty exact Fused permission names")
		}
		if _, duplicate := seen[scope]; duplicate {
			return fmt.Errorf("duplicate --scope %q", scope)
		}
		seen[scope] = struct{}{}
	}
	return nil
}

// init keeps temporary client registration separate from provider credential storage and CLI login.
func init() {
	oauth := commandGroup("oauth", "Manage Fused OAuth clients")
	client := commandGroup("client", "Manage Fused OAuth client registrations")
	client.AddCommand(newTemporaryOAuthClientCommand())
	oauth.AddCommand(client)
	RootCmd.AddCommand(oauth)
}
