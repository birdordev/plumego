// Package leaderboard provides Plumego-local in-memory ranked-data caches.
//
// The package extends store/cache with sorted-set style operations, but it does
// not promise Redis sorted-set compatibility. Missing leaderboard behavior is
// intentionally local to this package: aggregate/count removal methods return
// zero for a missing leaderboard, while member/range read and direct member
// removal methods return ErrLeaderboardNotFound.
package leaderboard

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	storecache "github.com/spcent/plumego/store/cache"
)

// LeaderboardCache extends store/cache with sorted-set operations for ranked data.
type LeaderboardCache interface {
	storecache.Cache

	// Sorted Set Operations
	ZAdd(ctx context.Context, key string, members ...*ZMember) error
	ZRem(ctx context.Context, key string, members ...string) error
	ZScore(ctx context.Context, key string, member string) (float64, error)
	ZIncrBy(ctx context.Context, key string, member string, delta float64) (float64, error)

	// Range Queries
	ZRange(ctx context.Context, key string, start, stop int64, desc bool) ([]*ZMember, error)
	ZRangeByScore(ctx context.Context, key string, min, max float64, desc bool) ([]*ZMember, error)
	ZRank(ctx context.Context, key string, member string, desc bool) (int64, error)

	// Cardinality
	ZCard(ctx context.Context, key string) (int64, error)
	ZCount(ctx context.Context, key string, min, max float64) (int64, error)

	// Removal by Range
	ZRemRangeByRank(ctx context.Context, key string, start, stop int64) (int64, error)
	ZRemRangeByScore(ctx context.Context, key string, min, max float64) (int64, error)
}

// ZMember represents a member in a sorted set with its score
type ZMember struct {
	Member string  // Member name/identifier
	Score  float64 // Score value
}

// LeaderboardConfig configures the leaderboard cache behavior
type LeaderboardConfig struct {
	MaxLeaderboards  int           // Maximum number of leaderboards (default: 1000)
	MaxMembersPerSet int           // Maximum members per leaderboard (default: 10000)
	DefaultTTL       time.Duration // Default TTL for leaderboards (default: 1 hour; NoExpirationTTL disables expiration)
	CleanupInterval  time.Duration // Cleanup interval for expired leaderboards (default: 5 minutes)
	EnableMetrics    bool          // Enable metrics collection. Defaults to true only in DefaultLeaderboardConfig; custom zero-value literals disable operation counters.
}

// NoExpirationTTL can be assigned to LeaderboardConfig.DefaultTTL to create
// leaderboards without expiration. A zero DefaultTTL still means use the package
// default.
const NoExpirationTTL time.Duration = -1

// DefaultLeaderboardConfig returns the default configuration
func DefaultLeaderboardConfig() *LeaderboardConfig {
	return &LeaderboardConfig{
		MaxLeaderboards:  1000,
		MaxMembersPerSet: 10000,
		DefaultTTL:       time.Hour,
		CleanupInterval:  5 * time.Minute,
		EnableMetrics:    true,
	}
}

// Validate checks if the configuration is valid
func (c *LeaderboardConfig) Validate() error {
	if c == nil {
		return nil
	}
	if c.MaxLeaderboards <= 0 {
		return storecache.ErrInvalidConfig
	}
	if c.MaxMembersPerSet <= 0 {
		return storecache.ErrInvalidConfig
	}
	if c.DefaultTTL < 0 && c.DefaultTTL != NoExpirationTTL {
		return storecache.ErrInvalidConfig
	}
	return nil
}

func normalizeLeaderboardConfig(config *LeaderboardConfig) (*LeaderboardConfig, error) {
	if config == nil {
		config = DefaultLeaderboardConfig()
	}
	normalized := *config
	if err := normalized.Validate(); err != nil {
		return nil, err
	}
	if normalized.CleanupInterval <= 0 {
		normalized.CleanupInterval = 5 * time.Minute
	}
	switch normalized.DefaultTTL {
	case NoExpirationTTL:
		normalized.DefaultTTL = 0
	case 0:
		normalized.DefaultTTL = time.Hour
	}
	return &normalized, nil
}

