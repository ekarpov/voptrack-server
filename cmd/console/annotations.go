package main

import (
	"bytes"
	"encoding/json"
)

// Keep opaque JSON annotations intact. Only string-valued annotations are used
// by the currently supported presentation rules; other JSON is not coerced.
func readAnnotations(raw json.RawMessage) (map[string]string, map[string]json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, nil, err
	}
	strings := map[string]string{}
	opaque := map[string]json.RawMessage{}
	for key, value := range values {
		var text string
		if err := json.Unmarshal(value, &text); err == nil && string(value) != "null" {
			strings[key] = text
			continue
		}
		var normalized any
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.UseNumber()
		if err := decoder.Decode(&normalized); err != nil {
			return nil, nil, err
		}
		opaque[key], _ = json.Marshal(normalized)
	}
	if len(strings) == 0 {
		strings = nil
	}
	if len(opaque) == 0 {
		opaque = nil
	}
	return strings, opaque, nil
}
func writeAnnotations(strings map[string]string, opaque map[string]json.RawMessage) map[string]json.RawMessage {
	if len(strings)+len(opaque) == 0 {
		return nil
	}
	out := map[string]json.RawMessage{}
	for k, v := range opaque {
		out[k] = v
	}
	for k, v := range strings {
		out[k], _ = json.Marshal(v)
	}
	return out
}
func (f *Field) UnmarshalJSON(raw []byte) error {
	type plain Field
	var wire struct {
		*plain
		Annotations json.RawMessage `json:"annotations"`
	}
	wire.plain = (*plain)(f)
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	var err error
	f.Annotations, f.OpaqueAnnotations, err = readAnnotations(wire.Annotations)
	return err
}
func (f Field) MarshalJSON() ([]byte, error) {
	type plain Field
	return json.Marshal(struct {
		plain
		Annotations map[string]json.RawMessage `json:"annotations,omitempty"`
	}{plain(f), writeAnnotations(f.Annotations, f.OpaqueAnnotations)})
}
func (m *Meta) UnmarshalJSON(raw []byte) error {
	type plain Meta
	var wire struct {
		*plain
		Annotations json.RawMessage `json:"annotations"`
	}
	wire.plain = (*plain)(m)
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	var err error
	m.Annotations, m.OpaqueAnnotations, err = readAnnotations(wire.Annotations)
	return err
}
func (m Meta) MarshalJSON() ([]byte, error) {
	type plain Meta
	return json.Marshal(struct {
		plain
		Annotations map[string]json.RawMessage `json:"annotations,omitempty"`
	}{plain(m), writeAnnotations(m.Annotations, m.OpaqueAnnotations)})
}
