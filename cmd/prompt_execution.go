package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// promptExecutionDraft binds reviewed TypeScript to the workspace plan shown before approval.
type promptExecutionDraft struct {
	source        string
	workspacePlan *sdkInitWorkspaceDraft
}

// finalizePromptExecutionPlan grounds source and service scope before the user authorizes deployment.
func finalizePromptExecutionPlan(cmd *cobra.Command, client *api.Client, plan promptInitPlan) (promptInitPlan, error) {
	// The Registry drafter accepts a finite exact operation set, never a changing select-all catalogue.
	if len(plan.primary.selectAll) != 0 || len(plan.primary.operations) == 0 || len(plan.primary.operations) > 16 {
		return promptInitPlan{}, errors.New("Execution App describe requires 1 to 16 specific operations; select-all is not supported")
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
	plan.execution = &promptExecutionDraft{source: source, workspacePlan: workspacePlan}
	return plan, nil
}

// draftPromptExecutionSource requests one contract-grounded source draft without exposing credentials.
func draftPromptExecutionSource(cmd *cobra.Command, client *api.Client, plan promptInitPlan) (string, error) {
	selections, err := promptExecutionDraftSelections(plan)
	if err != nil {
		return "", err
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "Execution App drafting sends your goal and selected operation contracts to Fused Registry's configured model. Provider credentials and execution data are not included.")
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
	// JSON-only output keeps model prose out of the Engine compiler input.
	if len(raw) > 128*1024 || json.Unmarshal([]byte(raw), &draft) != nil {
		return "", errors.New("Execution App draft was not valid bounded JSON")
	}
	// Ambiguous mappings require a revised goal rather than a guessed provider call.
	if strings.TrimSpace(draft.Clarification) != "" {
		return "", fmt.Errorf("clarify your Execution App goal: %s", draft.Clarification)
	}
	// Engine validates source and the compiled manifest before it accepts the app plan.
	if len(draft.Source) == 0 || len(draft.Source) > 64*1024 || !strings.Contains(draft.Source, "buildExecutionApp") {
		return "", errors.New("Execution App draft must contain bounded buildExecutionApp source")
	}
	return draft.Source, nil
}

// executePromptExecutionPlan activates the reviewed cart and lets Engine compile and deploy its source.
func executePromptExecutionPlan(cmd *cobra.Command, plan promptInitPlan) error {
	client, err := getAPIClient()
	if err != nil {
		return err
	}
	workspaceApplied, err := applySDKInitWorkspace(cmd, client, plan.execution.workspacePlan)
	// A failed workspace boundary must never submit an app plan.
	if err != nil {
		return err
	}
	return deployPromptExecution(cmd, client, plan, workspaceApplied)
}

// deployPromptExecution submits source and selected operations through Engine's normal App plan/apply lifecycle.
func deployPromptExecution(cmd *cobra.Command, client *api.Client, plan promptInitPlan, workspaceApplied bool) error {
	config := promptExecutionConfig(plan.primary, plan.execution.source)
	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	parsed, err := configfile.Parse(data, plan.primary.path)
	if err != nil {
		return err
	}
	planned, err := planOneConfig(client, parsed, client.BaseURL, "")
	// Engine compilation and immutable operation resolution must succeed before local app publication.
	if err != nil {
		return contextualizeUnifiedInitPrecommitFailure("Execution App plan and Engine compilation", plan.mode, plan.primary, workspaceApplied, err)
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
	fmt.Fprintf(cmd.OutOrStdout(), "Deployed Execution App %s (%s) with %d operation(s).\n", plan.primary.name, result.AppID, len(plan.primary.operations))
	// Engine returns the initial family token exactly once after deployment.
	if result.ExecutionToken != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Execution token (shown once): %s\n", result.ExecutionToken)
	}
	return nil
}

// publishPromptExecutionPlan stores the reviewed cart and its exact apply receipt.
func publishPromptExecutionPlan(path string, data []byte, parsed *configfile.ParsedConfig, planned plannedConfig) error {
	if err := atomicCreateFile(path, data, 0o644, nil); err != nil {
		return err
	}
	return writePlanReceiptFile(defaultReceiptPath(parsed.ConfigKey), planned.receipt)
}

// promptExecutionConfig creates one Engine-compiled App cart from reviewed source and operation scope.
func promptExecutionConfig(request scaffoldRequest, source string) configfile.AppConfig {
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
		Bucket: request.bucket, Source: source, Services: services,
	}
}
