package api

import "testing"

// TestAppPermissionDescriptions preserves exact JSON identifiers and clear type-specific CLI remediation.
func TestAppPermissionDescriptions(t *testing.T) {
	for _, test := range []struct{ permission, resource, want string }{
		{"app.sdk.create", "workspace", "create SDKs in this workspace"},
		{"app.mcp.create", "workspace", "create MCP servers in this workspace"},
		{"app.api.create", "workspace", "create REST APIs in this workspace"},
		{"app.webhook.create", "workspace", "create webhooks in this workspace"},
		{"app.api.manage", "app", "manage the selected REST API"},
		{"app.mcp.tokens.manage", "app", "manage execution tokens for the selected MCP server"},
	} {
		requirement := PermissionRequirement{Permission: test.permission, ResourceType: test.resource, ResourceID: "id"}
		// Human output must communicate the requested type, never an all-app alternative.
		if got := requirement.ProductDescription(); got != test.want {
			t.Errorf("%s = %q, want %q", test.permission, got, test.want)
		}
		// Structured/advanced output preserves the server's exact identifier.
		if got := requirement.Description(); got != test.permission+" on "+test.resource+" (id)" {
			t.Errorf("diagnostic changed: %s", got)
		}
	}
}
