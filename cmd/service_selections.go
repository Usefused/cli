package cmd

import (
	"fmt"
	"strings"
)

// parseAppServiceSelections keeps operation lists scoped to their service before reusing the existing lifecycle request.
func parseAppServiceSelections(values, legacyOperations []string) ([]scaffoldService, []scaffoldOperation, error) {
	services := make([]scaffoldService, 0, len(values))
	operations := make([]scaffoldOperation, 0)
	for _, value := range values {
		for _, group := range strings.Split(value, ";") {
			selector, operationList, scoped := strings.Cut(group, "=")
			// Bare comma-separated services remain accepted for scripts written before grouped selectors.
			if !scoped {
				parsed, err := parseScaffoldServices(strings.Split(selector, ","), false)
				// Empty groups and malformed versions must reject the entire request locally.
				if err != nil {
					return nil, nil, err
				}
				services = append(services, parsed...)
				continue
			}
			// Commas separate operations only after '=', so an operation list must have exactly one owner.
			if strings.Contains(selector, ",") {
				return nil, nil, fmt.Errorf("--service requires semicolons between services: 'jira=issueUpdate;slack=chatPostMessage'")
			}
			service, err := parseServiceSelector(selector, false)
			// Provider qualifiers and explicit version pins obey the same rules as bare selectors.
			if err != nil {
				return nil, nil, err
			}
			services = append(services, service)
			for _, rawOperation := range strings.Split(operationList, ",") {
				operation := strings.TrimSpace(rawOperation)
				// Missing members or nested assignments must not silently widen or misroute an allowlist.
				if operation == "" || strings.Contains(operation, "=") {
					return nil, nil, fmt.Errorf("--service %q requires <service>[@<version>]=<operationId>[,<operationId>...]", group)
				}
				operations = append(operations, scaffoldOperation{service: service.name, operation: operation})
			}
		}
	}
	legacy, err := parseScaffoldOperations(legacyOperations)
	// Existing --operation scripts retain their validation and additive scope during migration.
	if err != nil {
		return nil, nil, err
	}
	return services, append(operations, legacy...), nil
}

// completeAppServiceSelections makes bare services select all operations while preserving explicit legacy, event, and imported capability scope.
func completeAppServiceSelections(request *scaffoldRequest, aliases map[string]string) {
	imported := make(map[string]bool, len(request.mcpRequests))
	for name := range request.mcpRequests {
		imported[name] = true
		imported[aliases[name]] = true
	}
	for _, service := range request.services {
		// Any authored operation scope takes precedence over the bare service declaration used by older scripts.
		if sdkInitServiceHasOperationSelection(*request, service.name) || sdkInitServiceHasEventSelection(*request, service.name) {
			continue
		}
		// Imported MCP selections already express capability intent and must not gain unrelated physical endpoints.
		if imported[service.name] {
			continue
		}
		request.selectAll = append(request.selectAll, service.name)
	}
}
