package pagination

import "github.com/Usefused/cli/internal/contractjson"

// Config is the transport-only pagination v3 contract shared by workspace
// config and Registry API projections. Registry and Engine own normalization
// and execution so the CLI cannot drift into a competing pagination policy.
type Config struct {
	Version      int                `yaml:"version" json:"version"`
	Request      []RequestStep      `yaml:"request" json:"request"`
	Response     ResponsePlan       `yaml:"response" json:"response"`
	Continuation []ContinuationStep `yaml:"continuation" json:"continuation"`
	Termination  Termination        `yaml:"termination" json:"termination"`
	GraphQL      *GraphQLPlan       `yaml:"graphql,omitempty" json:"graphql,omitempty"`
	Limits       Limits             `yaml:"limits" json:"limits"`
}

type RequestStep struct {
	State     string        `yaml:"state,omitempty" json:"state,omitempty"`
	Target    RequestTarget `yaml:"target" json:"target"`
	ValueType string        `yaml:"value_type" json:"value_type"`
	Initial   *Scalar       `yaml:"initial,omitempty" json:"initial,omitempty"`
	Constant  *Scalar       `yaml:"constant,omitempty" json:"constant,omitempty"`
	Apply     string        `yaml:"apply" json:"apply"`
}

type ResponsePlan struct {
	Items  ItemsSource     `yaml:"items" json:"items"`
	Values []ResponseValue `yaml:"values" json:"values"`
}

type ItemsSource struct {
	Path  string            `yaml:"path,omitempty" json:"path,omitempty"`
	Paths []ConditionalPath `yaml:"paths,omitempty" json:"paths,omitempty"`
}

type ResponseValue struct {
	Name   string      `yaml:"name" json:"name"`
	Source ValueSource `yaml:"source" json:"source"`
}

type ConditionalPath struct {
	Path string           `yaml:"path" json:"path"`
	When RequestCondition `yaml:"when" json:"when"`
}

type RequestCondition struct {
	State    string  `yaml:"state" json:"state"`
	Operator string  `yaml:"operator" json:"operator"`
	Value    *Scalar `yaml:"value,omitempty" json:"value,omitempty"`
}

type ItemSelector struct {
	Position string `yaml:"position" json:"position"`
	Path     string `yaml:"path,omitempty" json:"path,omitempty"`
}

type ContinuationStep struct {
	Kind          string        `yaml:"kind" json:"kind"`
	State         string        `yaml:"state" json:"state"`
	ResponseValue string        `yaml:"response_value,omitempty" json:"response_value,omitempty"`
	Increment     *Increment    `yaml:"increment,omitempty" json:"increment,omitempty"`
	Origin        *OriginPolicy `yaml:"origin,omitempty" json:"origin,omitempty"`
}

type OriginPolicy struct {
	Mode           string   `yaml:"mode" json:"mode"`
	AllowedOrigins []string `yaml:"allowed_origins,omitempty" json:"allowed_origins,omitempty"`
}

type Termination struct {
	StopOnEmptyItems    bool                  `yaml:"stop_on_empty_items,omitempty" json:"stop_on_empty_items,omitempty"`
	StopOnShortPage     *ShortPageTermination `yaml:"stop_on_short_page,omitempty" json:"stop_on_short_page,omitempty"`
	StopOnMissingValues []string              `yaml:"stop_on_missing_values,omitempty" json:"stop_on_missing_values,omitempty"`
	Conditions          []ResponseCondition   `yaml:"conditions,omitempty" json:"conditions,omitempty"`
	RepeatedValue       string                `yaml:"repeated_value" json:"repeated_value"`
}

type ShortPageTermination struct {
	RequestState string `yaml:"request_state" json:"request_state"`
}

type ResponseCondition struct {
	ResponseValue string  `yaml:"response_value" json:"response_value"`
	State         string  `yaml:"state,omitempty" json:"state,omitempty"`
	Operator      string  `yaml:"operator" json:"operator"`
	Value         *Scalar `yaml:"value,omitempty" json:"value,omitempty"`
}

type GraphQLPlan struct {
	Variables              []GraphQLVariable    `yaml:"variables" json:"variables"`
	ResultAliases          []GraphQLResultAlias `yaml:"result_aliases" json:"result_aliases"`
	FirstPageTemplate      string               `yaml:"first_page_template" json:"first_page_template"`
	SubsequentPageTemplate string               `yaml:"subsequent_page_template" json:"subsequent_page_template"`
}

type GraphQLVariable struct {
	Name      string `yaml:"name" json:"name"`
	State     string `yaml:"state" json:"state"`
	ValueType string `yaml:"value_type" json:"value_type"`
}

type GraphQLResultAlias struct {
	Name  string `yaml:"name" json:"name"`
	Alias string `yaml:"alias" json:"alias"`
}

type Increment struct {
	Mode  string `yaml:"mode" json:"mode"`
	Value int64  `yaml:"value,omitempty" json:"value,omitempty"`
}

type RequestTarget struct {
	Location string `yaml:"location" json:"location"`
	Name     string `yaml:"name" json:"name"`
}

type ValueSource struct {
	Location  string            `yaml:"location" json:"location"`
	Path      string            `yaml:"path,omitempty" json:"path,omitempty"`
	Name      string            `yaml:"name,omitempty" json:"name,omitempty"`
	Relation  string            `yaml:"relation,omitempty" json:"relation,omitempty"`
	ValueType string            `yaml:"value_type" json:"value_type"`
	Paths     []ConditionalPath `yaml:"paths,omitempty" json:"paths,omitempty"`
	Item      *ItemSelector     `yaml:"item,omitempty" json:"item,omitempty"`
}

type Scalar struct {
	Type    string  `yaml:"type" json:"type"`
	String  *string `yaml:"string,omitempty" json:"string,omitempty"`
	Integer *int64  `yaml:"integer,omitempty" json:"integer,omitempty"`
	Boolean *bool   `yaml:"boolean,omitempty" json:"boolean,omitempty"`
}

type Limits struct {
	MaxPages      int   `yaml:"max_pages" json:"max_pages"`
	MaxItems      int64 `yaml:"max_items" json:"max_items"`
	MaxBytes      int64 `yaml:"max_bytes" json:"max_bytes"`
	MaxDurationMs int64 `yaml:"max_duration_ms" json:"max_duration_ms"`
}

// UnmarshalJSON rejects removed strategies before workspace sync can silently
// erase them and then publish an incomplete policy.
func (config *Config) UnmarshalJSON(payload []byte) error {
	type wireConfig Config
	var decoded wireConfig
	if err := contractjson.DecodeStrict(payload, &decoded, "pagination policy"); err != nil {
		return err
	}
	*config = Config(decoded)
	return nil
}
