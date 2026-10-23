package cmd

import (
	"fmt"
	"time"

	"github.com/Usefused/cli/internal/api"
	"github.com/charmbracelet/huh"
)

type connectAuthOption struct {
	auth   api.AuthConfig
	status string
}

var selectConnectAuthInteractively = promptConnectAuthSelect

// loadConnectAuthOptions inspects every metadata page so the picker can distinguish setup from editing.
func loadConnectAuthOptions(client *api.Client, bucketID, serviceID string, choices []api.AuthConfig) ([]connectAuthOption, error) {
	options := make([]connectAuthOption, len(choices))
	for i, auth := range choices {
		options[i] = connectAuthOption{auth: auth, status: "Not configured"}
	}
	now := time.Now()
	for offset := 0; ; {
		page, err := client.ListSecretMetaPage(bucketID, api.PageOptions{Limit: 100, Offset: offset})
		// Failed inspection cannot authorize editing or imply an absent credential.
		if err != nil {
			return nil, err
		}
		for _, metadata := range page.Items {
			for i := range options {
				status := connectAuthStatus(metadata, serviceID, options[i].auth, now)
				// Unrelated rows cannot change labels, and a complete family wins over partial metadata.
				if status != "" && options[i].status != "Configured" {
					options[i].status = status
				}
			}
		}
		offset += len(page.Items)
		// Even after finding configured credentials, later pages may describe other menu entries.
		if offset >= page.Total {
			return options, nil
		}
		// A stalled page must fail rather than display misleading missing statuses.
		if len(page.Items) == 0 {
			return nil, fmt.Errorf("credential metadata pagination made no progress")
		}
	}
}

// connectAuthStatus uses only scoped metadata, never plaintext, to label existing credential families.
func connectAuthStatus(metadata api.SecretMetaResponse, serviceID string, auth api.AuthConfig, now time.Time) string {
	// Scheme names alone cannot distinguish another service or a same-name authentication family.
	if metadata.ServiceID != serviceID || metadata.KeyName != secretAuthCredentialName(&auth) || canonicalSecretTypeName(metadata.CredentialType) != canonicalSecretAuthType(&auth) {
		return ""
	}
	// Expired material remains visibly present so users know they are replacing an existing credential.
	if metadata.ExpiresAt != nil && !metadata.ExpiresAt.After(now) {
		return "Expired"
	}
	// Only a complete, unexpired family is presented as ready for use.
	if connectAuthConfigured(metadata, serviceID, auth, now) {
		return "Configured"
	}
	return "Incomplete"
}

// chooseConnectAuth lets terminal users inspect and edit every method while keeping automation deterministic.
func chooseConnectAuth(options []connectAuthOption, automated bool) (*connectAuthOption, error) {
	// Interactive users see status even for a sole method before deciding to set or replace its value.
	if !automated {
		return selectConnectAuthInteractively(options)
	}
	// Automation must identify one exact scheme rather than favoring whichever already has credentials.
	if len(options) != 1 {
		return nil, fmt.Errorf("multiple authentication methods; pass --type and, when needed, --auth-name")
	}
	return &options[0], nil
}

// connectAuthOptionLabel makes existing values visible without exposing or pre-filling credential material.
func connectAuthOptionLabel(option connectAuthOption) string {
	label := fmt.Sprintf("%s (%s) — %s", option.auth.Name, canonicalSecretAuthType(&option.auth), option.status)
	// A checkmark highlights configured methods as editable entries instead of hiding them.
	if option.status == "Configured" {
		return "✓ " + label + " · edit"
	}
	return label + " · set up"
}

// promptConnectAuthSelect treats cancellation as leaving the selected bucket unchanged.
func promptConnectAuthSelect(available []connectAuthOption) (*connectAuthOption, error) {
	options := make([]huh.Option[int], len(available))
	for i, option := range available {
		options[i] = huh.NewOption(connectAuthOptionLabel(option), i)
	}
	var selected int
	err := huh.NewSelect[int]().Title("Choose authentication to set up or edit").Options(options...).Value(&selected).Run()
	// Cancelled input must never turn the initially highlighted option into a credential write.
	if err != nil {
		return nil, err
	}
	return &available[selected], nil
}
