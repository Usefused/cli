package cmd

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

type temporaryOAuthClientOptions struct {
	name, redirectURI string
	scopes            []string
}

type temporaryOAuthClientReceipt struct {
	Name string `json:"name"`
	api.TemporaryOAuthClientRegistration
}

// selectTemporaryOAuthScopes isolates terminal input so command tests can exercise scope discovery without a TTY.
var selectTemporaryOAuthScopes = promptTemporaryOAuthScopes

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
	// Invalid callback or name input must fail before any discovery or registration request.
	if err := validateTemporaryOAuthClientIdentity(options); err != nil {
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
	// An omitted scope opens a reviewable picker only for an interactive human run.
	if len(options.scopes) == 0 {
		// Machine-readable and unattended runs need explicit authority in their arguments.
		if nonInteractive() || wantsJSON(cmd) {
			return errors.New("--scope is required with --json, --no-input, or CI")
		}
		available, err := client.OAuthRegistrationScopes()
		if err != nil {
			return fmt.Errorf("load available OAuth scopes: %w", err)
		}
		options.scopes, err = selectTemporaryOAuthScopes(cmd, available)
		if err != nil {
			return fmt.Errorf("select OAuth scopes: %w", err)
		}
	}
	// Selected scopes still pass the same finite, exact validation as command-line flags.
	if err := validateTemporaryOAuthClientOptions(options); err != nil {
		return err
	}
	options.name = strings.TrimSpace(options.name)
	// A generated consent label identifies this otherwise unnamed local client.
	if options.name == "" {
		options.name, err = generatedTemporaryOAuthClientName()
		if err != nil {
			return err
		}
	}
	registration, err := client.RegisterTemporaryOAuthClient(api.TemporaryOAuthClientRequest{
		Name: options.name, RedirectURI: options.redirectURI, Scopes: options.scopes,
	})
	// An uncertain registration cannot be reported as successful or retried automatically.
	if err != nil {
		return fmt.Errorf("register temporary OAuth client: %w", err)
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "oauth_client")
	receipt := temporaryOAuthClientReceipt{Name: options.name, TemporaryOAuthClientRegistration: registration}
	// Structured output keeps all three one-time fields machine-readable without saving them locally.
	if wantsJSON(cmd) {
		return writeJSON(cmd, receipt)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "OAuth client registered. Save the secret now; Fused shows it once.\nName: %s\nClient ID: %s\nClient secret: %s\nExpires: %s\n", receipt.Name, registration.ClientID, registration.ClientSecret, registration.ClientIDExpiresAt.Format("2006-01-02T15:04:05Z07:00"))
	return err
}

// validateTemporaryOAuthClientIdentity mirrors Engine's local callback and label boundaries before any request.
func validateTemporaryOAuthClientIdentity(options temporaryOAuthClientOptions) error {
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
	// Engine accepts a generated label when none is provided, but bounds supplied names.
	if len(options.name) > 200 {
		return errors.New("--name must be at most 200 bytes")
	}
	return nil
}

// validateTemporaryOAuthClientOptions requires an explicit finite scope ceiling after terminal selection.
func validateTemporaryOAuthClientOptions(options temporaryOAuthClientOptions) error {
	// The selected scope set must not bypass callback validation after the picker returns.
	if err := validateTemporaryOAuthClientIdentity(options); err != nil {
		return err
	}
	// No registration may leave the consent ceiling empty or unbounded.
	if len(options.scopes) == 0 || len(options.scopes) > 64 {
		return errors.New("--scope must select 1 to 64 exact Fused permissions")
	}
	// The MCP issuer's runtime-only permission cannot be combined with management grants.
	if len(options.scopes) > 1 {
		for _, scope := range options.scopes {
			// A combined selection cannot pass Engine's runtime-only scope admission.
			if scope == "mcp.execute" {
				return errors.New("mcp.execute must be the only selected OAuth scope")
			}
		}
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

// generatedTemporaryOAuthClientName gives unnamed clients a short unpredictable label for consent review.
func generatedTemporaryOAuthClientName() (string, error) {
	var suffix [4]byte
	// Entropy failure must not create a misleading repeated client label.
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", errors.New("could not generate an OAuth client name")
	}
	return fmt.Sprintf("Local client %x", suffix), nil
}

// promptTemporaryOAuthScopes presents the issuer's live permission names without preselecting authority.
func promptTemporaryOAuthScopes(cmd *cobra.Command, values []string) ([]string, error) {
	// An interactive terminal is required because the picker is the user's explicit scope review.
	if !isTerminal(os.Stdin) {
		return nil, errors.New("an interactive terminal is required; pass --scope for automation")
	}
	options := make([]huh.Option[string], 0, len(values))
	// Every advertised permission is selectable, with no grant preselected.
	for _, value := range values {
		options = append(options, huh.NewOption(value, value))
	}
	selected := []string{}
	field := huh.NewMultiSelect[string]().Title("Choose OAuth scopes").Description("Space toggles; / filters; Enter confirms. Select only the permissions this client needs.").Options(options...).Filterable(true).Height(15).Validate(requireTemporaryOAuthScopeSelection).Value(&selected)
	// Terminal rendering belongs on stderr so one-time JSON receipts keep stdout clean.
	if err := huh.NewForm(huh.NewGroup(field)).WithInput(os.Stdin).WithOutput(cmd.ErrOrStderr()).Run(); err != nil {
		return nil, err
	}
	return selected, nil
}

// requireTemporaryOAuthScopeSelection keeps the picker open until the user chooses an explicit permission ceiling.
func requireTemporaryOAuthScopeSelection(selected []string) error {
	// An empty selection must not register an unscoped client.
	if len(selected) == 0 {
		return errors.New("select at least one OAuth scope")
	}
	return nil
}

// init keeps temporary client registration separate from provider credential storage and CLI login.
func init() {
	oauth := commandGroup("oauth", "Manage Fused OAuth clients")
	oauth.AddCommand(newTemporaryOAuthClientCommand())
	RootCmd.AddCommand(oauth)
}
