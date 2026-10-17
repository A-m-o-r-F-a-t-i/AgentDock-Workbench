package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// DecodeJSON reads one JSON value and preserves numbers in dynamic fields.
// Typed numeric fields keep their declared type and normal decoding checks.
// Custom UnmarshalJSON methods remain responsible for their own number handling.
func DecodeJSON(reader io.Reader, output any) error {
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("expected exactly one JSON value")
	}
	return nil
}

// Remarshal 通过 JSON 契约在动态边界值和明确结构之间转换。
func Remarshal(input, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	return DecodeJSON(bytes.NewReader(data), output)
}

// JSONTextEquivalent removes only a complete, provably identical JSON value.
// Keeping numeric tokens exact avoids treating adjacent large integers as equal.
func JSONTextEquivalent(text string, structured any) bool {
	var parsed any
	if err := DecodeJSON(strings.NewReader(text), &parsed); err != nil {
		return false
	}
	left, err := json.Marshal(parsed)
	if err != nil {
		return false
	}
	right, err := json.Marshal(structured)
	return err == nil && bytes.Equal(left, right)
}