// Leaderboard errors
var (
	ErrLeaderboardNotFound = errors.New("cache: leaderboard not found")
	ErrMemberNotFound      = errors.New("cache: member not found in sorted set")
	ErrInvalidMember       = errors.New("cache: invalid sorted set member")
	ErrLeaderboardFull     = errors.New("cache: leaderboard member limit reached")
	ErrInvalidScore        = errors.New("cache: invalid score value")
	ErrInvalidRange        = errors.New("cache: invalid range parameters")
	ErrClosed              = errors.New("cache: leaderboard closed")
)

// sortedSet represents a sorted set with skip list and score map
type sortedSet struct {
	mu         sync.RWMutex
	scores     map[string]float64 // Member -> Score mapping for O(1) lookup
	skipList   *skipList          // Skip list for ordered operations
	expiration time.Time          // Expiration time
}

// newSortedSet creates a new sorted set
func newSortedSet(ttl time.Duration) *sortedSet {
	var expiration time.Time
	if ttl > 0 {
		expiration = time.Now().Add(ttl)
	}

	return &sortedSet{
		scores:     make(map[string]float64),
		skipList:   newSkipList(),
		expiration: expiration,
	}
}

// isExpired checks if the sorted set has expired
func (ss *sortedSet) isExpired() bool {
	if ss.expiration.IsZero() {
		return false
	}
	return time.Now().After(ss.expiration)
}

// MemoryLeaderboardCache is an in-memory implementation of LeaderboardCache
type MemoryLeaderboardCache struct {
	*storecache.MemoryCache

	leaderboards sync.Map // key -> *sortedSet
	config       *LeaderboardConfig
	metrics      *leaderboardMetrics
	createMu     sync.Mutex
	keyMaxLength int
	count        atomic.Int64
	closed       atomic.Bool
	stopChan     chan struct{}
	wg           sync.WaitGroup
	closeOnce    sync.Once
	closeErr     error
}

// LeaderboardMetrics is an approximate exported snapshot of leaderboard metrics.
type LeaderboardMetrics struct {
	ZAdds             uint64
	ZRems             uint64
	ZIncrements       uint64
	ZRangeQueries     uint64
	ZScoreLookups     uint64
	ZRankCalculations uint64
	TotalLeaderboards int64
	TotalMembers      int64
}

type leaderboardMetrics struct {
	mu sync.RWMutex
	LeaderboardMetrics
}

// NewMemoryLeaderboardCache creates a new in-memory leaderboard cache.
func NewMemoryLeaderboardCache(cacheConfig storecache.Config, lbConfig *LeaderboardConfig) (*MemoryLeaderboardCache, error) {
	normalized, err := normalizeLeaderboardConfig(lbConfig)
	if err != nil {
		return nil, err
	}

	baseCache, err := storecache.NewMemoryCacheWithConfig(cacheConfig)
	if err != nil {
		return nil, err
	}

	lbc := &MemoryLeaderboardCache{
		MemoryCache:  baseCache,
		config:       normalized,
		metrics:      &leaderboardMetrics{},
		keyMaxLength: cacheConfig.MaxKeyLength,
		stopChan:     make(chan struct{}),
	}

	// Start cleanup goroutine
	lbc.wg.Add(1)
	go lbc.cleanupLoop()

	return lbc, nil
}

// Close stops the leaderboard cache and cleanup goroutine
func (lbc *MemoryLeaderboardCache) Close() error {
	if lbc == nil {
		return nil
	}
	lbc.closeOnce.Do(func() {
		lbc.closed.Store(true)
		close(lbc.stopChan)
		lbc.wg.Wait()
		lbc.closeErr = lbc.MemoryCache.Close()
	})
	return lbc.closeErr
}

// Clear removes all regular cache entries and all leaderboard data.
func (lbc *MemoryLeaderboardCache) Clear(ctx context.Context) error {
	if lbc.closed.Load() {
		return ErrClosed
	}
	if err := lbc.MemoryCache.Clear(ctx); err != nil {
		return err
	}

	lbc.createMu.Lock()
	defer lbc.createMu.Unlock()

	lbc.leaderboards.Range(func(key, value any) bool {
		ss := value.(*sortedSet)
		ss.mu.Lock()
		ss.skipList.clear()
		clear(ss.scores)
		ss.mu.Unlock()
		lbc.leaderboards.Delete(key)
		return true
	})
	lbc.count.Store(0)

	return nil
}

