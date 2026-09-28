package cmd

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// promptExecutionDraft keeps reviewed source and workspace activation bound to one describe proposal.
type promptExecutionDraft struct {
	source        string
	sourcePath    string
	specPath      string
	bundlePath    string
	manifestPath  string
	digestPath    string
	workspacePlan *sdkInitWorkspaceDraft
}

// finalizePromptExecutionPlan grounds hosted source before the user authorizes any workspace change.
func finalizePromptExecutionPlan(cmd *cobra.Command, client *api.Client, plan promptInitPlan) (promptInitPlan, error) {
	// A finite exact operation set is required to pin generated calls into a bundle manifest.
	if len(plan.primary.selectAll) != 0 || len(plan.primary.operations) == 0 || len(plan.primary.operations) > 16 {
		return promptInitPlan{}, errors.New("Execution App describe requires 1 to 16 specific operations; select-all is not supported")
	}
	// The compiler must exist before describe offers a deployment approval.
	if _, err := executionBuildCommand(); err != nil {
		return promptInitPlan{}, err
	}
	source, err := draftPromptExecutionSource(cmd, client, plan)
	if err != nil {
		return promptInitPlan{}, err
	}
	plan.primary, err = completeSDKInitCreateBucket(plan.primary, resolveScaffoldBucket)
	if err != nil {
		return promptInitPlan{}, err
	}
	workspacePlan, err := planSDKInitWorkspace(client, plan.resolved)
	// Activation is planned before approval and applied only after confirmation.
	if err != nil {
		return promptInitPlan{}, err
	}
	if err := printSDKInitWorkspacePlan(cmd, workspacePlan); err != nil {
		return promptInitPlan{}, err
	}
	base := strings.TrimSuffix(plan.primary.path, filepath.Ext(plan.primary.path))
	plan.execution = &promptExecutionDraft{
		source: source, sourcePath: filepath.Join(base, "app.ts"),
		specPath: filepath.Join(base, "spec.json"), bundlePath: filepath.Join(base, "bundle.js"),
		manifestPath: filepath.Join(base, "manifest.json"), digestPath: filepath.Join(base, "bundle.digest.json"),
		workspacePlan: workspacePlan,
	}
	return plan, nil
}

// draftPromptExecutionSource requests one contract-grounded authoring result without exposing credentials.
func draftPromptExecutionSource(cmd *cobra.Command, client *api.Client, plan promptInitPlan) (string, error) {
	selections, err := promptExecutionDraftSelections(plan)
	if err != nil {
		return "", err
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "Execution App drafting sends your goal and selected operation contracts to Fused Registry's configured model. No credentials or execution data are included.")
	raw, err := client.DraftPromptExecutionApp(plan.goal, selections)
	// A failed model draft cannot become an empty app shell.
	if err != nil {
		return "", fmt.Errorf("draft Execution App source: %w", err)
	}
	return decodePromptExecutionDraft(raw)
}

// promptExecutionDraftSelections maps only exact classified Registry operations to the model request.
func promptExecutionDraftSelections(plan promptInitPlan) ([]api.PromptOperationSelection, error) {
	byService := make(map[string]sdkInitResolvedService, len(plan.resolved))
	for _, service := range plan.resolved {
		byService[service.target.slug] = service
	}
	selections := make([]api.PromptOperationSelection, 0, len(plan.primary.operations))
	for _, operation := range plan.primary.operations {
		service, ok := byService[operation.service]
		// Model-authored source may only call operations grounded in an exact Registry version.
		if !ok {
			return nil, fmt.Errorf("unresolved Execution App operation %s.%s", operation.service, operation.operation)
		}
		selections = append(selections, api.PromptOperationSelection{
			Service: operation.service, ServiceID: service.target.serviceID,
			Version: service.version, Operation: operation.operation,
		})
	}
	return selections, nil
}

// decodePromptExecutionDraft requires one bounded source document and surfaces model ambiguity.
func decodePromptExecutionDraft(raw string) (string, error) {
	var draft struct {
		Clarification string `json:"clarification"`
		Source        string `json:"source"`
	}
	// Strict JSON keeps model prose out of the compiler input.
	if len(raw) > 128*1024 || json.Unmarshal([]byte(raw), &draft) != nil {
		return "", errors.New("Execution App draft was not valid bounded JSON")
	}
	// Ambiguous mappings require a revised goal rather than a guessed provider call.
	if strings.TrimSpace(draft.Clarification) != "" {
		return "", fmt.Errorf("clarify your Execution App goal: %s", draft.Clarification)
	}
	// A default builder export is the only entry the hosted compiler accepts.
	if len(draft.Source) == 0 || len(draft.Source) > 64*1024 || !strings.Contains(draft.Source, "buildExecutionApp") {
		return "", errors.New("Execution App draft must contain bounded buildExecutionApp source")
	}
	return draft.Source, nil
}

