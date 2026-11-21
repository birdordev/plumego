package log

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestNewLoggerDetachesConfigFields(t *testing.T) {
	for _, format := range []LoggerFormat{LoggerFormatText, LoggerFormatJSON} {
		t.Run(string(format), func(t *testing.T) {
			var output bytes.Buffer
			fields := Fields{"component": "original"}
			logger := NewLogger(LoggerConfig{Format: format, Output: &output, Fields: fields})

			fields["component"] = "replacement"
			logger.Info("example")
			got := output.String()
			if !strings.Contains(got, "original") || strings.Contains(got, "replacement") {
				t.Fatalf("log output = %q, want original component value", got)
			}
		})
	}
}

func TestDefaultLoggerRespectVerbosity(t *testing.T) {
	t.Run("respects-verbosity", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewLogger(LoggerConfig{
			Format:           LoggerFormatText,
			Output:           &buf,
			Level:            DEBUG,
			Verbosity:        0,
			RespectVerbosity: true,
		})
		logger.Debug("debug-suppressed")
		if got := buf.String(); got != "" {
			t.Fatalf("expected debug to be suppressed, got %q", got)
		}
	})

	t.Run("ignores-verbosity-when-disabled", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewLogger(LoggerConfig{
			Format:           LoggerFormatText,
			Output:           &buf,
			Level:            DEBUG,
			Verbosity:        0,
			RespectVerbosity: false,
		})
		logger.Debug("debug-ok")
		if got := buf.String(); !strings.Contains(got, "debug-ok") {
			t.Fatalf("expected debug to log, got %q", got)
		}
	})
}

func TestDerivedLoggerPreservesRespectVerbosityAcrossFormats(t *testing.T) {
	formats := []LoggerFormat{LoggerFormatText, LoggerFormatJSON}
	for _, format := range formats {
		t.Run(string(format), func(t *testing.T) {
			var buf bytes.Buffer
			logger := NewLogger(LoggerConfig{
				Format:           format,
				Output:           &buf,
				Level:            DEBUG,
				Verbosity:        0,
				RespectVerbosity: true,
			})

			child := logger.WithFields(Fields{"scope": "child"})
			child.Debug("debug-suppressed")
			if got := buf.String(); got != "" {
				t.Fatalf("expected derived logger to preserve verbosity gating, got %q", got)
			}

			child.Info("info-ok")
			got := buf.String()
			if !strings.Contains(got, "info-ok") {
				t.Fatalf("expected derived logger info to log, got %q", got)
			}
			if !strings.Contains(got, "child") {
				t.Fatalf("expected derived logger fields to be preserved, got %q", got)
			}
		})
	}
}

func TestDefaultLoggerErrorOutput(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	logger := NewLogger(LoggerConfig{
		Format:      LoggerFormatText,
		Output:      &out,
		ErrorOutput: &errOut,
		Level:       INFO,
	})

	logger.Info("info-msg")
	logger.Error("error-msg")

	if got := out.String(); !strings.Contains(got, "info-msg") {
		t.Fatalf("expected info log in output, got %q", got)
	}
	if got := out.String(); strings.Contains(got, "error-msg") {
		t.Fatalf("expected error log to skip output, got %q", got)
	}
	if got := errOut.String(); !strings.Contains(got, "error-msg") {
		t.Fatalf("expected error log in error output, got %q", got)
	}
}

func TestNewLoggerUnknownFormatPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("NewLogger with unknown format should panic")
		}
	}()
	var buf bytes.Buffer
	_ = NewLogger(LoggerConfig{
		Format: LoggerFormat("unknown"),
		Output: &buf,
		Level:  DEBUG,
	})
}

func TestTextLoggerMergesVariadicFieldsInOrder(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(LoggerConfig{
		Format: LoggerFormatText,
		Output: &buf,
		Level:  INFO,
		Fields: Fields{"base": "yes", "override": "base"},
	})

	logger.Info("multi", Fields{"first": "one", "override": "first"}, Fields{"second": 2, "override": "second"})

	got := buf.String()
	for _, want := range []string{"base=yes", "first=one", "second=2", "override=second"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected text log to contain %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "override=base") || strings.Contains(got, "override=first") {
		t.Fatalf("expected later fields to override earlier fields, got %q", got)
	}
}

func TestTextLoggerEscapesAmbiguousFields(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(LoggerConfig{
		Format: LoggerFormatText,
		Output: &buf,
		Level:  INFO,
	})

	logger.Info("fields", Fields{
		"bad key":            "value with space",
		`quote"key`:          "quote",
		"eq=key":             "x=y",
		"line":               "a\nb",
		"safe.key/sub-key_1": "plain",
	})

	got := buf.String()
	for _, want := range []string{`"bad key"="value with space"`, `"eq=key"="x=y"`, `"quote\"key"=quote`, `line="a\nb"`, `safe.key/sub-key_1=plain`} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected escaped field %q in %q", want, got)
		}
	}
	if strings.Contains(got, "a\nb\n") {
		t.Fatalf("expected newline field value to stay escaped on one log line, got %q", got)
	}
}

