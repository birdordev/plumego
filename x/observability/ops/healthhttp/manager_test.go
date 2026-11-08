package healthhttp

import (
	"context"
	"errors"
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
