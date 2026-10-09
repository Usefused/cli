package cmd

import (
	"encoding/json"
	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"strings"
	"testing"
)

// importedCatalogFixture includes same-named protocol entries and template punctuation to detect lossy selection.
func importedCatalogFixture() *api.MCPCatalogSnapshot {
	return &api.MCPCatalogSnapshot{ID: "00000000-0000-4000-8000-000000000003", Catalog: api.MCPCatalog{
		Tools:             []json.RawMessage{json.RawMessage(`{"name":"echo","description":"Repeat input"}`)},
		Prompts:           []json.RawMessage{json.RawMessage(`{"name":"echo"}`)},
		Resources:         []json.RawMessage{json.RawMessage(`{"name":"Guide","uri":"docs://guide"}`)},
		ResourceTemplates: []json.RawMessage{json.RawMessage(`{"name":"Page","uriTemplate":"docs://{+path}"}`)},
	}}
}

// TestMCPCatalogSelectionFreezesExactScope verifies selection controls cannot authorize future or unknown entries.
func TestMCPCatalogSelectionFreezesExactScope(t *testing.T) {
	snapshot := importedCatalogFixture()
	all, err := selectMCPCatalog(snapshot, mcpSelectOptions{all: true})
	// All is materialized into explicit names in every protocol namespace.
	if err != nil || len(all.Tools) != 1 || len(all.Prompts) != 1 || all.Resources[0] != "docs://guide" || all.ResourceTemplates[0] != "docs://{+path}" {
		t.Fatalf("all=%+v err=%v", all, err)
	}
	snapshot.Catalog.Tools = append(snapshot.Catalog.Tools, json.RawMessage(`{"name":"later"}`))
	// A later catalog change must not mutate a previously generated app grant.
	if len(all.Tools) != 1 {
		t.Fatal("selection followed mutable catalog")
	}
	for _, options := range []mcpSelectOptions{
		{revision: "different", all: true},
		{all: true, selection: configfile.ImportedMCPSelection{Tools: []string{"echo"}}},
		{selection: configfile.ImportedMCPSelection{Tools: []string{"missing"}}},
		{selection: configfile.ImportedMCPSelection{Tools: []string{"echo", "echo"}}},
		{},
	} {
		// Stale, conflicting, duplicate, unknown and empty requests are all explicit errors.
		if _, err := selectMCPCatalog(snapshot, options); err == nil {
			t.Fatalf("accepted %+v", options)
		}
	}
}

// TestMCPCatalogFiltersDoNotMutateSavedScope protects the UI-equivalent intersection of search and type filtering.
func TestMCPCatalogFiltersDoNotMutateSavedScope(t *testing.T) {
	snapshot := importedCatalogFixture()
	filtered := filterMCPCatalog(*snapshot, "resources", "guide")
	// Display filters retain revision identity and leave the underlying selection catalog untouched.
	if len(filtered.Catalog.Resources) != 1 || len(filtered.Catalog.Tools) != 0 || len(snapshot.Catalog.Tools) != 1 || filtered.ID != snapshot.ID {
		t.Fatal("filter changed source scope")
	}
	// Hostile provider text must not emit terminal control bytes in human output.
	if strings.ContainsAny(mcpTerminalText("name\x1b[2J\nother"), "\x1b\n") {
		t.Fatal("provider control sequence reached terminal")
	}
}
