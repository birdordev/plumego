package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/core"
	"github.com/birdordev/plumego/middleware/concurrencylimit"
	"github.com/birdordev/plumego/middleware/recovery"
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
	return app.RunContext(ctx, 10*time.Second)
}

func register(app *core.App, stop context.Context, capacity int, heartbeat time.Duration) error {
	boundedJSON := timeout.Middleware(timeout.Config{Timeout: 2 * time.Second})
	if err := app.Get("/healthz", boundedJSON(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = contract.WriteResponse(w, r, http.StatusOK, map[string]bool{"live": true}, nil)
	}))); err != nil {
		return err
	}
	limitStreams := concurrencylimit.Middleware(concurrencylimit.Config{MaxConcurrent: capacity})
	recoverStream, err := recovery.MiddlewareWithStreamAbort(recovery.Config{Logger: app.Logger()})
	if err != nil {
		return err
	}
	// Streaming responses flush before the handler returns.
	return app.Get("/events", recoverStream(limitStreams(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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
	}))))
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
