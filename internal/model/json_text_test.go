package model

import (
	"encoding/json"
	"testing"
)

// TestJSONTextMarshalJSON verifies stored JSON text is returned as structured
// JSON in API responses.
func TestJSONTextMarshalJSON(t *testing.T) {
	payload := struct {
		Actions JSONText `json:"actions"`
	}{Actions: JSONText(`[{"action_type":"view_previous_logs"}]`)}

	bytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"actions":[{"action_type":"view_previous_logs"}]}`
	if string(bytes) != want {
		t.Fatalf("unexpected JSONText marshal result: got %s want %s", string(bytes), want)
	}
}
