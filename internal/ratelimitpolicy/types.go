// Package ratelimitpolicy mirrors the provider-neutral quota and concurrency
// wire contract. The CLI transports this shape without enforcing it.
package ratelimitpolicy

import (
	"github.com/Usefused/cli/internal/contractjson"
)

type Unit string
type Mode string
type IdentityKind string
type Algorithm string
type ResponseSignalSource string
type ResetFormat string

const Version = 3

type Config struct {
	Version    int               `json:"version" yaml:"version"`
	Policies   []Policy          `json:"policies" yaml:"policies"`
	Cooldown   *Cooldown         `json:"cooldown,omitempty" yaml:"cooldown,omitempty"`
	RetryAfter *RetryAfterConfig `json:"retry_after,omitempty" yaml:"retry_after,omitempty"`
}

type Policy struct {
	Name            string           `json:"name" yaml:"name"`
	Mode            Mode             `json:"mode" yaml:"mode"`
	Unit            Unit             `json:"unit" yaml:"unit"`
	Identity        BucketIdentity   `json:"identity" yaml:"identity"`
	Cost            CostPlan         `json:"cost" yaml:"cost"`
	Algorithm       Algorithm        `json:"algorithm" yaml:"algorithm"`
	FixedWindow     *FixedWindow     `json:"fixed_window,omitempty" yaml:"fixed_window,omitempty"`
	RollingWindow   *RollingWindow   `json:"rolling_window,omitempty" yaml:"rolling_window,omitempty"`
	TokenBucket     *TokenBucket     `json:"token_bucket,omitempty" yaml:"token_bucket,omitempty"`
	Concurrency     *Concurrency     `json:"concurrency,omitempty" yaml:"concurrency,omitempty"`
	ResponseSignals *ResponseSignals `json:"response_signals,omitempty" yaml:"response_signals,omitempty"`
}

type BucketIdentity struct {
	Inputs []IdentityInput `json:"inputs" yaml:"inputs"`
}

type IdentityInput struct {
	Kind    IdentityKind `json:"kind" yaml:"kind"`
	Binding string       `json:"binding,omitempty" yaml:"binding,omitempty"`
	Name    string       `json:"name,omitempty" yaml:"name,omitempty"`
}

type CostPlan struct {
	Default int64      `json:"default" yaml:"default"`
	Rules   []CostRule `json:"rules" yaml:"rules"`
}

type CostRule struct {
	Operation string `json:"operation" yaml:"operation"`
	Cost      int64  `json:"cost" yaml:"cost"`
}

type FixedWindow struct {
	Limit      int64 `json:"limit" yaml:"limit"`
	DurationMs int64 `json:"duration_ms" yaml:"duration_ms"`
}

type RollingWindow struct {
	Limit      int64 `json:"limit" yaml:"limit"`
	DurationMs int64 `json:"duration_ms" yaml:"duration_ms"`
}

type TokenBucket struct {
	Capacity         int64 `json:"capacity" yaml:"capacity"`
	RefillUnits      int64 `json:"refill_units" yaml:"refill_units"`
	RefillIntervalMs int64 `json:"refill_interval_ms" yaml:"refill_interval_ms"`
}

type Concurrency struct {
	Limit int64 `json:"limit" yaml:"limit"`
}

type ResponseSignals struct {
	Limit     *ResponseSignal `json:"limit,omitempty" yaml:"limit,omitempty"`
	Remaining *ResponseSignal `json:"remaining,omitempty" yaml:"remaining,omitempty"`
	Reset     *ResetSignal    `json:"reset,omitempty" yaml:"reset,omitempty"`
	Cost      *ResponseSignal `json:"cost,omitempty" yaml:"cost,omitempty"`
}

type ResponseSignal struct {
	Source ResponseSignalSource `json:"source" yaml:"source"`
	Name   string               `json:"name,omitempty" yaml:"name,omitempty"`
	Path   string               `json:"path,omitempty" yaml:"path,omitempty"`
}

type ResetSignal struct {
	Signal ResponseSignal `json:"signal" yaml:"signal"`
	Format ResetFormat    `json:"format" yaml:"format"`
}

type Cooldown struct {
	Statuses []StatusRange    `json:"statuses" yaml:"statuses"`
	Headers  []CooldownHeader `json:"headers" yaml:"headers"`
}

type StatusRange struct {
	Min int `json:"min" yaml:"min"`
	Max int `json:"max" yaml:"max"`
}

type CooldownHeader struct {
	Name       string        `json:"name" yaml:"name"`
	Formats    []ResetFormat `json:"formats" yaml:"formats"`
	MaxDelayMs int64         `json:"max_delay_ms" yaml:"max_delay_ms"`
}

type RetryAfterConfig struct {
	Enabled    bool  `json:"enabled" yaml:"enabled"`
	MaxDelayMs int64 `json:"max_delay_ms" yaml:"max_delay_ms"`
}

// UnmarshalJSON makes the CLI a strict v3 mirror so removed quota fields
// cannot survive a sync and be emitted back to Engine.
func (config *Config) UnmarshalJSON(payload []byte) error {
	type wireConfig Config
	var decoded wireConfig
	if err := contractjson.DecodeStrict(payload, &decoded, "rate-limit policy"); err != nil {
		return err
	}
	*config = Config(decoded)
	return nil
}
