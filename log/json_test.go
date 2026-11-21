package log

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type customJSONSlice []int

func (*customJSONSlice) MarshalJSON() ([]byte, error) { return []byte(`"custom"`), nil }

func TestJSONLoggerSkipsInvalidUTF8FieldNames(t *testing.T) {
	const invalid, valid = "field\xff", "field\uFFFD"
	type namedKey string
	type namedMap map[namedKey]string
	var out bytes.Buffer
	logger := NewLogger(LoggerConfig{Format: LoggerFormatJSON, Output: &out})
	logger.Info("sample", Fields{
		invalid:  "discarded",
		valid:    "retained",
		"any":    map[string]any{invalid: "discarded", valid: "retained", "ordinary": true},
		"string": map[string]string{invalid: "discarded", valid: "retained", "ordinary": "yes"},
		"named":  namedMap{invalid: "discarded", valid: "retained", "ordinary": "yes"},
	})

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{
		"entry": out.Bytes(), "any": fields["any"], "string": fields["string"], "named": fields["named"],
	} {
		t.Run(name, func(t *testing.T) {
			decoder := json.NewDecoder(bytes.NewReader(raw))
			if token, err := decoder.Token(); token != json.Delim('{') || err != nil {
				t.Fatalf("expected JSON object, got %v, %v", token, err)
			}
			count := 0
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					t.Fatal(err)
				}
				var value json.RawMessage
				if err := decoder.Decode(&value); err != nil {
					t.Fatal(err)
				}
				if key == valid {
					count++
					if string(value) != `"retained"` {
						t.Errorf("field value=%s, want retained", value)
					}
				}
			}
			if count != 1 || (name != "entry" && !bytes.Contains(raw, []byte(`"ordinary":`))) {
				t.Errorf("valid field occurrences=%d, object=%s", count, raw)
			}
		})
	}
}

func TestJSONInvalidUTF8FieldNamesConsumeNestedBudget(t *testing.T) {
	type namedMap map[string]string
	anyFields := make(map[string]any)
	stringFields := make(map[string]string)
	for i := range maxJSONFieldNodes * 2 {
		key := "\xff" + strconv.Itoa(i)
		anyFields[key] = true
		stringFields[key] = "value"
	}
	for _, input := range []any{anyFields, stringFields, namedMap(stringFields)} {
		normalized, ok := normalizeJSONFieldValue(input).(map[string]any)
		if !ok || len(normalized) != 1 || normalized["[truncated]"] != "[truncated]" {
			t.Errorf("%T did not stop after exhausting the field budget", input)
		}
	}
}

func TestJSONBudgetAppliesToContainerPointers(t *testing.T) {
	seq := make([]int, 2*maxJSONFieldNodes)
	seq[0] = 17
	lookup := map[string][]int{"items": seq}
	custom := customJSONSlice(seq)
	var nilSlice *[]int
	data := []byte("abc")
	var indirect, customIndirect, nilIndirect, bytesIndirect, largeNumber any = seq, &custom, nil, &data, uint64(1<<63) + 1
	var output bytes.Buffer
	logger := NewLogger(LoggerConfig{Format: LoggerFormatJSON, Output: &output})
	logger.Info("sample", Fields{
		"direct": seq, "slice": &seq, "map": &lookup, "indirect": &indirect,
		"custom": &custom, "custom_indirect": &customIndirect, "nil": nilSlice, "nil_indirect": &nilIndirect,
		"bytes": &data, "bytes_indirect": &bytesIndirect, "number": &largeNumber,
	})
	var entry struct {
		Slice          []any            `json:"slice"`
		Direct         []any            `json:"direct"`
		Indirect       []any            `json:"indirect"`
		Map            map[string][]any `json:"map"`
		Custom         string           `json:"custom"`
		Nil            json.RawMessage  `json:"nil"`
		Bytes          string           `json:"bytes"`
		CustomIndirect string           `json:"custom_indirect"`
		NilIndirect    json.RawMessage  `json:"nil_indirect"`
		BytesIndirect  string           `json:"bytes_indirect"`
		Number         uint64           `json:"number"`
	}
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	for _, got := range [][]any{entry.Direct, entry.Slice, entry.Indirect, entry.Map["items"]} {
		if len(got) == 0 || len(got) > maxJSONFieldNodes || got[0] != float64(17) || got[len(got)-1] != "[truncated]" {
			t.Errorf("expected a bounded field with a truncation marker, got %d items", len(got))
		}
	}
	if entry.Custom != "custom" || string(entry.Nil) != "null" || entry.Bytes != "YWJj" {
		t.Fatalf("custom=%q, nil=%s, bytes=%q", entry.Custom, entry.Nil, entry.Bytes)
	}
	if entry.CustomIndirect != entry.Custom || string(entry.NilIndirect) != string(entry.Nil) || entry.BytesIndirect != entry.Bytes || entry.Number != uint64(1<<63)+1 {
		t.Fatalf("custom=%q, nil=%s, bytes=%q, number=%d", entry.CustomIndirect, entry.NilIndirect, entry.BytesIndirect, entry.Number)
	}
}

