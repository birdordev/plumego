package messaging

import (
	"context"
	"fmt"
	"time"

	"github.com/birdordev/plumego/x/tenant/core"
)

// QuotaChecker enforces per-tenant send limits before enqueue.
// It wraps tenant.QuotaManager to count message sends.
type QuotaChecker struct {
	mgr tenant.QuotaManager
}

// NewQuotaChecker creates a checker backed by a QuotaManager.
func NewQuotaChecker(mgr tenant.QuotaManager) *QuotaChecker {
	return &QuotaChecker{mgr: mgr}
}

// Allow checks whether the tenant is allowed to send.
// Returns nil if allowed, ErrQuotaExceeded (with RetryAfter) otherwise.
func (q *QuotaChecker) Allow(ctx context.Context, tenantID string) error {
	if q.mgr == nil || tenantID == "" {
		return nil // no quota enforcement
	}
	result, err := q.mgr.Allow(ctx, tenantID, tenant.QuotaRequest{
		Requests: 1,
		Now:      time.Now(),
	})
	if err != nil {
		return fmt.Errorf("messaging: quota check failed: %w", err)
	}
	if !result.Allowed {
		return fmt.Errorf("%w: retry after %s (remaining: req=%d, tok=%d)",
			ErrQuotaExceeded,
			result.RetryAfter,
			result.RemainingRequests,
			result.RemainingTokens,
		)
	}
	return nil
}

// ErrQuotaExceeded signals that the tenant has exceeded their send quota.
var ErrQuotaExceeded = tenant.ErrQuotaExceeded
