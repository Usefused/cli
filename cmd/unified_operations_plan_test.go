package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSDKPlanPreservesUnifiedDependenciesAndRollback verifies the canonical
// plan payload keeps private workflow mappings exact through YAML and JSON.
func TestSDKPlanPreservesUnifiedDependenciesAndRollback(t *testing.T) {
	dir := t.TempDir()
	path := writeSprintConfig(t, dir, "provisioning.yaml", `
apiVersion: fused/v1
kind: sdk
name: provisioning
version: "1.0.0"
language: typescript
services:
  stripe:
    operations: [createCustomer, deleteCustomer]
  github:
    operations: [createRepository]
unified_operations:
  account.provision:
    input: {type: object}
    bindings:
      stripe:
        operation: createCustomer
        rollback:
          operation: deleteCustomer
          input:
            customerId: ${response.stripe.id}
            audit: {attempt: 9007199254740993, enabled: true, note: null}
      github:
        operation: createRepository
        depends_on: [stripe]
`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sdk-config/plan" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body struct {
			Config json.RawMessage `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode plan request: %v", err)
		}
		for _, wanted := range [][]byte{
			[]byte(`"depends_on":["stripe"]`),
			[]byte(`"rollback":{"operation":"deleteCustomer","input":{"audit":{"attempt":9007199254740993,"enabled":true,"note":null},"customerId":"${response.stripe.id}"}}`),
		} {
			if !bytes.Contains(body.Config, wanted) {
				t.Fatalf("canonical plan config omitted %s:\n%s", wanted, body.Config)
			}
		}
		_, _ = w.Write([]byte(`{"plan_id":"plan-sdk","config_key":"sdk:provisioning:1.0.0","source_hash":"hash","base_generation":0,"summary":{}}`))
	}))
	defer server.Close()

	runCommandInDir(t, dir, server.URL, []string{"sdk", "plan", "-f", path})
}
