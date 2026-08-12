// Package signaturepolicy mirrors the credential-reference-only inbound
// verification contract. The CLI transports recipes but never resolves them.
package signaturepolicy

import "github.com/Usefused/cli/internal/contractjson"

type RuleKind string
type PredicateOperator string
type ValueLocation string
type VerificationKind string
type ComponentKind string
type Algorithm string
type Encoding string
type Comparison string
type ComponentJoin string

const Version = 1

type Config struct {
	Version int    `json:"version" yaml:"version"`
	Rules   []Rule `json:"rules" yaml:"rules"`
}

// Rule order is transported exactly because Registry assigns first-match
// semantics to keep challenge traffic out of event verification recipes.
type Rule struct {
	Name         string       `json:"name" yaml:"name"`
	Kind         RuleKind     `json:"kind" yaml:"kind"`
	Predicates   []Predicate  `json:"predicates" yaml:"predicates"`
	Verification Verification `json:"verification" yaml:"verification"`
}

type Predicate struct {
	Source   ValueSource       `json:"source" yaml:"source"`
	Operator PredicateOperator `json:"operator" yaml:"operator"`
	Value    string            `json:"value,omitempty" yaml:"value,omitempty"`
}

type ValueSource struct {
	Location ValueLocation `json:"location" yaml:"location"`
	Name     string        `json:"name,omitempty" yaml:"name,omitempty"`
	Path     string        `json:"path,omitempty" yaml:"path,omitempty"`
}

type Verification struct {
	Kind      VerificationKind       `json:"kind" yaml:"kind"`
	Signature *SignatureVerification `json:"signature,omitempty" yaml:"signature,omitempty"`
	JWT       *JWTVerification       `json:"jwt,omitempty" yaml:"jwt,omitempty"`
	Challenge *ChallengeResponse     `json:"challenge,omitempty" yaml:"challenge,omitempty"`
}

type SignatureVerification struct {
	SecretRef  string           `json:"secret_ref" yaml:"secret_ref"`
	Signature  ValueSource      `json:"signature" yaml:"signature"`
	Components []InputComponent `json:"components" yaml:"components"`
	Algorithm  Algorithm        `json:"algorithm" yaml:"algorithm"`
	Encoding   Encoding         `json:"encoding" yaml:"encoding"`
	Comparison Comparison       `json:"comparison" yaml:"comparison"`
	Prefix     string           `json:"prefix,omitempty" yaml:"prefix,omitempty"`
	// Separator is explicit because concatenation is provider contract data;
	// neither CLI nor Engine may infer it from a provider identity.
	ComponentSeparator string `json:"component_separator" yaml:"component_separator"`
}

type InputComponent struct {
	Kind      ComponentKind `json:"kind" yaml:"kind"`
	Names     []string      `json:"names" yaml:"names"`
	Join      ComponentJoin `json:"join,omitempty" yaml:"join,omitempty"`
	Algorithm Algorithm     `json:"algorithm,omitempty" yaml:"algorithm,omitempty"`
	Encoding  Encoding      `json:"encoding,omitempty" yaml:"encoding,omitempty"`
}

type JWTVerification struct {
	SecretRef   string      `json:"secret_ref" yaml:"secret_ref"`
	Token       ValueSource `json:"token" yaml:"token"`
	Algorithms  []string    `json:"algorithms" yaml:"algorithms"`
	Issuer      string      `json:"issuer,omitempty" yaml:"issuer,omitempty"`
	Audience    string      `json:"audience,omitempty" yaml:"audience,omitempty"`
	ClockSkewMs int64       `json:"clock_skew_ms" yaml:"clock_skew_ms"`
}

type ChallengeResponse struct {
	Value      ValueSource `json:"value" yaml:"value"`
	BodyField  string      `json:"body_field" yaml:"body_field"`
	StatusCode int         `json:"status_code" yaml:"status_code"`
}

func (config *Config) UnmarshalJSON(payload []byte) error {
	type wireConfig Config
	var decoded wireConfig
	if err := contractjson.DecodeStrict(payload, &decoded, "signature policy"); err != nil {
		return err
	}
	*config = Config(decoded)
	return nil
}
