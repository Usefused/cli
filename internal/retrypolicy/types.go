// Package retrypolicy mirrors the provider-neutral retry wire contract. The
// CLI transports rules unchanged and never replays provider requests itself.
package retrypolicy

import (
	"github.com/Usefused/cli/internal/contractjson"
)

type OperationKind string
type ErrorKind string
type BodyReplayability string
type IdempotencyKeyRequirement string
type BackoffStrategy string
type RetryAfterFormat string

const Version = 3

type Config struct {
	Version int    `json:"version" yaml:"version"`
	Rules   []Rule `json:"rules" yaml:"rules"`
}

type Rule struct {
	Predicates Predicates `json:"predicates" yaml:"predicates"`
	Action     Action     `json:"action" yaml:"action"`
}

type Predicates struct {
	Methods                 []string                `json:"methods" yaml:"methods"`
	OperationKinds          []OperationKind         `json:"operation_kinds" yaml:"operation_kinds"`
	Statuses                []StatusRange           `json:"statuses" yaml:"statuses"`
	Errors                  []ErrorKind             `json:"errors" yaml:"errors"`
	BodyReplayability       BodyReplayability       `json:"body_replayability" yaml:"body_replayability"`
	IdempotencyKey          IdempotencyKeyPredicate `json:"idempotency_key" yaml:"idempotency_key"`
	RequiredProviderHeaders []string                `json:"required_provider_headers" yaml:"required_provider_headers"`
}

type StatusRange struct {
	Min int `json:"min" yaml:"min"`
	Max int `json:"max" yaml:"max"`
}

type IdempotencyKeyPredicate struct {
	Requirement IdempotencyKeyRequirement `json:"requirement" yaml:"requirement"`
	Header      string                    `json:"header,omitempty" yaml:"header,omitempty"`
}

type Action struct {
	MaxAttempts       int                `json:"max_attempts" yaml:"max_attempts"`
	MaxElapsedMs      int64              `json:"max_elapsed_ms" yaml:"max_elapsed_ms"`
	Backoff           Backoff            `json:"backoff" yaml:"backoff"`
	RetryAfterHeaders []RetryAfterHeader `json:"retry_after_headers" yaml:"retry_after_headers"`
}

type Backoff struct {
	Strategy    BackoffStrategy `json:"strategy" yaml:"strategy"`
	BaseDelayMs int64           `json:"base_delay_ms" yaml:"base_delay_ms"`
	MaxDelayMs  int64           `json:"max_delay_ms" yaml:"max_delay_ms"`
	JitterMs    int64           `json:"jitter_ms" yaml:"jitter_ms"`
}

type RetryAfterHeader struct {
	Name       string             `json:"name" yaml:"name"`
	Formats    []RetryAfterFormat `json:"formats" yaml:"formats"`
	MaxDelayMs int64              `json:"max_delay_ms" yaml:"max_delay_ms"`
}

// UnmarshalJSON keeps CLI workspace and Registry response DTOs canonical-only;
// rejecting unknown fields prevents an obsolete shape becoming an empty policy.
func (config *Config) UnmarshalJSON(payload []byte) error {
	type wireConfig Config
	var decoded wireConfig
	if err := contractjson.DecodeStrict(payload, &decoded, "retry policy"); err != nil {
		return err
	}
	*config = Config(decoded)
	return nil
}
