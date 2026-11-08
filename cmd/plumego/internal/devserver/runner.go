package devserver

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/birdordev/plumego/x/messaging/pubsub"
)

const maxAppLogLineBytes = 1024 * 1024

// AppRunner manages the user application lifecycle
type AppRunner struct {
	dir         string
	binaryPath  string
	cmd         string
	args        []string
	env         []string
	printOutput bool

	process  *os.Process
	cancel   context.CancelFunc
	waitDone chan error
	mu       sync.Mutex

	pubsub      *pubsub.InProcBroker
	running     bool
	starting    bool
	stopTimeout time.Duration
}

// NewAppRunner creates a new application runner
func NewAppRunner(dir string, ps *pubsub.InProcBroker) *AppRunner {
	return &AppRunner{
		dir:         dir,
		binaryPath:  filepath.Join(dir, ".dev-server"),
		pubsub:      ps,
		env:         os.Environ(),
		printOutput: true,
		stopTimeout: 5 * time.Second,
	}
}

// HasCustomCommand reports whether a custom run command is configured.
func (r *AppRunner) HasCustomCommand() bool {
	return r.cmd != ""
}

// SetCustomCommand sets a custom run command
func (r *AppRunner) SetCustomCommand(cmd string, args []string) {
	r.cmd = cmd
	r.args = args
}

// SetOutputPassthrough controls whether app output is printed to console.
func (r *AppRunner) SetOutputPassthrough(enabled bool) {
	r.printOutput = enabled
}

// SetEnv adds environment variables
func (r *AppRunner) SetEnv(key, value string) {
	r.env = append(r.env, fmt.Sprintf("%s=%s", key, value))
}

// IsRunning returns whether the app is currently running
func (r *AppRunner) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// Start starts the application
func (r *AppRunner) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.running || r.starting {
		r.mu.Unlock()
		return fmt.Errorf("application already running")
	}
	r.starting = true
	r.mu.Unlock()
	started := false
	defer func() {
		if started {
			return
		}
		r.mu.Lock()
		r.starting = false
		r.mu.Unlock()
	}()

	// Publish start event
	r.publish(EventAppStart, AppLifecycleEvent{State: "starting"})

	// Create command
	var cmd *exec.Cmd
	if r.cmd != "" {
		cmd = exec.CommandContext(ctx, r.cmd, r.args...)
	} else {
		cmd = exec.CommandContext(ctx, r.binaryPath)
	}

	cmd.Dir = r.dir
	cmd.Env = r.env

	// Capture stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		r.publish(EventAppStart, AppLifecycleEvent{State: "crashed", Error: err.Error()})
		return fmt.Errorf("failed to start: %w", err)
	}
	started = true

	waitDone := make(chan error, 1)
	// Create cancel context for log streaming. Process ownership stays with
	// the wait goroutine below; Stop signals the process and waits on waitDone.
	streamCtx, cancel := context.WithCancel(ctx)

	r.mu.Lock()
	r.process = cmd.Process
	r.cancel = cancel
	r.waitDone = waitDone
	r.running = true
	r.starting = false
	r.mu.Unlock()

	// Stream output
	go r.streamOutput(streamCtx, stdout, "stdout")
	go r.streamOutput(streamCtx, stderr, "stderr")

	// Wait for process in background
	go func() {
		err := cmd.Wait()

		r.mu.Lock()
		if r.waitDone == waitDone {
			r.running = false
			r.starting = false
			r.process = nil
			r.cancel = nil
			r.waitDone = nil
		}
		r.mu.Unlock()

		waitDone <- err
		close(waitDone)

		if err != nil {
			r.publish(EventAppStop, AppLifecycleEvent{State: "crashed", Error: err.Error()})
		} else {
			r.publish(EventAppStop, AppLifecycleEvent{State: "stopped"})
		}
	}()

	// Publish running event
	r.publish(EventAppStart, AppLifecycleEvent{State: "running", PID: cmd.Process.Pid})

	return nil
}

// Stop stops the application gracefully
func (r *AppRunner) Stop() error {
	r.mu.Lock()
	process := r.process
	cancel := r.cancel
	waitDone := r.waitDone
	timeout := r.stopTimeout
	if !r.running || process == nil || waitDone == nil {
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()

	// Cancel context to stop log streaming
	if cancel != nil {
		cancel()
	}

	// Try graceful shutdown first (SIGTERM)
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return waitForProcess(waitDone, timeout)
	}

	// Wait for graceful shutdown with timeout
	if err := waitForProcess(waitDone, timeout); err == nil {
		return nil
	}

	// Force kill after timeout, then wait for the single wait owner to finish.
	if err := process.Kill(); err != nil {
		return fmt.Errorf("failed to kill process: %w", err)
	}
	return waitForProcess(waitDone, timeout)
}

// Restart restarts the application
func (r *AppRunner) Restart(ctx context.Context) error {
	r.pubsub.Publish(EventAppRestart, pubsub.Message{
		Topic: EventAppRestart,
		Data: AppLifecycleEvent{
			State: "restarting",
		},
	})

	if err := r.Stop(); err != nil {
		return fmt.Errorf("failed to stop: %w", err)
	}

	// Small delay to ensure clean shutdown
	time.Sleep(500 * time.Millisecond)

	return r.Start(ctx)
}

// streamOutput captures and publishes application output
func (r *AppRunner) streamOutput(ctx context.Context, reader io.Reader, source string) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), maxAppLogLineBytes)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
			line := scanner.Text()

			// Determine log level from content
			level := "info"
			lowerLine := strings.ToLower(line)
			if strings.Contains(lowerLine, "error") || strings.Contains(lowerLine, "fatal") {
				level = "error"
			} else if strings.Contains(lowerLine, "warn") {
				level = "warn"
			} else if strings.Contains(lowerLine, "debug") {
				level = "debug"
			}

			// Publish log event
			r.publish(EventAppLog, LogEvent{
				Level:   level,
				Message: line,
				Source:  source,
			})

			// Also print to console when enabled.
			if r.printOutput {
				if source == "stderr" {
					fmt.Fprintln(os.Stderr, line)
				} else {
					fmt.Println(line)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		r.publish(EventAppError, LogEvent{
			Level:   "error",
			Message: fmt.Sprintf("Error reading %s: %v", source, err),
			Source:  source,
		})
	}
}

func (r *AppRunner) publish(topic string, data any) {
	if r.pubsub == nil {
		return
	}
	r.pubsub.Publish(topic, pubsub.Message{
		Topic: topic,
		Data:  data,
	})
}

func waitForProcess(done <-chan error, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("process did not exit within %s", timeout)
	}
}