func TestTextStructuredLoggerReportsExternalCaller(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(LoggerConfig{
		Format: LoggerFormatText,
		Output: &buf,
		Level:  INFO,
	})

	callStructuredInfoForDepth(logger)

	got := buf.String()
	if !strings.Contains(got, "callsite_helper_test.go:") {
		t.Fatalf("expected structured text log to report external caller, got %q", got)
	}
	if strings.Contains(got, "logger.go:") || strings.Contains(got, "glog.go:") {
		t.Fatalf("expected structured text log to skip internal wrappers, got %q", got)
	}
}

func TestLoggerFatalExits(t *testing.T) {
	if os.Getenv("PLUMEGO_FATAL_CHILD") == "1" {
		format := LoggerFormat(os.Getenv("PLUMEGO_FATAL_FORMAT"))
		level, err := strconv.Atoi(os.Getenv("PLUMEGO_FATAL_LEVEL"))
		if err != nil {
			t.Fatal(err)
		}
		logger := NewLogger(LoggerConfig{
			Format:      format,
			Output:      os.Stdout,
			ErrorOutput: os.Stderr,
			Level:       Level(level),
		})
		if os.Getenv("PLUMEGO_FATAL_CONTEXT") == "true" {
			logger.FatalCtx(t.Context(), "fatal-exit")
		} else {
			logger.Fatal("fatal-exit")
		}
		return
	}

	formats := []LoggerFormat{LoggerFormatText, LoggerFormatJSON, LoggerFormatDiscard}
	for _, format := range formats {
		t.Run(string(format), func(t *testing.T) {
			for _, level := range []Level{DEBUG, FATAL + 1} {
				for _, withContext := range []bool{false, true} {
					cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestLoggerFatalExits$", "-test.v")
					cmd.Env = append(os.Environ(),
						"PLUMEGO_FATAL_CHILD=1",
						"PLUMEGO_FATAL_FORMAT="+string(format),
						"PLUMEGO_FATAL_LEVEL="+strconv.Itoa(int(level)),
						"PLUMEGO_FATAL_CONTEXT="+strconv.FormatBool(withContext),
					)
					output, err := cmd.CombinedOutput()
					if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
						t.Fatalf("level=%d withContext=%t: expected exit code 1, got %v", level, withContext, err)
					}
					if got, want := bytes.Contains(output, []byte("fatal-exit")), level <= FATAL && format != LoggerFormatDiscard; got != want {
						t.Fatalf("format=%s level=%d context=%t output=%q", format, level, withContext, output)
					}
				}
			}
		})
	}
}

func TestDerivedLoggerFieldsCopyOnlyTheTopLevel(t *testing.T) {
	for _, format := range []LoggerFormat{LoggerFormatText, LoggerFormatJSON} {
		var output bytes.Buffer
		base := NewLogger(LoggerConfig{Format: format, Output: &output})
		nested := map[string]string{"nested": "first-value"}
		input := Fields{"model": nested, "label": "original-label"}
		child := base.WithFields(input)
		input["label"] = "changed-label"
		nested["nested"] = "second-value"
		child.Info("entry")
		if got := output.String(); !strings.Contains(got, "original-label") ||
			!strings.Contains(got, "second-value") || strings.Contains(got, "changed-label") {
			t.Fatalf("format=%s output=%q", format, got)
		}
	}
}

func TestTextLoggerOutputSplitPreservesParentAndChildFields(t *testing.T) {
	var out, errors bytes.Buffer
	parent := NewLogger(LoggerConfig{Format: LoggerFormatText, Output: &out, ErrorOutput: &errors, Fields: Fields{"scope": "parent", "count": 1}})
	child := parent.WithFields(Fields{"scope": "child", "count": 2})
	child.Warn("warning fixture")
	child.Error("failure fixture")
	parent.Info("normal fixture")
	normal := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(normal) != 2 || !strings.Contains(normal[0], "warning fixture") ||
		!strings.Contains(normal[0], "scope=child") || !strings.Contains(normal[0], "count=2") ||
		!strings.Contains(normal[1], "normal fixture") || !strings.Contains(normal[1], "scope=parent") ||
		!strings.Contains(normal[1], "count=1") || !strings.Contains(errors.String(), "failure fixture") ||
		!strings.Contains(errors.String(), "scope=child") || strings.Count(errors.String(), "\n") != 1 {
		t.Fatalf("normal=%q errors=%q", out.String(), errors.String())
	}
}
