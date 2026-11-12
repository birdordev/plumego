package log

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestWideJSONBudget(t *testing.T) {
	wide := make([]any, 100000)
	mapping := make(map[string]any, 100000)
	for i := range wide {
		mapping[string(rune(i))] = i
	}
	var nested any = wide
	for range maxJSONFieldDepth - 1 {
		nested = []any{nested}
	}
	strings := make(map[string]string, len(mapping))
	for key := range mapping {
		strings[key] = "value"
	}
	for name, value := range map[string]any{"depth-boundary": nested, "string-map": strings, "slice": wide, "map": mapping, "mixed": []any{mapping, wide}} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			NewLogger(LoggerConfig{Format: LoggerFormatJSON, Output: &out}).Info("event", Fields{"value": value, "safe": "kept"})
			if !json.Valid(out.Bytes()) {
				t.Fatal("invalid JSON")
			}
			if out.Len() > 100000 {
				t.Fatalf("unbounded container: %d bytes", out.Len())
			}
		})
	}
}

type customJSONList []int

func (customJSONList) MarshalJSON() ([]byte, error) {
	return []byte(`"custom"`), nil
}

type customJSONItem int

func (*customJSONItem) MarshalJSON() ([]byte, error) {
	return []byte(`"custom item"`), nil
}

func TestJSONBudgetForTypedContainers(t *testing.T) {
	type count int64
	type name string
	const precise count = 1<<53 + 1
	wide := make([]count, 2*maxJSONFieldNodes)
	wide[0] = precise
	mapping := make(map[name][]count, 1)
	mapping["wide"] = wide

	var out bytes.Buffer
	NewLogger(LoggerConfig{Format: LoggerFormatJSON, Output: &out}).Info("event", Fields{
		"slice":  wide,
		"array":  [2 * maxJSONFieldNodes]int{},
		"map":    mapping,
		"bytes":  []byte("ok"),
		"custom": customJSONList{1, 2},
		"items":  []customJSONItem{1},
		"nil":    []int(nil),
	})
	var record struct {
		Slice  []json.RawMessage            `json:"slice"`
		Array  []json.RawMessage            `json:"array"`
		Map    map[string][]json.RawMessage `json:"map"`
		Bytes  string                       `json:"bytes"`
		Custom string                       `json:"custom"`
		Items  []string                     `json:"items"`
		Nil    json.RawMessage              `json:"nil"`
	}
	if err := json.Unmarshal(out.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	for field, values := range map[string][]json.RawMessage{"slice": record.Slice, "array": record.Array, "map": record.Map["wide"]} {
		if len(values) == 0 || len(values) > maxJSONFieldNodes || string(values[len(values)-1]) != `"[truncated]"` {
			t.Errorf("%s: expected bounded output with a truncation marker, got %d items", field, len(values))
		}
	}
	if len(record.Slice) == 0 || string(record.Slice[0]) != "9007199254740993" {
		t.Error("typed integer lost precision")
	}
	if record.Bytes != "b2s=" || record.Custom != "custom" || string(record.Nil) != "null" {
		t.Fatalf("bytes = %q, custom = %q, nil = %s", record.Bytes, record.Custom, record.Nil)
	}
	if len(record.Items) != 1 || record.Items[0] != "custom item" {
		t.Fatalf("items = %q, want custom element encoding", record.Items)
	}
}