type interleavedJSONSink struct {
	mu          sync.Mutex
	output      bytes.Buffer
	firstWrite  chan struct{}
	resumeFirst chan struct{}
}

func (w *interleavedJSONSink) Write(p []byte) (int, error) {
	w.mu.Lock()
	first := w.output.Len() == 0
	n, err := w.output.Write(p)
	w.mu.Unlock()
	if first {
		close(w.firstWrite)
		<-w.resumeFirst
	}
	return n, err
}

func TestIndependentJSONLoggersWriteWholeRecords(t *testing.T) {
	sink := &interleavedJSONSink{firstWrite: make(chan struct{}), resumeFirst: make(chan struct{})}
	first := newTestJSONLogger(t, LoggerConfig{Output: sink})
	second := newTestJSONLogger(t, LoggerConfig{Output: sink})
	done := make(chan struct{})
	go func() {
		first.Info("first")
		close(done)
	}()
	<-sink.firstWrite
	second.Info("second")
	close(sink.resumeFirst)
	<-done

	lines := strings.Split(strings.TrimSuffix(sink.output.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want two", len(lines))
	}
	for i, want := range []string{"first", "second"} {
		var entry testJSONLogEntry
		if err := json.Unmarshal([]byte(lines[i]), &entry); err != nil || entry.Msg != want {
			t.Fatalf("line %d=%q, err=%v, want message %q", i, lines[i], err, want)
		}
	}
}

func newTestJSONLogger(t *testing.T, cfg LoggerConfig) *jsonLogger {
	t.Helper()
	cfg.Format = LoggerFormatJSON
	raw := NewLogger(cfg)
	logger, ok := raw.(*jsonLogger)
	if !ok {
		t.Fatalf("expected *jsonLogger, got %T", raw)
	}
	return logger
}

type testJSONLogEntry struct {
	Time      string `json:"time"`
	Level     string `json:"level"`
	Msg       string `json:"msg"`
	Key       string `json:"key"`
	App       string `json:"app"`
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	Env       string `json:"env"`
	Version   string `json:"version"`
	Request   string `json:"request"`
	Scope     string `json:"scope"`
}

func decodeTestJSONLogEntry(t *testing.T, data []byte) testJSONLogEntry {
	t.Helper()

	var entry testJSONLogEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}
	return entry
}

func TestNewLoggerJSONFormat(t *testing.T) {
	t.Run("with defaults", func(t *testing.T) {
		logger := newTestJSONLogger(t, LoggerConfig{})
		if logger == nil {
			t.Error("expected logger to be created")
		}
	})

	t.Run("with custom config", func(t *testing.T) {
		var buf bytes.Buffer
		logger := newTestJSONLogger(t, LoggerConfig{
			Output: &buf,
			Level:  WARNING,
			Fields: Fields{"app": "test"},
		})

		if logger == nil {
			t.Error("expected logger to be created")
		}
	})
}

func TestJSONFormatLoggerInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestJSONLogger(t, LoggerConfig{
		Output:           &buf,
		Level:            INFO,
		RespectVerbosity: true,
	})

	logger.Info("test message", Fields{"key": "value"})

	entry := decodeTestJSONLogEntry(t, buf.Bytes())

	if entry.Level != "INFO" {
		t.Errorf("expected level INFO, got %v", entry.Level)
	}

	if entry.Msg != "test message" {
		t.Errorf("expected msg 'test message', got %v", entry.Msg)
	}

	if entry.Key != "value" {
		t.Errorf("expected key 'value', got %v", entry.Key)
	}

	if entry.Time == "" {
		t.Error("expected time field to be present")
	}
}

