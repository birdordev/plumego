package commands

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spcent/plumego/cmd/plumego/internal/devserver"
	"github.com/spcent/plumego/cmd/plumego/internal/output"
	"github.com/spcent/plumego/x/messaging/pubsub"
)

type fakeDashboard struct {
	pubsub      *pubsub.InProcBroker
	passthrough bool
	started     bool
	stopped     bool
	built       bool
	onBuilt     func()
}

func (d *fakeDashboard) Start(context.Context) error {
	d.started = true
	return nil
}

func (d *fakeDashboard) Stop(context.Context) error {
	d.stopped = true
	return nil
}

func (d *fakeDashboard) BuildAndRun(context.Context) error {
	d.built = true
	if d.onBuilt != nil {
		d.onBuilt()
	}
	return nil
}

func (d *fakeDashboard) Rebuild(context.Context) error { return nil }

func (d *fakeDashboard) PublishEvent(string, any) {}

func (d *fakeDashboard) GetPubSub() *pubsub.InProcBroker { return d.pubsub }

func (d *fakeDashboard) GetBuilder() devserver.BuilderAPI { return nil }

func (d *fakeDashboard) GetRunner() devserver.RunnerAPI { return nil }

func (d *fakeDashboard) SetOutputPassthrough(enabled bool) { d.passthrough = enabled }

func TestParseDevArgs(t *testing.T) {
	opts, err := parseDevArgs([]string{
		"--no-reload",
		"--dashboard-token", "dev-token",
		"--build-cmd", "go build -o .dev-server ./cmd/api",
		"--run-cmd", "./.dev-server",
	})
	if err != nil {
		t.Fatalf("parseDevArgs failed: %v", err)
	}

	if !opts.noReload {
		t.Fatalf("expected noReload true")
	}
	if opts.buildCmd == "" || opts.runCmd == "" {
		t.Fatalf("expected build and run commands to be set")
	}
	if opts.dashboardToken != "dev-token" {
		t.Fatalf("expected dashboard token to be parsed, got %q", opts.dashboardToken)
	}
}

func TestParseDevArgsRejectsUnexpectedArguments(t *testing.T) {
	if _, err := parseDevArgs([]string{"extra", "--no-reload"}); err == nil {
		t.Fatalf("expected unexpected argument error")
	}
}

func TestDevRunRejectsNonPositiveDebounce(t *testing.T) {
	out := output.NewFormatter()
	out.SetFormat("json")

	cmd := NewDevCmd()
	err := cmd.runWithContext(t.Context(), out, devOptions{
		dir:           t.TempDir(),
		addr:          ":8080",
		dashboardAddr: ":9999",
		debounceStr:   "0s",
	})
	if err == nil {
		t.Fatal("expected non-positive debounce error")
	}
}

func TestDevRunNoReload(t *testing.T) {
	tmpDir := t.TempDir()

	out := output.NewFormatter()
	out.SetFormat("text")
	var buf bytes.Buffer
	out.SetWriters(&buf, &buf)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Cancel once the dashboard has finished its initial build. runWithContext
	// only reaches its blocking wait after Start/BuildAndRun succeed, so an
	// event-driven cancel is deterministic and never races the setup phase.
	dash := &fakeDashboard{
		pubsub:  pubsub.New(),
		onBuilt: func() { cancel() },
	}

	cmd := &DevCmd{
		newDashboard: func(cfg devserver.Config) (devserver.DashboardAPI, error) {
			return dash, nil
		},
	}
	err := cmd.runWithContext(ctx, out, devOptions{
		dir:           tmpDir,
		addr:          ":8080",
		dashboardAddr: ":9999",
		debounceStr:   "500ms",
		noReload:      true,
	})
	if err != nil {
		t.Fatalf("runWithContext failed: %v", err)
	}

	if !dash.started || !dash.built {
		t.Fatalf("expected dashboard to start and build")
	}
	if !strings.Contains(buf.String(), "Auto reload disabled") {
		t.Fatalf("expected no-reload message, got: %s", buf.String())
	}
	if !dash.stopped {
		t.Fatal("expected dashboard to stop on no-reload shutdown")
	}
}

func TestDevRunReloadStopsDashboardOnContextCancel(t *testing.T) {
	tmpDir := t.TempDir()

	out := output.NewFormatter()
	out.SetFormat("text")
	var buf bytes.Buffer
	out.SetWriters(&buf, &buf)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Cancel once the initial build completes so the reload loop reaches its
	// blocking select before the shutdown path runs.
	dash := &fakeDashboard{
		pubsub:  pubsub.New(),
		onBuilt: func() { cancel() },
	}

	cmd := &DevCmd{
		newDashboard: func(cfg devserver.Config) (devserver.DashboardAPI, error) {
			return dash, nil
		},
	}
	err := cmd.runWithContext(ctx, out, devOptions{
		dir:           tmpDir,
		addr:          ":8080",
		dashboardAddr: ":9999",
		debounceStr:   "25ms",
	})
	if err != nil {
		t.Fatalf("runWithContext failed: %v", err)
	}

	if !dash.started || !dash.built {
		t.Fatalf("expected dashboard to start and build")
	}
	if !dash.stopped {
		t.Fatal("expected dashboard to stop after context cancellation")
	}
	if !strings.Contains(buf.String(), "Shutting down") {
		t.Fatalf("expected shutdown message, got: %s", buf.String())
	}
}

func TestDevRunBuildCmd(t *testing.T) {
	tmpDir := t.TempDir()

	var capturedCfg devserver.Config

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	dash := &fakeDashboard{
		pubsub:  pubsub.New(),
		onBuilt: func() { cancel() },
	}

	out := output.NewFormatter()
	out.SetFormat("text")

	cmd := &DevCmd{
		newDashboard: func(cfg devserver.Config) (devserver.DashboardAPI, error) {
			if filepath.Clean(cfg.ProjectDir) != filepath.Clean(tmpDir) {
				t.Fatalf("expected project dir %s, got %s", tmpDir, cfg.ProjectDir)
			}
			capturedCfg = cfg
			return dash, nil
		},
	}
	err := cmd.runWithContext(ctx, out, devOptions{
		dir:           tmpDir,
		addr:          ":8080",
		dashboardAddr: ":9999",
		debounceStr:   "500ms",
		noReload:      true,
		buildCmd:      "go build -o .dev-server ./cmd/api",
	})
	if err != nil {
		t.Fatalf("runWithContext failed: %v", err)
	}

	if capturedCfg.CustomBuildCmd != "go" {
		t.Fatalf("expected build cmd 'go', got %q", capturedCfg.CustomBuildCmd)
	}
	if len(capturedCfg.CustomBuildArgs) < 3 || capturedCfg.CustomBuildArgs[0] != "build" {
		t.Fatalf("unexpected build args: %#v", capturedCfg.CustomBuildArgs)
	}
}
