package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
)

// promptExecutionDraft binds reviewed TypeScript to the workspace plan shown before approval.
type promptExecutionDraft struct {
	source        string
	workspacePlan *sdkInitWorkspaceDraft
}

// finalizePromptExecutionPlan grounds source and service scope before the user authorizes deployment.
func finalizePromptExecutionPlan(cmd *cobra.Command, client *api.Client, plan promptInitPlan) (promptInitPlan, error) {
	// The Registry drafter accepts finite exact capabilities, never a changing select-all catalogue.
	count := len(plan.primary.operations) + len(plan.primary.events)
	if len(plan.primary.selectAll) != 0 || count == 0 || count > 16 {
		return promptInitPlan{}, errors.New("Unified App describe requires 1 to 16 specific operations or events; select-all is not supported")
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
	fmt.Fprintln(cmd.ErrOrStderr(), "Unified App drafting sends your goal and selected operation contracts to Fused Registry's configured model. Provider credentials and execution data are not included.")
	raw, err := client.DraftPromptUnifiedApp(plan.goal, selections)
	// A failed model draft cannot become an empty app shell.
	if err != nil {
		return "", fmt.Errorf("draft Unified App source: %w", err)
	}
	return decodePromptExecutionDraft(raw)
}

// promptExecutionDraftSelections maps exact classified operations and events to the model request.
func promptExecutionDraftSelections(plan promptInitPlan) ([]api.PromptOperationSelection, error) {
	byService := make(map[string]sdkInitResolvedService, len(plan.resolved))
	for _, service := range plan.resolved {
		byService[service.target.slug] = service
	}
	selections := make([]api.PromptOperationSelection, 0, len(plan.primary.operations)+len(plan.primary.events))
	for _, operation := range plan.primary.operations {
		service, ok := byService[operation.service]
		// Model-authored source may only call operations grounded in an exact Registry version.
		if !ok {
			return nil, fmt.Errorf("unresolved Unified App operation %s.%s", operation.service, operation.operation)
		}
		selections = append(selections, api.PromptOperationSelection{
			Service: operation.service, ServiceID: service.target.serviceID,
			Version: service.version, Operation: operation.operation,
		})
	}
	for _, event := range plan.primary.events {
		service, ok := byService[event.service]
		// Event input evidence must come from the same exact Registry service version as its trigger.
		if !ok {
			return nil, fmt.Errorf("unresolved Unified App event %s.%s", event.service, event.event)
		}
		selections = append(selections, api.PromptOperationSelection{
			Service: event.service, ServiceID: service.target.serviceID,
			Version: service.version, Event: event.event,
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
		return "", errors.New("Unified App draft was not valid bounded JSON")
	}
	// Ambiguous mappings require a revised goal rather than a guessed provider call.
	if strings.TrimSpace(draft.Clarification) != "" {
		return "", fmt.Errorf("clarify your Unified App goal: %s", draft.Clarification)
	}
	// Engine validates source and the compiled manifest before it accepts the app plan.
	if len(draft.Source) == 0 || len(draft.Source) > 64*1024 || !strings.Contains(draft.Source, "buildUnifiedApp") {
		return "", errors.New("Unified App draft must contain bounded buildUnifiedApp source")
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

// deployPromptExecution publishes one TypeScript declaration and submits its derived cart through Engine's normal plan/apply lifecycle.
func deployPromptExecution(cmd *cobra.Command, client *api.Client, plan promptInitPlan, workspaceApplied bool) error {
	sourceFile := plan.primary.path
	// A prompted Unified App writes the selected TypeScript path directly, so a YAML destination cannot be silently ignored.
	if extension := filepath.Ext(sourceFile); extension != ".ts" && extension != ".tsx" {
		return errors.New("Unified App describe destination must be a .ts or .tsx file")
	}
	data, err := promptUnifiedAppSource(plan.primary, plan.execution.source)
	if err != nil {
		return err
	}
	// The reviewed draft and selections become one editable local file before the CLI resolves its plan cart.
	if err := atomicCreateFile(sourceFile, data, 0o644, nil); err != nil {
		return err
	}
	parsed, err := configfile.ParseFile(sourceFile)
	if err != nil {
		_ = os.Remove(sourceFile)
		return err
	}
	planned, err := planOneConfig(client, parsed, client.BaseURL, "")
	// Engine compilation and immutable operation resolution must succeed before local app publication.
	if err != nil {
		_ = os.Remove(sourceFile)
		return contextualizeUnifiedInitPrecommitFailure("Unified App plan and Fused compilation", plan.mode, plan.primary, workspaceApplied, err)
	}
	// The source file is already published; only its exact plan receipt remains to be recorded.
	if err := writePlanReceiptFile(defaultReceiptPath(parsed.ConfigKey), planned.receipt); err != nil {
		return err
	}
	result, err := applyExecutionVersion(client, parsed, planned.receipt)
	// A published plan can be resumed without asking the model to regenerate source.
	if err != nil {
		return fmt.Errorf("%w; retry with fused-cli app apply -f %s", err, sourceFile)
	}
	recordAppliedChange(cmd.Context(), cmd.CommandPath(), "unified_app")
	fmt.Fprintf(cmd.OutOrStdout(), "Deployed Unified App %s (%s) with %d operation(s) and %d webhook event(s).\n", plan.primary.name, result.AppFamilyID, len(plan.primary.operations), len(plan.primary.events))
	fmt.Fprintf(cmd.OutOrStdout(), "Execution URL: %s\n", result.ExecutionURL)
	// Engine returns the initial family token exactly once after deployment.
	if result.ExecutionToken != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Execution token (shown once): %s\n", result.ExecutionToken)
	}
	return nil
}

// promptUnifiedAppSource places reviewed metadata beside executable code in one TypeScript file.
func promptUnifiedAppSource(request scaffoldRequest, body string) ([]byte, error) {
	config := promptUnifiedAppConfig(request)
	metadata, err := json.MarshalIndent(config, "", "  ")
	// JSON object syntax is valid TypeScript and admits deterministic parsing without local execution.
	if err != nil {
		return nil, err
	}
	return []byte("export const fusedConfig = " + string(metadata) + ";\n\n" + body), nil
}

// promptUnifiedAppConfig preserves reviewed event and operation scope for the source-owned cart.
func promptUnifiedAppConfig(request scaffoldRequest) configfile.AppConfig {
	services := make(map[string]configfile.AppService, len(request.services))
	for _, service := range request.services {
		services[service.name] = configfile.AppService{Version: service.version}
	}
	for _, operation := range request.operations {
		selected := services[operation.service]
		selected.Operations = append(selected.Operations, operation.operation)
		services[operation.service] = selected
	}
	for _, event := range request.events {
		selected := services[event.service]
		selected.Webhooks = append(selected.Webhooks, event.event)
		services[event.service] = selected
	}
	return configfile.AppConfig{
		BaseConfig: configfile.BaseConfig{APIVersion: configfile.APIVersionV1, Kind: configfile.KindUnifiedApp},
		Name:       request.name, Version: request.version,
		Bucket: request.bucket, WebhookAttachment: request.webhookAttachment, Services: services,
	}
}
