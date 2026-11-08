package devserver

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/core"
	plog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware/accesslog"
	"github.com/birdordev/plumego/middleware/cors"
	"github.com/birdordev/plumego/middleware/httpmetrics"
	"github.com/birdordev/plumego/middleware/recovery"
	"github.com/birdordev/plumego/middleware/requestid"
	mwtracing "github.com/birdordev/plumego/middleware/tracing"
	"github.com/birdordev/plumego/x/frontend"
	"github.com/birdordev/plumego/x/messaging/pubsub"
	"github.com/birdordev/plumego/x/observability/devtools"
	"github.com/birdordev/plumego/x/websocket"
)

const dashboardRoom = "dashboard"

const (
	dashboardActionTimeout       = 30 * time.Second
	dashboardStartCleanupTimeout = 5 * time.Second
)

const (
	devserverCodeConfigEditPathInvalid = "CONFIG_EDIT_PATH_INVALID"
	devserverCodeConfigEditReadFailed  = "CONFIG_EDIT_READ_FAILED"
	devserverCodeConfigEditWriteFailed = "CONFIG_EDIT_WRITE_FAILED"
	devserverCodeAppRebuildFailed      = "APP_REBUILD_FAILED"
	devserverCodeDependencyGraphFailed = "DEPENDENCY_GRAPH_FAILED"
	devserverCodeBuildFailed           = "BUILD_FAILED"
	devserverCodeAppRestartFailed      = "APP_RESTART_FAILED"
	devserverCodeAppStopFailed         = "APP_STOP_FAILED"
	devserverCodeAppNotRunning         = "APP_NOT_RUNNING"
	devserverCodeConfigFetchFailed     = "CONFIG_FETCH_FAILED"
	devserverCodeMetricsClearFailed    = "METRICS_CLEAR_FAILED"
	devserverCodeInvalidPprofRequest   = "INVALID_PPROF_REQUEST"
	devserverCodePprofFetchFailed      = "PPROF_FETCH_FAILED"
	devserverCodeAPITestFailed         = "API_TEST_FAILED"
	devserverCodeDashboardUnauthorized = "DASHBOARD_UNAUTHORIZED"
)

// Dashboard is the development dashboard server
type Dashboard struct {
	app       *core.App
	hub       *websocket.Hub
	pubsub    *pubsub.InProcBroker
	builder   *Builder
	runner    *AppRunner
	analyzer  *Analyzer
	depsCache *depsCache
	startTime time.Time
	server    *http.Server
	serveDone chan error

	lifecycleMu   sync.Mutex
	subCancel     context.CancelFunc
	subscriptions []pubsub.Subscription

	// Configuration
	dashboardAddr  string
	dashboardToken string
	appAddr        string
	projectDir     string
}

// Config holds dashboard configuration
type Config struct {
	DashboardAddr  string
	AppAddr        string
	ProjectDir     string
	UIPath         string
	DashboardToken string

	// Optional custom build command. Empty means use the default go build.
	CustomBuildCmd  string
	CustomBuildArgs []string

	// Optional custom run command. Empty means run the built binary.
	CustomRunCmd  string
	CustomRunArgs []string

	// OutputPassthrough forwards app stdout/stderr to the CLI output.
	OutputPassthrough bool
}

// NewDashboard creates a new development dashboard
func NewDashboard(cfg Config) (*Dashboard, error) {
	absDir, err := validateDashboardConfig(cfg)
	if err != nil {
		return nil, err
	}

	app, err := newDashboardApp(cfg.DashboardAddr)
	if err != nil {
		return nil, err
	}

	services, err := wireDashboardServices(absDir, cfg)
	if err != nil {
		return nil, err
	}

	d := &Dashboard{
		app:            app,
		hub:            services.hub,
		pubsub:         services.pubsub,
		builder:        services.builder,
		runner:         services.runner,
		analyzer:       services.analyzer,
		depsCache:      services.depsCache,
		dashboardAddr:  cfg.DashboardAddr,
		dashboardToken: cfg.DashboardToken,
		appAddr:        cfg.AppAddr,
		projectDir:     absDir,
		startTime:      time.Now(),
	}

	if err := d.registerRoutes(cfg.UIPath); err != nil {
		return nil, fmt.Errorf("register dashboard routes: %w", err)
	}

	return d, nil
}

