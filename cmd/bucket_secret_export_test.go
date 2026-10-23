package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestBucketSecretRevealPrintsOnlyExplicitOutput verifies the command does not wrap plaintext in ordinary mutation receipts.
func TestBucketSecretRevealPrintsOnlyExplicitOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		// No metadata discovery is necessary when exact bucket and secret IDs are supplied.
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/reveal") {
				t.Error("unexpected reveal request")
			}
			_, _ = w.Write([]byte(`{"value":"synthetic-secret\n"}`))
		}))
	defer server.Close()
	t.Cleanup(configureBucketSecretTestGlobals(server.URL))
	cmd := &cobra.Command{Use: "reveal"}
	output := &bytes.Buffer{}
	cmd.SetOut(output)
	// Explicit reveal prints exact bytes, with no unsolicited newline or status prefix.
	if err := runBucketSecretReveal(cmd, []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}); err != nil {
		t.Fatal(err)
	}
	if output.String() != "synthetic-secret\n" {
		t.Fatal("incorrect plaintext output")
	}
}

// TestBucketSecretExportPolicyRequiresChoice prevents an omitted flag from silently changing retrieval consent.
func TestBucketSecretExportPolicyRequiresChoice(t *testing.T) {
	cmd := &cobra.Command{Use: "exportable"}
	cmd.Flags().Bool("enabled", false, "")
	// Local admission must reject before client configuration or network access.
	if err := runBucketSecretExportable(cmd, []string{"bucket", "secret"}); err == nil {
		t.Fatal("missing explicit policy accepted")
	}
}
