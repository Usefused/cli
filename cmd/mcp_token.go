package cmd

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Usefused/cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

var mcpTokenCmd = commandGroup("token", "Manage MCP execution tokens")

var mcpTokenGenerateCmd = &cobra.Command{
	Use:   "generate <mcp-name-or-id> <token-name>",
	Short: "Generate an execution token shared by all versions of an MCP",
	Args:  cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.mcp.token.generate", func(cmd *cobra.Command, args []string) error {
		return runMCPTokenGenerate(cmd, args[0], args[1])
	}),
}

var mcpTokenListCmd = &cobra.Command{
	Use:   "list <mcp-name-or-id>",
	Short: "List execution tokens shared by all versions of an MCP",
	Args:  cobra.ExactArgs(1),
	RunE: WithTelemetry("cli.mcp.token.list", func(cmd *cobra.Command, args []string) error {
		return runMCPTokenList(cmd, args[0])
	}),
}

var mcpTokenRevokeCmd = &cobra.Command{
	Use:   "revoke <mcp-name-or-id> <token-name>",
	Short: "Revoke an execution token shared by all versions of an MCP",
	Args:  cobra.ExactArgs(2),
	RunE: WithTelemetry("cli.mcp.token.revoke", func(cmd *cobra.Command, args []string) error {
		return runMCPTokenRevoke(cmd, args[0], args[1])
	}),
}

// runMCPTokenGenerate validates MCP policies before using the shared issuance path.
func runMCPTokenGenerate(cmd *cobra.Command, target, name string) error {
	request, err := mcpTokenGenerateRequest(cmd, name)
	// Invalid policy flags must stop before family resolution or token mutation.
	if err != nil {
		return err
	}
	return issueAppToken(cmd, appTokenKindMCP, target, request)
}

// mcpTokenGenerateRequest combines MCP-only scope and bindings with the shared expiry policy.
func mcpTokenGenerateRequest(cmd *cobra.Command, name string) (api.AppTokenGenerateRequest, error) {
	allow, err := cmd.Flags().GetStringSlice("allow")
	// Invalid scope input must fail before any token mutation is attempted.
	if err != nil {
		return api.AppTokenGenerateRequest{}, err
	}
	expiresIn, err := appTokenExpirySeconds(cmd)
	// The same expiry contract must apply to both SDK and MCP credentials.
	if err != nil {
		return api.AppTokenGenerateRequest{}, err
	}
	bindings, err := mcpTokenBindingRequests(cmd)
	// Invalid connected-user bindings cannot be partially accepted.
	if err != nil {
		return api.AppTokenGenerateRequest{}, err
	}
	mode := ""
	// Binding mode is explicit only when the caller supplied fixed grants.
	if len(bindings) > 0 {
		mode = "fixed"
	}
	return api.AppTokenGenerateRequest{
		Name: name, Allow: allow, ExpiresIn: expiresIn,
		BindingMode: mode, Bindings: bindings,
	}, nil
}

// mcpTokenBindingRequests parses repeatable fixed grants without partial acceptance.
func mcpTokenBindingRequests(cmd *cobra.Command) ([]api.AppTokenBindingRequest, error) {
	values, err := cmd.Flags().GetStringArray("fixed-binding")
	// Cobra parsing errors invalidate the complete binding request.
	if err != nil {
		return nil, err
	}
	bindings := make([]api.AppTokenBindingRequest, len(values))
	// Each supplied grant is validated before the request can be issued.
	for index, value := range values {
		binding, err := parseMCPTokenBinding(value)
		// Position-aware errors help operators repair one repeated flag safely.
		if err != nil {
			return nil, fmt.Errorf("--fixed-binding %d: %w", index+1, err)
		}
		bindings[index] = binding
	}
	return bindings, nil
}