type dashboardServices struct {
	hub       *websocket.Hub
	pubsub    *pubsub.InProcBroker
	builder   *Builder
	runner    *AppRunner
	analyzer  *Analyzer
	depsCache *depsCache
}

func validateDashboardConfig(cfg Config) (string, error) {
	absDir, err := filepath.Abs(cfg.ProjectDir)
	if err != nil {
		return "", fmt.Errorf("invalid project directory: %w", err)
	}
	if cfg.DashboardToken == "" && !isLoopbackDashboardAddr(cfg.DashboardAddr) {
		return "", fmt.Errorf("dashboard address %q is not loopback; set --dashboard-token for remote dashboard access", cfg.DashboardAddr)
	}
	return absDir, nil
}

func newDashboardApp(addr string) (*core.App, error) {
	appCfg := core.DefaultConfig()
	appCfg.Addr = addr
	app := core.New(appCfg, core.AppDependencies{Logger: plog.NewLogger()})
	recoveryMw, err := recovery.Middleware(recovery.Config{Logger: app.Logger()})
	if err != nil {
		return nil, fmt.Errorf("configure recovery middleware: %w", err)
	}
	accesslogMw, err := accesslog.Middleware(accesslog.Config{Logger: app.Logger()})
	if err != nil {
		return nil, fmt.Errorf("configure access log middleware: %w", err)
	}
	if err := app.Use(
		requestid.Middleware(),
		recoveryMw,
		mwtracing.Middleware(nil),
		httpmetrics.Middleware(nil),
		accesslogMw,
		cors.Middleware(dashboardCORSOptions(addr)),
	); err != nil {
		return nil, fmt.Errorf("register dashboard middleware: %w", err)
	}
	return app, nil
}

func wireDashboardServices(absDir string, cfg Config) (dashboardServices, error) {
	ps := pubsub.New()
	builder := NewBuilder(absDir, ps)
	runner := NewAppRunner(absDir, ps)
	hub, err := websocket.NewHub(4, 100)
	if err != nil {
		return dashboardServices{}, fmt.Errorf("create websocket hub: %w", err)
	}

	runner.SetEnv("APP_ADDR", cfg.AppAddr)
	runner.SetEnv("APP_DEBUG", "true")
	if cfg.CustomBuildCmd != "" {
		builder.SetCustomBuild(cfg.CustomBuildCmd, cfg.CustomBuildArgs)
	}
	if cfg.CustomRunCmd != "" {
		runner.SetCustomCommand(cfg.CustomRunCmd, cfg.CustomRunArgs)
	}
	runner.SetOutputPassthrough(cfg.OutputPassthrough)

	return dashboardServices{
		hub:       hub,
		pubsub:    ps,
		builder:   builder,
		runner:    runner,
		analyzer:  NewAnalyzer(fmt.Sprintf("http://localhost%s", cfg.AppAddr)),
		depsCache: newDepsCache(),
	}, nil
}

