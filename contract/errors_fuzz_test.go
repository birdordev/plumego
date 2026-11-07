package contract

import (
	"encoding/json"
	"reflect"
	"testing"
)

// FuzzAPIErrorDetailsCloneIsIsolated verifies that Details() returns a
// deep copy: repeated calls are stable, and mutating one returned map never
// leaks into the stored error details.
func FuzzAPIErrorDetailsCloneIsIsolated(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"key":"value"}`,
		`{"nested":{"a":[1,2,3],"b":{"c":"d"}}}`,
		`{"items":[{"x":1},["y",2.5]],"empty":null}`,
		`{"unix":123,"pi":3.14159,"yes":true}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		var details map[string]any
		if err := json.Unmarshal([]byte(raw), &details); err != nil {
			return // malformed JSON is not a detail map
		}

		err := NewErrorBuilder().Details(details).Build()
		first := err.Details()
		second := err.Details()
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("Details() not stable: first=%#v second=%#v", first, second)
		}

		// Mutating a returned copy must not leak into the stored details.
		mutateFirstDetailValue(first)
		after := err.Details()
		if !reflect.DeepEqual(after, second) {
			t.Fatalf("mutation leaked into stored details: after=%#v want=%#v", after, second)
		}
	})
}

// mutateFirstDetailValue overwrites the first leaf value it encounters,
// depth-first, to prove the returned map owns its nested data.
func mutateFirstDetailValue(m map[string]any) {
	for k, v := range m {
		switch val := v.(type) {
		case map[string]any:
			mutateFirstDetailValue(val)
		case []any:
			if len(val) > 0 {
				val[0] = "MUTATED"
			}
		default:
			m[k] = "MUTATED"
		}
		return // mutate only the first key
	}
}
