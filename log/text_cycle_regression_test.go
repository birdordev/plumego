package log

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestTextCyclicFieldsAreBounded(t *testing.T) {
	cyclic := map[string]any{}
	cyclic["left"] = cyclic
	cyclic["right"] = cyclic
	items := make([]any, 1)
	items[0] = items
	for _, value := range []any{cyclic, items, Fields{"loop": cyclic}} {
		var output bytes.Buffer
		NewLogger(LoggerConfig{Output: &output}).Info("event", Fields{"cyclic": value, "safe": "kept"})
		if !strings.Contains(output.String(), "[truncated]") || !strings.Contains(output.String(), "safe=kept") {
			t.Fatalf("unexpected output: %s", output.String())
		}
		if output.Len() > 100000 {
			t.Fatalf("unbounded output: %d", output.Len())
		}
	}
}
func TestTextScalarFormattingIsPreserved(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  string
	}{{42, "42"}, {"plain", "plain"}, {"two words", `"two words"`}, {true, "true"}, {nil, "<nil>"}} {
		if got := formatTextFieldValue(tc.value); got != tc.want {
			t.Errorf("%v: got %q want %q", tc.value, got, tc.want)
		}
	}
}

func TestTextContainerPointersAreBounded(t *testing.T) {
	type node struct {
		Next *node
		Data map[string]any
	}
	root := &node{}
	root.Next = root
	root.Data = map[string]any{"back": root}
	if got := formatTextFieldValue(root); !strings.Contains(got, "[truncated]") {
		t.Fatalf("pointer cycle not bounded: %s", got)
	}
}

func TestTextTimeFormattingPreserved(t *testing.T) {
	value := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got, want := formatTextFieldValue(value), `"2026-01-02 03:04:05 +0000 UTC"`; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestTextWriterChangesKeepLevelDestinationsIndependent(t *testing.T) {
	logger := newGLogger()
	var info1, info2, error1, error2 bytes.Buffer
	logger.SetOutput(&info1)
	logger.SetErrorOutput(&error1)
	logger.Info("initial info")
	logger.Error("initial error")
	logger.SetOutput(&info2)
	logger.Info("switched info")
	logger.Error("retained error")
	logger.SetErrorOutput(&error2)
	logger.Info("retained info")
	logger.Error("switched error")
	for _, row := range []struct {
		output   *bytes.Buffer
		messages []string
	}{
		{&info1, []string{"initial info"}},
		{&info2, []string{"switched info", "retained info"}},
		{&error1, []string{"initial error", "retained error"}},
		{&error2, []string{"switched error"}},
	} {
		if strings.Count(row.output.String(), "\n") != len(row.messages) {
			t.Fatalf("unexpected output: %q", row.output.String())
		}
		for _, message := range row.messages {
			if !strings.Contains(row.output.String(), message) {
				t.Fatalf("missing %q in %q", message, row.output.String())
			}
		}
	}
}