// registerRoutes sets up HTTP routes.
func (d *Dashboard) registerRoutes(uiPath string) error {
	// WebSocket endpoint for real-time events
	if err := d.app.Get("/ws", http.HandlerFunc(d.handleWebSocket)); err != nil {
		return fmt.Errorf("register /ws: %w", err)
	}

	// API endpoints (without Group - register directly)
	if err := d.app.Get("/api/info", http.HandlerFunc(d.handleInfo)); err != nil {
		return fmt.Errorf("register /api/info: %w", err)
	}
	if err := d.app.Get("/api/status", http.HandlerFunc(d.handleStatus)); err != nil {
		return fmt.Errorf("register /api/status: %w", err)
	}
	if err := d.app.Get("/api/health", http.HandlerFunc(d.handleHealth)); err != nil {
		return fmt.Errorf("register /api/health: %w", err)
	}
	if err := d.app.Get("/api/routes", http.HandlerFunc(d.handleRoutes)); err != nil {
		return fmt.Errorf("register /api/routes: %w", err)
	}
	if err := d.app.Get("/api/config", http.HandlerFunc(d.handleConfig)); err != nil {
		return fmt.Errorf("register /api/config: %w", err)
	}
	if err := d.app.Get("/api/config/edit", http.HandlerFunc(d.requireDashboardAuth(d.handleConfigEditGet))); err != nil {
		return fmt.Errorf("register /api/config/edit GET: %w", err)
	}
	if err := d.app.Post("/api/config/edit", http.HandlerFunc(d.requireDashboardAuth(d.handleConfigEditSave))); err != nil {
		return fmt.Errorf("register /api/config/edit POST: %w", err)
	}
	if err := d.app.Get("/api/metrics", http.HandlerFunc(d.handleMetrics)); err != nil {
		return fmt.Errorf("register /api/metrics: %w", err)
	}
	if err := d.app.Post("/api/metrics/clear", http.HandlerFunc(d.requireDashboardAuth(d.handleMetricsClear))); err != nil {
		return fmt.Errorf("register /api/metrics/clear: %w", err)
	}
	if err := d.app.Get("/api/deps", http.HandlerFunc(d.handleDeps)); err != nil {
		return fmt.Errorf("register /api/deps: %w", err)
	}
	if err := d.app.Get("/api/pprof/types", http.HandlerFunc(d.handlePprofTypes)); err != nil {
		return fmt.Errorf("register /api/pprof/types: %w", err)
	}
	if err := d.app.Get("/api/pprof/raw", http.HandlerFunc(d.requireDashboardAuth(d.handlePprofRaw))); err != nil {
		return fmt.Errorf("register /api/pprof/raw: %w", err)
	}
	if err := d.app.Post("/api/test", http.HandlerFunc(d.requireDashboardAuth(d.handleAPITest))); err != nil {
		return fmt.Errorf("register /api/test: %w", err)
	}
	if err := d.app.Post("/api/build", http.HandlerFunc(d.requireDashboardAuth(d.handleBuild))); err != nil {
		return fmt.Errorf("register /api/build: %w", err)
	}
	if err := d.app.Post("/api/restart", http.HandlerFunc(d.requireDashboardAuth(d.handleRestart))); err != nil {
		return fmt.Errorf("register /api/restart: %w", err)
	}
	if err := d.app.Post("/api/stop", http.HandlerFunc(d.requireDashboardAuth(d.handleStop))); err != nil {
		return fmt.Errorf("register /api/stop: %w", err)
	}

	// Try embedded UI first, then fallback to disk
	if HasEmbeddedUI() {
		uiFS, err := GetUIFS()
		if err == nil {
			if err := frontend.RegisterFS(d.app, http.FS(uiFS),
				frontend.WithPrefix("/"),
				frontend.WithIndex("index.html"),
			); err != nil {
				return fmt.Errorf("register embedded ui: %w", err)
			}
		}
	} else if uiPath != "" {
		if err := frontend.RegisterFromDir(d.app, uiPath,
			frontend.WithPrefix("/"),
			frontend.WithIndex("index.html"),
		); err != nil {
			return fmt.Errorf("register ui dir: %w", err)
		}
	}

	return nil
}

func isLoopbackDashboardAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func dashboardCORSOptions(addr string) cors.CORSOptions {
	return cors.CORSOptions{
		AllowedOrigins: dashboardAllowedOrigins(addr),
		AllowedMethods: []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowedHeaders: []string{
			"Accept",
			"Accept-Language",
			"Content-Language",
			"Content-Type",
			"Authorization",
			"X-Plumego-Dashboard-Token",
		},
	}
}

func dashboardAllowedOrigins(addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}
	if host == "" || strings.EqualFold(host, "localhost") {
		return []string{
			"http://localhost:" + port,
			"http://127.0.0.1:" + port,
			"http://[::1]:" + port,
		}
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return []string{
			"http://" + host + ":" + port,
			"http://localhost:" + port,
		}
	}
	return []string{"http://" + host + ":" + port}
}

