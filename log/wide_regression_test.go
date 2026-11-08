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