// executionBuildCommand chooses the installed compiler with an explicit local override for development.
func executionBuildCommand() (string, error) {
	compiler := strings.TrimSpace(os.Getenv("FUSED_EXECUTION_BUILD"))
	// Normal installations discover the pinned authoring package's executable on PATH.
	if compiler == "" {
		compiler = "fused-execution-build"
	}
	path, err := exec.LookPath(compiler)
	if err != nil {
		return "", errors.New("fused-execution-build is required for Execution App describe; install @fused/execution or set FUSED_EXECUTION_BUILD")
	}
	return path, nil
}

// executePromptExecutionPlan activates reviewed service scope, compiles source, then applies and attaches one version.
func executePromptExecutionPlan(cmd *cobra.Command, plan promptInitPlan) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	compiler, err := executionBuildCommand()
	if err != nil {
		return err
	}
	workspaceApplied, err := applySDKInitWorkspace(cmd, client, plan.execution.workspacePlan)
	// A failed workspace boundary must never publish or apply an app config.
	if err != nil {
		return err
	}
	selections, err := client.ExecutionBuildSelections(promptExecutionEngineSelections(plan.primary))
	if err != nil {
		return fmt.Errorf("resolve exact Execution App operation IDs: %w", err)
	}
	// Engine must return precisely the reviewed scope before compilation.
	if len(selections) != len(plan.primary.operations) {
		return errors.New("Engine returned an incomplete Execution App operation selection")
	}
	if err := writePromptExecutionSource(plan.execution); err != nil {
		return contextualizeUnifiedInitPrecommitFailure("source publication", plan.mode, plan.primary, workspaceApplied, err)
	}
	if err := compilePromptExecution(cmd, compiler, plan.execution, selections); err != nil {
		return err
	}
	return deployPromptExecution(cmd, client, plan, selections)
}

// writePromptExecutionSource publishes only the approved source after workspace activation succeeds.
func writePromptExecutionSource(draft *promptExecutionDraft) error {
	if err := os.MkdirAll(filepath.Dir(draft.sourcePath), 0o755); err != nil {
		return err
	}
	return createMatchingPromptExecutionFile(draft.sourcePath, []byte(draft.source))
}

// createMatchingPromptExecutionFile permits an exact retry while protecting edited generated files.
func createMatchingPromptExecutionFile(path string, data []byte) error {
	current, err := os.ReadFile(path)
	// A repeated attempt may resume an identical draft after compiler failure.
	if err == nil && string(current) == string(data) {
		return nil
	}
	// Existing different content may contain user edits and must not be replaced by a model draft.
	if err == nil || !os.IsNotExist(err) {
		return fmt.Errorf("Execution App file already exists or is unreadable at %s; review it before retrying", path)
	}
	return atomicCreateFile(path, data, 0o644, nil)
}

// promptExecutionEngineSelections groups explicit operations under their immutable service pins.
func promptExecutionEngineSelections(request scaffoldRequest) []api.AppScaffoldSelection {
	groups := make([]api.AppScaffoldSelection, 0, len(request.services))
	byService := make(map[string]int, len(request.services))
	for _, service := range request.services {
		// Event-only services are rejected earlier, so every group is an exact call scope.
		if !sdkInitServiceHasOperationSelection(request, service.name) {
			continue
		}
		byService[service.name] = len(groups)
		groups = append(groups, api.AppScaffoldSelection{Service: service.name, Version: service.version})
	}
	for _, operation := range request.operations {
		// Resolution has already proved every operation belongs to a selected service.
		if index, ok := byService[operation.service]; ok {
			groups[index].Operations = append(groups[index].Operations, operation.operation)
		}
	}
	return groups
}

