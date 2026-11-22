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
	server := httptest.NewServer(app)
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	response, err := client.Get(server.URL + "/events")
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
	cancel()
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("stream did not end: %v", err)
	}
}
