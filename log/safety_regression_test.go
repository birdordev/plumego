package log

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type panickingLogStringer struct{}

func (panickingLogStringer) String() string { panic("private String diagnostic") }

type panickingLogError struct{}

func (panickingLogError) Error() string { panic("private Error diagnostic") }

type panickingLogFormatter struct{}

func (panickingLogFormatter) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("partial formatter output"))
	panic("private Format diagnostic")
}

type defaultVerbFormatter struct{}

func (defaultVerbFormatter) Format(state fmt.State, verb rune) {
	_, hasWidth := state.Width()
	_, hasPrecision := state.Precision()
	if verb == 'v' && !hasWidth && !hasPrecision && !state.Flag('#') {
		_, _ = state.Write([]byte("formatted"))
	}
}

type logErrorWithString struct{}

func (logErrorWithString) Error() string  { return "error-text" }
func (logErrorWithString) String() string { return "string-text" }

func TestTextLoggerContainsFormatPanics(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggerConfig{Format: LoggerFormatText, Output: &output})
	var absent *panickingLogStringer
	logger.Info("event", Fields{
		"stringer": panickingLogStringer{},
		"error":    panickingLogError{},
		"format":   panickingLogFormatter{},
		"valid":    defaultVerbFormatter{},
		"both":     logErrorWithString{},
		"nil":      absent,
		"sibling":  "kept",
		"wrapped":  reflect.ValueOf(42),
		"bad_wrap": reflect.ValueOf(panickingLogStringer{}),
	})
	line := output.String()
	for _, expected := range []string{
		"stringer=[unsupported]", "error=[unsupported]", "format=[unsupported]",
		"valid=formatted", "both=error-text", "nil=<nil>", "sibling=kept",
		"wrapped=42", "bad_wrap=[unsupported]",
	} {
		if !strings.Contains(line, expected) {
			t.Errorf("missing %q in %q", expected, line)
		}
	}
	if strings.Contains(line, "private") || strings.Contains(line, "partial formatter output") {
		t.Fatalf("unexpected raw diagnostic or partial output: %q", line)
	}
}

func TestJSONPreservesLargeIntegerFields(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggerConfig{Format: LoggerFormatJSON, Output: &output})
	logger.Info("numeric", Fields{"id": uint64(18446744073709551615), "nested": Fields{"id": int64(9007199254740993)}})
	var entry struct {
		ID     uint64 `json:"id"`
		Nested struct {
			ID int64 `json:"id"`
		} `json:"nested"`
	}
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.ID != 18446744073709551615 || entry.Nested.ID != 9007199254740993 {
		t.Fatalf("integer precision lost: %s", output.String())
	}
}

func TestJSONUnsupportedValueDoesNotExposeHiddenFields(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggerConfig{Format: LoggerFormatJSON, Output: &output})
	payload := struct {
		Hidden      string   `json:"-"`
		Unsupported chan int `json:"unsupported"`
	}{Hidden: "private-canary-do-not-log", Unsupported: make(chan int)}
	logger.Info("safe", Fields{"payload": payload, "sibling": "retained"})
	if strings.Contains(output.String(), payload.Hidden) {
		t.Fatal("fallback exposed a hidden field")
	}
	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["sibling"] != "retained" || entry["msg"] != "safe" {
		t.Fatal("unsupported field discarded safe siblings")
	}
}

type echoingFailureWriter struct{}

func (echoingFailureWriter) Write(data []byte) (int, error) {
	return 0, fmt.Errorf("writer echoed sensitive payload: %s", data)
}