func TestJSONFormatLoggerLevels(t *testing.T) {
	tests := []struct {
		name     string
		logLevel Level
		logFunc  func(logger *jsonLogger)
		want     string
	}{
		{
			name:     "info level",
			logLevel: INFO,
			logFunc: func(l *jsonLogger) {
				l.Info("info msg", nil)
			},
			want: "INFO",
		},
		{
			name:     "warning level",
			logLevel: INFO,
			logFunc: func(l *jsonLogger) {
				l.Warn("warn msg", nil)
			},
			want: "WARN",
		},
		{
			name:     "error level",
			logLevel: INFO,
			logFunc: func(l *jsonLogger) {
				l.Error("error msg", nil)
			},
			want: "ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := newTestJSONLogger(t, LoggerConfig{
				Output: &buf,
				Level:  tt.logLevel,
			})

			tt.logFunc(logger)

			entry := decodeTestJSONLogEntry(t, buf.Bytes())

			if entry.Level != tt.want {
				t.Errorf("expected level %s, got %v", tt.want, entry.Level)
			}
		})
	}
}

func TestJSONFormatLoggerWithFields(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestJSONLogger(t, LoggerConfig{
		Output: &buf,
		Level:  INFO,
		Fields: Fields{"app": "myapp"},
	})

	childLogger := logger.WithFields(Fields{"request_id": "123"})
	childLogger.Info("test", Fields{"status": "ok"})

	entry := decodeTestJSONLogEntry(t, buf.Bytes())

	if entry.App != "myapp" {
		t.Errorf("expected app 'myapp', got %v", entry.App)
	}
	if entry.RequestID != "123" {
		t.Errorf("expected request_id '123', got %v", entry.RequestID)
	}
	if entry.Status != "ok" {
		t.Errorf("expected status 'ok', got %v", entry.Status)
	}
}

func TestJSONFormatLoggerMergesVariadicFieldsInOrder(t *testing.T) {
	var buf bytes.Buffer
	var discarded int
	logger := newTestJSONLogger(t, LoggerConfig{
		Output: &buf,
		Level:  INFO,
		Fields: Fields{"base": "yes", "override": "base"},
	})

	first := Fields{"first": "one", "override": countingJSONField{&discarded}, "field\xff": countingJSONField{&discarded}}
	last := Fields{"second": float64(2), "override": "second", "level": countingJSONField{&discarded}}
	wantFirst, wantLast := maps.Clone(first), maps.Clone(last)
	logger.Info("multi", first, last)
	if discarded != 0 {
		t.Fatalf("discarded fields invoked encoders %d times", discarded)
	}
	if !maps.Equal(first, wantFirst) || !maps.Equal(last, wantLast) {
		t.Fatalf("logger changed caller fields: first=%v last=%v", first, last)
	}

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}

	for key, want := range map[string]any{
		"base":     "yes",
		"first":    "one",
		"second":   float64(2),
		"override": "second",
	} {
		if got := entry[key]; got != want {
			t.Fatalf("expected %s=%v, got %v in %#v", key, want, got, entry)
		}
	}

	buf.Reset()
	logger.Info("base only")
	var baseEntry struct {
		Override string `json:"override"`
	}
	if err := json.Unmarshal(buf.Bytes(), &baseEntry); err != nil {
		t.Fatalf("failed to parse base entry: %v", err)
	}
	if baseEntry.Override != "base" {
		t.Fatalf("base override = %q, want base", baseEntry.Override)
	}
}

func TestJSONFormatLoggerInfoCtxDoesNotAutoAttachTransportFields(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestJSONLogger(t, LoggerConfig{
		Output:           &buf,
		Level:            INFO,
		RespectVerbosity: true,
	})

	logger.InfoCtx(t.Context(), "test message", Fields{"key": "value"})

	entry := decodeTestJSONLogEntry(t, buf.Bytes())

	if entry.RequestID != "" {
		t.Fatalf("expected request_id to stay caller-owned, got %v", entry.RequestID)
	}
	if entry.Msg != "test message" {
		t.Errorf("expected msg 'test message', got %v", entry.Msg)
	}
}

