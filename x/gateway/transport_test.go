package gateway

import (
	"crypto/tls"
	"sync"
	"testing"
	"time"
)

func TestNewTransportPoolNilConfig(t *testing.T) {
	pool := NewTransportPool(nil)
	if pool == nil {
		t.Fatal("expected non-nil pool")
	}
}

func TestTransportPoolGet(t *testing.T) {
	pool := NewTransportPool(DefaultTransportConfig())

	tr := pool.Get("http://backend:8080")
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}

	// Same URL returns same transport instance
	tr2 := pool.Get("http://backend:8080")
	if tr != tr2 {
		t.Error("expected same transport instance for same URL")
	}
}

func TestTransportPoolGetDifferentURLs(t *testing.T) {
	pool := NewTransportPool(DefaultTransportConfig())
	trA := pool.Get("http://a:8080")
	trB := pool.Get("http://b:8080")
	if trA == trB {
		t.Error("different URLs should yield different transports")
	}
	if pool.Count() != 2 {
		t.Errorf("Count = %d, want 2", pool.Count())
	}
}

func TestTransportPoolConfigApplied(t *testing.T) {
	cfg := &TransportConfig{
		MaxIdleConns:          50,
		MaxIdleConnsPerHost:   5,
		IdleConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 500 * time.Millisecond,
		DisableKeepAlives:     true,
		DisableCompression:    true,
		DialTimeout:           15 * time.Second,
		DialKeepAlive:         15 * time.Second,
	}
	pool := NewTransportPool(cfg)
	tr := pool.Get("http://backend:8080")

	if tr.MaxIdleConns != 50 {
		t.Errorf("MaxIdleConns = %d, want 50", tr.MaxIdleConns)
	}
	if tr.MaxIdleConnsPerHost != 5 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 5", tr.MaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout != 60*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 60s", tr.IdleConnTimeout)
	}
	if !tr.DisableKeepAlives {
		t.Error("DisableKeepAlives should be true")
	}
	if !tr.DisableCompression {
		t.Error("DisableCompression should be true")
	}
}

func TestTransportPoolRemove(t *testing.T) {
	pool := NewTransportPool(DefaultTransportConfig())
	pool.Get("http://a:8080")
	pool.Get("http://b:8080")

	pool.Remove("http://a:8080")
	if pool.Count() != 1 {
		t.Errorf("Count after Remove = %d, want 1", pool.Count())
	}

	// Removing non-existent is a no-op
	pool.Remove("http://missing:8080")
	if pool.Count() != 1 {
		t.Errorf("Count after no-op Remove = %d, want 1", pool.Count())
	}
}

func TestTransportPoolClose(t *testing.T) {
	pool := NewTransportPool(DefaultTransportConfig())
	pool.Get("http://a:8080")
	pool.Get("http://b:8080")

	pool.Close()
	if pool.Count() != 0 {
		t.Errorf("Count after Close = %d, want 0", pool.Count())
	}

	// Get after close should create new transport
	tr := pool.Get("http://a:8080")
	if tr == nil {
		t.Error("should create new transport after close")
	}
}

func TestTransportPoolCloseIdleConnections(t *testing.T) {
	pool := NewTransportPool(DefaultTransportConfig())
	pool.Get("http://a:8080")
	// Should not panic
	pool.CloseIdleConnections()
}

func TestTransportPoolConcurrentGet(t *testing.T) {
	pool := NewTransportPool(DefaultTransportConfig())

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr := pool.Get("http://shared:8080")
			if tr == nil {
				t.Errorf("expected non-nil transport")
			}
		}()
	}
	wg.Wait()

	// All goroutines should have received the same transport
	if pool.Count() != 1 {
		t.Errorf("Count = %d, want 1 (concurrent creation should not duplicate)", pool.Count())
	}
}

func TestTransportPoolOwnsConfigSnapshot(t *testing.T) {
	cfg := DefaultTransportConfig()
	cfg.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	wantTimeout := cfg.ResponseHeaderTimeout
	pool := NewTransportPool(cfg)
	t.Cleanup(pool.Close)
	first := pool.Get("http://first.example")

	cfg.ResponseHeaderTimeout = time.Second
	cfg.TLSClientConfig.MinVersion = tls.VersionTLS13
	second := pool.Get("http://second.example")
	for _, tr := range []struct {
		timeout   time.Duration
		tlsConfig *tls.Config
	}{{first.ResponseHeaderTimeout, first.TLSClientConfig}, {second.ResponseHeaderTimeout, second.TLSClientConfig}} {
		if tr.timeout != wantTimeout || tr.tlsConfig == nil || tr.tlsConfig.MinVersion != tls.VersionTLS12 {
			t.Errorf("transport timeout = %v, TLS config = %+v", tr.timeout, tr.tlsConfig)
		}
	}
}
