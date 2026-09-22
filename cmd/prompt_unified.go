package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// draftPromptUnifiedOperation requests a grounded composition only for explicitly sequential runtime intent.
func draftPromptUnifiedOperation(cmd *cobra.Command, client *api.Client, plan promptInitPlan) (map[string]configfile.UnifiedOperation, error) {
	// These outputs have a supported typed Unified runtime; Go and direct REST prompt composition remain explicit errors.
	if plan.mode == unifiedInitModeAPI || plan.primary.language == "go" {
		return nil, fmt.Errorf("sequential prompt composition requires a TypeScript/Python SDK or MCP")
	}
	// Whole-catalogue selection is not sufficient evidence for a finite user-requested sequence.
	if len(plan.primary.selectAll) > 0 || len(plan.primary.operations) == 0 || len(plan.primary.operations) > 16 {
		return nil, fmt.Errorf("name the specific operations to compose sequentially (at most 16)")
	}
	selections := make([]api.PromptOperationSelection, 0, len(plan.primary.operations))
	for _, operation := range plan.primary.operations {
		for _, service := range plan.resolved {
			// Only exact resolved service versions may ground model-authored mappings.
			if service.target.slug == operation.service {
				selections = append(selections, api.PromptOperationSelection{Service: operation.service, ServiceID: service.target.serviceID, Version: service.version, Operation: operation.operation})
			}
		}
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "Sequential composition sends your goal and the selected operation contracts to Fused Registry's configured drafting model. Provider credentials and execution data are not included.")
	raw, err := client.DraftPromptUnifiedOperation(plan.goal, selections)
	// Failed drafting must not degrade an ordered request into independent SDK methods.
	if err != nil {
		return nil, fmt.Errorf("draft sequential operation: %w", err)
	}
	return decodePromptUnifiedDraft(raw, plan.primary)
}

// decodePromptUnifiedDraft validates model output against exact selected capabilities before it can enter a proposal.
func decodePromptUnifiedDraft(raw string, request scaffoldRequest) (map[string]configfile.UnifiedOperation, error) {
	// JSON-only output excludes YAML aliases and trailing model prose before strict authoring decoding.
	if len(raw) > 128*1024 || !json.Valid([]byte(raw)) {
		return nil, fmt.Errorf("invalid prompt composition JSON")
	}
	var draft struct {
		Clarification string                                 `yaml:"clarification"`
		Operations    map[string]configfile.UnifiedOperation `yaml:"unified_operations"`
	}
	decoder := yaml.NewDecoder(strings.NewReader(raw))
	decoder.KnownFields(true)
	// Unknown fields must not disappear into a subtly different executable definition.
	if err := decoder.Decode(&draft); err != nil {
		return nil, fmt.Errorf("invalid prompt composition: %w", err)
	}
	// An ambiguous dependency requires clearer intent and no config mutation.
	if strings.TrimSpace(draft.Clarification) != "" {
		return nil, fmt.Errorf("clarify your sequential goal: %s", draft.Clarification)
	}
	if len(draft.Operations) != 1 {
		// The initial composition surface is one explicit reusable operation per goal.
		return nil, fmt.Errorf("sequential prompt must produce exactly one Unified Operation")
	}
	allowed := map[scaffoldOperation]bool{}
	services := map[string]configfile.AppService{}
	for _, service := range request.services {
		services[service.name] = configfile.AppService{Version: service.version}
	}
	for _, selection := range request.operations {
		allowed[selection] = true
		service := services[selection.service]
		service.Operations = append(service.Operations, selection.operation)
		services[selection.service] = service
	}
	for _, operation := range draft.Operations {
		used := map[scaffoldOperation]bool{}
		for step, binding := range operation.Bindings {
			service := binding.Service
			// The standard binding shorthand selects the service named by its step key.
			if service == "" {
				service = step
			}
			selection := scaffoldOperation{service: service, operation: binding.Operation}
			// The model may compose the discovered scope but cannot add calls or invent compensation.
			if !allowed[selection] || binding.Rollback != nil {
				return nil, fmt.Errorf("prompt composition introduced an unselected operation or rollback at %q", step)
			}
			used[selection] = true
		}
		// Dropping a requested call would silently narrow the stated business workflow.
		if len(used) != len(allowed) {
			return nil, fmt.Errorf("prompt composition omitted a requested operation")
		}
		if _, err := promptSequentialOrder(operation); err != nil {
			// Sequential intent cannot produce independent parallel calls or a cyclic graph.
			return nil, err
		}
	}
	config := configfile.AppConfig{
		BaseConfig: configfile.BaseConfig{APIVersion: configfile.APIVersionV1, Kind: request.kind},
		Name:       request.name, Version: request.version, Description: request.description,
		Language: request.language, Services: services, UnifiedOperations: draft.Operations,
	}
	data, err := yaml.Marshal(config)
	// Serialization and the shared config compiler validate DynamicValue scope and the typed authoring shape.
	if err != nil {
		return nil, err
	}
	if _, err := configfile.Parse(data, request.path); err != nil {
		return nil, fmt.Errorf("invalid sequential operation: %w", err)
	}
	return draft.Operations, nil
}

