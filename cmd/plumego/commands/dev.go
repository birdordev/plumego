package commands

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/birdordev/plumego/cmd/plumego/internal/devserver"
	"github.com/birdordev/plumego/cmd/plumego/internal/output"
	"github.com/birdordev/plumego/cmd/plumego/internal/watcher"
	"github.com/birdordev/plumego/x/messaging/pubsub"
)

// DevCmd starts the development server. The newDashboard field allows
// test code to inject a mock without using package-level variables.
type DevCmd struct {
	newDashboard func(devserver.Config) (devserver.DashboardAPI, error)
}

// NewDevCmd constructs a DevCmd wired to the real dashboard constructor.
func NewDevCmd() *DevCmd {
	return &DevCmd{
		newDashboard: func(cfg devserver.Config) (devserver.DashboardAPI, error) {
			return devserver.NewDashboard(cfg)
		},
	}
}

type devOptions struct {
	dir             string
	addr            string
	dashboardAddr   string
	dashboardToken  string
	watchPatterns   string
	excludePatterns string
	debounceStr     string
	noReload        bool
	buildCmd        string
	runCmd          string
}

func (c *DevCmd) Name() string  { return "dev" }
func (c *DevCmd) Short() string { return "Start development server with dashboard and hot reload" }

func (c *DevCmd) Run(ctx *Context, args []string) error {
	opts, err := parseDevArgs(args)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}

	return c.runWithContext(context.Background(), ctx.Out, opts)
}

func parseDevArgs(args []string) (devOptions, error) {
	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	opts := devOptions{}
	fs.StringVar(&opts.dir, "dir", ".", "Project directory")
	fs.StringVar(&opts.addr, "addr", ":8080", "Application listen address (e.g. :8080 or 0.0.0.0:8080)")
	portFlag := fs.String("port", "", "Listen port (e.g. 3000); shorthand for --addr :PORT")
	fs.StringVar(portFlag, "p", "", "Listen port shorthand")
	fs.StringVar(&opts.dashboardAddr, "dashboard-addr", "127.0.0.1:9999", "Dashboard listen address")
	fs.StringVar(&opts.dashboardToken, "dashboard-token", "", "Token required for dashboard action APIs")
	fs.StringVar(&opts.watchPatterns, "watch", "**/*.go", "Watch patterns")
	fs.StringVar(&opts.excludePatterns, "exclude", "", "Exclude patterns")
	fs.StringVar(&opts.debounceStr, "debounce", "500ms", "Debounce duration")
	fs.BoolVar(&opts.noReload, "no-reload", false, "Disable hot reload")
	fs.StringVar(&opts.buildCmd, "build-cmd", "", "Custom build command")
	fs.StringVar(&opts.runCmd, "run-cmd", "", "Custom run command")

	positionals, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return devOptions{}, err
	}
	if len(positionals) > 0 {
		return devOptions{}, fmt.Errorf("unexpected arguments: %v", positionals)
	}

	if p := *portFlag; p != "" {
		if !strings.HasPrefix(p, ":") {
			p = ":" + p
		}
		opts.addr = p
	}

	return opts, nil
}

