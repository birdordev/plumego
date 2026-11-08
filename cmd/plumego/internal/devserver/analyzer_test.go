package devserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/observability/devtools"
)

func TestGetAppSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_debug/config" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(struct {
			Data devtools.ConfigSnapshot `json:"data"`
		}{
			Data: devtools.ConfigSnapshot{
				Debug:   true,
				EnvFile: ".env.test",
				RuntimeSnapshot: devtools.RuntimeSnapshot{
					Addr:              ":8080",
					ReadTimeout:       30 * time.Second,
					ReadHeaderTimeout: 5 * time.Second,
					WriteTimeout:      30 * time.Second,
					IdleTimeout:       60 * time.Second,
					MaxHeaderBytes:    1048576,
					HTTP2Enabled:      true,
					DrainInterval:     500 * time.Millisecond,
					PreparationState:  "server_prepared",
					TLS: devtools.TLSSnapshot{
						Enabled:  true,
						CertFile: "cert.pem",
						KeyFile:  "key.pem",
					},
				},
			},
		})
	}))
	defer srv.Close()

	analyzer := NewAnalyzer(srv.URL)
	snapshot, err := analyzer.GetAppSnapshot()
	if err != nil {
		t.Fatalf("GetAppSnapshot returned error: %v", err)
	}

	if snapshot.Addr != ":8080" {
		t.Fatalf("addr = %q, want %q", snapshot.Addr, ":8080")
	}
	if snapshot.EnvFile != ".env.test" {
		t.Fatalf("env_file = %q, want %q", snapshot.EnvFile, ".env.test")
	}
	if snapshot.PreparationState != "server_prepared" {
		t.Fatalf("unexpected preparation_state: %+v", snapshot)
	}
	if !snapshot.TLS.Enabled || snapshot.TLS.CertFile != "cert.pem" || snapshot.TLS.KeyFile != "key.pem" {
		t.Fatalf("unexpected tls snapshot: %+v", snapshot.TLS)
	}
}

func TestAnalyzerConfigResponseBodyIsBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_debug/config" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":"too large"}`))
	}))
	defer srv.Close()

	analyzer := NewAnalyzer(srv.URL)
	analyzer.maxBodyBytes = 4

	_, err := analyzer.GetAppSnapshot()
	if err == nil {
		t.Fatal("expected oversized config response to fail")
	}
	if !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("expected bounded response error, got: %v", err)
	}
}

func TestAnalyzerHealthResponseBodyIsBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	analyzer := NewAnalyzer(srv.URL)
	analyzer.maxBodyBytes = 4

	_, _, err := analyzer.HealthCheck()
	if err == nil {
		t.Fatal("expected oversized health response to fail")
	}
	if !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("expected bounded response error, got: %v", err)
	}
}

func TestAnalyzerMetricsResponseBodyIsBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_debug/metrics" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"enabled":true}`))
	}))
	defer srv.Close()

	analyzer := NewAnalyzer(srv.URL)
	analyzer.maxBodyBytes = 4

	_, err := analyzer.GetDevMetrics()
	if err == nil {
		t.Fatal("expected oversized metrics response to fail")
	}
	if !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("expected bounded response error, got: %v", err)
	}
}

func TestAnalyzerRoutesResponseBodyIsBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_debug/routes.json" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":{"routes":[]}}`))
	}))
	defer srv.Close()

	analyzer := NewAnalyzer(srv.URL)
	analyzer.maxBodyBytes = 4

	_, err := analyzer.GetRoutes()
	if err == nil {
		t.Fatal("expected oversized routes response to fail")
	}
	if !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("expected bounded response error, got: %v", err)
	}
}

func TestAnalyzerConfigStatusIsChecked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_debug/config" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	analyzer := NewAnalyzer(srv.URL)

	_, err := analyzer.GetAppSnapshot()
	if err == nil {
		t.Fatal("expected non-200 config response to fail")
	}
	if !strings.Contains(err.Error(), "config endpoint returned status 503") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoAPITestUsesCallerContext(t *testing.T) {
	analyzer := NewAnalyzer("http://127.0.0.1:1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := analyzer.DoAPITest(ctx, APITestRequest{Method: http.MethodGet, Path: "/"})
	if err == nil {
		t.Fatal("expected canceled context error")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected context canceled error, got %v", err)
	}
}
