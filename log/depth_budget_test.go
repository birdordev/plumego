package log

import (
	"bytes"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestTextWrappedUnexportedValueUsesPlaceholder(t *testing.T) {
	data := struct{ items []int }{items: make([]int, 2048)}
	var out bytes.Buffer
	NewLogger(LoggerConfig{Format: LoggerFormatText, Output: &out}).Info("wrapped", Fields{
		"private": reflect.ValueOf(data).Field(0),
		"public":  reflect.ValueOf(42),
		"invalid": reflect.Value{},
		"other":   "kept",
	})
	line := out.String()
	for _, field := range []string{"private=[unexported]", "public=42", "invalid=\"<invalid reflect.Value>\"", "other=kept"} {
		if !strings.Contains(line, field) {
			t.Errorf("missing %q in text log", field)
		}
	}
	if strings.Contains(line, "[0 0") {
		t.Error("wrapped unexported slice was traversed")
	}
}

func TestWideTextMapBudgetKeepsCompleteEntries(t *testing.T) {
	wide := make(map[string]string, 1024)
	for i := range 1024 {
		wide[strconv.Itoa(i)] = "kept-value"
	}
	var out bytes.Buffer
	NewLogger(LoggerConfig{Format: LoggerFormatText, Output: &out}).Info(
		"bounded", Fields{"value": wide, "small": map[string]int{"b": 2, "a": 1}},
	)
	line := out.String()
	if count := strings.Count(line, ":kept-value"); count != 511 || !strings.Contains(line, " [truncated]]") {
		t.Fatalf("complete entries=%d, truncated=%t", count, strings.Contains(line, " [truncated]]"))
	}
	if !strings.Contains(line, "map[a:1 b:2]") {
		t.Fatal("expected the small map to be sorted")
	}
}

func TestTextStructBudgetCountsUnexportedFields(t *testing.T) {
	fields := make([]reflect.StructField, 2048)
	fields[0] = reflect.StructField{Name: "Visible", Type: reflect.TypeFor[string]()}
	for i := 1; i < len(fields); i++ {
		fields[i] = reflect.StructField{
			Name:    "field" + strconv.Itoa(i),
			PkgPath: "github.com/birdordev/plumego/log",
			Type:    reflect.TypeFor[struct{}](),
		}
	}
	value := reflect.New(reflect.StructOf(fields)).Elem()
	value.Field(0).SetString("visible")
	var out bytes.Buffer
	NewLogger(LoggerConfig{Format: LoggerFormatText, Output: &out}).Info(
		"bounded", Fields{"value": value.Interface(), "sibling": "kept"},
	)

	line := out.String()
	if count := strings.Count(line, "[unexported]"); count != 1022 || !strings.Contains(line, "[truncated]") {
		t.Fatalf("unexported placeholders=%d, truncated=%t", count, strings.Contains(line, "[truncated]"))
	}
	if !strings.Contains(line, "visible") || !strings.Contains(line, "sibling=kept") {
		t.Fatal("expected visible field and sibling to be preserved")
	}
}

func TestTextDepthBudget(t *testing.T) {
	wide := make(map[string]any, 100000)
	for i := 0; i < 100000; i++ {
		wide[strconv.Itoa(i)] = "value"
	}
	var value any = &wide
	for i := 0; i < 15; i++ {
		value = []any{value}
	}
	logger := NewLogger(LoggerConfig{Format: LoggerFormatText, Output: io.Discard})
	allocations := testing.AllocsPerRun(1, func() { logger.Info("bounded", Fields{"value": value}) })
	if allocations > 20000 {
		t.Fatalf("depth-boundary traversal exceeded node allocation budget: %.0f", allocations)
	}
}