func (d *Dashboard) requireDashboardAuth(next http.HandlerFunc) http.HandlerFunc {
	if d.dashboardToken == "" {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if !validDashboardToken(r, d.dashboardToken) {
			writeDevserverError(w, r, contract.TypeUnauthorized, devserverCodeDashboardUnauthorized, "dashboard token required")
			return
		}
		next(w, r)
	}
}

func (d *Dashboard) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if d.dashboardToken != "" && !validDashboardWebSocketToken(r, d.dashboardToken) {
		writeDevserverError(w, r, contract.TypeUnauthorized, devserverCodeDashboardUnauthorized, "dashboard token required")
		return
	}
	websocket.ServeWSWithConfig(w, r, websocket.ServerConfig{
		Hub:                  d.hub,
		RoomAuth:             websocket.NewSimpleRoomAuth(),
		AllowUnauthenticated: true,
		QueueSize:            32,
		SendTimeout:          5 * time.Second,
		SendBehavior:         websocket.SendBlock,
		AllowedOrigins:       dashboardAllowedOrigins(d.dashboardAddr),
	})
}

func validDashboardToken(r *http.Request, want string) bool {
	got := strings.TrimSpace(r.Header.Get("X-Plumego-Dashboard-Token"))
	if got == "" {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if token, ok := strings.CutPrefix(auth, "Bearer "); ok {
			got = strings.TrimSpace(token)
		}
	}
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func validDashboardWebSocketToken(r *http.Request, want string) bool {
	if validDashboardToken(r, want) {
		return true
	}
	got := strings.TrimSpace(r.URL.Query().Get("token"))
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// subscribeEvents subscribes to all events and broadcasts to WebSocket.
func (d *Dashboard) subscribeEvents(ctx context.Context) error {
	// Subscribe to all events using wildcard (with default options)
	sub, err := d.pubsub.Subscribe(ctx, "*", pubsub.SubOptions{})
	if err != nil {
		return err
	}
	d.trackSubscription(sub)

	// Start a goroutine to forward events to WebSocket
	go func() {
		for msg := range sub.C() {
			// Create a simple event message
			event := map[string]any{
				"type":      msg.Topic,
				"timestamp": time.Now().Format(time.RFC3339),
				"data":      msg.Data,
			}

			// Convert to JSON
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}

			// Broadcast to all WebSocket clients in dashboard room
			// Note: OpText is 0x01 for text frames
			d.hub.BroadcastRoom(dashboardRoom, 0x01, data)
		}
	}()

	return nil
}

// Start starts the dashboard server
func (d *Dashboard) Start(ctx context.Context) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Note: WebSocket hub workers are automatically started in NewHub.

	if err := d.app.Prepare(); err != nil {
		return fmt.Errorf("prepare dashboard app: %w", err)
	}
	srv, err := d.app.Server()
	if err != nil {
		return fmt.Errorf("get dashboard server: %w", err)
	}

	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("listen dashboard %s: %w", srv.Addr, err)
	}

	subCtx, subCancel := context.WithCancel(context.Background())
	d.lifecycleMu.Lock()
	d.server = srv
	d.serveDone = make(chan error, 1)
	d.subCancel = subCancel
	d.lifecycleMu.Unlock()

	go func() {
		err := srv.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		d.lifecycleMu.Lock()
		done := d.serveDone
		d.lifecycleMu.Unlock()
		if done != nil {
			done <- err
			close(done)
		}
	}()

	started := false
	defer func() {
		if err == nil || started {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), dashboardStartCleanupTimeout)
		defer cancel()
		if cleanupErr := d.Stop(cleanupCtx); cleanupErr != nil {
			err = fmt.Errorf("%w; cleanup dashboard start: %w", err, cleanupErr)
		}
	}()

	// Publish initial dashboard info event
	d.publishDashboardInfo()

	if err := d.subscribeEvents(subCtx); err != nil {
		return fmt.Errorf("subscribe dashboard events: %w", err)
	}

	// Re-publish dashboard info on app lifecycle changes
	if err := d.subscribeLifecycleForInfo(subCtx); err != nil {
		return fmt.Errorf("subscribe dashboard lifecycle: %w", err)
	}

	started = true
	return nil
}

