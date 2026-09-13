package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Usefused/cli/internal/configfile"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// buildWebhookInitRequest admits ingress-only flags and validates signing references before any Engine call.
func buildWebhookInitRequest(cmd *cobra.Command, name string, opts *unifiedInitOptions) (scaffoldRequest, error) {
	for _, flag := range []string{"operation", "select-all", "version", "language", "bucket", "extend"} {
		// App scope and version flags cannot silently change registration semantics.
		if cmd.Flags().Changed(flag) {
			return scaffoldRequest{}, fmt.Errorf("--%s cannot be used with --webhook", flag)
		}
	}
	services, err := parseScaffoldServices(opts.services, false)
	// Ambiguous service syntax must fail before selecting a local destination.
	if err != nil {
		return scaffoldRequest{}, err
	}
	// A registration without services cannot receive any delivery.
	if len(services) == 0 {
		return scaffoldRequest{}, errors.New("init --webhook requires at least one --service")
	}
	path, err := scaffoldTargetPath(configfile.KindWebhook, strings.TrimSpace(name), ConfigFile)
	// Unsafe or missing target names must not reach publication.
	if err != nil {
		return scaffoldRequest{}, err
	}
	request := scaffoldRequest{kind: configfile.KindWebhook, name: strings.TrimSpace(name), path: path, services: services, noApply: opts.noApply, webhookSecrets: map[string]string{}}
	allowed := map[string]bool{}
	for _, service := range services {
		allowed[service.name] = true
	}
	for _, raw := range opts.secrets {
		service, ref, ok := strings.Cut(raw, "=")
		service, ref = strings.TrimSpace(service), strings.TrimSpace(ref)
		// Values are deliberately excluded from diagnostics because a user may accidentally pass a literal secret.
		if !ok || !allowed[service] || ref == "" {
			return scaffoldRequest{}, errors.New("--secret requires <selected-service>=<bucket-secret-reference>")
		}
		// Repeated bindings could conceal conflicting verification credentials.
		if _, exists := request.webhookSecrets[service]; exists {
			return scaffoldRequest{}, fmt.Errorf("duplicate --secret for service %q", service)
		}
		request.webhookSecrets[service] = ref
	}
	data, err := newWebhookScaffoldData(request)
	// Serialization is completed before semantic validation or any filesystem write.
	if err != nil {
		return scaffoldRequest{}, err
	}
	// The shared parser owns the bucket-reference grammar; never accept raw signing keys here.
	if _, err := configfile.Parse(data, path); err != nil {
		return scaffoldRequest{}, errors.New("invalid webhook config: provide a name and signing secrets as ${bucket.<name>.secret.<key>} references")
	}
	return request, nil
}

// newWebhookScaffoldData encodes only registration identity and explicit signing references.
func newWebhookScaffoldData(request scaffoldRequest) ([]byte, error) {
	config := configfile.WebhookConfig{BaseConfig: configfile.BaseConfig{APIVersion: configfile.APIVersionV1, Kind: configfile.KindWebhook}, Name: request.name, Services: map[string]configfile.WebhookService{}}
	for _, service := range request.services {
		config.Services[service.name] = configfile.WebhookService{Secret: request.webhookSecrets[service.name]}
	}
	return yaml.Marshal(config)
}

// prepareUnifiedInitLifecycle reuses app onboarding while keeping ingress free of operation selection and implicit buckets.
func prepareUnifiedInitLifecycle(cmd *cobra.Command, mode unifiedInitMode, request scaffoldRequest) (sdkInitLifecycle, error) {
	// Existing app modes retain their exact lifecycle and compatibility behavior.
	if mode != unifiedInitModeWebhook {
		return prepareSDKInitLifecycle(cmd, request, resolveScaffoldBucket)
	}
	client, err := getAPIClient()
	// One authenticated client must own resolution and both plan/apply boundaries.
	if err != nil {
		return sdkInitLifecycle{}, err
	}
	resolved, services, err := resolveSDKInitServices(request, client)
	// Inaccessible or ambiguous services are terminal, never a reason to switch credentials.
	if err != nil {
		return sdkInitLifecycle{}, err
	}
	resolved.webhookSecrets = map[string]string{}
	for _, service := range services {
		for _, alias := range append([]string{service.target.slug}, service.target.requestedRefs...) {
			ref := request.webhookSecrets[alias]
			// Unsigned selections do not acquire an invented signing secret.
			if ref == "" {
				continue
			}
			prior := resolved.webhookSecrets[service.target.slug]
			// Two aliases resolving to one provider must agree on its verification reference.
			if prior != "" && prior != ref {
				return sdkInitLifecycle{}, fmt.Errorf("conflicting signing references for %s", service.target.slug)
			}
			resolved.webhookSecrets[service.target.slug] = ref
		}
	}
	draft, err := planSDKInitWorkspace(client, services)
	// Missing workspace dependencies use the same scoped activation plan as other init modes.
	if err != nil {
		return sdkInitLifecycle{}, err
	}
	// A complete workspace review must render before either apply boundary.
	if err := printSDKInitWorkspacePlan(cmd, draft); err != nil {
		return sdkInitLifecycle{}, err
	}
	return sdkInitLifecycle{client: client, request: resolved, services: services, draft: draft}, nil
}
