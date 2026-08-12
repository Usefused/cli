// Package catalogcontract mirrors deterministic multi-spec composition
// metadata. Registry remains the only component that composes catalogues.
package catalogcontract

import "github.com/Usefused/cli/internal/contractjson"

type CollisionPolicy string

const Version = 1

type Composition struct {
	Version         int             `json:"version" yaml:"version"`
	CollisionPolicy CollisionPolicy `json:"collision_policy" yaml:"collision_policy"`
	Sources         []Source        `json:"sources" yaml:"sources"`
}

type Source struct {
	Name            string `json:"name" yaml:"name"`
	Namespace       string `json:"namespace" yaml:"namespace"`
	SourceRef       string `json:"source_ref" yaml:"source_ref"`
	OperationPrefix string `json:"operation_prefix" yaml:"operation_prefix"`
}

func (composition *Composition) UnmarshalJSON(payload []byte) error {
	type wireComposition Composition
	var decoded wireComposition
	if err := contractjson.DecodeStrict(payload, &decoded, "catalog composition"); err != nil {
		return err
	}
	*composition = Composition(decoded)
	return nil
}