// publishDashboardInfo publishes the current dashboard info via pubsub.
func (d *Dashboard) publishDashboardInfo() {
	d.pubsub.Publish(EventDashboard, pubsub.Message{
		Topic: EventDashboard,
		Data:  d.getDashboardInfo(),
	})
}

// subscribeLifecycleForInfo re-publishes dashboard info when app state changes.
func (d *Dashboard) subscribeLifecycleForInfo(ctx context.Context) error {
	patterns := []string{EventAppStart, EventAppStop, EventAppRestart}
	for _, pattern := range patterns {
		sub, err := d.pubsub.Subscribe(ctx, pattern, pubsub.SubOptions{})
		if err != nil {
			return err
		}
		d.trackSubscription(sub)
		go func() {
			for range sub.C() {
				// Small delay to let the runner state settle
				time.Sleep(100 * time.Millisecond)
				d.publishDashboardInfo()
			}
		}()
	}
	return nil
}

// Stop stops the dashboard server
func (d *Dashboard) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	// Stop the runner if running
	if d.runner.IsRunning() {
		if err := d.runner.Stop(); err != nil {
			return fmt.Errorf("stop dashboard runner: %w", err)
		}
	}

	d.cancelSubscriptions()

	if err := d.app.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown dashboard app: %w", err)
	}
	if err := d.waitForDashboardServer(ctx); err != nil {
		return err
	}

	// Stop WebSocket hub
	d.hub.Stop()

	return nil
}

func (d *Dashboard) trackSubscription(sub pubsub.Subscription) {
	d.lifecycleMu.Lock()
	defer d.lifecycleMu.Unlock()
	d.subscriptions = append(d.subscriptions, sub)
}

func (d *Dashboard) cancelSubscriptions() {
	d.lifecycleMu.Lock()
	cancel := d.subCancel
	subs := append([]pubsub.Subscription(nil), d.subscriptions...)
	d.subCancel = nil
	d.subscriptions = nil
	d.lifecycleMu.Unlock()

	if cancel != nil {
		cancel()
	}
	for _, sub := range subs {
		sub.Cancel()
	}
}

