package cmd

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Usefused/cli/internal/api"
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

func runMCPTokenGenerate(cmd *cobra.Command, target, name string) error {
	request, err := mcpTokenGenerateRequest(cmd, name)
	if err != nil {
		return err
	}
	return issueAppToken(cmd, appTokenKindMCP, target, request)
}

func mcpTokenGenerateRequest(cmd *cobra.Command, name string) (api.AppTokenGenerateRequest, error) {
	allow, err := cmd.Flags().GetStringSlice("allow")
	if err != nil {
		return api.AppTokenGenerateRequest{}, err
	}
	expiresIn, err := mcpTokenExpirySeconds(cmd)
	if err != nil {
		return api.AppTokenGenerateRequest{}, err
	}
	return api.AppTokenGenerateRequest{Name: name, Allow: allow, ExpiresIn: expiresIn}, nil
}

func mcpTokenExpirySeconds(cmd *cobra.Command) (*int64, error) {
	raw, err := cmd.Flags().GetString("expires-in")
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, err
	}
	duration, err := time.ParseDuration(raw)
	// Engine accepts whole seconds; rejecting truncation keeps the lifetime the
	// operator requested instead of silently extending or shortening it.
	if err != nil || duration <= 0 || duration%time.Second != 0 {
		return nil, errors.New("--expires-in must be a positive whole-second duration")
	}
	seconds := int64(duration / time.Second)
	return &seconds, nil
}

func runMCPTokenList(cmd *cobra.Command, target string) error {
	tokens, err := loadAppTokens(appTokenKindMCP, target)
	if err != nil {
		return err
	}
	if wantsJSON(cmd) {
		return writeJSON(cmd, tokens)
	}
	for _, token := range tokens {
		fmt.Fprintf(cmd.OutOrStdout(), "ID: %s, Name: %s, Allow: %s, Expires: %s, Last used: %s, Created: %s\n",
			token.ID, token.Name, strings.Join(token.Allow, ","), tokenExpiryDisplay(token.ExpiresAt), optionalTokenTimeDisplay(token.LastUsedAt), token.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return nil
}

func optionalTokenTimeDisplay(value *time.Time) string {
	if value == nil {
		return "never"
	}
	return value.Format(time.RFC3339)
}

func tokenExpiryDisplay(expiresAt *time.Time) string {
	if expiresAt == nil {
		return "never"
	}
	if time.Now().After(*expiresAt) {
		return "expired"
	}
	return expiresAt.Format(time.RFC3339)
}

func runMCPTokenRevoke(cmd *cobra.Command, target, name string) error {
	return revokeAppToken(cmd, appTokenKindMCP, target, name)
}

func init() {
	mcpCmd.AddCommand(mcpTokenCmd)
	mcpTokenCmd.AddCommand(mcpTokenGenerateCmd, mcpTokenListCmd, mcpTokenRevokeCmd)
	addJSONOutputFlag(mcpTokenListCmd)
	mcpTokenGenerateCmd.Flags().StringSlice("allow", []string{api.AppTokenAllowAllWildcard}, "Exact operation IDs to allow; * grants all operations")
	mcpTokenGenerateCmd.Flags().String("expires-in", "", "Optional token lifetime, for example 15m or 1h")
}