// cleanupLoop periodically removes expired leaderboards
func (lbc *MemoryLeaderboardCache) cleanupLoop() {
	defer lbc.wg.Done()

	ticker := time.NewTicker(lbc.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			lbc.cleanupExpiredLeaderboards()
		case <-lbc.stopChan:
			return
		}
	}
}

// cleanupExpiredLeaderboards removes expired leaderboards
func (lbc *MemoryLeaderboardCache) cleanupExpiredLeaderboards() {
	now := time.Now()

	lbc.createMu.Lock()
	defer lbc.createMu.Unlock()

	lbc.leaderboards.Range(func(key, value any) bool {
		ss := value.(*sortedSet)
		if !ss.expiration.IsZero() && now.After(ss.expiration) {
			lbc.leaderboards.Delete(key)
			lbc.count.Add(-1)
		}
		return true
	})
}

// getOrCreateSortedSet retrieves or creates a sorted set.
func (lbc *MemoryLeaderboardCache) getOrCreateSortedSet(key string, ttl time.Duration) (*sortedSet, bool, error) {
	lbc.createMu.Lock()
	defer lbc.createMu.Unlock()

	if value, ok := lbc.leaderboards.Load(key); ok {
		ss := value.(*sortedSet)
		if !ss.isExpired() {
			return ss, false, nil
		}
		lbc.leaderboards.Delete(key)
		lbc.count.Add(-1)
	}

	// Use default TTL if not specified
	if ttl == 0 {
		ttl = lbc.config.DefaultTTL
	}

	// Check max leaderboards limit before allocating.
	if lbc.count.Load() >= int64(lbc.config.MaxLeaderboards) {
		return nil, false, ErrLeaderboardFull
	}

	ss := newSortedSet(ttl)
	lbc.leaderboards.Store(key, ss)
	lbc.count.Add(1)
	return ss, true, nil
}

func (lbc *MemoryLeaderboardCache) deleteCreatedSortedSet(key string, ss *sortedSet) {
	if lbc.leaderboards.CompareAndDelete(key, ss) {
		lbc.count.Add(-1)
	}
}

// getSortedSet retrieves a sorted set (read-only)
func (lbc *MemoryLeaderboardCache) getSortedSet(key string) (*sortedSet, error) {
	value, ok := lbc.leaderboards.Load(key)
	if !ok {
		return nil, ErrLeaderboardNotFound
	}

	ss := value.(*sortedSet)
	if ss.isExpired() {
		lbc.createMu.Lock()
		if current, ok := lbc.leaderboards.Load(key); ok && current == ss {
			lbc.leaderboards.Delete(key)
			lbc.count.Add(-1)
		}
		lbc.createMu.Unlock()
		return nil, ErrLeaderboardNotFound
	}

	return ss, nil
}

func (lbc *MemoryLeaderboardCache) validateOperation(ctx context.Context, key string) error {
	if lbc.closed.Load() {
		return ErrClosed
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return lbc.validateKey(key)
}

func (lbc *MemoryLeaderboardCache) validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("%w: %w", storecache.ErrInvalidConfig, storecache.ErrInvalidKey)
	}
	if lbc.keyMaxLength > 0 && len(key) > lbc.keyMaxLength {
		return fmt.Errorf("%w: key length %d exceeds maximum %d",
			storecache.ErrKeyTooLong, len(key), lbc.keyMaxLength)
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c < 0x20 || c == 0x7F {
			return fmt.Errorf("%w: %w: control character at position %d", storecache.ErrInvalidConfig, storecache.ErrInvalidKey, i)
		}
	}
	return nil
}

// validateScore checks if a score is valid (not NaN or Inf)
func validateScore(score float64) error {
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return ErrInvalidScore
	}
	return nil
}

func validateScoreRange(min, max float64) error {
	if min > max {
		return ErrInvalidRange
	}
	return nil
}

func validateRankRange(start, stop int64) error {
	if stop >= 0 && start > stop {
		return ErrInvalidRange
	}
	return nil
}

