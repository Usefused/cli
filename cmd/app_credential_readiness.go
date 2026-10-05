package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Usefused/cli/internal/api"
	"github.com/Usefused/cli/internal/configfile"
	"github.com/google/uuid"
)

type appCredentialTarget struct {
	bucket      api.MissingCredentialBucket
	requirement api.MissingCredentialRequirement
}

// readinessAppConfig shares credential selection across app kinds without reinterpreting the authored buckets.
func readinessAppConfig(cfg *configfile.ParsedConfig) *configfile.AppConfig {
	// Non-app input cannot authorize a credential mutation.
	if cfg == nil {
		return nil
	}
	switch cfg.Kind {
	case configfile.KindSDK:
		return cfg.SDK
	case configfile.KindMCP:
		return cfg.MCP
	case configfile.KindUnifiedApp:
		return cfg.UnifiedApp
	default:
		return nil
	}
}

// credentialReadinessTargets validates every target before the first write, including service bucket overrides.
func credentialReadinessTargets(cfg *configfile.ParsedConfig, readiness *api.CredentialReadiness) ([]appCredentialTarget, error) {
	app := readinessAppConfig(cfg)
	// A typed app and missing requirements are required before setup can collect any credentials.
	if app == nil || readiness == nil || len(readiness.MissingCredentials) == 0 {
		return nil, errors.New("Engine returned incomplete credential readiness metadata")
	}
	buckets := make(map[string]api.MissingCredentialBucket)
	allowed := map[string]bool{strings.TrimSpace(app.Bucket): true}
	for _, service := range app.Services {
		// Each explicitly authored override is part of the reviewed credential boundary.
		if name := strings.TrimSpace(service.Bucket); name != "" {
			allowed[name] = true
		}
	}
	for _, bucket := range readiness.Buckets {
		// Bucket identity and name must both be usable before presenting a secure write target.
		if _, err := uuid.Parse(bucket.ID); err != nil || strings.TrimSpace(bucket.Name) == "" {
			return nil, errors.New("Engine returned invalid credential bucket metadata")
		}
		if _, duplicate := buckets[bucket.ID]; duplicate {
			return nil, errors.New("Engine returned duplicate credential buckets")
		}
		// An omitted legacy default delegates its name to Engine; explicit names and overrides remain exact.
		if !allowed[bucket.Name] && strings.TrimSpace(app.Bucket) != "" {
			return nil, fmt.Errorf("Engine resolved bucket %q outside the app config; no credentials were changed", bucket.Name)
		}
		buckets[bucket.ID] = bucket
	}
	// Older Engines have one default target; this cannot authorize a service override on its own.
	if len(buckets) == 0 && readiness.Bucket != nil {
		bucket, err := validateSDKPlanCredentialTarget(cfg, readiness.Bucket)
		if err != nil {
			return nil, err
		}
		buckets[bucket.ID] = *bucket
	}
	requirements := append([]api.MissingCredentialRequirement(nil), readiness.MissingCredentials...)
	var targets []appCredentialTarget
	for index, requirement := range requirements {
		// Only the legacy envelope permits an omitted per-requirement bucket ID.
		if requirement.BucketID == "" && len(readiness.Buckets) == 0 && readiness.Bucket != nil {
			requirement.BucketID = readiness.Bucket.ID
		}
		bucket, found := buckets[requirement.BucketID]
		if !found || (requirement.BucketName != "" && requirement.BucketName != bucket.Name) {
			return nil, errors.New("Engine returned an unresolved credential bucket; no credentials were changed")
		}
		requirement.BucketName = bucket.Name
		requirement.AuthType = canonicalSecretTypeName(requirement.AuthType)
		requirements[index] = requirement
		targets = append(targets, appCredentialTarget{bucket: bucket, requirement: requirement})
	}
	// Validate the complete list before setup begins so malformed later rows cannot cause partial writes.
	if _, err := validateMissingCredentialRequirements(requirements); err != nil {
		return nil, err
	}
	return targets, nil
}
