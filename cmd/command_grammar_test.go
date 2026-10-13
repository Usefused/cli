package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestCanonicalCommandParentsRequireAction covers only command groups that remain part of the public tree.
func TestCanonicalCommandParentsRequireAction(t *testing.T) {
	parents := []struct {
		name string
		args []string
	}{
		{name: "bucket", args: []string{"bucket"}},
		{name: "bucket secret", args: []string{"bucket", "secret"}},
		{name: "bucket credential", args: []string{"bucket", "credential"}},
		{name: "service", args: []string{"service"}},
		{name: "bucket value", args: []string{"bucket", "value"}},
		{name: "workspace", args: []string{"workspace"}},

		{name: "workspace service", args: []string{"service"}},
		{name: "workspace service version", args: []string{"service", "version"}},
		{name: "config", args: []string{"config"}},
		{name: "import", args: []string{"import"}},
		{name: "app", args: []string{"app"}},
		{name: "mcp token", args: []string{"app", "token"}},
		{name: "app operation", args: []string{"app", "operation"}},
		{name: "app service", args: []string{"app", "service"}},
		{name: "app token", args: []string{"app", "token"}},
		{name: "app webhook", args: []string{"app", "webhook"}},
		{name: "skill", args: []string{"skill"}},
		{name: "team", args: []string{"workspace", "team"}},
		{name: "user", args: []string{"workspace", "user"}},
	}
	for _, parent := range parents {
		t.Run(parent.name, func(t *testing.T) {
			message := runCommandInDirExpectError(t, t.TempDir(), "http://unused.invalid", parent.args)
			if !strings.Contains(message, "requires a subcommand") {
				t.Fatalf("expected an actionable usage error, got %q", message)
			}
		})
	}
}

func TestCommandTreeUsesCanonicalCobraExecutionContract(t *testing.T) {
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		for _, child := range command.Commands() {
			// Cobra owns these generated help/completion commands; the contract
			// below applies to Fused commands registered by this package.
			if child.Name() == "help" || child.Name() == "completion" {
				continue
			}
			if child.Run != nil {
				t.Errorf("%s uses Run; executable commands must return errors through RunE", child.CommandPath())
			}
			if child.Args == nil {
				t.Errorf("%s does not declare positional argument validation", child.CommandPath())
			}
			if len(child.Commands()) > 0 && child.RunE == nil {
				t.Errorf("%s is a command group without requireSubcommand behavior", child.CommandPath())
			}
			walk(child)
		}
	}
	walk(RootCmd)
}

// TestCommandUsageUsesConcreteArgumentNames verifies that command usage uses concrete argument names.
func TestCommandUsageUsesConcreteArgumentNames(t *testing.T) {
	// app-id is concrete now that every app type shares the same management namespace.
	ambiguous := []string{"family", "<name>", "<user>", "<ref>", "service_name", "operationId", "webhookId"}
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		for _, child := range command.Commands() {
			if child.Use != strings.ToLower(child.Use) {
				t.Errorf("%s usage must use lowercase kebab-case argument names: %q", child.CommandPath(), child.Use)
			}
			if strings.Contains(child.Use, "_") {
				t.Errorf("%s usage must use kebab-case, not underscores: %q", child.CommandPath(), child.Use)
			}
			for _, placeholder := range ambiguous {
				if strings.Contains(child.Use, placeholder) {
					t.Errorf("%s exposes ambiguous argument %q in usage %q", child.CommandPath(), placeholder, child.Use)
				}
			}
			walk(child)
		}
	}
	walk(RootCmd)
}

// TestAppsShareOneManagementRoot rejects all removed type-specific roots.
func TestAppsShareOneManagementRoot(t *testing.T) {
	for _, name := range []string{"sdk", "mcp", "api", "unified-app", "artifact"} {
		command, _, err := RootCmd.Find([]string{name})
		if err == nil && command != RootCmd {
			t.Fatalf("removed root %s remains registered", name)
		} // No hidden compatibility commands.
	}
	command, _, err := RootCmd.Find([]string{"app"})
	if err != nil || command != appCmd {
		t.Fatalf("shared app root missing: %v", err)
	} // Every app capability lives here.
}