func TestWriterErrorsDoNotLeakPayload(t *testing.T) {
	const canary = "private-writer-canary"
	if os.Getenv("PLUMEGO_LOG_ERROR_CHILD") == "1" {
		for _, format := range []LoggerFormat{LoggerFormatText, LoggerFormatJSON} {
			logger := NewLogger(LoggerConfig{Format: format, Output: echoingFailureWriter{}})
			logger.Info("entry", Fields{"value": canary})
			logger.WithFields(Fields{"child": true}).Info("entry", Fields{"value": canary})
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestWriterErrorsDoNotLeakPayload$")
	command.Env = append(os.Environ(), "PLUMEGO_LOG_ERROR_CHILD=1")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed: %v", err)
	}
	if strings.Contains(string(output), canary) {
		t.Fatal("write diagnostic exposed failed log payload")
	}
	if got := strings.Count(string(output), "failed to write log output"); got != 2 {
		t.Fatalf("diagnostic count = %d, want 2", got)
	}
}

func TestJSONCyclicFieldsAreBounded(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggerConfig{Format: LoggerFormatJSON, Output: &output})
	cycle := Fields{"safe": "retained"}
	cycle["self"] = cycle
	cycle["also"] = cycle
	slice := make([]any, 1)
	slice[0] = slice
	logger.Info("bounded", Fields{"cycle": cycle, "slice": slice})
	if !json.Valid(output.Bytes()) || !strings.Contains(output.String(), "[truncated]") {
		t.Fatal("cyclic fields must produce bounded valid JSON")
	}
	if output.Len() > 65536 || !strings.Contains(output.String(), "retained") {
		t.Fatal("cycle handling lost safe fields or exceeded expected bound")
	}
}

func TestTextFieldsEscapeControlsAndDelimiters(t *testing.T) {
	for _, value := range []string{"\x00", "\x1b[2J", "\b", "\"quoted\"", "a\\b", "\u00a0"} {
		var output bytes.Buffer
		logger := NewLogger(LoggerConfig{Format: LoggerFormatText, Output: &output})
		logger.Info("safe", Fields{"value": value})
		_, encoded, found := strings.Cut(strings.TrimSuffix(output.String(), "\n"), "value=")
		if !found {
			t.Fatal("field missing from text output")
		}
		decoded, err := strconv.Unquote(encoded)
		if err != nil || decoded != value {
			t.Fatalf("ambiguous text field: encoded=%q, error=%v", encoded, err)
		}
		if strings.ContainsAny(output.String(), "\x00\x1b\b") {
			t.Fatal("text output contains raw control characters")
		}
	}
}

func TestTextMessageCannotForgeEntries(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggerConfig{Format: LoggerFormatText, Output: &output})
	logger.Info("request\r\nFORGED\x1b[2J\u2028next\u2029end", Fields{"path": "/ok"})
	got := output.String()
	if strings.Count(got, "\n") != 1 || strings.ContainsAny(got, "\r\x1b\u2028\u2029") {
		t.Fatalf("log contains unescaped control characters: %q", got)
	}
	if !strings.Contains(got, `request\r\nFORGED\x1b[2J`) || !strings.Contains(got, "path=/ok") {
		t.Fatalf("log lost message or field data: %q", got)
	}
}

func TestTextMessageEncoding(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		want    string
	}{
		{"plain", "ready 世界\ufffd", "ready 世界\ufffd\n"},
		{"malformed UTF-8", "left\xff\xf0\x9f世界\ufffd", "left\ufffd\ufffd\ufffd世界\ufffd\n"},
		{"separators and trailing newline", "left\u0085\u2028\u2029right\n\n", "left\\u0085\\u2028\\u2029right\\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := NewLogger(LoggerConfig{Format: LoggerFormatText, Output: &output})
			logger.Info(tc.message)
			if got := output.String(); !strings.HasSuffix(got, " "+tc.want) {
				t.Fatalf("log = %q, want message suffix %q", got, tc.want)
			}
		})
	}
}

func TestEscapeLogMessagePlainTextAllocations(t *testing.T) {
	message := "service ready 世界\ufffd"
	var escaped string
	allocations := testing.AllocsPerRun(100, func() {
		escaped = escapeLogMessage(message)
	})
	if escaped != message || allocations != 0 {
		t.Fatalf("escapeLogMessage = %q with %.1f allocations, want %q with no allocations", escaped, allocations, message)
	}
}

type countingJSONField struct{ calls *int }

func (f countingJSONField) MarshalJSON() ([]byte, error) {
	(*f.calls)++
	return []byte(`"encoded"`), nil
}

