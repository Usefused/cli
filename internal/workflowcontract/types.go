// Package workflowcontract mirrors Engine-owned multi-request upload
// metadata. The CLI transports workflows but never advances their state.
package workflowcontract

import "github.com/Usefused/cli/internal/contractjson"

type UploadModeKind string
type UploadStepKind string
type UploadBodyKind string
type URLSourceKind string

const Version = 1

type UploadWorkflow struct {
	Version            int          `json:"version" yaml:"version"`
	AcceptedMediaTypes []string     `json:"accepted_media_types" yaml:"accepted_media_types"`
	MaxSizeBytes       int64        `json:"max_size_bytes,omitempty" yaml:"max_size_bytes,omitempty"`
	Modes              []UploadMode `json:"modes" yaml:"modes"`
}

type UploadMode struct {
	Kind  UploadModeKind `json:"kind" yaml:"kind"`
	Steps []UploadStep   `json:"steps" yaml:"steps"`
}

// Step order must survive transport because only the preceding initiation
// response may authorize a resumable transfer URL.
type UploadStep struct {
	Kind             UploadStepKind `json:"kind" yaml:"kind"`
	Method           string         `json:"method" yaml:"method"`
	URL              URLSource      `json:"url" yaml:"url"`
	Body             UploadBodyKind `json:"body" yaml:"body"`
	Chunking         *Chunking      `json:"chunking,omitempty" yaml:"chunking,omitempty"`
	SuccessStatuses  []StatusRange  `json:"success_statuses" yaml:"success_statuses"`
	ContinueStatuses []StatusRange  `json:"continue_statuses" yaml:"continue_statuses"`
}

type URLSource struct {
	Kind       URLSourceKind `json:"kind" yaml:"kind"`
	Path       string        `json:"path,omitempty" yaml:"path,omitempty"`
	HeaderName string        `json:"header_name,omitempty" yaml:"header_name,omitempty"`
	// Empty means same-origin only; explicit origins are transported for Engine
	// enforcement and never treated as caller-controlled upload destinations.
	AllowedOrigins []string `json:"allowed_origins,omitempty" yaml:"allowed_origins,omitempty"`
}

type StatusRange struct {
	Min int `json:"min" yaml:"min"`
	Max int `json:"max" yaml:"max"`
}

type Chunking struct {
	DefaultSizeBytes  int64 `json:"default_size_bytes" yaml:"default_size_bytes"`
	SizeMultipleBytes int64 `json:"size_multiple_bytes" yaml:"size_multiple_bytes"`
	MaxSizeBytes      int64 `json:"max_size_bytes" yaml:"max_size_bytes"`
}

func (workflow *UploadWorkflow) UnmarshalJSON(payload []byte) error {
	type wireWorkflow UploadWorkflow
	var decoded wireWorkflow
	if err := contractjson.DecodeStrict(payload, &decoded, "upload workflow"); err != nil {
		return err
	}
	*workflow = UploadWorkflow(decoded)
	return nil
}