// TestSDKPromptIsNotRegistered protects the temporary removal from the public SDK command surface.
func TestSDKPromptIsNotRegistered(t *testing.T) {
	command, remaining, err := sdkCmd.Find([]string{"prompt"})
	// Cobra leaves an unknown child unresolved at its parent, which proves there is no executable prompt command.
	if err != nil || command != sdkCmd || len(remaining) != 1 || remaining[0] != "prompt" {
		t.Fatalf("paused SDK command is still registered: command=%v remaining=%v err=%v", command, remaining, err)
	}
}

// TestCanonicalCommandsRejectSupersededForms prevents removed paths from silently acting as aliases.
func TestCanonicalCommandsRejectSupersededForms(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "bucket noun first", args: []string{"bucket", "prod", "create"}},
		{name: "bucket remove", args: []string{"bucket", "remove", "prod"}},
		{name: "bucket artifacts", args: []string{"bucket", "artifacts", "prod"}},
		{name: "connect noun first", args: []string{"connect", "github", "get"}},
		{name: "connect remove", args: []string{"connect", "remove", "github"}},
		{name: "secret noun first", args: []string{"secret", "github", "set", "token"}},
		{name: "secret remove", args: []string{"secret", "remove", "github", "token"}},
		{name: "service noun first", args: []string{"service", "github", "show"}},
		{name: "value noun first", args: []string{"bucket", "value", "prod", "list"}},
		{name: "top-level value list", args: []string{"value", "list", "prod"}},
		{name: "top-level value set", args: []string{"value", "set", "prod", "github", "header", "X-Region", "eu"}},
		{name: "top-level value delete", args: []string{"value", "delete", "prod", "github", "X-Region"}},
		{name: "bucket values shorthand", args: []string{"bucket", "values", "prod"}},
		{name: "value remove", args: []string{"bucket", "value", "remove", "prod", "github", "token"}},
		{name: "workspace service noun first", args: []string{"service", "github", "show"}},
		{name: "workspace service delete", args: []string{"service", "delete", "github"}},
		{name: "workspace version delete", args: []string{"service", "version", "delete", "github", "v1"}},
		{name: "sdk noun-first show", args: []string{"sdk", "security@1.0.0", "show"}},
		{name: "sdk noun-first download", args: []string{"sdk", "security@1.0.0", "download"}},
		{name: "app service noun-first", args: []string{"app", "service", "github", "add"}},
		{name: "app operation noun-first", args: []string{"app", "operation", "github", "add", "listUsers"}},
		{name: "app token noun-first", args: []string{"app", "token", "security", "list"}},
		{name: "app webhook noun-first", args: []string{"app", "webhook", "github", "add", "push"}},
		{name: "mcp noun-first remove", args: []string{"mcp", "support@1.0.0", "remove"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := runCommandInDirExpectError(t, t.TempDir(), "http://unused.invalid", test.args)
			if !strings.Contains(message, "unknown command") {
				t.Fatalf("expected removed form to be rejected, got %q", message)
			}
		})
	}
}

// TestActionFlagsStayOnConsumingSubcommand keeps removed command paths from leaving inherited flags behind.
func TestActionFlagsStayOnConsumingSubcommand(t *testing.T) {
	tests := []struct {
		command *cobra.Command
		flag    string
		present bool
	}{
		{secretCmd, "bucket", false}, {secretSetCmd, "bucket", true}, {secretListCmd, "bucket", false}, {secretDeleteCmd, "bucket", true},
		{serviceCmd, "search", false}, {serviceWebhooksCmd, "search", false}, {serviceOperationsCmd, "search", true},
		{workspaceServiceCmd, "force", false}, {workspaceServiceVersionCmd, "force", false},
		{workspaceServiceDeleteCmd, "force", true}, {workspaceServiceVersionDeleteCmd, "force", true},
	}
	for _, test := range tests {
		assertCommandFlagPresence(t, test.command, test.flag, test.present)
	}
}

