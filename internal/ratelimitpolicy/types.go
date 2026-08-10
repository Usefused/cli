// Package ratelimitpolicy mirrors the provider-neutral rate and quota wire
// contract consumed by the Engine. The CLI transports this shape unchanged;
// semantic policy validation and enforcement remain Engine responsibilities.
package ratelimitpolicy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

type Unit string
type Scope string
type Algorithm string
type ResetFormat string

const Version = 2

type Config struct {
	Version    int               `json:"version" yaml:"version"`
	Policies   []Policy          `json:"policies" yaml:"policies"`
	RetryAfter *RetryAfterConfig `json:"retry_after,omitempty" yaml:"retry_after,omitempty"`
}

type Policy struct {
	Name            string           `json:"name" yaml:"name"`
	Unit            Unit             `json:"unit" yaml:"unit"`
	Scope           Scope            `json:"scope" yaml:"scope"`
	DefaultCost     int64            `json:"default_cost" yaml:"default_cost"`
	OperationCosts  map[string]int64 `json:"operation_costs" yaml:"operation_costs"`
	Algorithm       Algorithm        `json:"algorithm" yaml:"algorithm"`
	FixedWindow     *FixedWindow     `json:"fixed_window,omitempty" yaml:"fixed_window,omitempty"`
	TokenBucket     *TokenBucket     `json:"token_bucket,omitempty" yaml:"token_bucket,omitempty"`
	ResponseHeaders *ResponseHeaders `json:"response_headers,omitempty" yaml:"response_headers,omitempty"`
}

type FixedWindow struct {
	Limit      int64 `json:"limit" yaml:"limit"`
	DurationMs int64 `json:"duration_ms" yaml:"duration_ms"`
}

type TokenBucket struct {
	Capacity         int64 `json:"capacity" yaml:"capacity"`
	RefillUnits      int64 `json:"refill_units" yaml:"refill_units"`
	RefillIntervalMs int64 `json:"refill_interval_ms" yaml:"refill_interval_ms"`
}

type ResponseHeaders struct {
	Limit     string       `json:"limit,omitempty" yaml:"limit,omitempty"`
	Remaining string       `json:"remaining,omitempty" yaml:"remaining,omitempty"`
	Reset     *ResetHeader `json:"reset,omitempty" yaml:"reset,omitempty"`
}

type ResetHeader struct {
	Name   string      `json:"name" yaml:"name"`
	Format ResetFormat `json:"format" yaml:"format"`
}

type RetryAfterConfig struct {
	Enabled    bool  `json:"enabled" yaml:"enabled"`
	MaxDelayMs int64 `json:"max_delay_ms" yaml:"max_delay_ms"`
}

// UnmarshalJSON is deliberately strict about wire fields while leaving
// semantic checks to the Engine. This prevents an old strategy/RPS document
// from being accepted as an empty v2 policy during GraphQL synchronization.
func (config *Config) UnmarshalJSON(payload []byte) error {
	type wireConfig Config
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var decoded wireConfig
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if err := requireEOF(decoder); err != nil {
		return err
	}
	*config = Config(decoded)
	return nil
}

func requireEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("rate-limit policy contains trailing JSON")
}
