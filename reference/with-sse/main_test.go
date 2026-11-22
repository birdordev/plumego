package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/birdordev/plumego/core"
)

func TestStreamingRouteFlushRejectAndDrain(t *testing.T) {
	stop, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := core.New(core.DefaultConfig(), core.AppDependencies{})
	if err := register(app, stop, 1, time.Hour); err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Stream") == "first" {
			defer close(firstDone)
		}
		app.ServeHTTP(w, r)
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Test-Stream", "first")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	reader := bufio.NewReader(response.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != "event: ready\n" {
		t.Fatalf("first frame = %q (%v)", line, err)
	}
	busy, err := client.Get(server.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	_ = busy.Body.Close()
	if busy.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("busy status = %d", busy.StatusCode)
	}
	health, err := client.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", health.StatusCode)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstDone:
	case <-time.After(3 * time.Second):
		t.Fatal("client disconnect did not release the stream")
	}
	reopened, err := client.Get(server.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Body.Close() }()
	if reopened.StatusCode != http.StatusOK {
		t.Fatalf("stream slot was not reusable: %d", reopened.StatusCode)
	}
	cancel()
	if _, err := io.ReadAll(reopened.Body); err != nil {
		t.Fatalf("stream did not end: %v", err)
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines           []time.Time
	flushedWithDeadline bool
}

func (w *deadlineRecorder) Flush() {
	w.flushedWithDeadline = len(w.deadlines) == 1 && !w.deadlines[0].IsZero()
	w.ResponseRecorder.Flush()
}

func (w *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func TestWriteFrameClearsDeadlineAfterFlush(t *testing.T) {
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	if err := writeFrame(w, http.NewResponseController(w), "heartbeat"); err != nil {
		t.Fatal(err)
	}
	if !w.flushedWithDeadline || len(w.deadlines) != 2 || !w.deadlines[1].IsZero() {
		t.Fatalf("expected a deadline during flush and no deadline afterwards: %v", w.deadlines)
	}
}
