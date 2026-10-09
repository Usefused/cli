package cmd

import (
	"fmt"
	"io"

	"github.com/Usefused/cli/internal/api"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

type initMCPCapabilityChoice struct{ kind, name, label string }

// initMCPCapabilityRunner isolates the terminal while tests exercise the real resolution and merge boundaries.
var initMCPCapabilityRunner = promptInitMCPCapabilities

// chooseInitMCPCapabilities offers one searchable list and records each protocol category in its established config field.
func chooseInitMCPCapabilities(cmd *cobra.Command, client *api.Client, request *scaffoldRequest, service sdkInitResolvedService, snapshot *api.MCPCatalogSnapshot) (mcpSelectOptions, error) {
	endpoints, err := client.ServiceOperations(service.target.serviceID, service.version)
	// Failed physical discovery must not hide available endpoint choices and imply a complete catalog.
	if err != nil {
		return mcpSelectOptions{}, err
	}
	choices := initMCPCapabilityChoices(endpoints, snapshot)
	selected, err := initMCPCapabilityRunner(sdkInput, cmd.OutOrStdout(), service.target.slug, choices)
	// Cancelling the selector leaves both desired state and workspace untouched.
	if err != nil {
		return mcpSelectOptions{}, err
	}
	return applyInitMCPCapabilityChoices(request, service.target.slug, choices, selected)
}

// initMCPCapabilityChoices keeps namespaced selection identity separate from searchable display labels.
func initMCPCapabilityChoices(endpoints []api.Integration, snapshot *api.MCPCatalogSnapshot) []initMCPCapabilityChoice {
	choices := []initMCPCapabilityChoice{}
	// A complete endpoint grant is useful only when this service version has endpoints.
	if len(endpoints) > 0 {
		choices = append(choices, initMCPCapabilityChoice{kind: "endpoints_all", label: "All endpoints"})
	}
	for _, endpoint := range endpoints {
		choices = append(choices, initMCPCapabilityChoice{kind: "endpoint", name: endpoint.Name, label: "Endpoint  " + sdkOperationSearchLabel(endpoint)})
	}
	choices = append(choices, initMCPCapabilityChoice{kind: "mcp_all", label: "All currently imported MCP capabilities"})
	for _, group := range mcpCatalogGroups(&snapshot.Catalog) {
		for _, raw := range *group.items {
			item := readMCPCatalogItem(raw)
			name := item.identity(group.kind)
			choices = append(choices, initMCPCapabilityChoice{kind: group.kind, name: name, label: "MCP " + group.kind + "  " + name + "  " + item.Description})
		}
	}
	return choices
}

// promptInitMCPCapabilities shares the existing terminal's filter, toggle, and confirm conventions without pregranting access.
func promptInitMCPCapabilities(input io.Reader, output io.Writer, service string, choices []initMCPCapabilityChoice) ([]int, error) {
	options := make([]huh.Option[int], 0, len(choices))
	for index, choice := range choices {
		options = append(options, huh.NewOption(mcpTerminalText(choice.label), index))
	}
	selected := []int{}
	field := huh.NewMultiSelect[int]().Title("Choose capabilities for " + service).Description("Press / to filter endpoints, tools, prompts, resources, or templates. Space toggles; Enter confirms.").Options(options...).Filterable(true).Validate(requireInitMCPCapabilities).Value(&selected)
	err := huh.NewForm(huh.NewGroup(field)).WithInput(input).WithOutput(output).Run()
	return selected, err
}

// requireInitMCPCapabilities prevents confirming a service with no selected capability.
func requireInitMCPCapabilities(selected []int) error {
	// Cancellation is separate from a confirmed but empty authority boundary.
	if len(selected) == 0 {
		return fmt.Errorf("select at least one endpoint or MCP capability")
	}
	return nil
}

// applyInitMCPCapabilityChoices maps admitted row identities without inferring tool names from UI labels.
func applyInitMCPCapabilityChoices(request *scaffoldRequest, service string, choices []initMCPCapabilityChoice, selected []int) (mcpSelectOptions, error) {
	options := mcpSelectOptions{}
	// Even a replaceable terminal runner must return a useful explicit selection.
	if err := requireInitMCPCapabilities(selected); err != nil {
		return options, err
	}
	for _, index := range selected {
		// Stale or malformed selection indexes must not accidentally choose another grant.
		if index < 0 || index >= len(choices) {
			return options, fmt.Errorf("invalid capability selection")
		}
		// Each chosen row contributes to exactly one capability namespace.
		if err := applyInitMCPCapabilityChoice(request, &options, service, choices[index]); err != nil {
			return options, err
		}

	}
	// Overlapping all-items choices are redundant rather than competing identity authorities in the picker.
	if options.all {
		options.selection.Tools = nil
		options.selection.Prompts = nil
		options.selection.Resources = nil
		options.selection.ResourceTemplates = nil
	}
	// Selecting all endpoints subsumes individual endpoint rows from this same service only.
	if containsString(request.selectAll, service) {
		request.operations = withoutInitMCPPhysicalSubset(request.operations, service)
	}
	return options, nil
}

// applyInitMCPCapabilityChoice maps one admitted choice into physical or imported authoring intent.
func applyInitMCPCapabilityChoice(request *scaffoldRequest, options *mcpSelectOptions, service string, choice initMCPCapabilityChoice) error {
	// Provider identities are kept separate from the terminal labels and protocol categories.
	switch choice.kind {
	case "endpoint":
		request.operations = append(request.operations, scaffoldOperation{service: service, operation: choice.name})
	case "endpoints_all":
		request.selectAll = append(request.selectAll, service)
	case "mcp_all":
		options.all = true
	default:
		return appendMCPInitValue(options, choice.kind, choice.name)
	}
	return nil
}

// withoutInitMCPPhysicalSubset removes redundant explicit rows only after the user chose the complete endpoint surface.
func withoutInitMCPPhysicalSubset(operations []scaffoldOperation, service string) []scaffoldOperation {
	result := make([]scaffoldOperation, 0, len(operations))
	for _, operation := range operations {
		// Other providers keep their independently selected endpoint authority.
		if operation.service != service {
			result = append(result, operation)
		}
	}
	return result
}
