package devserver

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/spcent/plumego/x/messaging/pubsub"
)

func TestLimitedBufferBoundsCapturedOutput(t *testing.T) {
	buf := newLimitedBuffer(8)

	n, err := buf.Write([]byte("12345"))
	if err != nil || n != 5 {
		t.Fatalf("first write n=%d err=%v", n, err)
	}
	n, err = buf.Write([]byte("67890"))
	if err != nil || n != 5 {
		t.Fatalf("second write n=%d err=%v", n, err)
	}

	out := buf.String()
	if !strings.HasPrefix(out, "12345678") {
		t.Fatalf("expected retained prefix, got %q", out)
	}
	if !strings.Contains(out, "output truncated after 8 bytes") || !strings.Contains(out, "2 bytes omitted") {
		t.Fatalf("expected truncation marker, got %q", out)
	}
}

func TestLimitedBufferConsumesAllBytesWhenLimitIsZero(t *testing.T) {
	buf := newLimitedBuffer(0)

	n, err := buf.Write([]byte("discarded"))
	if err != nil || n != len("discarded") {
		t.Fatalf("write n=%d err=%v", n, err)
	}
	if !strings.Contains(buf.String(), "9 bytes omitted") {
		t.Fatalf("expected omitted byte count, got %q", buf.String())
	}
}

func TestBuilderBuildRespectsCanceledContext(t *testing.T) {
	builder := NewBuilder(t.TempDir(), pubsub.New())
	builder.SetCustomBuild(os.Args[0], []string{"-test.run=TestBuilderHelperProcess"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := builder.Build(ctx); err == nil {
		t.Fatal("expected canceled build to fail")
	}
}

// TestBuilderHelperProcess is the marker entry point for the re-exec'd test
// binary that stands in for a built devserver (see
// TestBuilderBuildRespectsCanceledContext). It deliberately has an empty body.
func TestBuilderHelperProcess(t *testing.T) {}
