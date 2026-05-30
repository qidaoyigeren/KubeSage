package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
)

// JSONText stores JSON in text columns and returns it as structured API JSON.
type JSONText string

// MarshalJSON emits valid stored JSON as structured JSON instead of a quoted
// string, while still falling back safely for malformed legacy values.
func (j JSONText) MarshalJSON() ([]byte, error) {
	text := strings.TrimSpace(string(j))
	if text == "" {
		return []byte("null"), nil
	}
	if json.Valid([]byte(text)) {
		return []byte(text), nil
	}
	return json.Marshal(string(j))
}

// Value stores JSONText as a plain database string.
func (j JSONText) Value() (driver.Value, error) {
	return string(j), nil
}

// Scan loads JSONText from common database string/byte representations.
func (j *JSONText) Scan(value interface{}) error {
	switch v := value.(type) {
	case nil:
		*j = ""
	case string:
		*j = JSONText(v)
	case []byte:
		*j = JSONText(string(v))
	default:
		return fmt.Errorf("unsupported JSONText value %T", value)
	}
	return nil
}