func TestJSONFormatLoggerContextLevels(t *testing.T) {
	tests := []struct {
		name      string
		logFunc   func(logger *jsonLogger, ctx context.Context)
		wantLevel string
	}{
		{
			name: "debug ctx",
			logFunc: func(l *jsonLogger, ctx context.Context) {
				l.DebugCtx(ctx, "debug msg", nil)
			},
			wantLevel: "DEBUG",
		},
		{
			name: "info ctx",
			logFunc: func(l *jsonLogger, ctx context.Context) {
				l.InfoCtx(ctx, "info msg", nil)
			},
			wantLevel: "INFO",
		},
		{
			name: "warn ctx",
			logFunc: func(l *jsonLogger, ctx context.Context) {
				l.WarnCtx(ctx, "warn msg", nil)
			},
			wantLevel: "WARN",
		},
		{
			name: "error ctx",
			logFunc: func(l *jsonLogger, ctx context.Context) {
				l.ErrorCtx(ctx, "error msg", nil)
			},
			wantLevel: "ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := newTestJSONLogger(t, LoggerConfig{
				Output: &buf,
				Level:  DEBUG,
			})

			tt.logFunc(logger, t.Context())

			entry := decodeTestJSONLogEntry(t, buf.Bytes())

			if entry.Level != tt.wantLevel {
				t.Errorf("expected level %s, got %v", tt.wantLevel, entry.Level)
			}
			if entry.RequestID != "" {
				t.Fatalf("expected request_id to remain absent without explicit fields, got %v", entry.RequestID)
			}
		})
	}
}

func TestJSONFormatLoggerLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestJSONLogger(t, LoggerConfig{
		Output: &buf,
		Level:  ERROR,
	})

	logger.Info("should not appear", nil)
	logger.Warn("should not appear", nil)
	logger.Error("should appear", nil)

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 log line, got %d", len(lines))
	}

	entry := decodeTestJSONLogEntry(t, []byte(lines[0]))

	if entry.Level != "ERROR" {
		t.Errorf("expected level ERROR, got %v", entry.Level)
	}
}

func TestJSONFormatLoggerFieldOverride(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestJSONLogger(t, LoggerConfig{
		Output: &buf,
		Level:  INFO,
		Fields: Fields{"env": "dev", "version": "1.0"},
	})

	logger.Info("test", Fields{"env": "prod", "request": "123"})

	entry := decodeTestJSONLogEntry(t, buf.Bytes())

	if entry.Env != "prod" {
		t.Errorf("expected env 'prod', got %v", entry.Env)
	}
	if entry.Version != "1.0" {
		t.Errorf("expected version '1.0', got %v", entry.Version)
	}
	if entry.Request != "123" {
		t.Errorf("expected request '123', got %v", entry.Request)
	}
}

func TestJSONFormatLoggerNilFields(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestJSONLogger(t, LoggerConfig{
		Output: &buf,
		Level:  INFO,
	})

	logger.Info("test message", nil)

	entry := decodeTestJSONLogEntry(t, buf.Bytes())

	if entry.Msg != "test message" {
		t.Errorf("expected msg 'test message', got %v", entry.Msg)
	}
}

func TestJSONFormatLoggerDebugVerbosityGate(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestJSONLogger(t, LoggerConfig{
		Output:           &buf,
		Level:            DEBUG,
		RespectVerbosity: true,
		Verbosity:        0,
	})

	logger.Debug("hidden debug", nil)
	if strings.TrimSpace(buf.String()) != "" {
		t.Fatalf("expected debug log to be filtered when verbosity is 0")
	}

	logger.verbosity = 1
	logger.Debug("visible debug", nil)

	entry := decodeTestJSONLogEntry(t, bytes.TrimSpace(buf.Bytes()))
	if entry.Msg != "visible debug" {
		t.Fatalf("expected visible debug message, got %v", entry.Msg)
	}
	if entry.Level != "DEBUG" {
		t.Fatalf("expected DEBUG level, got %v", entry.Level)
	}
}

