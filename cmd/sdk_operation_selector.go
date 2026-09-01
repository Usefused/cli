package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/charmbracelet/huh"
)

type sdkOperationScope string

const (
	sdkOperationScopeAll    sdkOperationScope = "all"
	sdkOperationScopeChoose sdkOperationScope = "choose"
)

type sdkOperationSelection struct {
	operations []string
	selectAll  bool
}

// sdkOperationSelectionRunner keeps the terminal boundary replaceable so selector behavior can be tested without emulating a TTY.
var sdkOperationSelectionRunner = promptSDKOperationSelection

// promptSDKOperationSelection makes the complete service surface the visible default and opens detailed selection only on request.
func promptSDKOperationSelection(input io.Reader, output io.Writer, serviceName, serviceVersion string, endpoints []api.Integration) (sdkOperationSelection, error) {
	scope := sdkOperationScopeAll
	scopeField := huh.NewSelect[sdkOperationScope]().
		Title(fmt.Sprintf("Select operations for %s %s", serviceName, serviceVersion)).
		Description("Enter accepts all operations; choose a narrower set when this app needs less access.").
		Options(
			huh.NewOption("All operations", sdkOperationScopeAll),
			huh.NewOption("Choose operations…", sdkOperationScopeChoose),
		).
		Value(&scope)
	// The initialized value and first option keep Enter explicit: the highlighted row says exactly what will be granted.
	if err := huh.NewForm(huh.NewGroup(scopeField)).WithInput(input).WithOutput(output).Run(); err != nil {
		return sdkOperationSelection{}, fmt.Errorf("selecting operation scope: %w", err)
	}
	// Selecting the complete surface preserves select_all intent instead of freezing today's operation IDs.
	if scope == sdkOperationScopeAll {
		return sdkOperationSelection{operations: operationNames(endpoints), selectAll: true}, nil
	}

	selected := make([]string, 0)
	options := make([]huh.Option[string], 0, len(endpoints))
	for _, endpoint := range endpoints {
		options = append(options, huh.NewOption(sdkOperationSearchLabel(endpoint), endpoint.Name))
	}
	multiField := huh.NewMultiSelect[string]().
		Title(fmt.Sprintf("Choose operations for %s %s", serviceName, serviceVersion)).
		Description("Press / to filter by operation ID, method, path, description, or tag. Space toggles; Enter confirms.").
		Options(options...).
		Filterable(true).
		Validate(requireSDKOperationSelection).
		Value(&selected)
	// Search and selection happen in one bounded list so the chosen IDs always come from the fetched Registry surface.
	if err := huh.NewForm(huh.NewGroup(multiField)).WithInput(input).WithOutput(output).Run(); err != nil {
		return sdkOperationSelection{}, fmt.Errorf("choosing operations: %w", err)
	}
	return sdkOperationSelection{operations: selected}, nil
}

// requireSDKOperationSelection prevents an explicit narrowing path from producing an invalid service with no operation scope.
func requireSDKOperationSelection(operations []string) error {
	// Choosing the narrower path must name at least one executable operation; cancellation remains distinct from selecting all.
	if len(operations) == 0 {
		return errors.New("select at least one operation, or go back and choose All operations")
	}
	return nil
}

// sdkOperationSearchLabel exposes every available Registry search attribute to huh's case-insensitive text filter.
func sdkOperationSearchLabel(endpoint api.Integration) string {
	parts := []string{endpoint.Name, strings.ToUpper(strings.TrimSpace(endpoint.Method)), strings.TrimSpace(endpoint.Path)}
	appendDistinct := func(value string) {
		value = strings.Join(strings.Fields(value), " ")
		// Empty and repeated documentation fragments add noise without improving keyboard search.
		if value == "" || containsFold(parts, value) {
			return
		}
		parts = append(parts, value)
	}
	appendDistinct(endpoint.Description)
	// Structured documentation is optional, so only admitted fields enrich the visible and searchable label.
	if endpoint.Documentation != nil {
		appendDistinct(endpoint.Documentation.Summary)
		appendDistinct(endpoint.Documentation.Description)
		// Tags remain separate search tokens while the brackets keep their provider grouping legible.
		if len(endpoint.Documentation.Tags) > 0 {
			appendDistinct("[" + strings.Join(endpoint.Documentation.Tags, ", ") + "]")
		}
	}
	return strings.Join(parts, "  ")
}

// containsFold reports whether one normalized label part already carries the same searchable metadata.
func containsFold(values []string, candidate string) bool {
	for _, value := range values {
		// Case-only differences do not represent a distinct search attribute.
		if strings.EqualFold(value, candidate) {
			return true
		}
	}
	return false
}
