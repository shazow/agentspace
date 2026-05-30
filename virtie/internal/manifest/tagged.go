package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type taggedRegistry[T any] []taggedVariant[T]

type taggedVariant[T any] struct {
	name   string
	decode func([]byte) (T, error)
}

func taggedValue[T any, V any](name string) taggedVariant[T] {
	return taggedVariant[T]{
		name: name,
		decode: func(data []byte) (T, error) {
			var target V
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&target); err != nil {
				var zero T
				return zero, err
			}
			return any(target).(T), nil
		},
	}
}

func decodeTaggedJSONList[T any](data []byte, path string, registry taggedRegistry[T]) ([]T, error) {
	var rawValues []json.RawMessage
	if err := json.Unmarshal(data, &rawValues); err != nil {
		return nil, err
	}
	values := make([]T, 0, len(rawValues))
	for i, raw := range rawValues {
		value, err := decodeTaggedJSONValue(raw, path, i, registry)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func decodeTaggedTOMLList[T any](data any, path string, registry taggedRegistry[T]) ([]T, error) {
	rawValues, err := taggedTOMLMaps(data, path)
	if err != nil {
		return nil, err
	}
	values := make([]T, 0, len(rawValues))
	for i, raw := range rawValues {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", path, i, err)
		}
		value, err := decodeTaggedJSONValue(encoded, path, i, registry)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func marshalTaggedJSONList[T any](values []T, tag func(T) string) ([]byte, error) {
	marshaled := make([]any, 0, len(values))
	for _, value := range values {
		marshaledValue, err := marshalTaggedJSONValue(value, tag)
		if err != nil {
			return nil, err
		}
		marshaled = append(marshaled, marshaledValue)
	}
	return json.Marshal(marshaled)
}

func decodeTaggedJSONValue[T any](data []byte, path string, index int, registry taggedRegistry[T]) (T, error) {
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		var zero T
		return zero, fmt.Errorf("%s[%d]: %w", path, index, err)
	}
	variant, ok := registry.lookup(header.Type)
	if !ok {
		var zero T
		return zero, fmt.Errorf("%s[%d].type must be %s", path, index, registry.expectedTypes())
	}
	value, err := variant.decode(data)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("%s[%d]: %w", path, index, err)
	}
	return value, nil
}

func marshalTaggedJSONValue[T any](value T, tag func(T) string) (map[string]any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	result["type"] = tag(value)
	return result, nil
}

func taggedTOMLMaps(data any, path string) ([]map[string]any, error) {
	switch values := data.(type) {
	case []map[string]any:
		return values, nil
	case []any:
		rawValues := make([]map[string]any, 0, len(values))
		for i, value := range values {
			raw, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s[%d] must be a table", path, i)
			}
			rawValues = append(rawValues, raw)
		}
		return rawValues, nil
	default:
		return nil, fmt.Errorf("%s must be an array of tables", path)
	}
}

func (r taggedRegistry[T]) lookup(name string) (taggedVariant[T], bool) {
	for _, variant := range r {
		if variant.name == name {
			return variant, true
		}
	}
	return taggedVariant[T]{}, false
}

func (r taggedRegistry[T]) expectedTypes() string {
	names := make([]string, 0, len(r))
	for _, variant := range r {
		names = append(names, variant.name)
	}
	switch len(names) {
	case 0:
		return "set"
	case 1:
		return names[0]
	case 2:
		return names[0] + " or " + names[1]
	default:
		return "one of " + strings.Join(names[:len(names)-1], ", ") + ", or " + names[len(names)-1]
	}
}
