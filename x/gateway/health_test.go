package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// TestHealthCheckerCheck performs an immediate check on a live backend.
func TestHealthCheckerCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	pool, _ := NewBackendPool([]string{srv.URL})
	checker := NewHealthChecker(pool, &HealthCheckConfig{
		Path:           "/",
		ExpectedStatus: http.StatusOK,
		Timeout:        2 * time.Second,
	})

	backend := pool.Backends()[0]
	healthy := checker.Check(backend)
	if !healthy {
		t.Error("backend should be healthy")
	}
}

// TestHealthCheckerCheckUnhealthy checks a backend returning non-200.
func TestHealthCheckerCheckUnhealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	pool, _ := NewBackendPool([]string{srv.URL})
	checker := NewHealthChecker(pool, &HealthCheckConfig{
		Path:           "/",
		ExpectedStatus: http.StatusOK,
		Timeout:        2 * time.Second,
	})

	backend := pool.Backends()[0]
	healthy := checker.Check(backend)
	if healthy {
		t.Error("backend should be unhealthy when status != expected")
	}
}

// TestHealthCheckerCheckAll checks all backends at once.
func TestHealthCheckerCheckAll(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer healthy.Close()

	unhealthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer unhealthy.Close()

	pool, _ := NewBackendPool([]string{healthy.URL, unhealthy.URL})
	checker := NewHealthChecker(pool, &HealthCheckConfig{
		Path:           "/",
		ExpectedStatus: http.StatusOK,
		Timeout:        2 * time.Second,
	})

	checker.CheckAll()

	backends := pool.Backends()
	if !backends[0].IsHealthy() {
		t.Error("first backend should be healthy")
	}
	if backends[1].IsHealthy() {
		t.Error("second backend should be unhealthy")
	}
}

// TestHealthCheckerOnHealthChange verifies the callback fires on status change.
func TestHealthCheckerOnHealthChange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	pool, _ := NewBackendPool([]string{srv.URL})

	var changed bool
	checker := NewHealthChecker(pool, &HealthCheckConfig{
		Path:           "/",
		ExpectedStatus: http.StatusOK,
		Timeout:        2 * time.Second,
		OnHealthChange: func(b *Backend, h bool) {
			changed = true
		},
	})

	// Backend starts healthy; check returns unhealthy → triggers callback
	checker.Check(pool.Backends()[0])
	if !changed {
		t.Error("OnHealthChange should have been called")
	}
}

// TestHealthCheckerStartStop verifies the background goroutine lifecycle.
func TestHealthCheckerStartStop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	pool, _ := NewBackendPool([]string{srv.URL})
	checker := NewHealthChecker(pool, &HealthCheckConfig{
		Path:           "/",
		ExpectedStatus: http.StatusOK,
		Interval:       50 * time.Millisecond,
		Timeout:        1 * time.Second,
	})

	checker.Start()

	// Let at least one tick happen
	time.Sleep(120 * time.Millisecond)

	// Stop should not hang
	done := make(chan struct{})
	go func() {
		checker.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() timed out")
	}
}

// TestHealthCheckerStopIdempotent verifies Stop can be called multiple times.
func TestHealthCheckerStopIdempotent(t *testing.T) {
	pool, _ := NewBackendPool([]string{"http://localhost:9999"})
	checker := NewHealthChecker(pool, &HealthCheckConfig{
		Path:     "/health",
		Timeout:  100 * time.Millisecond,
		Interval: 10 * time.Second,
	})
	checker.Start()
	checker.Stop()
	checker.Stop() // second call should not panic
}

// TestHealthCheckerUnreachableBackend verifies unreachable backends are marked unhealthy.
func TestHealthCheckerUnreachableBackend(t *testing.T) {
	pool, _ := NewBackendPool([]string{"http://127.0.0.1:19999"}) // nothing listening
	checker := NewHealthChecker(pool, &HealthCheckConfig{
		Path:           "/health",
		ExpectedStatus: http.StatusOK,
		Timeout:        200 * time.Millisecond,
	})

	backend := pool.Backends()[0]
	healthy := checker.Check(backend)
	if healthy {
		t.Error("unreachable backend should be unhealthy")
	}
}

// TestHealthCheckerDefaultConfig verifies defaults are applied.
func TestHealthCheckerDefaultConfig(t *testing.T) {
	pool, _ := NewBackendPool([]string{"http://localhost:9999"})
	checker := NewHealthChecker(pool, &HealthCheckConfig{})

	if checker.config.Path != "/health" {
		t.Errorf("default path = %q, want /health", checker.config.Path)
	}
	if checker.config.Interval != 10*time.Second {
		t.Errorf("default interval = %v, want 10s", checker.config.Interval)
	}
	if checker.config.Timeout != 5*time.Second {
		t.Errorf("default timeout = %v, want 5s", checker.config.Timeout)
	}
	if checker.config.ExpectedStatus != http.StatusOK {
		t.Errorf("default status = %d, want 200", checker.config.ExpectedStatus)
	}
}

type healthTestTransport func(*http.Request) (*http.Response, error)

