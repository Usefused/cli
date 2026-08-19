// Package connectionprofile mirrors credential-free post-auth discovery and
// resource binding metadata. CLI transports it; Engine performs discovery.
package connectionprofile

import "github.com/Usefused/cli/internal/contractjson"

type Profile struct {
	AuthType          string                   `json:"auth_type,omitempty" yaml:"auth_type,omitempty"`
	AuthName          string                   `json:"auth_name,omitempty" yaml:"auth_name,omitempty"`
	OAuth2Flow        string                   `json:"oauth2_flow,omitempty" yaml:"oauth2_flow,omitempty"`
	ResourceDiscovery *ResourceDiscoveryConfig `json:"resource_discovery,omitempty" yaml:"resource_discovery,omitempty"`
	ResourceInput     *ResourceInputConfig     `json:"resource_input,omitempty" yaml:"resource_input,omitempty"`
	Metadata          map[string]string        `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Bindings          []Binding                `json:"bindings,omitempty" yaml:"bindings,omitempty"`
}

type ResourceDiscoveryConfig struct {
	Version         int      `json:"version" yaml:"version"`
	Stage           string   `json:"stage" yaml:"stage"`
	OperationID     string   `json:"operation_id" yaml:"operation_id"`
	Server          string   `json:"server,omitempty" yaml:"server,omitempty"`
	IDPath          string   `json:"id_path" yaml:"id_path"`
	NamePath        string   `json:"name_path,omitempty" yaml:"name_path,omitempty"`
	BaseURLPath     string   `json:"base_url_path,omitempty" yaml:"base_url_path,omitempty"`
	BaseURLTemplate string   `json:"base_url_template,omitempty" yaml:"base_url_template,omitempty"`
	ScopesPath      string   `json:"scopes_path,omitempty" yaml:"scopes_path,omitempty"`
	ResourceType    string   `json:"resource_type" yaml:"resource_type"`
	AutoRun         string   `json:"auto_run,omitempty" yaml:"auto_run,omitempty"`
	Lifecycle       string   `json:"lifecycle,omitempty" yaml:"lifecycle,omitempty"`
	AllowedHosts    []string `json:"allowed_hosts,omitempty" yaml:"allowed_hosts,omitempty"`
}

func (discovery *ResourceDiscoveryConfig) UnmarshalJSON(payload []byte) error {
	type wireDiscovery ResourceDiscoveryConfig
	var decoded wireDiscovery
	if err := contractjson.DecodeStrict(payload, &decoded, "resource discovery"); err != nil {
		return err
	}
	*discovery = ResourceDiscoveryConfig(decoded)
	return nil
}

type ResourceInputConfig struct {
	Fields          []ResourceInputField         `json:"fields" yaml:"fields"`
	BaseURLTemplate string                       `json:"base_url_template" yaml:"base_url_template"`
	ResourceType    string                       `json:"resource_type" yaml:"resource_type"`
	AllowedHosts    []string                     `json:"allowed_hosts,omitempty" yaml:"allowed_hosts,omitempty"`
	DiscoveryMatch  *ResourceInputDiscoveryMatch `json:"discovery_match,omitempty" yaml:"discovery_match,omitempty"`
}

// ResourceInputDiscoveryMatch identifies the provider-returned metadata field
// that must equal the customer-derived resource URL.
type ResourceInputDiscoveryMatch struct {
	MetadataKey string `json:"metadata_key" yaml:"metadata_key"`
}

// UnmarshalJSON keeps the nested input contract closed so CLI round-trips
// cannot silently erase fields introduced by a newer control plane.
func (input *ResourceInputConfig) UnmarshalJSON(payload []byte) error {
	type wireResourceInput ResourceInputConfig
	var decoded wireResourceInput
	// Unknown nested fields fail before the CLI can rewrite an incomplete contract.
	if err := contractjson.DecodeStrict(payload, &decoded, "resource input"); err != nil {
		return err
	}
	*input = ResourceInputConfig(decoded)
	return nil
}

// UnmarshalJSON keeps match semantics closed because an ignored field could
// turn a customer constraint into a different resource selection rule.
func (match *ResourceInputDiscoveryMatch) UnmarshalJSON(payload []byte) error {
	type wireDiscoveryMatch ResourceInputDiscoveryMatch
	var decoded wireDiscoveryMatch
	// Match rules remain version-locked with the CLI that transports them.
	if err := contractjson.DecodeStrict(payload, &decoded, "resource input discovery match"); err != nil {
		return err
	}
	*match = ResourceInputDiscoveryMatch(decoded)
	return nil
}

type ResourceInputField struct {
	Name     string `json:"name" yaml:"name"`
	Label    string `json:"label,omitempty" yaml:"label,omitempty"`
	Required bool   `json:"required,omitempty" yaml:"required,omitempty"`
	Pattern  string `json:"pattern,omitempty" yaml:"pattern,omitempty"`
}

type Binding struct {
	Value             string   `json:"value" yaml:"value"`
	Location          string   `json:"location" yaml:"location"`
	Name              string   `json:"name,omitempty" yaml:"name,omitempty"`
	Mode              string   `json:"mode" yaml:"mode"`
	Operations        []string `json:"operations,omitempty" yaml:"operations,omitempty"`
	ProviderExtension bool     `json:"provider_extension,omitempty" yaml:"provider_extension,omitempty"`
}

func (profile *Profile) UnmarshalJSON(payload []byte) error {
	type wireProfile Profile
	var decoded wireProfile
	if err := contractjson.DecodeStrict(payload, &decoded, "connection profile"); err != nil {
		return err
	}
	*profile = Profile(decoded)
	return nil
}
