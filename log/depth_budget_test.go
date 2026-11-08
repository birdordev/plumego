package log

import (
	"io"
	"strconv"
	"testing"
)

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