func TestJSONFormatLoggerReservedFieldsCannotOverrideCoreKeys(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestJSONLogger(t, LoggerConfig{
		Output: &buf,
		Level:  INFO,
		Fields: Fields{
			"level": "OVERRIDE",
			"msg":   "override",
			"time":  "not-time",
		},
	})

	logger.InfoCtx(t.Context(), "actual message", Fields{
		"level":      "SHOULD_NOT_APPLY",
		"msg":        "SHOULD_NOT_APPLY",
		"time":       "SHOULD_NOT_APPLY",
		"request_id": "caller-owned",
	})

	entry := decodeTestJSONLogEntry(t, buf.Bytes())

	if entry.Level != "INFO" {
		t.Fatalf("expected reserved level to remain INFO, got %v", entry.Level)
	}
	if entry.Msg != "actual message" {
		t.Fatalf("expected reserved msg to remain actual message, got %v", entry.Msg)
	}
	if entry.Time == "SHOULD_NOT_APPLY" || entry.Time == "not-time" {
		t.Fatalf("expected reserved time to be generated by logger, got %v", entry.Time)
	}
	if entry.RequestID != "caller-owned" {
		t.Fatalf("expected caller-supplied request_id, got %v", entry.RequestID)
	}
}

type customFloat float64

func (customFloat) MarshalJSON() ([]byte, error) { return []byte(`"custom"`), nil }

func TestJSONFormatLoggerStringifiesUnsupportedFieldValues(t *testing.T) {
	type ratio32 float32
	type ratio64 float64
	var buf bytes.Buffer
	logger := newTestJSONLogger(t, LoggerConfig{
		Output: &buf,
		Level:  INFO,
	})

	logger.Info("kept", Fields{
		"safe":         "yes",
		"fn":           func() {},
		"nan":          math.NaN(),
		"named_nan":    ratio32(math.NaN()),
		"positive_inf": ratio64(math.Inf(1)),
		"negative_inf": ratio32(math.Inf(-1)),
		"finite":       ratio64(1.25),
		"custom":       customFloat(math.NaN()),
	})

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}

	if entry["msg"] != "kept" {
		t.Fatalf("expected original message to be preserved, got %#v", entry)
	}
	if entry["level"] != "INFO" {
		t.Fatalf("expected original level to be preserved, got %#v", entry)
	}
	if entry["safe"] != "yes" {
		t.Fatalf("expected safe field to be preserved, got %#v", entry)
	}
	if got, ok := entry["fn"].(string); !ok || got == "" {
		t.Fatalf("expected unsupported function field to be stringified, got %#v", entry["fn"])
	}
	if got := entry["nan"]; got != "NaN" {
		t.Fatalf("expected invalid number field to be stringified, got %#v", got)
	}
	for key, want := range map[string]any{"named_nan": "NaN", "positive_inf": "+Inf", "negative_inf": "-Inf", "finite": 1.25, "custom": "custom"} {
		if got := entry[key]; got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
	if strings.Contains(buf.String(), "failed to marshal log entry") {
		t.Fatalf("expected bad fields not to collapse entry, got %q", buf.String())
	}
}

func TestJSONFormatLoggerPreservesSafeNestedFields(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestJSONLogger(t, LoggerConfig{
		Output: &buf,
		Level:  INFO,
	})

	logger.Info("nested", Fields{
		"outer": map[string]any{
			"safe": "yes",
			"bad":  func() {},
			"list": []any{"ok", func() {}},
		},
	})

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to parse JSON output: %v", err)
	}

	outer, ok := entry["outer"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested object to survive normalization, got %#v", entry["outer"])
	}
	if outer["safe"] != "yes" {
		t.Fatalf("expected safe nested field to be preserved, got %#v", outer)
	}
	if got, ok := outer["bad"].(string); !ok || got == "" {
		t.Fatalf("expected unsupported nested value to be stringified, got %#v", outer["bad"])
	}
	list, ok := outer["list"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("expected nested list to be preserved, got %#v", outer["list"])
	}
	if list[0] != "ok" {
		t.Fatalf("expected safe nested list value to be preserved, got %#v", list)
	}
	if got, ok := list[1].(string); !ok || got == "" {
		t.Fatalf("expected unsupported nested list value to be stringified, got %#v", list[1])
	}
}

