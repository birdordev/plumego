package devserver

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/birdordev/plumego/cmd/plumego/internal/buildtarget"
	"github.com/birdordev/plumego/cmd/plumego/internal/executil"
	"github.com/birdordev/plumego/x/messaging/pubsub"
)

const maxBuildOutputBytes = 128 * 1024
const defaultBuildTimeout = 10 * time.Minute

// Builder manages application builds
type Builder struct {
	dir        string
	outputPath string
	buildCmd   string
	buildArgs  []string

	pubsub *pubsub.InProcBroker
}

// NewBuilder creates a new builder
func NewBuilder(dir string, ps *pubsub.InProcBroker) *Builder {
	return &Builder{
		dir:        dir,
		outputPath: filepath.Join(dir, ".dev-server"),
		pubsub:     ps,
	}
}

// HasCustomBuild reports whether a custom build command is configured.
func (b *Builder) HasCustomBuild() bool {
	return b.buildCmd != ""
}

// SetCustomBuild sets a custom build command
func (b *Builder) SetCustomBuild(cmd string, args []string) {
	b.buildCmd = cmd
	b.buildArgs = args
}

// Build builds the application.
func (b *Builder) Build(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now()

	// Publish build start event
	b.pubsub.Publish(EventBuildStart, pubsub.Message{
		Topic: EventBuildStart,
		Data: BuildEvent{
			Success: false,
		},
	})

	var name string
	var args []string
	if b.buildCmd != "" {
		name = b.buildCmd
		args = b.buildArgs
	} else {
		name = "go"
		args = []string{"build", "-o", b.outputPath, buildtarget.Default(b.dir)}
	}

	run, err := executil.Run(ctx, executil.Options{
		Name:        name,
		Args:        args,
		Dir:         b.dir,
		Timeout:     defaultBuildTimeout,
		OutputLimit: maxBuildOutputBytes,
	})
	duration := time.Since(start)

	output := run.CombinedOutput()

	if err != nil {
		// Build failed
		errMsg := fmt.Sprintf("Build failed: %v", err)
		if output != "" {
			errMsg = output
		}

		b.pubsub.Publish(EventBuildFail, pubsub.Message{
			Topic: EventBuildFail,
			Data: BuildEvent{
				Success:  false,
				Duration: duration,
				Error:    errMsg,
				Output:   output,
			},
		})

		return fmt.Errorf("build failed: %w\n%s", err, output)
	}

	// Build succeeded
	b.pubsub.Publish(EventBuildSuccess, pubsub.Message{
		Topic: EventBuildSuccess,
		Data: BuildEvent{
			Success:  true,
			Duration: duration,
			Output:   output,
		},
	})

	return nil
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated int
}

func newLimitedBuffer(limit int) limitedBuffer {
	return limitedBuffer{limit: limit}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		b.truncated += len(p)
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = b.buf.Write(p[:remaining])
	}
	if extra := len(p) - remaining; extra > 0 {
		b.truncated += extra
	}
	return len(p), nil
}

func (b *limitedBuffer) Len() int {
	return b.buf.Len()
}

func (b *limitedBuffer) String() string {
	out := b.buf.String()
	if b.truncated > 0 {
		out += fmt.Sprintf("\n[plumego: output truncated after %d bytes; %d bytes omitted]\n", b.limit, b.truncated)
	}
	return out
}

// Clean removes build artifacts
func (b *Builder) Clean() error {
	if _, err := os.Stat(b.outputPath); err == nil {
		return os.Remove(b.outputPath)
	}
	return nil
}

// OutputPath returns the path to the built binary
func (b *Builder) OutputPath() string {
	return b.outputPath
}

// Verify checks if the build environment is valid
func (b *Builder) Verify() error {
	// Check if go.mod exists
	goModPath := filepath.Join(b.dir, "go.mod")
	if _, err := os.Stat(goModPath); os.IsNotExist(err) {
		return fmt.Errorf("go.mod not found in %s", b.dir)
	}

	if b.buildCmd != "" {
		return nil
	}

	if !buildtarget.HasDefaultEntrypoint(b.dir) {
		return fmt.Errorf("no main package found in %s or %s", b.dir, filepath.Join(b.dir, "cmd", "app"))
	}

	return nil
}
