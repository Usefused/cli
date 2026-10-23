package cmd

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

const entryTestBucketID = "11111111-1111-4111-8111-111111111111"

// configureBucketSecretTestGlobals isolates endpoint and automation settings without modifying real credentials.
func configureBucketSecretTestGlobals(engineURL string) func() {
	oldURL, oldKey, oldNoInput := EngineURL, APIKey, NoInput
	EngineURL, APIKey, NoInput = engineURL, "fsk_test", true
	// Restore the process-wide CLI environment after each command fixture.
	return func() { EngineURL, APIKey, NoInput = oldURL, oldKey, oldNoInput }
}

// executeBucketEntryTest runs fresh commands so flags cannot leak between test invocations.
func executeBucketEntryTest(t *testing.T, kind string, args []string, input string) (string, error) {
	t.Helper()
	root := commandGroup(kind, "test bucket entries")
	addBucketEntryCommands(root, kind)
	output := &bytes.Buffer{}
	root.SetOut(output)
	root.SetErr(output)
	root.SetIn(strings.NewReader(input))
	root.SilenceErrors, root.SilenceUsage = true, true
	root.SetArgs(args)
	err := root.Execute()
	return output.String(), err
}

// TestBucketEntriesSetUsesDefaultAndSelectedBucket verifies inline values and multiple assignments stay scoped.
func TestBucketEntriesSetUsesDefaultAndSelectedBucket(t *testing.T) {
	for _, test := range []bucketSetTestCase{
		{"secret", []string{"set", "token=a:b==;tail", "--json", "--exportable"}, "", "default", "token", "a:b==;tail"},
		{"env", []string{"set", "URL=https://example.test:443/path", "EMPTY=", "--json", "--bucket", "staging"}, "", "staging", "URL", "https://example.test:443/path"},
		{"secret", []string{"set", "token=explicit-token", "--bucket", entryTestBucketID}, "", "", "token", "explicit-token"},
	} {
		// Exercise every supported input channel against the actual command parser and HTTP boundary.
		t.Run(test.kind+test.value, func(t *testing.T) {
			writes, resolves := 0, 0
			// The fake Engine records only assertions; no credentials leave the loopback server.
			server := httptest.NewServer(bucketSetTestHandler(t, test, &writes, &resolves))
			defer server.Close()
			t.Cleanup(configureBucketSecretTestGlobals(server.URL))
			output, err := executeBucketEntryTest(t, test.kind, test.args, test.input)
			if err != nil || writes == 0 || strings.Contains(output, test.value) {
				t.Fatalf("writes=%d err=%v unsafe receipt=%t", writes, err, strings.Contains(output, test.value))
			} // Mutation receipts never contain values.
			if test.bucket != "" && resolves != 1 {
				t.Fatalf("resolves=%d", resolves)
			} // A named bucket resolves exactly once.
		})
	}
}

// TestBucketEntryPromptAndValidation checks missing-value prompting and batch validation before mutation.
func TestBucketEntryPromptAndValidation(t *testing.T) {
	t.Cleanup(configureBucketSecretTestGlobals("http://unused.invalid"))
	t.Setenv("CI", "false")
	NoInput = false
	oldCollector := collectBucketEntryValue
	t.Cleanup(func() { collectBucketEntryValue = oldCollector }) // Keep the production prompt available to later tests.
	prompted := false
	// Prompt injection proves that an omitted value reaches the collector with only its public name.
	collectBucketEntryValue = func(kind, name string) (string, error) {
		prompted = kind == "secret" && name == "token"
		return "prompted-value", nil
	}
	command := &cobra.Command{Use: "set"}
	entries, err := collectBucketEntries(command, "secret", []string{"token"})
	if err != nil || !prompted || len(entries) != 1 || entries[0].value != "prompted-value" {
		t.Fatalf("prompted=%t err=%v", prompted, err)
	} // Missing values must not be written empty.
	NoInput = true
	for _, args := range [][]string{{"set", "token"}, {"set", "token=a", "token=b"}, {"get", "token=value"}, {"remove", "token=value"}} {
		_, err := executeBucketEntryTest(t, "secret", args, "")
		if err == nil || strings.Contains(err.Error(), "unused.invalid") {
			t.Fatalf("expected local input error: %v", err)
		} // Bad input must not reach the configured Engine.
	}
}