func TestJSONFormatLoggerLimitsNonFiniteFallbackToScalars(t *testing.T) {
	type report struct {
		Count int
		Ratio float64
	}
	var buf bytes.Buffer
	logger := newTestJSONLogger(t, LoggerConfig{Output: &buf, Level: INFO})
	logger.Info("test", Fields{
		"object": report{Count: 5, Ratio: math.NaN()},
		"nested": Fields{"ratio": math.Inf(-1), "count": 5},
		"safe":   true,
	})

	var entry struct {
		Object string `json:"object"`
		Nested struct {
			Ratio string `json:"ratio"`
			Count int    `json:"count"`
		} `json:"nested"`
		Safe bool `json:"safe"`
	}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Object != "[unsupported]" || entry.Nested.Ratio != "-Inf" || entry.Nested.Count != 5 || !entry.Safe {
		t.Fatalf("unexpected JSON output: %s", &buf)
	}
}

func TestJSONFormatLoggerWithFieldsSharesWriterLock(t *testing.T) {
	var buf bytes.Buffer
	base := newTestJSONLogger(t, LoggerConfig{
		Output: &buf,
		Level:  INFO,
	})
	child := base.WithFields(Fields{"scope": "child"})

	const n = 200
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < n; i++ {
			base.Info("base", Fields{"idx": i})
		}
	}()

	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < n; i++ {
			child.Info("child", Fields{"idx": i})
		}
	}()

	close(start)
	wg.Wait()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2*n {
		t.Fatalf("expected %d lines, got %d", 2*n, len(lines))
	}
	for _, line := range lines {
		entry := decodeTestJSONLogEntry(t, []byte(line))
		if entry.Level != "INFO" || (entry.Msg == "child") != (entry.Scope == "child") {
			t.Fatalf("unexpected level or scope in %q", line)
		}
	}
}

func TestJSONFormatLoggerWithFieldsSharesWriteErrorState(t *testing.T) {
	base := newTestJSONLogger(t, LoggerConfig{
		Output: io.Discard,
		Level:  INFO,
	})
	child, ok := base.WithFields(Fields{"scope": "child"}).(*jsonLogger)
	if !ok {
		t.Fatalf("expected *jsonLogger child")
	}

	if base.writeErrOnce == nil {
		t.Fatal("expected base write error state to be initialized")
	}
	if child.writeErrOnce != base.writeErrOnce {
		t.Fatal("expected derived JSON logger to share write error state")
	}
}

type panickingJSONField struct{}

func (panickingJSONField) MarshalJSON() ([]byte, error) {
	panic("private encoder panic")
}

type panickingTextField struct{}

func (panickingTextField) MarshalText() ([]byte, error) {
	panic("private encoder panic")
}

func TestJSONEncoderPanicIsIsolatedToTheField(t *testing.T) {
	var out bytes.Buffer
	writes := 0
	logger := newTestJSONLogger(t, LoggerConfig{Output: diagnosticOutputFunc(func(p []byte) (int, error) {
		writes++
		return out.Write(p)
	})})
	logger.Info("event", Fields{
		"good": "keep", "json": panickingJSONField{}, "nested": []any{panickingTextField{}, "keep"},
	})
	var entry struct {
		Good, JSON string
		Nested     []string
	}
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if writes != 1 || entry.Good != "keep" || entry.JSON != "[unsupported]" || len(entry.Nested) != 2 ||
		entry.Nested[0] != "[unsupported]" || entry.Nested[1] != "keep" || strings.Contains(out.String(), "private encoder panic") {
		t.Fatalf("unexpected log: %s", out.String())
	}
}

func BenchmarkJSONRecordFieldBudget(b *testing.B) {
	for _, count := range []int{1, 32} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			l := NewLogger(LoggerConfig{Format: LoggerFormatJSON, Output: io.Discard})
			fields := make(Fields, count)
			wide := make([]any, maxJSONFieldNodes+10)
			for i := range wide {
				wide[i] = i
			}
			for i := range count {
				fields[strconv.Itoa(i)] = wide
			}
			b.ReportAllocs()
			for b.Loop() {
				l.Info("bounded", fields)
			}
		})
	}
}

type diagnosticOutputFunc func([]byte) (int, error)

func (fn diagnosticOutputFunc) Write(p []byte) (int, error) {
	return fn(p)
}

