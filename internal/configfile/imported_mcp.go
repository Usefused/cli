package configfile

import (
	"fmt"
	"github.com/google/uuid"
)

// ImportedMCPSelection is the portable reference to one approved catalog, never a provider URL or credential.
type ImportedMCPSelection struct {
	RevisionID        string   `yaml:"revision_id" json:"revision_id"`
	Tools             []string `yaml:"tools,omitempty" json:"tools,omitempty"`
	Prompts           []string `yaml:"prompts,omitempty" json:"prompts,omitempty"`
	Resources         []string `yaml:"resources,omitempty" json:"resources,omitempty"`
	ResourceTemplates []string `yaml:"resource_templates,omitempty" json:"resource_templates,omitempty"`
}

// ValidateImportedMCP rejects implicit authority locally; Engine still owns revision and membership admission.
func ValidateImportedMCP(selection *ImportedMCPSelection, kind ConfigKind) error {
	// Existing physical-only declarations retain their historical behavior.
	if selection == nil {
		return nil
	}
	// SDK and Unified App adapters do not implement imported MCP dispatch.
	if kind != KindMCP {
		return fmt.Errorf("imported MCP capabilities require kind: mcp")
	}
	id, err := uuid.Parse(selection.RevisionID)
	// A concrete nonzero catalog revision is required even for an explicit all-items snapshot.
	if err != nil || id == uuid.Nil {
		return fmt.Errorf("mcp.revision_id must be a nonzero UUID")
	}
	count := 0
	for _, names := range [][]string{selection.Tools, selection.Prompts, selection.Resources, selection.ResourceTemplates} {
		// Duplicate identity checks stay namespace-local: a tool and prompt can share a name.
		if err := validateImportedMCPNames(names); err != nil {
			return err
		}
		count += len(names)
	}
	// An empty object must never substitute for a useful app capability grant.
	if count == 0 {
		return fmt.Errorf("select at least one imported MCP capability")
	}
	return nil
}

// validateImportedMCPNames preserves exact provider identities instead of trimming or silently deduplicating them.
func validateImportedMCPNames(names []string) error {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		// Empty or repeated identities usually indicate an accidental or ambiguous selection.
		if name == "" || seen[name] {
			return fmt.Errorf("imported MCP selections must contain nonempty, unique identities")
		}
		seen[name] = true
	}
	return nil
}
