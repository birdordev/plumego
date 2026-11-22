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
	"syscall"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/core"
	"github.com/birdordev/plumego/middleware/auth"
	"github.com/birdordev/plumego/security/authn"
	_ "modernc.org/sqlite"
)

func main() {
	address := flag.String("addr", "127.0.0.1:8090", "HTTP listen address")
	path := flag.String("db", "items.db", "SQLite database path")
	migrate := flag.Bool("migrate", false, "apply database migrations and exit")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *address, *path, os.Getenv("SQL_API_TOKEN"), *migrate); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, address, path, token string, migrate bool) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	repo := repository{db: db}
	if migrate {
		return repo.migrate(ctx)
	}
	version, err := repo.schemaVersion(ctx)
	if err != nil {
		return err
	}
	if version != 1 {
		return fmt.Errorf("schema version %d: run the migration before serving", version)
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

	workerCtx, cancelWorker := context.WithCancel(context.WithoutCancel(ctx))
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		runOutbox(workerCtx, repo)
	}()
	defer func() { cancelWorker(); <-workerDone }()

	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	log.Printf("listening on %s", listener.Addr())
	select {
	case err := <-serveDone:
		return normalizeServeError(err)
	case <-ctx.Done():
		drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		err := app.Shutdown(drainCtx)
		if err != nil {
			err = errors.Join(err, server.Close())
		}
		return errors.Join(err, normalizeServeError(<-serveDone))
	}
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
	publish := func(_ context.Context, event outboxEvent) error {
		log.Printf("event=%d kind=%s item_id=%d", event.ID, event.Kind, event.ItemID)
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 限制每轮处理数量，避免持续到来的事件阻塞其他清理。
			for range 16 {
				found, err := repo.sendNext(ctx, publish)
				if err != nil {
					if ctx.Err() == nil {
						log.Print("outbox delivery failed; will retry")
					}
					break
				}
				if !found {
					break
				}
			}
			if _, err := repo.db.ExecContext(ctx, "DELETE FROM create_receipts WHERE expires_at <= ?", time.Now().Unix()); err != nil && ctx.Err() == nil {
				log.Print("receipt cleanup failed; will retry")
			}
		}
	}
}