// assertCommandFlagPresence checks one ownership rule so the table remains easy to extend.
func assertCommandFlagPresence(t *testing.T, command *cobra.Command, flag string, present bool) {
	t.Helper()
	found := command.Flags().Lookup(flag) != nil
	// A mismatch identifies the exact command/flag pair instead of a compound assertion.
	if found != present {
		t.Fatalf("%s flag --%s presence=%v, want %v", command.CommandPath(), flag, found, present)
	}
}

// TestSecretSetDefaultsToPromptsAndRequiresStdinWithoutInput verifies the
// terminal default and the explicit automation-safe credential channel.
func TestSecretSetDefaultsToPromptsAndRequiresStdinWithoutInput(t *testing.T) {
	previousInteractive, previousStdin, previousNoInput := secretSetInteractive, secretSetValueStdin, NoInput
	t.Cleanup(func() {
		secretSetInteractive = previousInteractive
		secretSetValueStdin = previousStdin
		NoInput = previousNoInput
	})
	t.Setenv("CI", "")

	secretSetInteractive, secretSetValueStdin = false, false
	NoInput = false
	// A normal terminal invocation defers value collection to the selected auth prompt.
	if err := validateSecretSetArgs(secretSetCmd, []string{"github"}); err != nil {
		t.Fatalf("default interactive validation: %v", err)
	}
	value, err := readSecretValue(secretSetCmd)
	if err != nil || value != "" {
		t.Fatalf("default prompt seed = %q, %v", value, err)
	}
	NoInput = true
	// Disabling prompts without providing stdin must fail before any service lookup.
	if err := validateSecretSetArgs(secretSetCmd, []string{"github"}); err == nil || !strings.Contains(err.Error(), "--value-stdin") {
		t.Fatalf("expected missing non-interactive input error, got %v", err)
	}
	secretSetValueStdin = true
	// Extra positional values remain forbidden so secrets cannot leak through argv.
	if err := validateSecretSetArgs(secretSetCmd, []string{"github", "visible-secret"}); err == nil {
		t.Fatal("secret values in argv must be rejected")
	}
	secretSetCmd.SetIn(strings.NewReader("sensitive-value\n"))
	value, err = readSecretValue(secretSetCmd)
	if err != nil {
		t.Fatalf("read stdin credential: %v", err)
	}
	if value != "sensitive-value" {
		t.Fatalf("credential = %q, want trimmed line ending", value)
	}
}

// TestValueSetUsesExactLookupsAndEmitsAuditEvent exercises bucket value set through its public command path.
func TestValueSetUsesExactLookupsAndEmitsAuditEvent(t *testing.T) {
	const bucketID = "11111111-1111-4111-8111-111111111111"
	var valueRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/graphql":
			var body struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode service query: %v", err)
			}
			if !strings.Contains(body.Query, "GetServiceInfo") {
				t.Fatalf("expected exact service lookup, got %s", body.Query)
			}
			_, _ = w.Write([]byte(`{"data":{"service":{"id":"svc-1"}}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/workspace/buckets/"+bucketID+"/values":
			if err := json.NewDecoder(r.Body).Decode(&valueRequest); err != nil {
				t.Fatalf("decode value request: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previousProvider) })

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"bucket", "value", "set", bucketID, "github", "header", "X-Token", "sensitive-value"})
	if !strings.Contains(out, `Bucket value "X-Token" set.`) {
		t.Fatalf("unexpected output: %q", out)
	}
	if valueRequest["service_id"] != "svc-1" || valueRequest["location"] != "header" || valueRequest["key_name"] != "X-Token" {
		t.Fatalf("unexpected value request: %#v", valueRequest)
	}
	if got := countAppliedChangeEvents(exporter.GetSpans()); got != 1 {
		t.Fatalf("applied change event count = %d, want 1", got)
	}
	if strings.Contains(strings.ToLower(spanText(exporter.GetSpans())), "sensitive-value") {
		t.Fatal("audit spans must not contain the configured value")
	}
}

func spanText(spans tracetest.SpanStubs) string {
	var builder strings.Builder
	for _, span := range spans {
		builder.WriteString(span.Name)
		for _, event := range span.Events {
			builder.WriteString(event.Name)
			for _, attr := range event.Attributes {
				builder.WriteString(attr.Value.Emit())
			}
		}
	}
	return builder.String()
}
