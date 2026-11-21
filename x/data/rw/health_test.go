package rw

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewHealthChecker(t *testing.T) {
	config := HealthCheckConfig{
		Enabled:           true,
		Interval:          10 * time.Second,
		Timeout:           2 * time.Second,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
	}

	hc := NewHealthChecker(config)
	if hc == nil {
		t.Fatal("expected health checker, got nil")
	}

	if hc.config.Interval != config.Interval {
		t.Errorf("interval = %v, want %v", hc.config.Interval, config.Interval)
	}
}

func TestHealthCheckerGetCounts(t *testing.T) {
	config := DefaultHealthCheckConfig()
	hc := NewHealthChecker(config)

	// Set failure count
	hc.mu.Lock()
	hc.failures[0] = 5
	hc.successes[0] = 3
	hc.mu.Unlock()

	if got := hc.GetFailureCount(0); got != 5 {
		t.Errorf("failure count = %d, want 5", got)
	}

	if got := hc.GetSuccessCount(0); got != 3 {
		t.Errorf("success count = %d, want 3", got)
	}
}

func TestHealthCheckerReset(t *testing.T) {
	config := DefaultHealthCheckConfig()
	hc := NewHealthChecker(config)

	// Set some failures
	hc.mu.Lock()
	hc.failures[0] = 5
	hc.successes[1] = 3
	hc.mu.Unlock()

	// Reset
	hc.Reset()

	// Counts should be cleared
	if got := hc.GetFailureCount(0); got != 0 {
		t.Errorf("after reset, failure count = %d, want 0", got)
	}
	if got := hc.GetSuccessCount(1); got != 0 {
		t.Errorf("after reset, success count = %d, want 0", got)
	}
}

func TestHealthCheckerCheckAll(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	// Create one healthy and one failing replica
	healthyReplica := newStubDB()
	defer healthyReplica.Close()

	failingReplica := sql.OpenDB(&stubConnector{
		conn: &stubConn{pingErr: errors.New("connection failed")},
	})
	defer failingReplica.Close()

	config := Config{
		Primary:  primary,
		Replicas: []*sql.DB{healthyReplica, failingReplica},
		HealthCheck: HealthCheckConfig{
			Enabled:           false, // Don't start background checker
			Interval:          1 * time.Second,
			Timeout:           100 * time.Millisecond,
			FailureThreshold:  2,
			RecoveryThreshold: 2,
		},
	}

	cluster, err := New(config)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cluster.Close()

	hc := NewHealthChecker(config.HealthCheck)

	// Run health check multiple times to exceed failure threshold
	for i := 0; i < 3; i++ {
		hc.checkAll(t.Context(), cluster)
		time.Sleep(10 * time.Millisecond) // Small delay
	}

	// Healthy replica should remain healthy
	if !cluster.ReplicaHealth()[0] {
		t.Error("expected healthy replica to remain healthy")
	}

	// Failing replica should be marked unhealthy
	if cluster.ReplicaHealth()[1] {
		t.Error("expected failing replica to be marked unhealthy")
	}

	// Check metrics
	if cluster.metrics.HealthCheckCount.Load() == 0 {
		t.Error("expected health check count > 0")
	}
}

func TestHealthCheckerRecovery(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	replica := newStubDB()
	defer replica.Close()

	config := Config{
		Primary:  primary,
		Replicas: []*sql.DB{replica},
		HealthCheck: HealthCheckConfig{
			Enabled:           false,
			Interval:          1 * time.Second,
			Timeout:           100 * time.Millisecond,
			FailureThreshold:  2,
			RecoveryThreshold: 2,
		},
	}

	cluster, err := New(config)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cluster.Close()

	// Mark replica as unhealthy
	cluster.markReplicaHealth(0, false)
	if cluster.ReplicaHealth()[0] {
		t.Fatal("failed to mark replica as unhealthy")
	}

	hc := NewHealthChecker(config.HealthCheck)

	// Run health checks to trigger recovery
	for i := 0; i < 3; i++ {
		hc.checkAll(t.Context(), cluster)
		time.Sleep(10 * time.Millisecond)
	}

	// Replica should recover
	if !cluster.ReplicaHealth()[0] {
		t.Error("expected replica to recover to healthy state")
	}
}

