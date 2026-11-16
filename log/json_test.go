package log

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"strings"
	"sync"
	"testing"
)

type customJSONSlice []int

func (*customJSONSlice) MarshalJSON() ([]byte, error) { return []byte(`"custom"`), nil }

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
	logger := newTestJSONLogger(t, LoggerConfig{
		Output: &buf,
		Level:  INFO,
		Fields: Fields{"base": "yes", "override": "base"},
	})

	logger.Info("multi", Fields{"first": "one", "override": "first"}, Fields{"second": float64(2), "override": "second"})

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
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			base.Info("base", Fields{"idx": i})
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			child.Info("child", Fields{"idx": i})
		}
	}()

	wg.Wait()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2*n {
		t.Fatalf("expected %d lines, got %d", 2*n, len(lines))
	}
	for _, line := range lines {
		_ = decodeTestJSONLogEntry(t, []byte(line))
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
	logger := newTestJSONLogger(t, LoggerConfig{Output: &out})
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
	if entry.Good != "keep" || entry.JSON != "[unsupported]" || len(entry.Nested) != 2 ||
		entry.Nested[0] != "[unsupported]" || entry.Nested[1] != "keep" || strings.Contains(out.String(), "private encoder panic") {
		t.Fatalf("unexpected log: %s", out.String())
	}
}
