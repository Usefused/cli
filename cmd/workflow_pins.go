package cmd

import (
	"fmt"
	"github.com/Usefused/cli/internal/configfile"
)

type workflowServicePin struct {
	serviceID string
	versionID string
	version   string
}

// mergeWorkflowPins prevents equal display labels from hiding incompatible immutable dependency identities.
func mergeWorkflowPins(request *scaffoldRequest, template workflowTemplate) error {
	// Ordinary scaffolds allocate no workflow-specific state until the first release is selected.
	if request.workflowPins == nil {
		request.workflowPins = map[string]workflowServicePin{}
	}
	for key, service := range template.Services {
		pin := workflowServicePin{service.ServiceID, service.ServiceVersionID, service.Version}
		existing, ok := request.workflowPins[key]
		// One canonical provider key must retain the same exact identity across all selected workflows.
		if ok && existing != pin {
			return fmt.Errorf("workflow service identity conflict for %s", key)
		}
		request.workflowPins[key] = pin
	}
	return nil
}

// bindWorkflowPins attaches published version UUIDs only after canonical provider resolution proves the service identity.
func bindWorkflowPins(pins map[string]workflowServicePin, services []sdkInitResolvedService) error {
	for index := range services {
		service := &services[index]
		refs := append([]string{service.target.slug}, service.target.requestedRefs...)
		for _, key := range refs {
			pin, exists := pins[key]
			// Ordinary physical selections have no published dependency identity to carry forward.
			if !exists {
				continue
			}
			// A resolver result must not substitute another provider or reused version label.
			if pin.serviceID != service.target.serviceID || pin.version != service.version {
				return fmt.Errorf("workflow dependency resolution changed identity for %s", key)
			}
			service.workflowVersionID = pin.versionID
		}
	}
	return nil
}

// pinWorkflowWorkspaceVersions adds exact release identity to the existing workspace desired-state document.
func pinWorkflowWorkspaceVersions(config *configfile.WorkspaceConfig, services []sdkInitResolvedService) error {
	for _, selected := range services {
		// Ordinary init retains its existing version-resolution behavior.
		if selected.workflowVersionID == "" {
			continue
		}
		service := config.Services[selected.target.slug]
		for index := range service.Versions {
			version := &service.Versions[index]
			// Other enabled versions remain untouched by this additive installation.
			if version.Version != selected.version {
				continue
			}
			// Existing immutable membership cannot be replaced because a Registry label was reused.
			if version.ServiceVersionID != "" && version.ServiceVersionID != selected.workflowVersionID {
				return fmt.Errorf("workflow version identity conflicts with workspace service %s", selected.target.slug)
			}
			version.ServiceVersionID = selected.workflowVersionID
		}
		service.ServiceID = selected.target.serviceID
		config.Services[selected.target.slug] = service
	}
	return nil
}