func TestHealthCheckerStartStop(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	replica := newStubDB()
	defer replica.Close()

	config := Config{
		Primary:  primary,
		Replicas: []*sql.DB{replica},
		HealthCheck: HealthCheckConfig{
			Enabled:           true,
			Interval:          50 * time.Millisecond,
			Timeout:           10 * time.Millisecond,
			FailureThreshold:  2,
			RecoveryThreshold: 2,
		},
	}

	cluster, err := New(config)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}

	// Health checker should be running
	if cluster.health == nil {
		t.Fatal("expected health checker to be started")
	}

	// Wait for at least one health check
	time.Sleep(100 * time.Millisecond)

	// Stop cluster (which stops health checker)
	cluster.Close()

	// Should not panic after close
	time.Sleep(100 * time.Millisecond)
}

func TestHealthCheckerStopIdempotent(t *testing.T) {
	hc := NewHealthChecker(HealthCheckConfig{
		Enabled:           true,
		Interval:          time.Hour,
		Timeout:           10 * time.Millisecond,
		FailureThreshold:  1,
		RecoveryThreshold: 1,
	})

	hc.Stop()
	hc.Stop()
}

func TestHealthCheckerDisabled(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	replica := newStubDB()
	defer replica.Close()

	config := Config{
		Primary:  primary,
		Replicas: []*sql.DB{replica},
		HealthCheck: HealthCheckConfig{
			Enabled:  false,
			Interval: 30 * time.Second, // Set other fields to avoid defaults
		},
	}

	cluster, err := New(config)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cluster.Close()

	// Health checker should not be started when explicitly disabled
	if cluster.health != nil {
		t.Errorf("expected health checker to be nil when disabled, got %v", cluster.health)
	}
}

func TestHealthCheckerContextCancellation(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	replica := newStubDB()
	defer replica.Close()

	config := HealthCheckConfig{
		Enabled:           true,
		Interval:          10 * time.Millisecond,
		Timeout:           5 * time.Millisecond,
		FailureThreshold:  2,
		RecoveryThreshold: 2,
	}

	hc := NewHealthChecker(config)

	cluster := &Cluster{
		primary:       primary,
		replicas:      []*sql.DB{replica},
		replicaHealth: []bool{true},
	}

	// Start with cancellable context
	ctx, cancel := context.WithCancel(t.Context())
	hc.Start(ctx, cluster)

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Wait for goroutine to stop
	time.Sleep(50 * time.Millisecond)

	// Should not panic
}

func TestHealthCheckConfigDefaults(t *testing.T) {
	config := DefaultHealthCheckConfig()

	if !config.Enabled {
		t.Error("expected Enabled to be true")
	}
	if config.Interval != 30*time.Second {
		t.Errorf("Interval = %v, want 30s", config.Interval)
	}
	if config.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", config.Timeout)
	}
	if config.FailureThreshold != 3 {
		t.Errorf("FailureThreshold = %d, want 3", config.FailureThreshold)
	}
	if config.RecoveryThreshold != 2 {
		t.Errorf("RecoveryThreshold = %d, want 2", config.RecoveryThreshold)
	}
}

func TestHealthCheckerConcurrency(t *testing.T) {
	config := DefaultHealthCheckConfig()
	hc := NewHealthChecker(config)

	// Concurrent access to health checker
	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			hc.mu.Lock()
			hc.failures[0] = i
			hc.mu.Unlock()
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			_ = hc.GetFailureCount(0)
			_ = hc.IsHealthy(0)
		}
		done <- true
	}()

	// Wait for both
	<-done
	<-done

	// Should not panic or race
}

func BenchmarkHealthCheckerCheckAll(b *testing.B) {
	primary := newStubDB()
	defer primary.Close()

	replicas := make([]*sql.DB, 5)
	for i := range replicas {
		replicas[i] = newStubDB()
		defer replicas[i].Close()
	}

	config := Config{
		Primary:  primary,
		Replicas: replicas,
		HealthCheck: HealthCheckConfig{
			Enabled:           false,
			Timeout:           100 * time.Millisecond,
			FailureThreshold:  3,
			RecoveryThreshold: 2,
		},
	}

	cluster, _ := New(config)
	defer cluster.Close()

	hc := NewHealthChecker(config.HealthCheck)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hc.checkAll(b.Context(), cluster)
	}
}

type healthPingConn struct {
	stubConn
	ping func(context.Context) error
}

func (c *healthPingConn) Ping(ctx context.Context) error { return c.ping(ctx) }

