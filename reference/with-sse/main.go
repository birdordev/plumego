package main

import (
	"context"
	"errors"
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
	"github.com/birdordev/plumego/middleware/concurrencylimit"
	"github.com/birdordev/plumego/middleware/timeout"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	cfg := core.DefaultConfig()
	cfg.Addr = "127.0.0.1:8092"
	// 流式处理器为每一帧重新设置写入时限。
	cfg.WriteTimeout = 0
	app := core.New(cfg, core.AppDependencies{})
	if err := register(app, ctx, 32, 15*time.Second); err != nil {
		return err
	}
	if err := app.Prepare(); err != nil {
		return err
	}
	server, err := app.Server()
	if err != nil {
		return err
	}
	listener, err := new(net.ListenConfig).Listen(ctx, "tcp", cfg.Addr)
	if err != nil {
		return err
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	select {
	case err := <-serveDone:
		return err
	case <-ctx.Done():
		drain, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		err := app.Shutdown(drain)
		if err != nil {
			err = errors.Join(err, server.Close())
		}
		if serveErr := <-serveDone; !errors.Is(serveErr, http.ErrServerClosed) {
			err = errors.Join(err, serveErr)
		}
		return err
	}
}

func register(app *core.App, stop context.Context, capacity int, heartbeat time.Duration) error {
	boundedJSON := timeout.Middleware(timeout.Config{Timeout: 2 * time.Second})
	if err := app.Get("/healthz", boundedJSON(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = contract.WriteResponse(w, r, http.StatusOK, map[string]bool{"live": true}, nil)
	}))); err != nil {
		return err
	}
	limitStreams := concurrencylimit.Middleware(concurrencylimit.Config{MaxConcurrent: capacity})
	// 流式路由需要在处理器返回前刷新响应。
	return app.Get("/events", limitStreams(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if stop.Err() != nil {
			_ = contract.WriteError(w, req, contract.NewErrorBuilder().Type(contract.TypeUnavailable).Build())
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		if req.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		controller := http.NewResponseController(w)
		if err := writeFrame(w, controller, "ready"); err != nil {
			return
		}
		ticker := time.NewTicker(heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-req.Context().Done():
				return
			case <-stop.Done():
				return
			case <-ticker.C:
				if err := writeFrame(w, controller, "heartbeat"); err != nil {
					return
				}
			}
		}
	})))
}

// writeFrame 只限制当前帧的写入时间，心跳间隔不占用写入时限。
func writeFrame(w http.ResponseWriter, controller *http.ResponseController, event string) error {
	if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: {}\n\n", event); err != nil {
		return err
	}
	if err := controller.Flush(); err != nil {
		return err
	}
	return controller.SetWriteDeadline(time.Time{})
}