func TestJSONWriterDiagnosticsUseSeparateOutput(t *testing.T) {
	var main, diagnostics bytes.Buffer
	sinkLogger := NewLogger(LoggerConfig{Format: LoggerFormatJSON, Output: &diagnostics})
	l := NewLogger(LoggerConfig{Format: LoggerFormatJSON, Output: diagnosticOutputFunc(func(p []byte) (int, error) {
		sinkLogger.Info("write diagnostic")
		return main.Write(p)
	})})
	l.WithFields(Fields{"scope": "child"}).Info("application")
	if got := decodeTestJSONLogEntry(t, main.Bytes()); got.Msg != "application" || got.Scope != "child" {
		t.Fatalf("application entry = %+v", got)
	}
	if got := decodeTestJSONLogEntry(t, diagnostics.Bytes()); got.Msg != "write diagnostic" {
		t.Fatalf("diagnostic entry = %+v", got)
	}
}

func TestNewLoggerUsesFirstConfig(t *testing.T) {
	var first, later bytes.Buffer
	l := NewLogger(
		LoggerConfig{Format: LoggerFormatJSON, Output: &first},
		LoggerConfig{Format: LoggerFormatText, Output: &later},
	)
	l.Info("first config")
	if got := decodeTestJSONLogEntry(t, first.Bytes()); got.Msg != "first config" || later.Len() != 0 {
		t.Fatalf("first = %+v, later = %q", got, later.String())
	}
}

func TestMixedInvalidFieldsAndTruncationKeepSiblingPrecision(t *testing.T) {
	var out bytes.Buffer
	logger := newTestJSONLogger(t, LoggerConfig{Output: &out})
	wide := make(map[string]any, maxJSONFieldNodes*2)
	for i := range maxJSONFieldNodes * 2 {
		wide["\xff"+strconv.Itoa(i)] = "discarded"
	}
	logger.Info("fixture", Fields{
		"invalid\xff": "discarded", "precision": uint64(1<<63) + 1,
		"nested": map[string]any{"wide": wide},
	})
	var entry struct {
		Precision uint64
		Nested    map[string]map[string]string
	}
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil || entry.Precision != uint64(1<<63)+1 ||
		entry.Nested["wide"]["[truncated]"] != "[truncated]" || strings.Contains(out.String(), "discarded") {
		t.Fatalf("json=%s err=%v", &out, err)
	}
}

func BenchmarkNormalizeJSONFieldMapLimits(b *testing.B) {
	for _, count := range []int{32, maxJSONFieldNodes * 2} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			fields := make(map[string]any, count)
			for i := range count {
				key := strconv.Itoa(i)
				if i%2 == 0 {
					key += "\xff"
				}
				fields[key] = i
			}
			b.ReportAllocs()
			for b.Loop() {
				remaining := maxJSONFieldNodes
				_ = normalizeJSONFieldMap(fields, 0, &remaining)
			}
		})
	}
}

func TestTypedNilMarshalerKeepsValidSiblingFields(t *testing.T) {
	var out bytes.Buffer
	logger := newTestJSONLogger(t, LoggerConfig{Output: &out})
	var nilJSON *panickingJSONField
	var nilText *panickingTextField
	logger.Info("fixture", Fields{"json_value": nilJSON, "text_value": nilText, "ordinary": 123})
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil || bytes.Count(out.Bytes(), []byte{'\n'}) != 1 ||
		string(parsed["json_value"]) != "null" || string(parsed["text_value"]) != "null" ||
		string(parsed["ordinary"]) != "123" {
		t.Fatalf("record=%s err=%v", &out, err)
	}
}

