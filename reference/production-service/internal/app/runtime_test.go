package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/core"
	"github.com/birdordev/plumego/metrics"
	"production-service/internal/config"
)

func TestRuntimeRollsBackComponentsOnStartupFailure(t *testing.T) {
	a, err := New(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	startErr := errors.New("fixture cannot start")
	var calls []string
	for _, name := range []string{"db", "worker"} {
		a.components.Add(name, core.Component{
			OnStart: func(context.Context) error {
				calls = append(calls, "start:"+name)
				if name == "worker" {
					return startErr
				}
				return nil
			},
			OnStop: func(context.Context) error {
				calls = append(calls, "stop:"+name)
				return nil
			},
		})
	}
	if err := a.Start(t.Context()); !errors.Is(err, startErr) {
		t.Fatalf("failed startup: %v", err)
	}
	want := []string{"start:db", "start:worker", "stop:worker", "stop:db"}
	if !slices.Equal(calls, want) || a.ready.Load() {
		t.Fatalf("startup rollback: %v ready=%v", calls, a.ready.Load())
	}
}

func TestRuntimeReadinessTracksDependencies(t *testing.T) {
	a, err := New(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.RegisterRoutes(); err != nil {
		t.Fatal(err)
	}
	a.components.Add("fixture", core.Component{OnReady: func(context.Context) error {
		return errors.New("fixture-private-marker")
	}})
	if err := a.components.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.components.Stop(context.Background()) }()
	a.ready.Store(true)
	w := httptest.NewRecorder()
	a.Core.ServeHTTP(w, httptest.NewRequest("GET", "/readyz", nil))
	if w.Code != 503 || strings.Contains(w.Body.String(), "fixture-private-marker") {
		t.Fatalf("unready response: %d %s", w.Code, w.Body)
	}
}

func TestRuntimeDrainKeepsDependenciesUntilRequestReturns(t *testing.T) {
	a, err := New(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	requestEntered := make(chan struct{})
	requestRelease := make(chan struct{})
	defer closeIfOpen(requestRelease)
	stopped := make(chan struct{})
	a.components.Add("dependency", core.Component{OnStop: func(context.Context) error {
		close(stopped)
		return nil
	}})
	if err := a.Core.Get("/hold", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		close(requestEntered)
		select {
		case <-requestRelease:
			w.WriteHeader(http.StatusNoContent)
		case <-req.Context().Done():
		}
	})); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	done := make(chan error, 1)
	go func() {
		done <- a.start(ctx, func(context.Context, string) (net.Listener, error) { return listener, nil })
	}()
	client := &http.Client{Timeout: 4 * time.Second}
	response := make(chan int, 1)
	go func() {
		resp, err := client.Get("http://" + listener.Addr().String() + "/hold")
		if err != nil {
			response <- 0
			return
		}
		_ = resp.Body.Close()
		response <- resp.StatusCode
	}()
	select {
	case <-requestEntered:
	case <-time.After(4 * time.Second):
		t.Fatal("request did not enter handler")
	}
	cancel()
	select {
	case <-stopped:
		t.Fatal("dependency stopped before in-flight request finished")
	default:
	}
	closeIfOpen(requestRelease)
	if status := <-response; status != 204 {
		t.Fatalf("in-flight status = %d", status)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("server did not stop")
	}
	if a.ready.Load() {
		t.Fatal("app is ready after shutdown")
	}
}

func closeIfOpen(signal chan struct{}) {
	select {
	case <-signal:
	default:
		close(signal)
	}
}

func TestHTTPMetricsIncludeEarlyRejectionsAndRecoveredPanics(t *testing.T) {
	cfg := config.Defaults()
	cfg.App.BodyLimitBytes = 4
	cfg.App.RequestTimeout = 300 * time.Millisecond
	cfg.App.RateLimit = 1
	cfg.App.RateBurst = 1
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.RateLimit.Stop)
	if err := a.Core.Post("/read", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buffer := make([]byte, 32)
		_, _ = r.Body.Read(buffer)
	})); err != nil {
		t.Fatal(err)
	}
	if err := a.Core.Get("/panic", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("test panic")
	})); err != nil {
		t.Fatal(err)
	}
	if err := a.Core.Get("/timeout", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, body, peer string
		status                   int
	}{
		{"POST", "/read", "12345", "192.0.2.1:444", 413},
		{"POST", "/read", "", "192.0.2.1:444", 429},
		{"GET", "/panic", "", "192.0.2.2:444", 500},
		{"POST", "/read", "", "192.0.2.3:444", 200},
		{"GET", "/timeout", "", "192.0.2.4:444", 504},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.RemoteAddr = tc.peer
		rec := httptest.NewRecorder()
		a.Core.ServeHTTP(rec, r)
		if rec.Code != tc.status {
			t.Fatalf("%s status=%d, want %d", tc.path, rec.Code, tc.status)
		}
	}
	stats := a.Metrics.GetStats()
	if stats.NameBreakdown[metrics.MetricHTTPRequest] != 5 || stats.ErrorRecords != 4 {
		t.Fatalf("request stats = %+v", stats)
	}
}