// promptSequentialOrder requires one unambiguous execution order while allowing earlier direct data dependencies.
func promptSequentialOrder(operation configfile.UnifiedOperation) ([]string, error) {
	// A reusable sequence needs multiple steps and must fit the existing bounded graph runtime.
	if len(operation.Bindings) < 2 || len(operation.Bindings) > 16 {
		return nil, fmt.Errorf("sequential Unified Operation requires 2 to 16 steps")
	}
	done := map[string]bool{}
	order := make([]string, 0, len(operation.Bindings))
	for len(order) < len(operation.Bindings) {
		ready := []string{}
		for step, binding := range operation.Bindings {
			// Settled steps cannot be admitted twice while finding a total order.
			if done[step] {
				continue
			}
			eligible := true
			for _, dependency := range binding.DependsOn {
				eligible = eligible && done[dependency]
			}
			if eligible {
				// All dependencies must precede the next admitted step.
				ready = append(ready, step)
			}
		}
		// Zero means a cycle/missing dependency; multiple means accidental parallel execution.
		if len(ready) != 1 {
			return nil, fmt.Errorf("prompt composition must have one complete sequential execution order")
		}
		done[ready[0]] = true
		order = append(order, ready[0])
	}
	return order, nil
}

// mergePromptUnifiedOperations adds new definitions while preserving every existing authored composition.
func mergePromptUnifiedOperations(config *configfile.AppConfig, requested map[string]configfile.UnifiedOperation) (bool, error) {
	changed := false
	for name, operation := range requested {
		// A repeated identical definition is idempotent; a conflicting name needs a deliberate separate edit.
		if existing, exists := config.UnifiedOperations[name]; exists {
			before, _ := json.Marshal(existing)
			after, _ := json.Marshal(operation)
			if !bytes.Equal(before, after) {
				return false, fmt.Errorf("Unified Operation %q already exists with a different definition; request a new operation name", name)
			}
			continue
		}
		// Allocate only for a real addition so untouched configs preserve their omission semantics.
		if config.UnifiedOperations == nil {
			config.UnifiedOperations = map[string]configfile.UnifiedOperation{}
		}
		config.UnifiedOperations[name] = operation
		changed = true
	}
	return changed, nil
}

// printPromptUnifiedOperations exposes execution order and every mapping before the single apply confirmation.
func printPromptUnifiedOperations(out io.Writer, operations map[string]configfile.UnifiedOperation) error {
	names := make([]string, 0, len(operations))
	for name := range operations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		order, err := promptSequentialOrder(operations[name])
		// Invalid order must fail review rather than show a misleading runtime summary.
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Unified Operation %s: %s\n", name, strings.Join(order, " -> "))
	}
	// Plain capability requests need no empty composition block in their proposal.
	if len(operations) == 0 {
		return nil
	}
	data, err := yaml.Marshal(map[string]any{"unified_operations": operations})
	if err != nil {
		// Failed serialization cannot be treated as a fully reviewed definition.
		return err
	}
	_, err = out.Write(data)
	return err
}