// compilePromptExecution invokes the isolated package compiler on an exact Engine-owned operation spec.
func compilePromptExecution(cmd *cobra.Command, compiler string, draft *promptExecutionDraft, selections []api.ExecutionBuildSelection) error {
	spec := struct {
		EntryFile          string                        `json:"entryFile"`
		SelectedOperations []api.ExecutionBuildSelection `json:"selectedOperations"`
	}{EntryFile: "./app.ts", SelectedOperations: selections}
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	if err := createMatchingPromptExecutionFile(draft.specPath, append(data, '\n')); err != nil {
		return err
	}
	args := []string{"--config", draft.specPath, "--out", draft.bundlePath, "--manifest", draft.manifestPath, "--digest", draft.digestPath}
	command := exec.CommandContext(cmd.Context(), compiler, args...)
	output, err := command.CombinedOutput()
	// Compiler failure leaves reviewable source and spec for correction, never an applied app.
	if err != nil {
		return fmt.Errorf("compile Execution App: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// deployPromptExecution pins compiler bytes into desired state and attaches them only to the applied version.
func deployPromptExecution(cmd *cobra.Command, client *api.Client, plan promptInitPlan, selections []api.ExecutionBuildSelection) error {
	bundle, manifest, digest, err := readPromptExecutionArtifacts(plan.execution)
	if err != nil {
		return err
	}
	if err := validatePromptExecutionManifest(manifest, selections); err != nil {
		return err
	}
	config := promptExecutionConfig(plan.primary, digest)
	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	parsed, err := configfile.Parse(data, plan.primary.path)
	if err != nil {
		return err
	}
	planned, err := planOneConfig(client, parsed, client.BaseURL, "")
	if err != nil {
		return err
	}
	if err := publishPromptExecutionPlan(plan.primary.path, data, parsed, planned); err != nil {
		return err
	}
	result, err := applyExecutionVersion(client, parsed, planned.receipt)
	// A published plan can be resumed without asking the model to regenerate source.
	if err != nil {
		return fmt.Errorf("%w; retry with fused-cli execution apply -f %s", err, plan.primary.path)
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "execution")
	// The initial token is returned once, so surface it even if the later attachment fails.
	if result.ExecutionToken != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Execution token (shown once): %s\n", result.ExecutionToken)
	}
	_, err = client.AttachExecutionBundle(result.AppID, planned.receipt.SourceHash, bundle, json.RawMessage(manifest))
	// The applied version remains visible with an actionable attach path if upload fails.
	if err != nil {
		return fmt.Errorf("Execution App %s applied but bundle attachment failed: %w; run fused-cli execution bundle attach %s --source-hash %s --bundle %s --manifest %s", result.AppID, err, result.AppID, planned.receipt.SourceHash, plan.execution.bundlePath, plan.execution.manifestPath)
	}
	recordAppliedChange(cmd.Context(), "execution.bundle.attach", "execution")
	fmt.Fprintf(cmd.OutOrStdout(), "Deployed Execution App %s (%s) with %d operation(s).\n", plan.primary.name, result.AppID, len(selections))
	return nil
}

// validatePromptExecutionManifest confirms compiled authority still matches the reviewed Engine IDs.
func validatePromptExecutionManifest(data []byte, expected []api.ExecutionBuildSelection) error {
	var manifest struct {
		SchemaVersion      int                           `json:"schemaVersion"`
		InputSchema        map[string]any                `json:"inputSchema"`
		OutputSchema       map[string]any                `json:"outputSchema"`
		SelectedOperations []api.ExecutionBuildSelection `json:"selectedOperations"`
	}
	// The compiler contract requires typed object boundaries and the exact selected operation IDs.
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.SchemaVersion != 1 || manifest.InputSchema["type"] != "object" || manifest.OutputSchema["type"] != "object" {
		return errors.New("Execution App compiler manifest is invalid")
	}
	if !reflect.DeepEqual(manifest.SelectedOperations, expected) {
		return errors.New("Execution App compiler manifest does not match selected operations")
	}
	return nil
}

// readPromptExecutionArtifacts verifies that the compiler manifest and digest exist before app planning.
func readPromptExecutionArtifacts(draft *promptExecutionDraft) ([]byte, []byte, string, error) {
	digest, err := readPromptExecutionDigest(draft.digestPath)
	if err != nil {
		return nil, nil, "", err
	}
	bundle, err := readBoundedSDKBundleFile(draft.bundlePath, maxCLIExecutionBundleBytes)
	if err != nil {
		return nil, nil, "", err
	}
	// The compiler sidecar must describe the exact bytes before any app plan or apply.
	if "sha256:"+fmt.Sprintf("%x", sha256.Sum256(bundle)) != digest {
		return nil, nil, "", errors.New("Execution App bundle bytes do not match the planned digest")
	}
	manifest, err := readBoundedSDKBundleFile(draft.manifestPath, maxCLIExecutionManifestBytes)
	return bundle, manifest, digest, err
}

// publishPromptExecutionPlan stores the reviewed desired state and its exact apply receipt.
func publishPromptExecutionPlan(path string, data []byte, parsed *configfile.ParsedConfig, planned plannedConfig) error {
	if err := atomicCreateFile(path, data, 0o644, nil); err != nil {
		return err
	}
	return writePlanReceiptFile(defaultReceiptPath(parsed.ConfigKey), planned.receipt)
}

// readPromptExecutionDigest admits only canonical compiler provenance.
func readPromptExecutionDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var result struct {
		BundleDigest string `json:"bundle_digest"`
	}
	// The config parser repeats this digest validation at the final desired-state boundary.
	if err := json.Unmarshal(data, &result); err != nil || !strings.HasPrefix(result.BundleDigest, "sha256:") || len(result.BundleDigest) != 71 {
		return "", errors.New("compiler returned an invalid bundle digest")
	}
	return result.BundleDigest, nil
}

// promptExecutionConfig creates the one hosted App desired-state document from reviewed scope.
func promptExecutionConfig(request scaffoldRequest, digest string) configfile.AppConfig {
	generate := false
	services := make(map[string]configfile.AppService, len(request.services))
	for _, service := range request.services {
		services[service.name] = configfile.AppService{Version: service.version}
	}
	for _, operation := range request.operations {
		selected := services[operation.service]
		selected.Operations = append(selected.Operations, operation.operation)
		services[operation.service] = selected
	}
	return configfile.AppConfig{
		BaseConfig: configfile.BaseConfig{APIVersion: configfile.APIVersionV1, Kind: configfile.KindExecution},
		Name:       request.name, Version: request.version, Language: "typescript", Generate: &generate,
		Bucket: request.bucket, BundleDigest: digest, Services: services,
	}
}
