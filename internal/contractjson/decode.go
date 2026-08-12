// Package contractjson owns strict decoding shared by mirrored execution DTOs.
package contractjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// DecodeStrict rejects additive wire fields until the CLI mirror explicitly
// understands them, preventing a config round-trip from silently erasing data.
func DecodeStrict(payload []byte, target any, contract string) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("%s contains trailing JSON", contract)
}