// ZAdd adds or updates members in a sorted set
func (lbc *MemoryLeaderboardCache) ZAdd(ctx context.Context, key string, members ...*ZMember) error {
	if err := lbc.validateOperation(ctx, key); err != nil {
		return err
	}
	if len(members) == 0 {
		return nil
	}

	// Validate scores
	for _, m := range members {
		if m == nil || m.Member == "" {
			return ErrInvalidMember
		}
		if err := validateScore(m.Score); err != nil {
			return err
		}
	}

	ss, created, err := lbc.getOrCreateSortedSet(key, 0)
	if err != nil {
		return err
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()

	// Check member limit
	newMembers := 0
	pendingNew := make(map[string]struct{})
	for _, m := range members {
		if _, exists := ss.scores[m.Member]; exists {
			continue
		}
		if _, exists := pendingNew[m.Member]; !exists {
			newMembers++
			pendingNew[m.Member] = struct{}{}
		}
	}

	if int64(len(ss.scores)+newMembers) > int64(lbc.config.MaxMembersPerSet) {
		if created {
			lbc.deleteCreatedSortedSet(key, ss)
		}
		return ErrLeaderboardFull
	}

	// Add/update members
	for _, m := range members {
		// Remove the old node before every update. The skip list stores member
		// identity in nodes, so same-score updates must also replace the node.
		if oldScore, exists := ss.scores[m.Member]; exists {
			ss.skipList.delete(m.Member, oldScore)
		}

		// Update score map
		ss.scores[m.Member] = m.Score

		// Insert into skip list (will replace if exists)
		ss.skipList.insert(m.Member, m.Score)
	}

	// Update metrics
	if lbc.config.EnableMetrics {
		lbc.metrics.mu.Lock()
		lbc.metrics.ZAdds += uint64(len(members))
		lbc.metrics.mu.Unlock()
	}

	return nil
}

// ZRem removes members from a sorted set
func (lbc *MemoryLeaderboardCache) ZRem(ctx context.Context, key string, members ...string) error {
	if err := lbc.validateOperation(ctx, key); err != nil {
		return err
	}
	if len(members) == 0 {
		return nil
	}

	ss, err := lbc.getSortedSet(key)
	if err != nil {
		return err
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()

	removed := 0
	for _, member := range members {
		if score, exists := ss.scores[member]; exists {
			ss.skipList.delete(member, score)
			delete(ss.scores, member)
			removed++
		}
	}

	// Update metrics
	if lbc.config.EnableMetrics {
		lbc.metrics.mu.Lock()
		lbc.metrics.ZRems += uint64(removed)
		lbc.metrics.mu.Unlock()
	}

	return nil
}

// ZScore returns the score of a member
func (lbc *MemoryLeaderboardCache) ZScore(ctx context.Context, key string, member string) (float64, error) {
	if err := lbc.validateOperation(ctx, key); err != nil {
		return 0, err
	}
	ss, err := lbc.getSortedSet(key)
	if err != nil {
		return 0, err
	}

	ss.mu.RLock()
	defer ss.mu.RUnlock()

	score, exists := ss.scores[member]
	if !exists {
		return 0, ErrMemberNotFound
	}

	// Update metrics
	if lbc.config.EnableMetrics {
		lbc.metrics.mu.Lock()
		lbc.metrics.ZScoreLookups++
		lbc.metrics.mu.Unlock()
	}

	return score, nil
}

// ZIncrBy increments the score of a member by delta.
//
// If the resulting score is invalid, the logical member state is restored. The
// implementation does not promise that internal skiplist node placement is
// structurally unchanged after a failed increment.
func (lbc *MemoryLeaderboardCache) ZIncrBy(ctx context.Context, key string, member string, delta float64) (float64, error) {
	if err := lbc.validateOperation(ctx, key); err != nil {
		return 0, err
	}
	if member == "" {
		return 0, ErrInvalidMember
	}
	if err := validateScore(delta); err != nil {
		return 0, err
	}

	ss, created, err := lbc.getOrCreateSortedSet(key, 0)
	if err != nil {
		return 0, err
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()

	// Get current score or start from 0
	oldScore := float64(0)
	existed := false
	if score, exists := ss.scores[member]; exists {
		existed = true
		oldScore = score
		// Remove from skip list
		ss.skipList.delete(member, oldScore)
	} else {
		// Check member limit for new member
		if int64(len(ss.scores)+1) > int64(lbc.config.MaxMembersPerSet) {
			if created {
				lbc.deleteCreatedSortedSet(key, ss)
			}
			return 0, ErrLeaderboardFull
		}
	}

	// Calculate new score
	newScore := oldScore + delta
	if err := validateScore(newScore); err != nil {
		// Restore old score if new score is invalid
		if existed {
			ss.skipList.insert(member, oldScore)
		} else if created {
			lbc.deleteCreatedSortedSet(key, ss)
		}
		return 0, err
	}

	// Update score map
	ss.scores[member] = newScore

	// Insert into skip list with new score
	ss.skipList.insert(member, newScore)

	if lbc.config.EnableMetrics {
		lbc.metrics.mu.Lock()
		lbc.metrics.ZIncrements++
		lbc.metrics.mu.Unlock()
	}

	return newScore, nil
}

// ZRange returns members in the specified rank range
func (lbc *MemoryLeaderboardCache) ZRange(ctx context.Context, key string, start, stop int64, desc bool) ([]*ZMember, error) {
	if err := lbc.validateOperation(ctx, key); err != nil {
		return nil, err
	}
	if err := validateRankRange(start, stop); err != nil {
		return nil, err
	}
	ss, err := lbc.getSortedSet(key)
	if err != nil {
		return nil, err
	}

	ss.mu.RLock()
	defer ss.mu.RUnlock()

	nodes := ss.skipList.getRange(start, stop, desc)
	if nodes == nil {
		return []*ZMember{}, nil
	}

	results := make([]*ZMember, len(nodes))
	for i, node := range nodes {
		results[i] = &ZMember{
			Member: node.member,
			Score:  node.score,
		}
	}

	// Update metrics
	if lbc.config.EnableMetrics {
		lbc.metrics.mu.Lock()
		lbc.metrics.ZRangeQueries++
		lbc.metrics.mu.Unlock()
	}

	return results, nil
}

// ZRangeByScore returns members with scores in the specified range.
//
// This in-memory implementation scans the skiplist's base level for score
// ranges. It is intended for bounded in-process leaderboards, not Redis-scale
// range analytics.
func (lbc *MemoryLeaderboardCache) ZRangeByScore(ctx context.Context, key string, min, max float64, desc bool) ([]*ZMember, error) {
	if err := lbc.validateOperation(ctx, key); err != nil {
		return nil, err
	}
	if err := validateScore(min); err != nil {
		return nil, err
	}
	if err := validateScore(max); err != nil {
		return nil, err
	}
	if err := validateScoreRange(min, max); err != nil {
		return nil, err
	}

	ss, err := lbc.getSortedSet(key)
	if err != nil {
		return nil, err
	}

	ss.mu.RLock()
	defer ss.mu.RUnlock()

	nodes := ss.skipList.getRangeByScore(min, max, desc)
	if nodes == nil {
		return []*ZMember{}, nil
	}

	results := make([]*ZMember, len(nodes))
	for i, node := range nodes {
		results[i] = &ZMember{
			Member: node.member,
			Score:  node.score,
		}
	}

	// Update metrics
	if lbc.config.EnableMetrics {
		lbc.metrics.mu.Lock()
		lbc.metrics.ZRangeQueries++
		lbc.metrics.mu.Unlock()
	}

	return results, nil
}

// ZRank returns the rank of a member (0-based)
func (lbc *MemoryLeaderboardCache) ZRank(ctx context.Context, key string, member string, desc bool) (int64, error) {
	if err := lbc.validateOperation(ctx, key); err != nil {
		return -1, err
	}
	ss, err := lbc.getSortedSet(key)
	if err != nil {
		return -1, err
	}

	ss.mu.RLock()
	defer ss.mu.RUnlock()

	score, exists := ss.scores[member]
	if !exists {
		return -1, ErrMemberNotFound
	}

	rank := ss.skipList.getRank(member, score, desc)
	if rank < 0 {
		return -1, ErrMemberNotFound
	}

	// Update metrics
	if lbc.config.EnableMetrics {
		lbc.metrics.mu.Lock()
		lbc.metrics.ZRankCalculations++
		lbc.metrics.mu.Unlock()
	}

	return rank, nil
}

// ZCard returns the number of members in a sorted set
func (lbc *MemoryLeaderboardCache) ZCard(ctx context.Context, key string) (int64, error) {
	if err := lbc.validateOperation(ctx, key); err != nil {
		return 0, err
	}
	ss, err := lbc.getSortedSet(key)
	if err != nil {
		if errors.Is(err, ErrLeaderboardNotFound) {
			return 0, nil
		}
		return 0, err
	}

	ss.mu.RLock()
	defer ss.mu.RUnlock()

	return ss.skipList.length, nil
}

// ZCount returns the number of members with scores in the specified range.
//
// This in-memory implementation scans the skiplist's base level for score
// ranges. Use MaxMembersPerSet to bound per-leaderboard work.
func (lbc *MemoryLeaderboardCache) ZCount(ctx context.Context, key string, min, max float64) (int64, error) {
	if err := lbc.validateOperation(ctx, key); err != nil {
		return 0, err
	}
	if err := validateScore(min); err != nil {
		return 0, err
	}
	if err := validateScore(max); err != nil {
		return 0, err
	}
	if err := validateScoreRange(min, max); err != nil {
		return 0, err
	}

	ss, err := lbc.getSortedSet(key)
	if err != nil {
		if errors.Is(err, ErrLeaderboardNotFound) {
			return 0, nil
		}
		return 0, err
	}

	ss.mu.RLock()
	defer ss.mu.RUnlock()

	return ss.skipList.count(min, max), nil
}

// ZRemRangeByRank removes members in the specified rank range
func (lbc *MemoryLeaderboardCache) ZRemRangeByRank(ctx context.Context, key string, start, stop int64) (int64, error) {
	if err := lbc.validateOperation(ctx, key); err != nil {
		return 0, err
	}
	if err := validateRankRange(start, stop); err != nil {
		return 0, err
	}
	ss, err := lbc.getSortedSet(key)
	if err != nil {
		if errors.Is(err, ErrLeaderboardNotFound) {
			return 0, nil
		}
		return 0, err
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()

	// Get nodes to delete
	nodes := ss.skipList.getRange(start, stop, false)
	if nodes == nil {
		return 0, nil
	}

	// Remove from score map first, then delete the range in skip list once.
	for _, node := range nodes {
		delete(ss.scores, node.member)
	}
	deleted := ss.skipList.deleteRangeByRank(start, stop)

	// Update metrics
	if lbc.config.EnableMetrics {
		lbc.metrics.mu.Lock()
		lbc.metrics.ZRems += uint64(deleted)
		lbc.metrics.mu.Unlock()
	}

	return deleted, nil
}

// ZRemRangeByScore removes members with scores in the specified range.
//
// This in-memory implementation scans the skiplist's base level to find score
// range members before deletion.
func (lbc *MemoryLeaderboardCache) ZRemRangeByScore(ctx context.Context, key string, min, max float64) (int64, error) {
	if err := lbc.validateOperation(ctx, key); err != nil {
		return 0, err
	}
	if err := validateScore(min); err != nil {
		return 0, err
	}
	if err := validateScore(max); err != nil {
		return 0, err
	}
	if err := validateScoreRange(min, max); err != nil {
		return 0, err
	}

	ss, err := lbc.getSortedSet(key)
	if err != nil {
		if errors.Is(err, ErrLeaderboardNotFound) {
			return 0, nil
		}
		return 0, err
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()

	// Get nodes to delete
	nodes := ss.skipList.getRangeByScore(min, max, false)
	if nodes == nil {
		return 0, nil
	}

	// Remove from score map first, then delete the score range in skip list once.
	for _, node := range nodes {
		delete(ss.scores, node.member)
	}
	deleted := ss.skipList.deleteRangeByScore(min, max)

	// Update metrics
	if lbc.config.EnableMetrics {
		lbc.metrics.mu.Lock()
		lbc.metrics.ZRems += uint64(deleted)
		lbc.metrics.mu.Unlock()
	}

	return deleted, nil
}

// GetLeaderboardMetrics returns an approximate snapshot of leaderboard metrics.
//
// Operation counters and current member totals are gathered under different
// locks, so concurrent callers should treat the result as operational telemetry
// rather than a strongly consistent point-in-time view.
func (lbc *MemoryLeaderboardCache) GetLeaderboardMetrics() *LeaderboardMetrics {
	lbc.metrics.mu.RLock()
	metrics := lbc.metrics.LeaderboardMetrics
	lbc.metrics.mu.RUnlock()

	// Count current members.
	totalMembers := int64(0)

	lbc.leaderboards.Range(func(key, value any) bool {
		ss := value.(*sortedSet)
		ss.mu.RLock()
		totalMembers += ss.skipList.length
		ss.mu.RUnlock()
		return true
	})

	metrics.TotalLeaderboards = lbc.count.Load()
	metrics.TotalMembers = totalMembers
	return &metrics
}
