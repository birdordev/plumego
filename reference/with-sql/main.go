package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/core"
	"github.com/birdordev/plumego/middleware/auth"
	"github.com/birdordev/plumego/security/authn"
	_ "modernc.org/sqlite"
)

const (
	modeCombined = "combined"
	modeAPI      = "api"
	modeWorker   = "worker"
	modeMigrate  = "migrate"
)

func main() {
	address := flag.String("addr", "127.0.0.1:8090", "HTTP listen address")
	path := flag.String("db", "items.db", "SQLite database path")
	migrate := flag.Bool("migrate", false, "apply database migrations and exit")
	mode := flag.String("mode", modeCombined, "serve the API and worker (combined), the API only (api), or the worker only (worker)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *migrate {
		if *mode != modeCombined {
			log.Fatal("-migrate cannot be combined with a serving mode")
		}
		*mode = modeMigrate
	}
	if err := run(ctx, *address, *path, os.Getenv("SQL_API_TOKEN"), *mode); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, address, path, token, mode string) error {
	switch mode {
	case modeCombined, modeAPI, modeWorker, modeMigrate:
	default:
		return errors.New("invalid SQL service mode")
	}
	if path == "" {
		return errors.New("-db requires a database path")
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	db, err := sql.Open("sqlite", path+separator+"_pragma=busy_timeout(1000)")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	repo := repository{db: db}
	if mode == modeMigrate {
		return repo.migrate(ctx)
	}
	version, err := repo.schemaVersion(ctx)
	if err != nil {
		return err
	}
	if version != currentSchemaVersion {
		return fmt.Errorf("schema version %d: run the migration before serving", version)
	}
	if mode == modeWorker {
		runOutbox(ctx, repo)
		return nil
	}
	app, err := newApp(address, token, repo)
	if err != nil {
		return err
	}
	if err := app.Prepare(); err != nil {
		return err
	}
	server, err := app.Server()
	if err != nil {
		return err
	}
	listener, err := new(net.ListenConfig).Listen(ctx, "tcp", address)
	if err != nil {
		return err
	}

	if mode == modeCombined {
		workerCtx, cancelWorker := context.WithCancel(context.WithoutCancel(ctx))
		workerDone := make(chan struct{})
		go func() {
			defer close(workerDone)
			runOutbox(workerCtx, repo)
		}()
		defer func() { cancelWorker(); <-workerDone }()
	}

	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	log.Printf("listening on %s", listener.Addr())
	var serveErr error
	serving := true
	select {
	case serveErr = <-serveDone:
		serving = false
	case <-ctx.Done():
	}
	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	drainErr := app.Shutdown(drainCtx)
	if drainErr != nil {
		drainErr = errors.Join(drainErr, server.Close())
	}
	if serving {
		serveErr = <-serveDone
	}
	return errors.Join(drainErr, normalizeServeError(serveErr))
}

func normalizeServeError(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func newApp(address, token string, repo repository) (*core.App, error) {
	if token == "" {
		return nil, errors.New("SQL_API_TOKEN is required to serve the API")
	}
	cfg := core.DefaultConfig()
	cfg.Addr = address
	cfg.Router.MethodNotAllowed = true
	app := core.New(cfg, core.AppDependencies{})
	if err := app.EnableStandardPathValues(); err != nil {
		return nil, err
	}
	authenticate, err := auth.Authenticate(authn.StaticToken(token))
	if err != nil {
		return nil, err
	}
	if err := app.SetNotFoundHandler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		writeAPIError(w, req, contract.TypeNotFound, "route not found")
	})); err != nil {
		return nil, err
	}
	h := handlers{repo: repo}
	for _, route := range []struct {
		method, path string
		handler      http.Handler
	}{
		{http.MethodGet, "/healthz", http.HandlerFunc(h.live)},
		{http.MethodGet, "/readyz", http.HandlerFunc(h.ready)},
		{http.MethodPost, "/items", authenticate(http.HandlerFunc(h.create))},
		{http.MethodGet, "/items/:id", authenticate(http.HandlerFunc(h.get))},
		{http.MethodPut, "/items/:id", authenticate(http.HandlerFunc(h.update))},
		{http.MethodDelete, "/items/:id", authenticate(http.HandlerFunc(h.delete))},
	} {
		if err := app.AddRoute(route.method, route.path, route.handler); err != nil {
			return nil, err
		}
	}
	return app, nil
}

func runOutbox(ctx context.Context, repo repository) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := deliverOutboxBatch(ctx, repo); err != nil && ctx.Err() == nil {
			log.Print("outbox processing failed; will retry")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func deliverOutboxBatch(ctx context.Context, repo repository) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var deliveryErr error
	// 每轮限量处理，让过期收据的清理也能执行。
	for range 16 {
		sent, err := repo.sendNext(ctx, repo.recordDelivery)
		if err != nil || !sent {
			deliveryErr = err
			break
		}
	}
	_, cleanupErr := repo.db.ExecContext(ctx, "DELETE FROM create_receipts WHERE expires_at <= ?", time.Now().Unix())
	return errors.Join(deliveryErr, cleanupErr)
}
