package gateway

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHealthReusesConnectionsWithShortBodies(t *testing.T) {
	for _, sendBody := range []bool{false, true} {
		t.Run(fmt.Sprint(sendBody), func(t *testing.T) {
			const checks = 12
			var connections atomic.Int64
			origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if sendBody {
					_, _ = io.WriteString(w, "ready")
				}
			}))
			origin.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					connections.Add(1)
				}
			}
			origin.Start()
			defer origin.Close()
			pool, err := NewBackendPool([]string{origin.URL})
			if err != nil {
				t.Fatal(err)
			}
			h := NewHealthChecker(pool, &HealthCheckConfig{Method: "GET"})
			defer h.Stop()
			for range checks {
				if !h.Check(pool.Backends()[0]) {
					t.Fatal("health check failed")
				}
			}
			if got := connections.Load(); got != 1 {
				t.Fatalf("accepted %d connections for %d probes", got, checks)
			}
		})
	}
}

func TestGatewaySupportsHTTP2ALPN(t *testing.T) {
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test-Proto", r.Proto)
		w.WriteHeader(http.StatusNoContent)
	}))
	origin.EnableHTTP2 = true
	origin.StartTLS()
	defer origin.Close()
	direct, err := origin.Client().Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Body.Close()

	cfg := DefaultTransportConfig()
	cfg.TLSClientConfig = origin.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	cfg.TLSClientConfig.NextProtos = []string{"h2", "http/1.1"}
	proxy, err := NewGateway(GatewayConfig{Targets: []string{origin.URL}, Transport: cfg})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusNoContent || w.Header().Get("X-Test-Proto") != "HTTP/2.0" || direct.ProtoMajor != 2 {
		t.Fatalf("proxy returned status %d and upstream protocol %q", w.Code, w.Header().Get("X-Test-Proto"))
	}
}

type firstBodyWriter struct {
	http.ResponseWriter
	bodySent chan struct{}
	once     sync.Once
}

func (w *firstBodyWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.once.Do(func() { close(w.bodySent) })
	return n, err
}

func (w *firstBodyWriter) Flush() {
	w.ResponseWriter.(http.Flusher).Flush()
}

func TestGatewayFlushesSSEBeforeOriginCloses(t *testing.T) {
	upstreamEnd := make(chan struct{})
	var endOnce sync.Once
	endUpstream := func() { endOnce.Do(func() { close(upstreamEnd) }) }
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-upstreamEnd:
		case <-r.Context().Done():
		}
	}))
	defer origin.Close()
	p, err := NewGateway(GatewayConfig{Targets: []string{origin.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	bodySent := make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.ServeHTTP(&firstBodyWriter{ResponseWriter: w, bodySent: bodySent}, r)
	}))
	defer proxy.Close()
	defer endUpstream()
	type outcome struct {
		response *http.Response
		err      error
	}
	finished := make(chan outcome, 1)
	go func() {
		resp, err := proxy.Client().Get(proxy.URL)
		finished <- outcome{resp, err}
	}()
	select {
	case <-bodySent:
	case <-time.After(2 * time.Second):
		t.Fatal("proxy did not copy upstream bytes")
	}
	var result outcome
	select {
	case result = <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("response blocked until stream completion")
	}
	if result.err != nil {
		t.Fatal(result.err)
	}
	defer result.response.Body.Close()
	raw := make([]byte, len("data: first\n\n"))
	_, readErr := io.ReadFull(result.response.Body, raw)
	if readErr != nil || string(raw) != "data: first\n\n" {
		t.Fatalf("unexpected response: %q, %v", raw, readErr)
	}
}
