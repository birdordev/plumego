package tenant

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

type rollbackTestStore struct {
	reserve func(context.Context, QuotaReserveRequest) (QuotaUsage, bool, error)
	release func(context.Context, QuotaReleaseRequest) error
}

func (s rollbackTestStore) Reserve(ctx context.Context, req QuotaReserveRequest) (QuotaUsage, bool, error) {
	return s.reserve(ctx, req)
}

func (s rollbackTestStore) Release(ctx context.Context, req QuotaReleaseRequest) error {
	return s.release(ctx, req)
}

func TestWindowQuotaManagerRollbackAfterPartialReservation(t *testing.T) {
	for _, cancelRequest := range []bool{true, false} {
		name := "denial and release failure"
		if cancelRequest {
			name = "cancellation"
		}
		t.Run(name, func(t *testing.T) {
			type contextKey struct{}
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), contextKey{}, "request"))
			defer cancel()
			now := time.Now().UTC()
			cfg := NewInMemoryConfigManager()
			cfg.SetTenantConfig(Config{TenantID: "tenant", Quota: QuotaConfig{
				Limits: []QuotaLimit{
					{Window: QuotaWindowMinute, Requests: 4},
					{Window: QuotaWindowHour, Requests: 4},
					{Window: QuotaWindowDay, Requests: 4},
				},
			}})
			underlying := NewInMemoryQuotaStore()
			releaseErr := errors.New("release failed")
			var released []QuotaWindow
			store := rollbackTestStore{
				reserve: func(ctx context.Context, req QuotaReserveRequest) (QuotaUsage, bool, error) {
					if req.Window != QuotaWindowDay {
						return underlying.Reserve(ctx, req)
					}
					if cancelRequest {
						cancel()
						return QuotaUsage{}, false, ctx.Err()
					}
					return QuotaUsage{}, false, nil
				},
				release: func(ctx context.Context, req QuotaReleaseRequest) error {
					released = append(released, req.Window)
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > quotaRollbackTimeout || ctx.Value(contextKey{}) != "request" {
						t.Error("rollback context lost its cleanup deadline or request value")
					}
					if err := ctx.Err(); err != nil {
						return err
					}
					if !cancelRequest && req.Window == QuotaWindowHour {
						return releaseErr
					}
					return underlying.Release(ctx, req)
				},
			}
			got, err := NewWindowQuotaManager(cfg, store).Allow(ctx, "tenant", QuotaRequest{Now: now})
			if got.Allowed {
				t.Fatal("request allowed after reservation failure")
			}
			if cancelRequest {
				if !errors.Is(err, context.Canceled) {
					t.Errorf("error = %v, want context.Canceled", err)
				}
			} else if !errors.Is(err, ErrQuotaExceeded) || !errors.Is(err, releaseErr) {
				t.Errorf("error = %v, want quota denial and release failure", err)
			}
			if !slices.Equal(released, []QuotaWindow{QuotaWindowHour, QuotaWindowMinute}) {
				t.Errorf("released windows = %v", released)
			}
			usage, _ := underlying.Usage("tenant", QuotaWindowMinute, quotaWindowStart(now, QuotaWindowMinute))
			if usage.Requests != 0 {
				t.Errorf("minute window retained %d requests", usage.Requests)
			}
			if cancelRequest {
				usage, _ := underlying.Usage("tenant", QuotaWindowHour, quotaWindowStart(now, QuotaWindowHour))
				if usage.Requests != 0 {
					t.Errorf("hour window retained %d requests", usage.Requests)
				}
			}
		})
	}
}

func TestWindowQuotaManager_FallbackMinute(t *testing.T) {
	cfg := NewInMemoryConfigManager()
	cfg.SetTenantConfig(Config{
		TenantID: "t-1",
		Quota: QuotaConfig{
			Limits: []QuotaLimit{{Window: QuotaWindowMinute, Requests: 1}},
		},
	})

	store := NewInMemoryQuotaStore()
	manager := NewWindowQuotaManager(cfg, store)
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	res, err := manager.Allow(t.Context(), "t-1", QuotaRequest{Requests: 1, Now: now})
	if err != nil || !res.Allowed {
		t.Fatalf("expected first request allowed, got err=%v res=%+v", err, res)
	}

	res, err = manager.Allow(t.Context(), "t-1", QuotaRequest{Requests: 1, Now: now})
	if err != ErrQuotaExceeded || res.Allowed {
		t.Fatalf("expected quota exceeded, got err=%v res=%+v", err, res)
	}
}

func TestWindowQuotaManager_MultiWindowRollback(t *testing.T) {
	cfg := NewInMemoryConfigManager()
	cfg.SetTenantConfig(Config{
		TenantID: "t-1",
		Quota: QuotaConfig{
			Limits: []QuotaLimit{
				{Window: QuotaWindowMinute, Requests: 2},
				{Window: QuotaWindowDay, Requests: 1},
			},
		},
	})

	store := NewInMemoryQuotaStore()
	manager := NewWindowQuotaManager(cfg, store)
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	res, err := manager.Allow(t.Context(), "t-1", QuotaRequest{Requests: 1, Now: now})
	if err != nil || !res.Allowed {
		t.Fatalf("expected first request allowed, got err=%v res=%+v", err, res)
	}

	res, err = manager.Allow(t.Context(), "t-1", QuotaRequest{Requests: 1, Now: now})
	if err != ErrQuotaExceeded || res.Allowed {
		t.Fatalf("expected quota exceeded, got err=%v res=%+v", err, res)
	}

	windowStart := quotaWindowStart(now, QuotaWindowMinute)
	usage, ok := store.Usage("t-1", QuotaWindowMinute, windowStart)
	if !ok {
		t.Fatalf("expected usage entry for minute window")
	}
	if usage.Requests != 1 {
		t.Fatalf("expected minute usage to stay at 1 after rollback, got %d", usage.Requests)
	}
}
