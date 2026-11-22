package tenant

import (
	"context"
	"sort"
	"sync"
	"time"
)

// SlidingWindowQuotaManager implements quota enforcement with a sliding window algorithm.
// This is more accurate than fixed windows and prevents burst traffic at window boundaries.
//
// SlidingWindowQuotaManager only enforces the per-minute window (QuotaWindowMinute).
// If the tenant configuration contains limits for any other window type
// (QuotaWindowHour, QuotaWindowDay, QuotaWindowMonth), Allow returns
// ErrUnsupportedQuotaWindow. Use WindowQuotaManager for hour/day/month enforcement.
type SlidingWindowQuotaManager struct {
	provider QuotaConfigProvider
	windows  sync.Map // map[string]*slidingWindow
}

// slidingWindow tracks requests in a rolling time window.
type slidingWindow struct {
	mu           sync.RWMutex
	requestTimes []time.Time // sorted list of request timestamps (oldest first)
	tokenUsage   []tokenUsage
	totalTokens  int64
}

// tokenUsage tracks token consumption with timestamp.
type tokenUsage struct {
	time   time.Time
	tokens int64
}

// NewSlidingWindowQuotaManager creates a new sliding window quota manager.
func NewSlidingWindowQuotaManager(provider QuotaConfigProvider) *SlidingWindowQuotaManager {
	return &SlidingWindowQuotaManager{
		provider: provider,
	}
}

// DeleteTenant removes the sliding window state for a tenant, reclaiming memory.
// Call this when a tenant is deleted or permanently deactivated.
func (m *SlidingWindowQuotaManager) DeleteTenant(tenantID string) {
	m.windows.Delete(tenantID)
}

// Allow checks if the request is within quota using sliding window algorithm.
// It returns ErrUnsupportedQuotaWindow if the tenant config includes any window
// other than QuotaWindowMinute; use WindowQuotaManager for those cases.
func (m *SlidingWindowQuotaManager) Allow(ctx context.Context, tenantID string, req QuotaRequest) (QuotaResult, error) {
	if m == nil || m.provider == nil {
		return QuotaResult{Allowed: true}, nil
	}
	cfg, err := m.provider.QuotaConfig(ctx, tenantID)
	if err != nil {
		return QuotaResult{Allowed: false}, err
	}

	// Reject configurations that include unsupported window types.
	for _, l := range cfg.Limits {
		if isValidQuotaWindow(l.Window) && l.Window != QuotaWindowMinute {
			return QuotaResult{Allowed: false}, ErrUnsupportedQuotaWindow
		}
	}

	reqLimit, tokLimit := slidingWindowEffectiveLimits(cfg)
	if reqLimit <= 0 && tokLimit <= 0 {
		return QuotaResult{Allowed: true}, nil
	}

	windowInterface, _ := m.windows.LoadOrStore(tenantID, &slidingWindow{
		requestTimes: make([]time.Time, 0),
		tokenUsage:   make([]tokenUsage, 0),
	})
	window := windowInterface.(*slidingWindow)

	normalizeQuotaRequest(&req)

	window.mu.Lock()
	defer window.mu.Unlock()

	cutoff := req.Now.Add(-time.Minute)
	window.cleanup(cutoff)

	currentRequests := int64(len(window.requestTimes))
	currentTokens := window.totalTokens

	if reqLimit > 0 && currentRequests+req.Requests > reqLimit {
		needed := int(currentRequests + req.Requests - reqLimit)
		retryAfter := window.calculateRequestRetryAfter(req.Now, needed)
		return QuotaResult{
			Allowed:           false,
			RemainingRequests: remaining(reqLimit, currentRequests),
			RemainingTokens:   remaining(tokLimit, currentTokens),
			RetryAfter:        retryAfter,
		}, ErrQuotaExceeded
	}

	if tokLimit > 0 && currentTokens+req.Tokens > tokLimit {
		needed := int(currentTokens + req.Tokens - tokLimit)
		retryAfter := window.calculateTokenRetryAfter(req.Now, needed)
		return QuotaResult{
			Allowed:           false,
			RemainingRequests: remaining(reqLimit, currentRequests),
			RemainingTokens:   remaining(tokLimit, currentTokens),
			RetryAfter:        retryAfter,
		}, ErrQuotaExceeded
	}

	for i := int64(0); i < req.Requests; i++ {
		window.requestTimes = append(window.requestTimes, req.Now)
	}
	if req.Tokens > 0 {
		window.totalTokens += req.Tokens
		window.tokenUsage = append(window.tokenUsage, tokenUsage{
			time:   req.Now,
			tokens: req.Tokens,
		})
	}

	return QuotaResult{
		Allowed:           true,
		RemainingRequests: remaining(reqLimit, currentRequests+req.Requests),
		RemainingTokens:   remaining(tokLimit, currentTokens+req.Tokens),
	}, nil
}

// cleanup removes entries older than the cutoff time using binary search.
func (w *slidingWindow) cleanup(cutoff time.Time) {
	// requestTimes: binary search for first entry after cutoff
	idx := sort.Search(len(w.requestTimes), func(i int) bool {
		return w.requestTimes[i].After(cutoff)
	})
	if idx > 0 {
		w.requestTimes = w.requestTimes[idx:]
	}

	// tokenUsage: binary search for first entry after cutoff
	tidx := sort.Search(len(w.tokenUsage), func(i int) bool {
		return w.tokenUsage[i].time.After(cutoff)
	})
	if tidx > 0 {
		for _, usage := range w.tokenUsage[:tidx] {
			w.totalTokens -= usage.tokens
		}
		w.tokenUsage = w.tokenUsage[tidx:]
	}
}

// calculateRequestRetryAfter returns how long until `needed` request slots free up.
// `needed` is the number of existing request entries that must expire to allow the request.
func (w *slidingWindow) calculateRequestRetryAfter(now time.Time, needed int) time.Duration {
	if len(w.requestTimes) == 0 || needed <= 0 {
		return 0
	}
	if needed > len(w.requestTimes) {
		needed = len(w.requestTimes)
	}
	// The oldest `needed` entries must expire. The last one (index needed-1) is the
	// latest-expiring among those. It expires 1 minute after its timestamp.
	expireAt := w.requestTimes[needed-1].Add(time.Minute)
	d := expireAt.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}

// calculateTokenRetryAfter returns how long until enough tokens free up.
// `needed` is the total token count that must expire to allow the request.
func (w *slidingWindow) calculateTokenRetryAfter(now time.Time, needed int) time.Duration {
	if len(w.tokenUsage) == 0 || needed <= 0 {
		return 0
	}
	// Scan from oldest to newest, accumulating freed tokens, until we've covered `needed`.
	accumulated := 0
	var expireEntry time.Time
	for _, u := range w.tokenUsage {
		accumulated += int(u.tokens)
		expireEntry = u.time
		if accumulated >= needed {
			break
		}
	}
	// expireEntry is the timestamp of the last tokenUsage entry that must expire.
	expireAt := expireEntry.Add(time.Minute)
	d := expireAt.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}

// slidingWindowEffectiveLimits returns the per-minute request and token limits for
// the sliding window manager. Only the QuotaWindowMinute entry from Limits is used;
// all other windows require WindowQuotaManager.
func slidingWindowEffectiveLimits(cfg QuotaConfig) (reqLimit, tokLimit int64) {
	for _, l := range cfg.Limits {
		if l.Window == QuotaWindowMinute {
			return l.Requests, l.Tokens
		}
	}
	// No minute window defined — this manager cannot enforce hour/day/month limits;
	// callers should use WindowQuotaManager.
	return 0, 0
}
