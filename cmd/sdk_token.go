package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var sdkTokenCmd = &cobra.Command{
	Use:   "token <sdk-id> [generate|list|revoke] [args...]",
	Short: "Manage SDK tokens",
	Args:  validateSDKTokenArgs,
	// Why: Write to OTEL to audit user/agent-triggered mutative execution.
	RunE: WithTelemetry("cli.sdk.token", func(cmd *cobra.Command, args []string) error {
		return runSDKTokenAction(cmd, args)
	}),
	ValidArgsFunction: completeSDKTokenArgs,
}

func validateSDKTokenArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) < 2 {
		return fmt.Errorf("sdk token action is required (e.g. generate, list, revoke)")
	}
	action := args[1]
	if action != "generate" && action != "list" && action != "revoke" {
		return fmt.Errorf("unknown sdk token action %q", action)
	}
	if action == "generate" {
		if len(args) != 3 {
			return fmt.Errorf("generate accepts exactly 1 arg after action (token-name)")
		}
	} else if action == "list" {
		if len(args) != 2 {
			return fmt.Errorf("list accepts exactly 0 args after action")
		}
	} else if action == "revoke" {
		if len(args) != 3 {
			return fmt.Errorf("revoke accepts exactly 1 arg after action (token-name)")
		}
	}
	return nil
}

func runSDKTokenAction(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}

	artifactID := args[0]
	action := args[1]

	switch action {
	case "generate":
		name := args[2]
		return runSDKTokenGenerate(cmd, artifactID, name)
	case "list":
		return runSDKTokenList(cmd, artifactID)
	case "revoke":
		name := args[2]
		return runSDKTokenRevoke(cmd, artifactID, name)
	default:
		return fmt.Errorf("unknown action %s", action)
	}
}

func runSDKTokenGenerate(cmd *cobra.Command, artifactID, name string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	res, err := client.GenerateSDKToken(artifactID, name)
	if err != nil {
		return err
	}
	fmt.Printf("Token generated: %s\n", res.Token)
	fmt.Printf("Make sure to copy it now, it won't be shown again.\n")
	return nil
}

func runSDKTokenList(cmd *cobra.Command, artifactID string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	tokens, err := client.ListSDKTokens(artifactID)
	if err != nil {
		return err
	}
	for _, t := range tokens {
		fmt.Printf("ID: %s, Name: %s, Created: %s\n", t.ID, t.Name, t.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	return nil
}

func runSDKTokenRevoke(cmd *cobra.Command, artifactID, name string) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	err = client.RevokeSDKToken(artifactID, name)
	if err != nil {
		return err
	}
	fmt.Printf("Token '%s' revoked successfully.\n", name)
	return nil
}

func completeSDKTokenArgs(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		// Provide suggestions for SDK IDs if possible, otherwise no completions
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	if len(args) == 1 {
		actions := []string{"generate", "list", "revoke"}
		var matches []string
		for _, a := range actions {
			if strings.HasPrefix(a, toComplete) {
				matches = append(matches, a)
			}
		}
		return matches, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	sdkCmd.AddCommand(sdkTokenCmd)
}
