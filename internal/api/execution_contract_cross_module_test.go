package api_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Usefused/cli/internal/api"
)

type sharedExecutionFixtureManifest struct {
	ManifestVersion int                      `json:"manifest_version"`
	Fixtures        []sharedExecutionFixture `json:"fixtures"`
}

type sharedExecutionFixture struct {
	File    string `json:"file"`
	Outcome string `json:"outcome"`
}

func TestSharedExecutionFixtureManifestRoundTripsThroughCLIDTO(t *testing.T) {
	root := filepath.Join("..", "..", "..", "contract-fixtures", "execution")
	manifest := readCLIExecutionFixtureManifest(t, root)
	for _, fixture := range manifest.Fixtures {
		fixture := fixture
		t.Run(fixture.File, func(t *testing.T) {
			payload, err := os.ReadFile(filepath.Join(root, fixture.File))
			if err != nil {
				t.Fatal(err)
			}
			var contract api.ServiceRuntimeContract
			if err := json.Unmarshal(payload, &contract); err != nil {
				t.Fatalf("decode CLI DTO: %v", err)
			}
			encoded, err := json.Marshal(contract)
			if err != nil {
				t.Fatalf("re-encode CLI DTO: %v", err)
			}
			assertEquivalentJSON(t, encoded, payload)
			assertCLIExecutionFixtureOutcome(t, contract, fixture.Outcome)
		})
	}
}

func readCLIExecutionFixtureManifest(t *testing.T, root string) sharedExecutionFixtureManifest {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join(root, "execution-fixture-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest sharedExecutionFixtureManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatalf("decode execution fixture manifest: %v", err)
	}
	if manifest.ManifestVersion != 1 || len(manifest.Fixtures) == 0 {
		t.Fatalf("invalid execution fixture manifest: %#v", manifest)
	}
	return manifest
}

func assertCLIExecutionFixtureOutcome(t *testing.T, contract api.ServiceRuntimeContract, outcome string) {
	t.Helper()
	if outcome == "accepted" {
		return
	}
	if outcome == "execution_capability_required" && len(contract.RequiredCapabilities) == 1 && contract.RequiredCapabilities[0] == "http.future-unknown.v1" {
		return
	}
	t.Fatalf("fixture outcome %q does not match capabilities %#v", outcome, contract.RequiredCapabilities)
}