func TestJSONScopedFieldsStayIndependentAfterWriteFailure(t *testing.T) {
	const child = "PLUMEGO_JSON_SCOPE_CHILD"
	if os.Getenv(child) != "1" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		var diagnostics bytes.Buffer
		command := exec.Command(executable, "-test.run=^TestJSONScopedFieldsStayIndependentAfterWriteFailure$")
		command.Env = append(os.Environ(), child+"=1")
		command.Stderr = &diagnostics
		if output, err := command.Output(); err != nil {
			t.Fatalf("child failed: %v, stdout=%s stderr=%s", err, output, diagnostics.String())
		}
		if got := diagnostics.String(); got != "json logger: failed to write log output\n" {
			t.Fatalf("diagnostics=%q", got)
		}
		return
	}

	var output bytes.Buffer
	writes := 0
	base := NewLogger(LoggerConfig{Format: LoggerFormatJSON, Output: diagnosticOutputFunc(func(p []byte) (int, error) {
		writes++
		if writes <= 2 {
			return 0, errors.New("write fixture")
		}
		return output.Write(p)
	})}).WithFields(Fields{"app": "service", "scope": "base"})
	left := base.WithFields(Fields{"scope": "left"})
	right := base.WithFields(Fields{"scope": "right"})
	left.Info("failed left")
	right.Info("failed right")
	left.Info("accepted left")
	right.Info("accepted right")
	base.Info("accepted base")
	decoder := json.NewDecoder(&output)
	for _, scope := range []string{"left", "right", "base"} {
		var entry testJSONLogEntry
		if err := decoder.Decode(&entry); err != nil || entry.Scope != scope ||
			entry.App != "service" || entry.Msg != "accepted "+scope {
			t.Fatalf("entry=%+v err=%v", entry, err)
		}
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("extra=%v err=%v", extra, err)
	}
}

func TestJSONDerivedScopeStaysWithSelectedOutput(t *testing.T) {
	var normal, failures bytes.Buffer
	logger := NewLogger(LoggerConfig{Format: LoggerFormatJSON, Output: &normal, ErrorOutput: &failures, Fields: Fields{"scope": "parent"}})
	child := logger.WithFields(Fields{"scope": "child"})
	grandchild := child.WithFields(Fields{"scope": "grandchild"})
	child.Warn("warning")
	grandchild.Error("error")
	logger.Info("info")
	normalLines := strings.Split(strings.TrimSuffix(normal.String(), "\n"), "\n")
	if len(normalLines) != 2 {
		t.Fatalf("normal=%q", normal.String())
	}
	warning := decodeTestJSONLogEntry(t, []byte(normalLines[0]))
	info := decodeTestJSONLogEntry(t, []byte(normalLines[1]))
	failure := decodeTestJSONLogEntry(t, failures.Bytes())
	if warning.Msg != "warning" || warning.Scope != "child" || info.Msg != "info" || info.Scope != "parent" || failure.Msg != "error" || failure.Scope != "grandchild" {
		t.Fatalf("warning=%+v info=%+v error=%+v", warning, info, failure)
	}
}

type countedFixtureField struct {
	calls  *int
	failed bool
}

func (f countedFixtureField) MarshalJSON() ([]byte, error) {
	*f.calls++
	if f.failed {
		return nil, errors.New("fixture encoding")
	}
	return []byte(`{"value":9007199254740993}`), nil
}

func TestJSONFieldEncoderRunsOnceAndIsolatesFailedSibling(t *testing.T) {
	var output bytes.Buffer
	good, bad := 0, 0
	logger := NewLogger(LoggerConfig{Format: LoggerFormatJSON, Output: &output, Fields: Fields{"fixture": countedFixtureField{&good, false}}})
	logger.WithFields(Fields{"broken": countedFixtureField{&bad, true}}).Info("child")
	logger.Info("parent")
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 || good != 2 || bad != 1 || !strings.Contains(lines[0], `"broken":"[unsupported]"`) ||
		strings.Contains(lines[1], `"broken"`) {
		t.Fatalf("good=%d bad=%d output=%s", good, bad, output.String())
	}
	for _, line := range lines {
		if !json.Valid([]byte(line)) || !strings.Contains(line, `"value":9007199254740993`) {
			t.Fatalf("record=%s", line)
		}
	}
}

func TestJSONLoggerPreservesUnicodeFieldNames(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(LoggerConfig{Format: LoggerFormatJSON, Output: &output})
	for range 3 {
		logger.Info("fixture", Fields{"zone": "local", "地区": "east", "文字": 42})
	}
	decoder := json.NewDecoder(&output)
	for range 3 {
		var fields map[string]any
		if err := decoder.Decode(&fields); err != nil {
			t.Fatal(err)
		}
		if fields["zone"] != "local" || fields["地区"] != "east" || fields["文字"] != float64(42) {
			t.Fatalf("fields=%v", fields)
		}
	}
}
