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

func TestCanonicalCommandParentsRequireAction(t *testing.T) {
	parents := []struct {
		name string
		args []string
	}{
		{name: "bucket", args: []string{"bucket"}},
		{name: "connect", args: []string{"connect"}},
		{name: "secret", args: []string{"secret"}},
		{name: "service", args: []string{"service"}},
		{name: "value", args: []string{"value"}},
		{name: "workspace", args: []string{"workspace"}},
		{name: "workspace services", args: []string{"workspace", "services"}},
		{name: "workspace service", args: []string{"workspace", "service"}},
		{name: "workspace service version", args: []string{"workspace", "service", "version"}},
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

func TestSDKAndMCPHaveDistinctManagementRoots(t *testing.T) {
	for name, expected := range map[string]*cobra.Command{"sdk": sdkCmd, "mcp": mcpCmd} {
		command, _, err := RootCmd.Find([]string{name})
		if err != nil || command != expected {
			t.Fatalf("%s root is unavailable: command=%v err=%v", name, command, err)
		}
	}
	command, _, err := RootCmd.Find([]string{"artifact"})
	if err == nil && command != RootCmd {
		t.Fatalf("shared artifact management root is still registered: %v", command)
	}
}

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
		{name: "value noun first", args: []string{"value", "prod", "list"}},
		{name: "value remove", args: []string{"value", "remove", "prod", "github", "token"}},
		{name: "workspace service noun first", args: []string{"workspace", "service", "github", "show"}},
		{name: "workspace service remove", args: []string{"workspace", "service", "remove", "github"}},
		{name: "workspace version remove", args: []string{"workspace", "service", "version", "remove", "github", "v1"}},
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

func TestActionFlagsStayOnConsumingSubcommand(t *testing.T) {
	if secretCmd.Flags().Lookup("bucket") != nil {
		t.Fatal("secret parent must not expose action-specific --bucket")
	}
	if secretSetCmd.Flags().Lookup("bucket") == nil || secretListCmd.Flags().Lookup("bucket") == nil || secretDeleteCmd.Flags().Lookup("bucket") == nil {
		t.Fatal("secret actions must expose their own --bucket flag")
	}
	if serviceCmd.Flags().Lookup("q") != nil || serviceWebhooksCmd.Flags().Lookup("q") != nil {
		t.Fatal("service --q must only be available on operations")
	}
	if serviceOperationsCmd.Flags().Lookup("q") == nil {
		t.Fatal("service operations must expose --q")
	}
	if connectCmd.Flags().Lookup("interactive") != nil || connectGetCmd.Flags().Lookup("interactive") != nil {
		t.Fatal("connect interactive input must only be available on set")
	}
	if connectSetCmd.Flags().Lookup("interactive") == nil || connectSetCmd.Flags().Lookup("value-stdin") == nil {
		t.Fatal("connect set must expose secret-safe input modes")
	}
	if workspaceServiceCmd.Flags().Lookup("force") != nil || workspaceServiceVersionCmd.Flags().Lookup("force") != nil {
		t.Fatal("workspace service parents must not expose delete-only --force")
	}
	if workspaceServiceDeleteCmd.Flags().Lookup("force") == nil || workspaceServiceVersionDeleteCmd.Flags().Lookup("force") == nil {
		t.Fatal("workspace delete actions must expose --force")
	}
}

func TestSecretSetRequiresSecretSafeInput(t *testing.T) {
	previousInteractive, previousStdin := secretSetInteractive, secretSetValueStdin
	t.Cleanup(func() {
		secretSetInteractive = previousInteractive
		secretSetValueStdin = previousStdin
	})

	secretSetInteractive, secretSetValueStdin = false, false
	if err := validateSecretSetArgs(secretSetCmd, []string{"github"}); err == nil || !strings.Contains(err.Error(), "choose exactly one") {
		t.Fatalf("expected missing input mode error, got %v", err)
	}
	secretSetValueStdin = true
	if err := validateSecretSetArgs(secretSetCmd, []string{"github", "visible-secret"}); err == nil {
		t.Fatal("secret values in argv must be rejected")
	}
	secretSetCmd.SetIn(strings.NewReader("sensitive-value\n"))
	value, err := readSecretValue(secretSetCmd)
	if err != nil {
		t.Fatalf("read stdin credential: %v", err)
	}
	if value != "sensitive-value" {
		t.Fatalf("credential = %q, want trimmed line ending", value)
	}
}

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

	out := runCommandInDirOutput(t, t.TempDir(), server.URL, []string{"value", "set", bucketID, "github", "header", "X-Token", "sensitive-value"})
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
