package tenant

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const quotaRollbackTimeout = 5 * time.Second

// WindowQuotaManager enforces per-tenant quota limits for multiple windows.
// It uses a QuotaStore to atomically reserve usage per window.
type WindowQuotaManager struct {
	provider QuotaConfigProvider
	store    QuotaStore
}

// NewWindowQuotaManager creates a quota manager backed by a QuotaStore.
func NewWindowQuotaManager(provider QuotaConfigProvider, store QuotaStore) *WindowQuotaManager {
	return &WindowQuotaManager{
		provider: provider,
		store:    store,
	}
}

// Allow checks quota usage for a tenant. If a reservation fails, it releases
// earlier reservations with a bounded cleanup context and reports release errors.
func (m *WindowQuotaManager) Allow(ctx context.Context, tenantID string, req QuotaRequest) (QuotaResult, error) {
	if m == nil || m.provider == nil || m.store == nil {
		return QuotaResult{Allowed: true}, nil
	}

	cfg, err := m.provider.QuotaConfig(ctx, tenantID)
	if err != nil {
		return QuotaResult{Allowed: false}, err
	}

	limits := normalizeQuotaLimits(cfg)
	if len(limits) == 0 {
		return QuotaResult{Allowed: true}, nil
	}

	normalizeQuotaRequest(&req)

	var (
		reserved []QuotaReleaseRequest
		minReq   int64 = -1
		minTok   int64 = -1
	)

	for _, limit := range limits {
		if limit.Requests <= 0 && limit.Tokens <= 0 {
			continue
		}

		windowStart := quotaWindowStart(req.Now, limit.Window)
		usage, allowed, err := m.store.Reserve(ctx, QuotaReserveRequest{
			TenantID:      tenantID,
			Window:        limit.Window,
			WindowStart:   windowStart,
			DeltaRequests: req.Requests,
			DeltaTokens:   req.Tokens,
			LimitRequests: limit.Requests,
			LimitTokens:   limit.Tokens,
		})
		if err != nil || !allowed {
			retryAfter := time.Until(quotaWindowEnd(windowStart, limit.Window))
			if retryAfter < 0 {
				retryAfter = 0
			}

			result := QuotaResult{
				Allowed:           false,
				RemainingRequests: remaining(limit.Requests, usage.Requests),
				RemainingTokens:   remaining(limit.Tokens, usage.Tokens),
				RetryAfter:        retryAfter,
			}
			if err == nil {
				err = ErrQuotaExceeded
			}
			return result, m.rollback(ctx, reserved, err)
		}

		reserved = append(reserved, QuotaReleaseRequest{
			TenantID:      tenantID,
			Window:        limit.Window,
			WindowStart:   windowStart,
			DeltaRequests: req.Requests,
			DeltaTokens:   req.Tokens,
		})
		minReq = minRemaining(minReq, remaining(limit.Requests, usage.Requests))
		minTok = minRemaining(minTok, remaining(limit.Tokens, usage.Tokens))
	}

	return QuotaResult{
		Allowed:           true,
		RemainingRequests: minReq,
		RemainingTokens:   minTok,
	}, nil
}

func (m *WindowQuotaManager) rollback(ctx context.Context, reserved []QuotaReleaseRequest, cause error) error {
	if len(reserved) == 0 {
		return cause
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), quotaRollbackTimeout)
	defer cancel()
	for i := len(reserved) - 1; i >= 0; i-- {
		if err := m.store.Release(cleanupCtx, reserved[i]); err != nil {
			cause = errors.Join(cause, fmt.Errorf("release %s quota reservation: %w", reserved[i].Window, err))
		}
	}
	return cause
}

func minRemaining(current, candidate int64) int64 {
	if candidate < 0 {
		return current
	}
	if current < 0 {
		return candidate
	}
	if candidate < current {
		return candidate
	}
	return current
}
