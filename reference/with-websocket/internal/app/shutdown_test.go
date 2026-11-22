package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"with-websocket/internal/config"
)

func TestShutdownAttemptsBothTransports(t *testing.T) {
	httpErr, wsErr := errors.New("HTTP failed"), errors.New("WebSocket failed")
	got := shutdownTransports(context.Background(),
		func(context.Context) error { return httpErr },
		func(context.Context) error { return wsErr },
	)
	if !errors.Is(got, httpErr) || !errors.Is(got, wsErr) {
		t.Fatalf("shutdown lost an error: %v", got)
	}
}

func TestListenerStopsWhenWebSocketShutdownFails(t *testing.T) {
	cfg := config.Defaults()
	cfg.WSSecret = "0123456789abcdef0123456789abcdef"
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.WS.Shutdown(context.Background()) }()
	if err := app.Core.Get("/live", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	wsErr := errors.New("fixture WebSocket shutdown error")
	done := make(chan error, 1)
	go func() {
		done <- app.start(ctx, func(context.Context, string) (net.Listener, error) {
			return listener, nil
		}, func(context.Context) error { return wsErr })
	}()
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + listener.Addr().String() + "/live")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("server did not start: %d", resp.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, wsErr) {
			t.Fatalf("shutdown error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("listener kept serving after WebSocket shutdown failed")
	}
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatal("listener remained reachable")
	}
}

func TestBindFailureClosesHub(t *testing.T) {
	cfg := config.Defaults()
	cfg.WSSecret = "0123456789abcdef0123456789abcdef"
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.WS.Shutdown(context.Background()) })
	bindErr := errors.New("fixture bind error")
	got := app.start(t.Context(), func(context.Context, string) (net.Listener, error) {
		return nil, bindErr
	}, app.WS.Shutdown)
	if !errors.Is(got, bindErr) || app.WS.Hub() != nil {
		t.Fatalf("bind failure = %v, WebSocket hub still active = %v", got, app.WS.Hub() != nil)
	}
}