func (f healthTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHealthCheckerIgnoresOlderResult(t *testing.T) {
	for _, tc := range []struct {
		name       string
		old, newer int
	}{
		{"late success", http.StatusOK, http.StatusServiceUnavailable},
		{"late failure", http.StatusServiceUnavailable, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				pool := mustPool(t, "http://first.example", "http://other.example")
				backends := pool.Backends()
				backend, other := backends[0], backends[1]
				wantHealthy := tc.newer == http.StatusOK
				backend.SetHealthy(!wantHealthy)
				other.SetHealthy(false)
				type change struct {
					backend *Backend
					healthy bool
				}
				changes := make(chan change, 4)
				checker := NewHealthChecker(pool, &HealthCheckConfig{
					OnHealthChange: func(b *Backend, healthy bool) { changes <- change{b, healthy} },
				})
				defer checker.Stop()
				started, resume := make(chan struct{}), make(chan struct{})
				calls := 0
				checker.client.Transport = healthTestTransport(func(r *http.Request) (*http.Response, error) {
					status := http.StatusOK
					if r.URL.Host == "first.example" {
						calls++
						status = tc.newer
						if calls == 1 {
							close(started)
							<-resume
							status = tc.old
						}
					}
					return &http.Response{StatusCode: status, Body: http.NoBody}, nil
				})

				oldResult := make(chan bool, 1)
				go func() { oldResult <- checker.Check(backend) }()
				<-started
				newResult := checker.Check(backend)
				otherResult := checker.Check(other)
				close(resume)
				if got := <-oldResult; got != wantHealthy || newResult != wantHealthy || !otherResult || backend.IsHealthy() != wantHealthy {
					t.Errorf("old = %v, new = %v, other = %v, current = %v; want primary %v and other healthy", got, newResult, otherResult, backend.IsHealthy(), wantHealthy)
				}
				if len(changes) != 2 {
					t.Fatalf("health changes = %d, want 2", len(changes))
				}
				if got := <-changes; got != (change{backend, wantHealthy}) {
					t.Errorf("primary change = %+v", got)
				}
				if got := <-changes; got != (change{other, true}) {
					t.Errorf("other change = %+v", got)
				}
			})
		})
	}
}

func TestHealthCheckerDoesNotStartAfterStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pool, err := NewBackendPool([]string{"http://example.invalid"})
		if err != nil {
			t.Fatal(err)
		}
		checker := NewHealthChecker(pool, &HealthCheckConfig{Interval: time.Hour})
		calls := 0
		checker.client.Transport = healthTestTransport(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
		})
		checker.Stop()
		checker.Start()
		synctest.Wait()
		if calls != 0 {
			t.Errorf("background probes after Stop = %d, want 0", calls)
		}
		checker.Check(pool.Backends()[0])
		checker.CheckAll()
		if calls != 2 {
			t.Errorf("manual probes after Stop = %d, want 2", calls)
		}
	})
}

func TestHealthCheckerStopCancelsProbeWithoutMarkingUnhealthy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pool, err := NewBackendPool([]string{"http://example.invalid"})
		if err != nil {
			t.Fatal(err)
		}
		checker := NewHealthChecker(pool, &HealthCheckConfig{Interval: time.Hour, Timeout: time.Minute})
		started := make(chan struct{}, 2)
		results := make(chan error, 2)
		checker.client.Transport = healthTestTransport(func(r *http.Request) (*http.Response, error) {
			started <- struct{}{}
			<-r.Context().Done()
			err := r.Context().Err()
			results <- err
			return nil, err
		})
		checker.Start()
		<-started
		checker.Start()
		synctest.Wait()
		if len(started) != 0 {
			t.Error("repeated Start launched another probe")
		}
		checker.Stop()
		if got := <-results; got != context.Canceled {
			t.Errorf("probe error = %v, want canceled", got)
		}
		if !pool.Backends()[0].IsHealthy() {
			t.Error("Stop marked backend unhealthy")
		}
	})
}

func TestHealthCheckerProbeTimeoutMarksUnhealthy(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pool, err := NewBackendPool([]string{"http://example.invalid"})
		if err != nil {
			t.Fatal(err)
		}
		updates := make(chan bool, 1)
		checker := NewHealthChecker(pool, &HealthCheckConfig{
			Interval:       time.Hour,
			Timeout:        time.Second,
			OnHealthChange: func(_ *Backend, healthy bool) { updates <- healthy },
		})
		checker.client.Transport = healthTestTransport(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		})
		checker.Start()
		defer checker.Stop()
		if healthy := <-updates; healthy || pool.Backends()[0].IsHealthy() {
			t.Error("probe timeout did not mark backend unhealthy")
		}
	})
}

func TestGatewayHealthIntervalConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name           string
		interval, want time.Duration
	}{
		{"default", 0, 10 * time.Second},
		{"negative", -time.Second, 10 * time.Second},
		{"explicit", time.Minute, time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				pool, err := NewBackendPool(nil)
				if err != nil {
					t.Fatal(err)
				}
				checker := NewHealthChecker(pool, &HealthCheckConfig{Interval: tc.interval})
				if checker.config.Interval != tc.want {
					t.Fatalf("interval = %v, want %v", checker.config.Interval, tc.want)
				}
				checker.Start()
				synctest.Wait()
				checker.Stop()
			})
		})
	}
}