func (d *Dashboard) waitForDashboardServer(ctx context.Context) error {
	d.lifecycleMu.Lock()
	done := d.serveDone
	d.lifecycleMu.Unlock()
	if done == nil {
		return nil
	}

	select {
	case err := <-done:
		d.lifecycleMu.Lock()
		d.server = nil
		d.serveDone = nil
		d.lifecycleMu.Unlock()
		if err != nil {
			return fmt.Errorf("dashboard server stopped with error: %w", err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// BuildAndRun builds and runs the application
func (d *Dashboard) BuildAndRun(ctx context.Context) error {
	// Verify build environment
	if err := d.builder.Verify(); err != nil {
		return fmt.Errorf("build verification failed: %w", err)
	}

	// Build
	if err := d.builder.Build(ctx); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}

	if d.builder.HasCustomBuild() && !d.runner.HasCustomCommand() {
		if _, err := os.Stat(d.builder.OutputPath()); err != nil {
			return fmt.Errorf("custom build command must output %s or set --run-cmd", d.builder.OutputPath())
		}
	}

	// Start the application
	if err := d.runner.Start(ctx); err != nil {
		return fmt.Errorf("failed to start application: %w", err)
	}

	return nil
}

// Rebuild rebuilds and restarts the application
func (d *Dashboard) Rebuild(ctx context.Context) error {
	// Stop if running
	if d.runner.IsRunning() {
		if err := d.runner.Stop(); err != nil {
			return fmt.Errorf("failed to stop: %w", err)
		}
	}

	// Build and run
	return d.BuildAndRun(ctx)
}

type dashboardActionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

type dashboardStatusResponse struct {
	Dashboard dashboardStatusDashboard `json:"dashboard"`
	App       dashboardStatusApp       `json:"app"`
	Project   dashboardStatusProject   `json:"project"`
}

type dashboardStatusDashboard struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	Uptime  string `json:"uptime"`
}

type dashboardStatusApp struct {
	URL     string `json:"url"`
	Running bool   `json:"running"`
	PID     int    `json:"pid"`
}

type dashboardStatusProject struct {
	Dir       string `json:"dir"`
	GoVersion string `json:"go_version"`
}

type dashboardHealthResponse struct {
	Healthy bool                  `json:"healthy"`
	Checks  dashboardHealthChecks `json:"checks"`
}

type dashboardHealthChecks struct {
	App string `json:"app"`
}

type dashboardRoutesResponse struct {
	Routes []RouteInfo `json:"routes"`
	Count  int         `json:"count,omitempty"`
	Error  string      `json:"error,omitempty"`
}

type dashboardMetricsResponse struct {
	Dashboard  dashboardMetricsDashboard `json:"dashboard"`
	App        dashboardMetricsApp       `json:"app"`
	Alerts     []RequestAlert            `json:"alerts"`
	Thresholds RequestAlertThresholds    `json:"thresholds"`
}

type dashboardMetricsDashboard struct {
	Uptime    float64 `json:"uptime"`
	StartTime string  `json:"startTime"`
}

type dashboardMetricsApp struct {
	Running       bool                      `json:"running"`
	PID           int                       `json:"pid"`
	Healthy       *bool                     `json:"healthy,omitempty"`
	HealthDetails map[string]any            `json:"healthDetails,omitempty"`
	Requests      *devtools.DevHTTPSnapshot `json:"requests,omitempty"`
	DB            *devtools.DevDBSnapshot   `json:"db,omitempty"`
	RequestsError string                    `json:"requests_error,omitempty"`
}

type dashboardPprofTypesResponse struct {
	Types []pprofProfile `json:"types"`
}

type dashboardPprofPreviewResponse struct {
	Type        string `json:"type"`
	Seconds     int    `json:"seconds"`
	ContentType string `json:"content_type"`
	SizeBytes   int    `json:"size_bytes"`
	PreviewHex  string `json:"preview_hex"`
	DownloadURL string `json:"download_url"`
}

func writeDashboardActionResponse(w http.ResponseWriter, r *http.Request, message string) {
	_ = contract.WriteResponse(w, r, http.StatusOK, dashboardActionResponse{
		Success: true,
		Message: message,
	}, nil)
}

// HTTP Handlers

func (d *Dashboard) handleInfo(w http.ResponseWriter, r *http.Request) {
	_ = contract.WriteResponse(w, r, http.StatusOK, d.getDashboardInfo(), nil)
}

func (d *Dashboard) handleStatus(w http.ResponseWriter, r *http.Request) {
	info := d.getDashboardInfo()
	status := dashboardStatusResponse{
		Dashboard: dashboardStatusDashboard{
			Version: info.Version,
			URL:     info.DashboardURL,
			Uptime:  info.Uptime,
		},
		App: dashboardStatusApp{
			URL:     info.AppURL,
			Running: info.AppRunning,
			PID:     info.AppPID,
		},
		Project: dashboardStatusProject{
			Dir:       info.ProjectDir,
			GoVersion: info.GoVersion,
		},
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, status, nil)
}

func (d *Dashboard) handleHealth(w http.ResponseWriter, r *http.Request) {
	healthy := d.runner.IsRunning()
	appStatus := "stopped"
	if healthy {
		appStatus = "running"
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, dashboardHealthResponse{
		Healthy: healthy,
		Checks:  dashboardHealthChecks{App: appStatus},
	}, nil)
}

func (d *Dashboard) handleBuild(w http.ResponseWriter, r *http.Request) {
	actionCtx, cancel := context.WithTimeout(r.Context(), dashboardActionTimeout)
	defer cancel()

	if err := d.builder.Build(actionCtx); err != nil {
		writeDevserverError(w, r, contract.TypeInternal, devserverCodeBuildFailed, "build failed")
		return
	}

	writeDashboardActionResponse(w, r, "Build completed successfully")
}

func (d *Dashboard) handleRestart(w http.ResponseWriter, r *http.Request) {
	actionCtx, cancel := context.WithTimeout(r.Context(), dashboardActionTimeout)
	defer cancel()

	if err := d.Rebuild(actionCtx); err != nil {
		writeDevserverError(w, r, contract.TypeInternal, devserverCodeAppRestartFailed, "application restart failed")
		return
	}

	writeDashboardActionResponse(w, r, "Application restarted successfully")
}

func (d *Dashboard) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := d.runner.Stop(); err != nil {
		writeDevserverError(w, r, contract.TypeInternal, devserverCodeAppStopFailed, "application stop failed")
		return
	}

	writeDashboardActionResponse(w, r, "Application stopped successfully")
}

func (d *Dashboard) handleRoutes(w http.ResponseWriter, r *http.Request) {
	if !d.runner.IsRunning() {
		writeDevserverError(w, r, contract.TypeUnavailable, devserverCodeAppNotRunning, "application is not running")
		return
	}

	// Try to fetch routes from the running app
	routes, err := d.analyzer.GetRoutes()
	if err != nil {
		// Fallback to probing if debug endpoint is not available
		routes = d.analyzer.ProbeEndpoints()
		if len(routes) == 0 {
			_ = contract.WriteResponse(w, r, http.StatusOK, dashboardRoutesResponse{
				Routes: []RouteInfo{},
				Error:  "could not fetch routes",
			}, nil)
			return
		}
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, dashboardRoutesResponse{
		Routes: routes,
		Count:  len(routes),
	}, nil)
}

func (d *Dashboard) handleConfig(w http.ResponseWriter, r *http.Request) {
	if !d.runner.IsRunning() {
		writeDevserverError(w, r, contract.TypeUnavailable, devserverCodeAppNotRunning, "application is not running")
		return
	}

	snapshot, err := d.analyzer.GetAppSnapshot()
	if err != nil {
		writeDevserverError(w, r, contract.TypeInternal, devserverCodeConfigFetchFailed, "config unavailable")
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, snapshot, nil)
}

func (d *Dashboard) handleMetrics(w http.ResponseWriter, r *http.Request) {
	appMetrics := dashboardMetricsApp{
		Running: d.runner.IsRunning(),
		PID:     d.getAppPID(),
	}

	alerts, thresholds := evaluateRequestAlerts(nil)

	// If app is running, try to get health info
	if d.runner.IsRunning() {
		healthy, details, err := d.analyzer.HealthCheck()
		if err == nil {
			appMetrics.Healthy = &healthy
			appMetrics.HealthDetails = details
		}

		if devMetrics, err := d.analyzer.GetDevMetrics(); err == nil {
			appMetrics.Requests = &devMetrics.HTTP
			appMetrics.DB = &devMetrics.DB
			alerts, thresholds = evaluateRequestAlerts(&devMetrics.HTTP)
		} else {
			appMetrics.RequestsError = "request metrics unavailable"
		}
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, dashboardMetricsResponse{
		Dashboard: dashboardMetricsDashboard{
			Uptime:    time.Since(d.startTime).Seconds(),
			StartTime: d.startTime.Format(time.RFC3339),
		},
		App:        appMetrics,
		Alerts:     alerts,
		Thresholds: thresholds,
	}, nil)
}

func (d *Dashboard) handleMetricsClear(w http.ResponseWriter, r *http.Request) {
	if !d.runner.IsRunning() {
		writeDevserverError(w, r, contract.TypeUnavailable, devserverCodeAppNotRunning, "application is not running")
		return
	}

	if err := d.analyzer.ClearDevMetrics(); err != nil {
		writeDevserverError(w, r, contract.TypeInternal, devserverCodeMetricsClearFailed, "metrics could not be cleared")
		return
	}

	writeDashboardActionResponse(w, r, "")
}

func (d *Dashboard) handlePprofTypes(w http.ResponseWriter, r *http.Request) {
	_ = contract.WriteResponse(w, r, http.StatusOK, dashboardPprofTypesResponse{Types: pprofProfiles()}, nil)
}

func (d *Dashboard) handlePprofRaw(w http.ResponseWriter, r *http.Request) {
	if !d.runner.IsRunning() {
		writeDevserverError(w, r, contract.TypeUnavailable, devserverCodeAppNotRunning, "application is not running")
		return
	}

	profileType, seconds, err := parsePprofRequest(r)
	if err != nil {
		writeDevserverError(w, r, contract.TypeValidation, devserverCodeInvalidPprofRequest, "invalid pprof request")
		return
	}

	payload, contentType, err := d.analyzer.FetchPprof(profileType, seconds)
	if err != nil {
		writeDevserverError(w, r, contract.TypeInternal, devserverCodePprofFetchFailed, "pprof data unavailable")
		return
	}

	if download := strings.TrimSpace(r.URL.Query().Get("download")); download == "0" || strings.EqualFold(download, "false") {
		_ = contract.WriteResponse(w, r, http.StatusOK, dashboardPprofPreviewResponse{
			Type:        profileType,
			Seconds:     seconds,
			ContentType: contentType,
			SizeBytes:   len(payload),
			PreviewHex:  previewHex(payload, 96),
			DownloadURL: fmt.Sprintf("/api/pprof/raw?type=%s&seconds=%d", profileType, seconds),
		}, nil)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.pprof", profileType))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

func (d *Dashboard) handleAPITest(w http.ResponseWriter, r *http.Request) {
	if !d.runner.IsRunning() {
		writeDevserverError(w, r, contract.TypeUnavailable, devserverCodeAppNotRunning, "application is not running")
		return
	}

	var req APITestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeValidation).
			Code(contract.CodeInvalidJSON).
			Message("invalid request body").
			Build())
		return
	}

	resp, err := d.analyzer.DoAPITest(r.Context(), req)
	if err != nil {
		writeDevserverError(w, r, contract.TypeValidation, devserverCodeAPITestFailed, "api test failed")
		return
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, resp, nil)
}

