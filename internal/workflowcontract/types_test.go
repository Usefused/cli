package workflowcontract_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Usefused/cli/internal/workflowcontract"
	"gopkg.in/yaml.v3"
)

// TestUploadWorkflowStrictJSONAndYAMLRoundTrip prevents CLI serialization from
// changing reviewed upload steps, bounds, or allowed response origins.
func TestUploadWorkflowStrictJSONAndYAMLRoundTrip(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "..", "..", "contract-fixtures", "workflow", "v1_media_upload.json"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow workflowcontract.UploadWorkflow
	if err := json.Unmarshal(payload, &workflow); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(workflow)
	if err != nil {
		t.Fatal(err)
	}
	assertSemanticJSON(t, encoded, payload)
	yamlPayload, err := yaml.Marshal(workflow)
	if err != nil {
		t.Fatal(err)
	}
	var fromYAML workflowcontract.UploadWorkflow
	if err := yaml.Unmarshal(yamlPayload, &fromYAML); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromYAML, workflow) {
		t.Fatalf("YAML transport changed upload workflow:\n%s", yamlPayload)
	}
}

func TestUploadWorkflowRejectsUnknownJSON(t *testing.T) {
	var workflow workflowcontract.UploadWorkflow
	if err := json.Unmarshal([]byte(`{"version":1,"accepted_media_types":[],"modes":[],"provider":"google"}`), &workflow); err == nil {
		t.Fatal("provider-specific workflow field was accepted")
	}
}

func assertSemanticJSON(t *testing.T, gotPayload, wantPayload []byte) {
	t.Helper()
	var got, want any
	if err := json.Unmarshal(gotPayload, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wantPayload, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workflow transport changed JSON\ngot:  %s\nwant: %s", gotPayload, wantPayload)
	}
}
