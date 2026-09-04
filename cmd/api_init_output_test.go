package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

// TestAPIInitShowsOpenAPIAndConnectionSteps verifies package-free onboarding
// identifies the exact spec export and the selected OAuth connection command.
func TestAPIInitShowsOpenAPIAndConnectionSteps(t *testing.T) {
	parsed := &configfile.ParsedConfig{SDK: &configfile.SDKConfig{Name: "threadify", Version: "1.0.0", Bucket: "selected-bucket", Services: map[string]configfile.AppService{
		"google-sheets": {SelectAll: true, Auth: &configfile.AppAuth{Type: "oauth", Name: "sheetsOAuth", Ref: "${bucket.auth.gmail.gmailOAuth}"}, Connect: &configfile.AppConnect{Scopes: []string{"sheets.readonly"}}},
	}}}
	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&output)
	printUnifiedInitAPINextStep(command, api.NewClient("https://engine.example", "control-never-print"), parsed, sdkOpenAPITestVersionID)
	for _, want := range []string{
		"fused-cli api openapi '" + sdkOpenAPITestVersionID + "' --out app.openapi.yaml",
		"workspace service connect 'google-sheets' --bucket 'selected-bucket' --user-ref 'user-123' --type 'oauth' --auth-name 'sheetsOAuth'",
		"--auth-ref '${bucket.auth.gmail.gmailOAuth}' --scope 'sheets.readonly'", `"selector":{"end_user_ref":"user-123"}`, `"operation":"<operationId>"`, "${FUSED_SDK_TOKEN:?",
	} {
		// Setup must use literal reusable selectors rather than inferred credential or provider values.
		if !strings.Contains(output.String(), want) {
			t.Errorf("API onboarding missing %q", want)
		}
	}
	// The template must stay readable without leaking management credentials.
	if strings.Contains(output.String(), `\u003c`) || strings.Contains(output.String(), "control-never-print") || strings.Contains(strings.ToLower(output.String()), "readme") {
		t.Fatal("onboarding escaped the placeholder, exposed a credential, or referenced removed documentation")
	}
	// Direct API onboarding must not route users through the generated SDK command namespace.
	if strings.Contains(output.String(), "fused-cli sdk openapi") {
		t.Fatal("onboarding mislabeled the REST API export as an SDK command")
	}
}

// TestAPIInitDoesNotAssignOAuthToOtherOperations protects mixed anonymous/static
// and connected selections from a misleading user selector in the curl template.
func TestAPIInitDoesNotAssignOAuthToOtherOperations(t *testing.T) {
	config := &configfile.SDKConfig{Services: map[string]configfile.AppService{
		"public": {Operations: []string{"public.list"}},
		"gmail":  {Operations: []string{"gmail.list"}, Auth: &configfile.AppAuth{Type: "oauth", Name: "googleOAuth"}},
	}}
	// Only the explicitly connected operation should receive automatic user routing.
	if apiInitNeedsConnection(config, "public.list") || apiInitNeedsConnection(config, "<operationId>") || !apiInitNeedsConnection(config, "gmail.list") {
		t.Fatal("mixed auth selections produced incorrect user routing")
	}
}