// parseMCPTokenBinding keeps the public tuple human-readable; Engine resolves
// the slug to its internal service identity atomically with every other binding.
func parseMCPTokenBinding(value string) (api.AppTokenBindingRequest, error) {
	parts := strings.Split(value, ",")
	// Only the required tuple and its optional resource identifier are supported.
	if len(parts) != 3 && len(parts) != 4 {
		return api.AppTokenBindingRequest{}, errors.New("expected service-slug,auth-name,end-user-ref[,resource-id]")
	}
	// Trimming each bounded field keeps equivalent operator input canonical.
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	// Slugs are public service identities; UUID parsing here would leak Engine's
	// internal persistence identity into every agent-token workflow.
	if !api.ValidServiceSlugReference(parts[0]) || parts[1] == "" || parts[2] == "" {
		return api.AppTokenBindingRequest{}, errors.New("a bare or @provider/service slug, auth-name, and end-user-ref are required")
	}
	binding := api.AppTokenBindingRequest{ServiceSlug: parts[0], AuthName: parts[1], EndUserRef: parts[2]}
	// Resource validation applies only to the explicitly supplied fourth tuple field.
	if len(parts) == 4 {
		// Malformed resource identifiers cannot be deferred to connection lookup.
		if _, err := uuid.Parse(parts[3]); err != nil {
			return api.AppTokenBindingRequest{}, errors.New("resource-id must be a UUID")
		}
		binding.ResourceID = &parts[3]
	}
	return binding, nil
}

// runMCPTokenList reports retained policy and usage metadata without credential material.
func runMCPTokenList(cmd *cobra.Command, target string) error {
	tokens, err := loadAppTokens(appTokenKindMCP, target)
	// Resolution or transport failure prevents presenting an incomplete inventory.
	if err != nil {
		return err
	}
	// JSON output preserves all MCP policy and lifecycle fields for automation.
	if wantsJSON(cmd) {
		return writeJSON(cmd, tokens)
	}
	// Human output renders each bounded metadata row without additional lookups.
	for _, token := range tokens {
		fmt.Fprintf(cmd.OutOrStdout(), "ID: %s, Name: %s, Status: %s, Binding: %s, Allow: %s, Uses: %d executions/%d sessions, Expires: %s, Last used: %s, Created: %s\n",
			token.ID, token.Name, token.Status, token.BindingMode, strings.Join(token.Allow, ","), token.ExecutionCount, token.SessionCount,
			tokenExpiryDisplay(token.ExpiresAt), optionalTokenTimeDisplay(token.LastUsedAt), token.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return nil
}

// optionalTokenTimeDisplay gives absent usage timestamps an explicit human value.
func optionalTokenTimeDisplay(value *time.Time) string {
	// A missing last-use time means the credential has never executed anything.
	if value == nil {
		return "never"
	}
	return value.Format(time.RFC3339)
}

// tokenExpiryDisplay distinguishes permanent, expired, and still-active deadlines.
func tokenExpiryDisplay(expiresAt *time.Time) string {
	// A missing deadline is the intentional non-expiring default.
	if expiresAt == nil {
		return "never"
	}
	// Past deadlines are clearer as lifecycle state than as a stale timestamp.
	if time.Now().After(*expiresAt) {
		return "expired"
	}
	return expiresAt.Format(time.RFC3339)
}

// runMCPTokenRevoke removes one named MCP family credential through the shared path.
func runMCPTokenRevoke(cmd *cobra.Command, target, name string) error {
	return revokeAppToken(cmd, appTokenKindMCP, target, name)
}

// init registers MCP token actions and their MCP-specific policy flags.
func init() {
	mcpCmd.AddCommand(mcpTokenCmd)
	mcpTokenCmd.AddCommand(mcpTokenGenerateCmd, mcpTokenListCmd, mcpTokenRevokeCmd)
	addJSONOutputFlag(mcpTokenListCmd)
	mcpTokenGenerateCmd.Flags().StringSlice("allow", []string{api.AppTokenAllowAllWildcard}, "Exact operation IDs to allow; * grants all operations")
	addAppTokenExpiryFlag(mcpTokenGenerateCmd)
	// Repeating the flag supports a different connected-user reference per
	// service while keeping the token itself fixed to the resolved grants.
	mcpTokenGenerateCmd.Flags().StringArray("fixed-binding", nil, "Bind service-slug,auth-name,end-user-ref[,resource-id]; repeat per service/auth pair")
}