func (c *DevCmd) runWithContext(ctx context.Context, out *output.Formatter, opts devOptions) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	debounce, err := time.ParseDuration(opts.debounceStr)
	if err != nil {
		return out.Error(fmt.Sprintf("invalid debounce duration: %v", err), 1)
	}
	if debounce <= 0 {
		return out.Error("debounce duration must be positive", 1)
	}

	absDir, err := resolveDir(opts.dir)
	if err != nil {
		return out.Error(err.Error(), 1)
	}

	if err := emitDevStart(out, absDir, opts.addr, opts.dashboardAddr); err != nil {
		return err
	}

	cfg := devserver.Config{
		DashboardAddr:     opts.dashboardAddr,
		AppAddr:           opts.addr,
		ProjectDir:        absDir,
		DashboardToken:    opts.dashboardToken,
		OutputPassthrough: out.Format() == "text" && !out.IsQuiet(),
	}

	if opts.buildCmd != "" {
		cmd, args, err := parseCommandLine(opts.buildCmd)
		if err != nil {
			return out.Error(fmt.Sprintf("invalid build command: %v", err), 1)
		}
		if cmd == "" {
			return out.Error("build command is empty", 1)
		}
		cfg.CustomBuildCmd = cmd
		cfg.CustomBuildArgs = args
	}

	if opts.runCmd != "" {
		cmd, args, err := parseCommandLine(opts.runCmd)
		if err != nil {
			return out.Error(fmt.Sprintf("invalid run command: %v", err), 1)
		}
		if cmd == "" {
			return out.Error("run command is empty", 1)
		}
		cfg.CustomRunCmd = cmd
		cfg.CustomRunArgs = args
	}

	dash, err := c.newDashboard(cfg)
	if err != nil {
		return out.Error(fmt.Sprintf("failed to create dashboard: %v", err), 1)
	}

	if out.Format() != "text" {
		stopForwarder, err := startDevEventForwarder(runCtx, out, dash.GetPubSub())
		if err != nil {
			return out.Error(fmt.Sprintf("failed to subscribe to dev events: %v", err), 1)
		}
		defer stopForwarder()
	}

	if err := dash.Start(runCtx); err != nil {
		return out.Error(fmt.Sprintf("failed to start dashboard: %v", err), 1)
	}
	defer dash.Stop(runCtx)

	if err := emitDashboardStarted(out, opts.dashboardAddr); err != nil {
		return err
	}

	if err := dash.BuildAndRun(runCtx); err != nil {
		return out.Error(fmt.Sprintf("failed to build and run: %v", err), 1)
	}
	if err := emitAppReady(out, opts.addr); err != nil {
		return err
	}

	if opts.noReload {
		if err := emitReloadDisabled(out); err != nil {
			return err
		}

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigChan)

		select {
		case <-sigChan:
		case <-runCtx.Done():
		}
		if err := emitShutdown(out); err != nil {
			return err
		}
		return nil
	}

	watches := parsePatterns(opts.watchPatterns)
	excludes := parsePatterns(opts.excludePatterns)

	excludes = append(excludes,
		"**/vendor/**",
		"**/node_modules/**",
		"**/.git/**",
		"**/*_test.go",
		"**/.dev-server",
	)

	w, err := watcher.NewWatcher(absDir, watches, excludes, debounce)
	if err != nil {
		return out.Error(fmt.Sprintf("failed to create watcher: %v", err), 1)
	}
	defer w.Close()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	if err := emitWatching(out); err != nil {
		return err
	}

	for {
		select {
		case path := <-w.Events():
			if err := emitFileChanged(out, path); err != nil {
				return err
			}

			dash.PublishEvent(devserver.EventFileChange, devserver.FileChangeEvent{
				Path:   path,
				Action: "modify",
			})

			if out.Format() != "text" {
				if err := out.Event(output.Event{
					Event:   "reloading",
					Message: "Reloading application",
					Data: map[string]any{
						"reason": "file_changed",
						"path":   path,
					},
				}); err != nil {
					return err
				}
			}

			if err := dash.Rebuild(runCtx); err != nil {
				if err := emitReloadFailed(out, err); err != nil {
					return err
				}
			} else {
				if err := emitReloadComplete(out); err != nil {
					return err
				}
			}

		case err := <-w.Errors():
			if err := emitWatcherError(out, err); err != nil {
				return err
			}

		case <-sigChan:
			if err := emitShutdown(out); err != nil {
				return err
			}
			return nil

		case <-runCtx.Done():
			if err := emitShutdown(out); err != nil {
				return err
			}
			return nil
		}
	}
}

func parsePatterns(s string) []string {
	if s == "" {
		return nil
	}

	patterns := strings.Split(s, ",")
	result := make([]string, 0, len(patterns))

	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}

	return result
}

func parseCommandLine(input string) (string, []string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", nil, nil
	}

	var args []string
	var buf strings.Builder
	inSingle := false
	inDouble := false
	escaped := false

	flush := func() {
		if buf.Len() > 0 {
			args = append(args, buf.String())
			buf.Reset()
		}
	}

	for _, r := range input {
		if escaped {
			buf.WriteRune(r)
			escaped = false
			continue
		}

		if r == '\\' && !inSingle {
			escaped = true
			continue
		}

		if r == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}

		if r == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}

		if !inSingle && !inDouble && unicode.IsSpace(r) {
			flush()
			continue
		}

		buf.WriteRune(r)
	}

	if escaped {
		return "", nil, fmt.Errorf("unfinished escape sequence")
	}
	if inSingle || inDouble {
		return "", nil, fmt.Errorf("unterminated quote")
	}

	flush()
	if len(args) == 0 {
		return "", nil, fmt.Errorf("empty command")
	}

	return args[0], args[1:], nil
}

