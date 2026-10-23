package cmd

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestProviderCredentialSetupHasOnePublicEntryPoint keeps setup under connect while retaining scoped removal.
func TestProviderCredentialSetupHasOnePublicEntryPoint(t *testing.T) {
	command, _, err := RootCmd.Find([]string{"bucket", "credential"})
	// Provider deletion still needs a public owner even after setup moves to connect.
	if err != nil || command.Name() != "credential" {
		t.Fatal("provider credential group is missing")
	}
	for _, child := range command.Commands() {
		// Removing the duplicate means no visible, hidden, or forwarding set command remains.
		if child.Name() == "set" {
			t.Fatal("provider credential setup is duplicated")
		}
	}
}

// TestPublicCommandTreeRejectsCompatibilityAliases prevents hidden forwarding commands from returning unnoticed.
func TestPublicCommandTreeRejectsCompatibilityAliases(t *testing.T) {
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		// Public commands have a single visible spelling; renamed paths cannot hide in help or completion.
		if len(command.Aliases) != 0 || command.Hidden || command.Deprecated != "" {
			t.Errorf("noncanonical command %s: aliases=%v hidden=%t deprecated=%q", command.CommandPath(), command.Aliases, command.Hidden, command.Deprecated)
		}
		checkFlag := func(flag *pflag.Flag) {
			// Normal short options remain supported; deprecated or hidden long options must not preserve old syntax.
			if flag.Hidden || flag.Deprecated != "" {
				t.Errorf("noncanonical flag %s --%s", command.CommandPath(), flag.Name)
			}
		}
		command.LocalNonPersistentFlags().VisitAll(checkFlag)
		command.PersistentFlags().VisitAll(checkFlag)
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(RootCmd)
	for _, name := range []string{"sdk", "mcp", "rest", "api", "unified-app", "secret", "value", "team", "user"} {
		command, _, err := RootCmd.Find([]string{name})
		// Assert exact removal separately so a visible forwarding command also fails the contract.
		if err == nil && command != RootCmd {
			t.Errorf("removed root %s is still registered", name)
		}
	}
}

// TestActiveDocsUseCanonicalCommandNames catches wrapped command examples as well as ordinary one-line snippets.
func TestActiveDocsUseCanonicalCommandNames(t *testing.T) {
	obsolete := regexp.MustCompile("(?:fused-cli\\s+|`)(?:(?:sdk|mcp|api|unified-app)\\s+(?:init|plan|apply|list|versions|token|download|openapi|show|sync|invoke|activity|schedule|promote)\\b|secret\\s+(?:set|delete|list|--help)\\b|value\\s+(?:set|delete|list)\\b|(?:team|user)\\s+(?:list|show|create|update|access|credential|member|archive|eligible-owners)\\b|workspace\\s+(?:services?\\s+(?:list|show|add|delete|connect|versions|operations|webhooks)|has)\\b|services\\s+refresh-missing-contracts\\b|service\\s+(?:app|search|versions|operations|webhooks|delete)\\b|bucket\\s+(?:secrets|values)\\b)")
	for _, root := range []string{"../README.md", "../docs", "../skills/dev"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			} // Missing documentation is a failed audit, not a clean result.
			if entry.IsDir() || filepath.Ext(path) != ".md" {
				return nil
			} // Only current Markdown guidance is part of this contract.
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			} // Unreadable content cannot count as verified.
			for _, match := range obsolete.FindAllIndex(data, -1) {
				t.Errorf("%s:%d: obsolete command %q", path, strings.Count(string(data[:match[0]]), "\n")+1, string(data[match[0]:match[1]]))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		} // Frozen release snapshots intentionally stay outside the active documentation roots.
	}
}