// TestBucketEntryReadsAndRemovalsKeepNamespaces proves exact generic identities and the audited reveal path.
func TestBucketEntryReadsAndRemovalsKeepNamespaces(t *testing.T) {
	for _, kind := range []string{"secret", "env", "cert"} {
		// Identical public names must not cross credential, environment, and certificate namespaces.
		t.Run(kind, func(t *testing.T) {
			reveals, deletes := 0, 0
			// Supply a provider collision and a correct bucket-global row to test identity isolation.
			server := httptest.NewServer(bucketReadTestHandler(t, kind, &reveals, &deletes))
			defer server.Close()
			t.Cleanup(configureBucketSecretTestGlobals(server.URL))
			output, err := executeBucketEntryTest(t, kind, []string{"get", "shared", "--bucket", entryTestBucketID}, "")
			if err != nil || output != "retrieved" {
				t.Fatalf("get output=%q err=%v", output, err)
			} // Get prints exact bytes with no status prefix.
			_, err = executeBucketEntryTest(t, kind, []string{"remove", "shared", "--bucket", entryTestBucketID}, "")
			if err != nil || deletes != 1 || (kind == "secret" && reveals != 1) {
				t.Fatalf("deletes=%d reveals=%d err=%v", deletes, reveals, err)
			} // Each mutation is issued once.
		})
	}
}

// fmtJSON writes fixture responses while making serialization failures visible to the test.
func fmtJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	} // A malformed fixture must not hide an implementation failure.
}

// testBucketCertificate creates a short-lived public certificate without persisting its private key.
func testBucketCertificate(t *testing.T) string {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	} // Certificate tests require a valid signing key.
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	encoded, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	} // Only parseable X.509 fixtures may reach the storage test.
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: encoded}))
}

// TestBucketCertificateAcceptsOnlyPublicPEM proves certificate storage cannot accidentally accept private keys.
func TestBucketCertificateAcceptsOnlyPublicPEM(t *testing.T) {
	certificate := testBucketCertificate(t)
	for _, valid := range []string{certificate, certificate + certificate} {
		if err := validateBucketCertificate(valid); err != nil {
			t.Fatal(err)
		} // Single certificates and chains are supported.
	}
	for _, invalid := range []string{"", "not PEM", "-----BEGIN PRIVATE KEY-----\nfixture\n-----END PRIVATE KEY-----", certificate + "private material", "prefix" + certificate} {
		if err := validateBucketCertificate(invalid); err == nil {
			t.Fatal("accepted non-certificate material")
		} // Reject every non-certificate segment.
	}
	calls := 0
	// Verify that a real cert set request uses the dedicated value namespace and no provider identity.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		} // Preserve malformed request evidence.
		// Public PEM remains exact and separately named.
		assertBucketEntryFields(t, body, map[string]any{"key_name": "cert:root", "location": "cert", "value": certificate})
		calls++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	t.Cleanup(configureBucketSecretTestGlobals(server.URL))
	_, err := executeBucketEntryTest(t, "cert", []string{"set", "root=" + certificate, "--bucket", entryTestBucketID}, "")
	if err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	} // One validated certificate produces one write.
}

// TestBucketSecretGetNeverEnablesExport preserves existing reveal permission and opt-in policy on named reads.
func TestBucketSecretGetNeverEnablesExport(t *testing.T) {
	calls := 0
	// Denial at the reveal boundary must stop without a policy update or plaintext output.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost {
			t.Error("read attempted mutation")
			http.Error(w, "unexpected", 400)
			return
		} // Neither metadata resolution nor get may enable export.
		if strings.HasSuffix(r.URL.Path, "/reveal") {
			http.Error(w, "private material must not be printed", http.StatusForbidden)
			return
		} // Engine remains authoritative for reveal grants.
		fmtJSON(t, w, map[string]any{"data": map[string]any{"secretMetaPage": map[string]any{"total": 1, "items": []map[string]any{{"id": "private", "service_id": "00000000-0000-0000-0000-000000000000", "key_name": "secret:token", "credential_type": "bucket_secret", "exportable": false}}}}})
	}))
	defer server.Close()
	t.Cleanup(configureBucketSecretTestGlobals(server.URL))
	output, err := executeBucketEntryTest(t, "secret", []string{"get", "token", "--bucket", entryTestBucketID}, "")
	if err == nil || output != "" || calls != 2 {
		t.Fatalf("calls=%d output=%q err=%v", calls, output, err)
	} // Failed reads must not look like a successful empty secret.
	if strings.Contains(err.Error(), "private material") {
		t.Fatal("remote error body leaked")
	} // Reveal errors cannot echo arbitrary response material.
}