func startDevEventForwarder(ctx context.Context, out *output.Formatter, ps *pubsub.InProcBroker) (func(), error) {
	patterns := []string{"app.*", "build.*"}
	opts := pubsub.DefaultSubOptions()
	subs := make([]pubsub.Subscription, 0, len(patterns))

	for _, pattern := range patterns {
		sub, err := ps.SubscribePatternWithContext(ctx, pattern, opts)
		if err != nil {
			for _, existing := range subs {
				existing.Cancel()
			}
			return func() {}, err
		}
		subs = append(subs, sub)
		go forwardDevEvents(ctx, out, sub)
	}

	return func() {
		for _, sub := range subs {
			sub.Cancel()
		}
	}, nil
}

func forwardDevEvents(ctx context.Context, out *output.Formatter, sub pubsub.Subscription) {
	for {
		select {
		case <-ctx.Done():
			sub.Cancel()
			return
		case msg, ok := <-sub.C():
			if !ok {
				return
			}
			emitPubSubEvent(out, msg)
		}
	}
}

func emitPubSubEvent(out *output.Formatter, msg pubsub.Message) {
	event := output.Event{
		Event: msg.Topic,
	}
	if !msg.Time.IsZero() {
		event.Time = msg.Time.Format(time.RFC3339)
	}

	switch msg.Topic {
	case devserver.EventBuildStart:
		event.Message = "Build started"
		if data, ok := msg.Data.(devserver.BuildEvent); ok {
			event.Data = buildEventData(data)
		}
	case devserver.EventBuildSuccess:
		event.Message = "Build succeeded"
		if data, ok := msg.Data.(devserver.BuildEvent); ok {
			event.Data = buildEventData(data)
		}
	case devserver.EventBuildFail:
		event.Message = "Build failed"
		event.Level = "error"
		if data, ok := msg.Data.(devserver.BuildEvent); ok {
			event.Data = buildEventData(data)
		}
	case devserver.EventAppStart, devserver.EventAppStop, devserver.EventAppRestart:
		if data, ok := msg.Data.(devserver.AppLifecycleEvent); ok {
			event.Data = map[string]any{
				"state": data.State,
			}
			if data.PID != 0 {
				event.Data["pid"] = data.PID
			}
			if data.Error != "" {
				event.Data["error"] = data.Error
				event.Level = "error"
			}
			event.Message = "Application " + data.State
		}
	case devserver.EventAppLog, devserver.EventAppError:
		if data, ok := msg.Data.(devserver.LogEvent); ok {
			event.Level = data.Level
			event.Message = data.Message
			event.Data = map[string]any{
				"source": data.Source,
			}
		}
	default:
		event.Data = toEventMap(msg.Data)
	}

	_ = out.Event(event)
}

func buildEventData(data devserver.BuildEvent) map[string]any {
	payload := map[string]any{
		"success": data.Success,
	}
	if data.Duration > 0 {
		payload["duration_ms"] = data.Duration.Milliseconds()
	}
	if data.Output != "" {
		payload["output"] = data.Output
	}
	if data.Error != "" {
		payload["error"] = data.Error
	}
	return payload
}

func toEventMap(data any) map[string]any {
	switch v := data.(type) {
	case map[string]any:
		return v
	case map[string]string:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[k] = val
		}
		return out
	default:
		raw, err := json.Marshal(data)
		if err != nil {
			return map[string]any{"value": fmt.Sprintf("%v", data)}
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			return map[string]any{"value": fmt.Sprintf("%v", data)}
		}
		return out
	}
}

// addrToURL formats a network listen address as an HTTP URL for display.
// ":8080" → "http://localhost:8080"
// "0.0.0.0:8080" → "http://localhost:8080"
// "127.0.0.1:9999" → "http://127.0.0.1:9999"
func addrToURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	if strings.HasPrefix(addr, "0.0.0.0:") {
		return "http://localhost:" + strings.TrimPrefix(addr, "0.0.0.0:")
	}
	return "http://" + addr
}

