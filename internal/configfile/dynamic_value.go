package configfile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"

	"gopkg.in/yaml.v3"
)

var (
	jsonNumberPattern         = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)
	jsonNumberFractionPattern = regexp.MustCompile(`[.eE]`)
)

// DynamicValue is an exact recursive JSON value carried through YAML or JSON
// config. Raw contains nil, bool, string, json.Number, []DynamicValue, or
// map[string]DynamicValue; YAML-only types are rejected instead of coerced.
type DynamicValue struct {
	Raw any
}

// UnmarshalYAML preserves a portable JSON value from its original YAML node.
func (v *DynamicValue) UnmarshalYAML(node *yaml.Node) error {
	decoded, err := decodeDynamicYAML(node, 0)
	if err != nil {
		return err
	}
	*v = decoded
	return nil
}

// decodeDynamicYAML recursively converts one YAML node without YAML coercions.
func decodeDynamicYAML(node *yaml.Node, depth int) (DynamicValue, error) {
	if depth > MaxUnifiedValueDepth {
		return DynamicValue{}, fmt.Errorf("DynamicValue exceeds maximum depth %d", MaxUnifiedValueDepth)
	}
	switch node.Kind {
	case yaml.ScalarNode:
		return decodeDynamicYAMLScalar(node)
	case yaml.SequenceNode:
		return decodeDynamicYAMLSequence(node, depth)
	case yaml.MappingNode:
		return decodeDynamicYAMLMapping(node, depth)
	case yaml.AliasNode:
		return DynamicValue{}, fmt.Errorf("DynamicValue YAML aliases are not supported")
	default:
		return DynamicValue{}, fmt.Errorf("DynamicValue must be a JSON-compatible scalar, sequence, or mapping")
	}
}

// decodeDynamicYAMLScalar accepts only scalar forms with exact JSON equivalents.
func decodeDynamicYAMLScalar(node *yaml.Node) (DynamicValue, error) {
	switch node.ShortTag() {
	case "!!null":
		return DynamicValue{Raw: nil}, nil
	case "!!str":
		return DynamicValue{Raw: node.Value}, nil
	case "!!bool":
		var value bool
		if err := node.Decode(&value); err != nil {
			return DynamicValue{}, err
		}
		return DynamicValue{Raw: value}, nil
	case "!!int", "!!float":
		if !jsonNumberPattern.MatchString(node.Value) {
			return DynamicValue{}, fmt.Errorf("DynamicValue number %q is not valid JSON", node.Value)
		}
		return DynamicValue{Raw: json.Number(node.Value)}, nil
	default:
		return DynamicValue{}, fmt.Errorf("DynamicValue YAML tag %q is not JSON-compatible", node.ShortTag())
	}
}

// decodeDynamicYAMLSequence preserves every item in a portable JSON array.
func decodeDynamicYAMLSequence(node *yaml.Node, depth int) (DynamicValue, error) {
	values := make([]DynamicValue, len(node.Content))
	for i, child := range node.Content {
		value, err := decodeDynamicYAML(child, depth+1)
		if err != nil {
			return DynamicValue{}, err
		}
		values[i] = value
	}
	return DynamicValue{Raw: values}, nil
}

// decodeDynamicYAMLMapping preserves string-keyed portable JSON objects.
func decodeDynamicYAMLMapping(node *yaml.Node, depth int) (DynamicValue, error) {
	values := make(map[string]DynamicValue, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		if keyNode.ShortTag() != "!!str" || keyNode.Value == "<<" {
			return DynamicValue{}, fmt.Errorf("DynamicValue object keys must be explicit strings")
		}
		if _, exists := values[keyNode.Value]; exists {
			return DynamicValue{}, fmt.Errorf("DynamicValue object contains duplicate key %q", keyNode.Value)
		}
		value, err := decodeDynamicYAML(node.Content[i+1], depth+1)
		if err != nil {
			return DynamicValue{}, err
		}
		values[keyNode.Value] = value
	}
	return DynamicValue{Raw: values}, nil
}

// UnmarshalJSON decodes one exact JSON value and rejects trailing documents.
func (v *DynamicValue) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return err
	}
	*v = dynamicValueFromJSON(raw)
	return nil
}

// rejectTrailingJSON ensures a DynamicValue contains exactly one JSON value.
func rejectTrailingJSON(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("DynamicValue contains trailing JSON")
}

// dynamicValueFromJSON wraps recursively decoded JSON collections.
func dynamicValueFromJSON(raw any) DynamicValue {
	switch value := raw.(type) {
	case []any:
		items := make([]DynamicValue, len(value))
		for i, item := range value {
			items[i] = dynamicValueFromJSON(item)
		}
		return DynamicValue{Raw: items}
	case map[string]any:
		items := make(map[string]DynamicValue, len(value))
		for key, item := range value {
			items[key] = dynamicValueFromJSON(item)
		}
		return DynamicValue{Raw: items}
	default:
		return DynamicValue{Raw: value}
	}
}

// MarshalJSON emits the preserved JSON-compatible value without coercion.
func (v DynamicValue) MarshalJSON() ([]byte, error) { return json.Marshal(v.Raw) }

// MarshalYAML keeps exact JSON number spelling when rewriting configuration.
func (v DynamicValue) MarshalYAML() (any, error) {
	if number, ok := v.Raw.(json.Number); ok {
		tag := "!!int"
		if jsonNumberFractionPattern.MatchString(number.String()) {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: number.String()}, nil
	}
	return v.Raw, nil
}