type healthPingConnector struct{ conn *healthPingConn }

func (c healthPingConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }
func (healthPingConnector) Driver() driver.Driver                          { return stubDriver{} }

func TestHealthCheckCancelsActivePing(t *testing.T) {
	for _, closeCluster := range []bool{false, true} {
		name := "cancel parent"
		if closeCluster {
			name = "close cluster"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			var laterPings atomic.Int32
			first := sql.OpenDB(healthPingConnector{&healthPingConn{ping: func(ctx context.Context) error {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			}}})
			next := sql.OpenDB(healthPingConnector{&healthPingConn{ping: func(context.Context) error {
				laterPings.Add(1)
				return nil
			}}})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			config := DefaultHealthCheckConfig()
			config.Enabled = true
			config.Interval = time.Millisecond
			config.Timeout = 5 * time.Second
			cluster, err := New(Config{Primary: newStubDB(), Replicas: []*sql.DB{first, next}, HealthCheck: config, HealthCheckContext: ctx})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cluster.Close() })
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("health check did not start")
			}
			done := make(chan struct{})
			if closeCluster {
				go func() { _ = cluster.Close(); close(done) }()
			} else {
				cancel()
				go func() { cluster.health.wg.Wait(); close(done) }()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("active ping ignored shutdown")
			}
			if laterPings.Load() != 0 || cluster.health.GetFailureCount(0) != 0 {
				t.Fatal("canceling a ping caused an extra probe or a health failure")
			}
		})
	}
}

func TestHealthCheckerIntervalConfiguration(t *testing.T) {
	db := sql.OpenDB(&stubConnector{conn: &stubConn{}})
	defer db.Close()
	for _, interval := range []time.Duration{-time.Second, 0, time.Second} {
		config := HealthCheckConfig{Enabled: true, Interval: interval}
		checker := NewHealthChecker(config)
		want := interval
		if want <= 0 {
			want = DefaultHealthCheckConfig().Interval
		}
		if checker.config.Interval != want {
			t.Fatalf("interval %v normalized to %v, want %v", interval, checker.config.Interval, want)
		}
		checker.Start(t.Context(), &Cluster{})
		checker.Stop()
	}
	c, err := New(Config{Primary: db, Replicas: []*sql.DB{db}, HealthCheck: HealthCheckConfig{Enabled: true, Interval: -time.Second}})
	if c != nil || err == nil {
		t.Fatalf("cluster with negative interval: %v, %v", c, err)
	}
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("caller-owned database: %v", err)
	}
}

func TestCheckerHealthTracksRecoveryThreshold(t *testing.T) {
	replica := &stubConn{}
	cluster, err := New(Config{
		Primary:  newStubDB(),
		Replicas: []*sql.DB{sql.OpenDB(&stubConnector{conn: replica})},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Close()

	checker := NewHealthChecker(HealthCheckConfig{
		FailureThreshold: 2, RecoveryThreshold: 2, Timeout: time.Second,
	})
	assertHealth := func(stage string, want bool) {
		t.Helper()
		if routed, observed := cluster.ReplicaHealth()[0], checker.IsHealthy(0); routed != want || observed != want {
			t.Errorf("%s: cluster=%t checker=%t; want both %t", stage, routed, observed, want)
		}
	}

	assertHealth("before any check", true)
	replica.pingErr = errors.New("replica unavailable")
	checker.checkAll(t.Context(), cluster)
	assertHealth("first failure", true)
	checker.checkAll(t.Context(), cluster)
	assertHealth("second failure", false)

	replica.pingErr = nil
	checker.checkAll(t.Context(), cluster)
	assertHealth("first success", false)
	checker.checkAll(t.Context(), cluster)
	assertHealth("second success", true)

	replica.pingErr = errors.New("replica unavailable")
	checker.checkAll(t.Context(), cluster)
	checker.checkAll(t.Context(), cluster)
	assertHealth("failed again", false)
	checker.Reset()
	if !checker.IsHealthy(0) || checker.GetFailureCount(0) != 0 || checker.GetSuccessCount(0) != 0 {
		t.Fatal("Reset did not restore the checker's initial state")
	}
	if cluster.ReplicaHealth()[0] {
		t.Fatal("Reset changed the cluster routing state")
	}
	checker.checkAll(t.Context(), cluster)
	assertHealth("first check after reset", false)
}
