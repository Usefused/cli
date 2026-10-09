package cmd

import (
	"fmt"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
)

type mcpSelectOptions struct {
	revision  string
	all       bool
	selection configfile.ImportedMCPSelection
}

// selectMCPCatalog pins a single reviewed revision and expands all only into today's finite identities.
func selectMCPCatalog(snapshot *api.MCPCatalogSnapshot, options mcpSelectOptions) (*configfile.ImportedMCPSelection, error) {
	// A service without an approved catalog cannot contribute imported capabilities.
	if snapshot == nil {
		return nil, fmt.Errorf("no imported MCP catalog; run workspace service mcp discover and apply first")
	}
	// Review-to-selection races require another review instead of silently switching revisions.
	if options.revision != "" && options.revision != snapshot.ID {
		return nil, fmt.Errorf("MCP catalog revision changed; review the saved catalog again")
	}
	selection := options.selection
	selection.RevisionID = snapshot.ID
	targets := []*[]string{&selection.Tools, &selection.Prompts, &selection.Resources, &selection.ResourceTemplates}
	for index, group := range mcpCatalogGroups(&snapshot.Catalog) {
		selected, err := selectMCPCatalogGroup(group, *targets[index], options.all)
		// One invalid namespace makes the entire intended grant unusable.
		if err != nil {
			return nil, err
		}
		*targets[index] = selected
	}
	// Apply the same local kind and duplicate rules as hand-authored YAML.
	if err := configfile.ValidateImportedMCP(&selection, configfile.KindMCP); err != nil {
		return nil, err
	}
	return &selection, nil
}

// selectMCPCatalogGroup validates membership without treating display labels or substring matches as authority.
func selectMCPCatalogGroup(group mcpCatalogGroup, wanted []string, all bool) ([]string, error) {
	// Explicit names combined with all are competing authoring intents.
	if all && len(wanted) > 0 {
		return nil, fmt.Errorf("--mcp-all cannot be combined with explicit capability flags")
	}
	available := map[string]bool{}
	complete := make([]string, 0, len(*group.items))
	for _, raw := range *group.items {
		name := readMCPCatalogItem(raw).identity(group.kind)
		available[name] = true
		complete = append(complete, name)
	}
	// Materializing names freezes all-items selection instead of authorizing future catalog additions.
	if all {
		return complete, nil
	}
	for _, name := range wanted {
		// Exact names and URI templates may contain commas, spaces, or braces and are never normalized.
		if !available[name] {
			return nil, fmt.Errorf("unknown or duplicate %s capability %q", group.kind, name)
		}
		delete(available, name)
	}
	return append([]string(nil), wanted...), nil
}