func TestJSONReservedFieldsSkipCustomEncoding(t *testing.T) {
	var out bytes.Buffer
	var reserved, ordinary int
	NewLogger(LoggerConfig{Format: LoggerFormatJSON, Output: &out}).Info("canonical", Fields{
		"time": countingJSONField{&reserved}, "level": countingJSONField{&reserved},
		"msg": countingJSONField{&reserved}, "ordinary": countingJSONField{&ordinary},
	})
	if reserved != 0 || ordinary != 1 {
		t.Fatalf("marshal calls: reserved = %d, ordinary = %d", reserved, ordinary)
	}
	var entry map[string]string
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["msg"] != "canonical" || entry["level"] != "INFO" || entry["time"] == "" || entry["ordinary"] != "encoded" {
		t.Errorf("entry = %v", entry)
	}
}

func TestJSONPreservesNilAndEmptyContainerShapes(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggerConfig{Format: LoggerFormatJSON, Output: &output})
	type namedMap map[string]int
	type namedSlice []string
	logger.Info("containers", Fields{
		"nil_fields":       Fields(nil),
		"nil_map":          map[string]any(nil),
		"nil_string_map":   map[string]string(nil),
		"nil_list":         []any(nil),
		"nil_named_map":    namedMap(nil),
		"nil_named_list":   namedSlice(nil),
		"empty_fields":     Fields{},
		"empty_map":        map[string]any{},
		"empty_string_map": map[string]string{},
		"empty_list":       []any{},
		"empty_named_map":  namedMap{},
		"empty_named_list": namedSlice{},
	})
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"nil_fields", "nil_map", "nil_string_map", "nil_list", "nil_named_map", "nil_named_list"} {
		if got := string(fields[key]); got != "null" {
			t.Errorf("%s = %s, want null", key, got)
		}
	}
	for _, key := range []string{"empty_fields", "empty_map", "empty_string_map", "empty_named_map"} {
		if got := string(fields[key]); got != "{}" {
			t.Errorf("%s = %s, want {}", key, got)
		}
	}
	for _, key := range []string{"empty_list", "empty_named_list"} {
		if got := string(fields[key]); got != "[]" {
			t.Errorf("%s = %s, want []", key, got)
		}
	}
}

type countedLogField struct{ calls *int }

func (f countedLogField) String() string {
	(*f.calls)++
	return "observed"
}

func TestTextLoggerSkipsFormattingFilteredFields(t *testing.T) {
	var output bytes.Buffer
	calls := 0
	logger := NewLogger(LoggerConfig{Format: LoggerFormatText, Level: ERROR, Output: &output}).
		WithFields(Fields{"value": "base"})
	logger.Info("filtered", Fields{"value": countedLogField{&calls}})
	if calls != 0 || output.Len() != 0 {
		t.Fatalf("filtered calls = %d, output = %q", calls, output.String())
	}
	logger.Error("written", Fields{"value": countedLogField{&calls}})
	if got := output.String(); calls != 1 || !strings.Contains(got, "safety_regression_test.go:") ||
		!strings.Contains(got, "written value=observed") {
		t.Fatalf("written calls = %d, output = %q", calls, got)
	}
}

func TestTextErrorFieldEscapesControlsAndKeepsSiblingInteger(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggerConfig{Format: LoggerFormatText, Output: &output})
	logger.Info("fixture", Fields{
		"cause": fmt.Errorf("left\r\nright\xff"),
		"count": int64(9007199254740993),
	})
	got := output.String()
	if strings.Count(got, "\n") != 1 || strings.Contains(got, "\r") || strings.Contains(got, "\xff") ||
		!strings.Contains(got, "count=9007199254740993") {
		t.Fatalf("invalid record=%q", got)
	}
	fields := strings.Fields(got)
	for _, field := range fields {
		if encoded, ok := strings.CutPrefix(field, "cause="); ok {
			value, err := strconv.Unquote(encoded)
			if value != "left\r\nright\xff" || err != nil {
				t.Fatalf("error field=%q err=%v", value, err)
			}
			return
		}
	}
	t.Fatalf("missing error field in %q", got)
}