func emitDevStart(out *output.Formatter, absDir, addr, dashboardAddr string) error {
	if out.Format() == "text" {
		message := fmt.Sprintf(
			"Starting Plumego Dev Server\n   Project: %s\n   App URL: %s\n   Dashboard URL: %s\n",
			absDir,
			addrToURL(addr),
			addrToURL(dashboardAddr),
		)
		return out.Event(output.Event{
			Event:   "starting",
			Message: message,
		})
	}

	return out.Event(output.Event{
		Event:   "starting",
		Message: "Plumego Dev Server starting",
		Data: map[string]any{
			"project":        absDir,
			"app_addr":       addr,
			"dashboard_addr": dashboardAddr,
		},
	})
}

func emitDashboardStarted(out *output.Formatter, dashboardAddr string) error {
	if out.Format() == "text" {
		return out.Event(output.Event{
			Event:   "dashboard_started",
			Message: fmt.Sprintf("Dashboard started at %s\n", addrToURL(dashboardAddr)),
		})
	}

	return out.Event(output.Event{
		Event:   "dashboard_started",
		Message: "Dashboard started",
		Data: map[string]any{
			"url": addrToURL(dashboardAddr),
		},
	})
}

func emitAppReady(out *output.Formatter, addr string) error {
	url := addrToURL(addr)
	if out.Format() == "text" {
		return out.Event(output.Event{
			Event:   "ready",
			Message: fmt.Sprintf("App ready at %s\n", url),
		})
	}

	return out.Event(output.Event{
		Event:   "ready",
		Message: "Application ready",
		Data: map[string]any{
			"url": url,
		},
	})
}

func emitWatching(out *output.Formatter) error {
	if out.Format() == "text" {
		return out.Event(output.Event{
			Event:   "watching",
			Message: "Watching for changes...\n   Press Ctrl+C to stop",
		})
	}

	return out.Event(output.Event{
		Event:   "watching",
		Message: "Watching for changes",
	})
}

func emitReloadDisabled(out *output.Formatter) error {
	return out.Event(output.Event{
		Event:   "reload_disabled",
		Message: "Auto reload disabled.\nPress Ctrl+C to stop",
		Data: map[string]any{
			"auto_reload": false,
		},
	})
}

func emitFileChanged(out *output.Formatter, path string) error {
	if out.Format() == "text" {
		return out.Event(output.Event{
			Event:   "file_changed",
			Message: fmt.Sprintf("\nFile changed: %s", path),
			Data: map[string]any{
				"path": path,
			},
		})
	}

	return out.Event(output.Event{
		Event:   "file_changed",
		Message: "File changed",
		Data: map[string]any{
			"path": path,
		},
	})
}

func emitReloadFailed(out *output.Formatter, reloadErr error) error {
	message := "Reload failed"
	if out.Format() == "text" {
		message = fmt.Sprintf("Reload failed: %v", reloadErr)
	}

	return out.Event(output.Event{
		Event:   "reload_failed",
		Level:   "error",
		Message: message,
		Data: map[string]any{
			"error": reloadErr.Error(),
		},
	})
}

func emitReloadComplete(out *output.Formatter) error {
	if out.Format() == "text" {
		return out.Event(output.Event{
			Event:   "reload_complete",
			Message: "Reload complete\n",
		})
	}

	return out.Event(output.Event{
		Event:   "reload_complete",
		Message: "Reload complete",
	})
}

func emitWatcherError(out *output.Formatter, watchErr error) error {
	message := "Watcher error"
	if out.Format() == "text" {
		message = fmt.Sprintf("Watcher error: %v", watchErr)
	}

	return out.Event(output.Event{
		Event:   "watcher_error",
		Level:   "warn",
		Message: message,
		Data: map[string]any{
			"error": watchErr.Error(),
		},
	})
}

func emitShutdown(out *output.Formatter) error {
	if out.Format() == "text" {
		return out.Event(output.Event{
			Event:   "stopped",
			Message: "\nShutting down...",
			Data: map[string]any{
				"code": 0,
			},
		})
	}

	return out.Event(output.Event{
		Event:   "stopped",
		Message: "Shutting down",
		Data: map[string]any{
			"code": 0,
		},
	})
}

// getExecutableDir returns the directory containing the plumego executable.
func getExecutableDir() string {
	ex, err := os.Executable()
	if err != nil {
		wd, _ := os.Getwd()
		return wd
	}
	return filepath.Dir(ex)
}