func writeDevserverError(w http.ResponseWriter, r *http.Request, errorType contract.ErrorType, code string, message string) {
	_ = contract.WriteError(w, r, contract.NewErrorBuilder().
		Type(errorType).
		Code(code).
		Message(message).
		Build())
}

// Helper methods

func (d *Dashboard) getDashboardInfo() DashboardInfo {
	return DashboardInfo{
		Version:      "0.1.0",
		DashboardURL: fmt.Sprintf("http://localhost%s", d.dashboardAddr),
		AppURL:       fmt.Sprintf("http://localhost%s", d.appAddr),
		Uptime:       time.Since(d.startTime).String(),
		UptimeMS:     time.Since(d.startTime).Milliseconds(),
		StartTime:    d.startTime.Format(time.RFC3339),
		ProjectDir:   d.projectDir,
		GoVersion:    runtime.Version(),
		AppRunning:   d.runner.IsRunning(),
		AppPID:       d.getAppPID(),
	}
}

func (d *Dashboard) getAppPID() int {
	if d.runner.process != nil {
		return d.runner.process.Pid
	}
	return 0
}

// PublishEvent publishes an event to the event bus
func (d *Dashboard) PublishEvent(eventType string, data any) {
	d.pubsub.Publish(eventType, pubsub.Message{
		Topic: eventType,
		Data:  data,
	})
}

// GetPubSub returns the PubSub instance for external use
func (d *Dashboard) GetPubSub() *pubsub.InProcBroker {
	return d.pubsub
}

// GetBuilder exposes the builder behind the dashboard.
func (d *Dashboard) GetBuilder() BuilderAPI {
	return d.builder
}

// GetRunner exposes the runner behind the dashboard.
func (d *Dashboard) GetRunner() RunnerAPI {
	return d.runner
}

// SetOutputPassthrough controls whether app stdout/stderr is forwarded to the CLI.
func (d *Dashboard) SetOutputPassthrough(enabled bool) {
	d.runner.SetOutputPassthrough(enabled)
}