type bucketSetTestCase struct {
	kind                      string
	args                      []string
	input, bucket, key, value string
}

// bucketSetTestHandler verifies bucket resolution and assignment bytes at the HTTP boundary.
func bucketSetTestHandler(t *testing.T, test bucketSetTestCase, writes, resolves *int) http.HandlerFunc {
	// Serve only synthetic fixture data while checking exact request scope.
	return func(w http.ResponseWriter, r *http.Request) {

		if r.Method == http.MethodPost { // Named buckets resolve before any write.
			(*resolves)++
			request := decodeGraphQLTestRequest(t, r)
			if request.Variables["reference"] != test.bucket {
				t.Errorf("wrong bucket reference")
			} // Never substitute another bucket.
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"bucketReference": map[string]string{"id": entryTestBucketID}}})
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		} // Malformed writes fail the contract.
		if !strings.Contains(r.URL.Path, entryTestBucketID) {
			t.Error("wrong write bucket")
		} // Resolved identity must own the mutation.
		// The first assignment contains the significant bytes for this fixture.
		if *writes == 0 {
			assertBucketEntryFields(t, body, map[string]any{"key_name": test.key, "value": test.value})
		}
		// Environment entries must be bucket-global rather than provider bindings.
		if test.kind == "env" {
			assertBucketEntryFields(t, body, map[string]any{"location": "env", "service_id": "00000000-0000-0000-0000-000000000000"})
		}
		(*writes)++
		w.WriteHeader(http.StatusNoContent)
	}
}

// bucketReadTestHandler distinguishes generic-secret reveal, public-value reads, and exact removals.
func bucketReadTestHandler(t *testing.T, kind string, reveals, deletes *int) http.HandlerFunc {
	// Serve only synthetic fixture data while checking exact request scope.
	return func(w http.ResponseWriter, r *http.Request) {

		switch r.Method { // Reads use GraphQL metadata or values; secrets reveal only through POST /reveal.
		case http.MethodPost:
			if strings.HasSuffix(r.URL.Path, "/reveal") {
				(*reveals)++
				fmtJSON(t, w, map[string]string{"value": "retrieved"})
				return
			} // Plaintext requires the dedicated route.
			request := decodeGraphQLTestRequest(t, r)
			if request.Variables["bucketId"] != entryTestBucketID {
				t.Error("wrong read bucket")
			} // Every page stays in the requested bucket.
			if kind == "secret" {
				fmtJSON(t, w, map[string]any{"data": map[string]any{"secretMetaPage": map[string]any{"total": 2, "items": []map[string]any{
					{"id": "provider", "service_id": "provider", "key_name": "secret:shared", "credential_type": "api_key"},
					{"id": "generic", "service_id": "00000000-0000-0000-0000-000000000000", "key_name": "secret:shared", "credential_type": "bucket_secret", "exportable": true},
				}}}})
			} else { // Public env/cert values never require credential reveal permission.
				fmtJSON(t, w, map[string]any{"data": map[string]any{"bucketValuePage": map[string]any{"total": 1, "items": []map[string]string{{"service_id": "00000000-0000-0000-0000-000000000000", "key_name": bucketEntryKey(kind, "shared"), "location": kind, "value": "retrieved"}}}}})
			}
		case http.MethodDelete:
			(*deletes)++
			if r.URL.Query().Get("key_name") != bucketEntryKey(kind, "shared") || r.URL.Query().Get("service_id") != "00000000-0000-0000-0000-000000000000" {
				t.Error("wrong delete identity")
			} // Deletion cannot touch a provider credential or sibling namespace.
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Error("unexpected mutation")
		}
	}
}

// assertBucketEntryFields checks request contracts without logging supplied values on failure.
func assertBucketEntryFields(t *testing.T, got, expected map[string]any) {
	t.Helper()
	for field, value := range expected {
		// A mismatched field indicates incorrect routing or input preservation.
		if got[field] != value {
			t.Errorf("incorrect request field %s", field)
		}
	}
}
