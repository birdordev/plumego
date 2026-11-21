package healthhttp

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/birdordev/plumego/health"
)

type flakyChecker struct {
	name      string
	failCount int32
	attempts  int32
}

func (fc *flakyChecker) Name() string {
	return fc.name
}

func (fc *flakyChecker) Check(context.Context) error {
	atomic.AddInt32(&fc.attempts, 1)
	if atomic.LoadInt32(&fc.attempts) <= fc.failCount {
		return errors.New("flaky failure")
	}
	return nil
}

type delayChecker struct {
	name  string
	delay time.Duration
}

func (dc *delayChecker) Name() string {
	return dc.name
}

func (dc *delayChecker) Check(ctx context.Context) error {
	if dc.delay <= 0 {
		return nil
	}
	select {
	case <-time.After(dc.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestManagerReadiness(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	if status := manager.Readiness(); status.Ready {
		t.Fatalf("expected initial not-ready status, got ready")
	} else if status.Timestamp.IsZero() {
		t.Fatalf("expected initial readiness timestamp")
	}

	manager.MarkReady()
	if ready := manager.Readiness(); !ready.Ready || ready.Reason != "" {
		t.Fatalf("expected ready without reason, got %+v", ready)
	}

	manager.MarkNotReady("maintenance")
	notReady := manager.Readiness()
	if notReady.Ready || notReady.Reason != "maintenance" {
		t.Fatalf("unexpected readiness after MarkNotReady: %+v", notReady)
	}
}

func TestManagerCheckComponentRetry(t *testing.T) {
	manager, err := NewManager(Config{
		RetryCount: 2,
		RetryDelay: 0,
	})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	checker := &flakyChecker{name: "flaky", failCount: 1}
	if err := manager.RegisterComponent(checker); err != nil {
		t.Fatalf("failed to register component: %v", err)
	}

	if err := manager.CheckComponent(t.Context(), "flaky"); err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}

	if attempts := atomic.LoadInt32(&checker.attempts); attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}

	component, ok := manager.GetComponentHealth("flaky")
	if !ok {
		t.Fatalf("expected component health")
	}
	attemptsVal, ok := component.Details["check_attempts"].(int)
	if !ok || attemptsVal != 2 {
		t.Fatalf("expected check_attempts to be 2, got %v", component.Details["check_attempts"])
	}
}

func TestManagerCheckComponentTimeout(t *testing.T) {
	manager, err := NewManager(Config{
		Timeout: 30 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	checker := &delayChecker{name: "slow", delay: 100 * time.Millisecond}
	if err := manager.RegisterComponent(checker); err != nil {
		t.Fatalf("failed to register component: %v", err)
	}

	err = manager.CheckComponent(t.Context(), "slow")
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected deadline error, got %v", err)
	}

	component, ok := manager.GetComponentHealth("slow")
	if !ok {
		t.Fatalf("expected component health")
	}
	if component.Status != health.StatusUnhealthy {
		t.Fatalf("expected unhealthy status, got %v", component.Status)
	}
	if attemptsVal, ok := component.Details["check_attempts"].(int); !ok || attemptsVal != 1 {
		t.Fatalf("expected check_attempts to be 1, got %v", component.Details["check_attempts"])
	}
}

func TestManagerSetConfigPreservesStateOnRejection(t *testing.T) {
	m, err := NewManager(Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	config := Config{Enabled: true, Timeout: time.Second, RetryCount: 1}
	if err := m.SetConfig(config); err != nil || m.GetConfig() != config {
		t.Fatalf("valid update: config=%+v error=%v", m.GetConfig(), err)
	}
	if err := m.SetConfig(Config{Timeout: -time.Second}); err == nil || m.GetConfig() != config {
		t.Errorf("invalid update: config=%+v error=%v", m.GetConfig(), err)
	}
	m.MarkReady()
	if !m.Readiness().Ready {
		t.Fatal("expected ready before Close")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.SetConfig(Config{}); !errors.Is(err, errManagerClosed) || m.GetConfig() != config || m.Readiness().Ready {
		t.Errorf("closed update: config=%+v readiness=%+v error=%v", m.GetConfig(), m.Readiness(), err)
	}
}

type namingChecker struct {
	name func() string
}

func (c namingChecker) Name() string              { return c.name() }
func (namingChecker) Check(context.Context) error { return nil }

func TestAcceptanceRegisterNameRunsOutsideManagerLock(t *testing.T) {
	for _, closeDuringName := range []bool{false, true} {
		manager, err := NewManager(Config{RetryCount: 1})
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() {
			result <- manager.RegisterComponent(namingChecker{name: func() string {
				if config := manager.GetConfig(); config.RetryCount != 1 {
					t.Errorf("config=%+v", config)
				}
				if closeDuringName {
					if err := manager.Close(); err != nil {
						t.Error(err)
					}
				}
				return "fixture"
			}})
		}()
		select {
		case err := <-result:
			_, registered := manager.GetComponentHealth("fixture")
			if closeDuringName {
				if !errors.Is(err, errManagerClosed) || registered {
					t.Fatalf("closed registration=%t err=%v", registered, err)
				}
			} else if err != nil || !registered {
				t.Fatalf("registration=%t err=%v", registered, err)
			}
		case <-time.After(time.Second):
			t.Fatal("component name callback did not finish")
		}
	}
}

func TestAcceptanceRetryCountRejectsUnrepresentableAttempts(t *testing.T) {
	for _, retry := range []int{-1, math.MaxInt} {
		if manager, err := NewManager(Config{RetryCount: retry}); err == nil || manager != nil {
			t.Fatalf("NewManager retries=%d: manager=%v err=%v", retry, manager, err)
		}
	}
	for _, retry := range []int{0, 1} {
		manager, err := NewManager(Config{RetryCount: retry})
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.SetConfig(Config{RetryCount: math.MaxInt}); err == nil || manager.GetConfig().RetryCount != retry {
			t.Fatalf("SetConfig error=%v config=%+v", err, manager.GetConfig())
		}
		checker := &flakyChecker{name: "fixture", failCount: 1}
		if err := manager.RegisterComponent(checker); err != nil {
			t.Fatal(err)
		}
		err = manager.CheckComponent(t.Context(), "fixture")
		if (err == nil) != (retry == 1) || checker.attempts != int32(retry+1) {
			t.Fatalf("retries=%d attempts=%d error=%v", retry, checker.attempts, err)
		}
	}
}

func TestManagerPreservesSchedulingIntervalForCaller(t *testing.T) {
	manager, err := NewManager(Config{HealthCheckInterval: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	config := manager.GetConfig()
	if config.HealthCheckInterval != time.Minute {
		t.Fatalf("configured interval=%v", config.HealthCheckInterval)
	}
	config.HealthCheckInterval = time.Second
	if err := manager.SetConfig(config); err != nil {
		t.Fatal(err)
	}
	if got := manager.GetConfig().HealthCheckInterval; got != time.Second {
		t.Fatalf("updated interval=%v", got)
	}
}
