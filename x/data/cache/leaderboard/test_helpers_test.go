package leaderboard

import (
	"testing"

	storecache "github.com/birdordev/plumego/store/cache"
)

func mustNewMemoryLeaderboardCache(tb testing.TB, cacheConfig storecache.Config, lbConfig *LeaderboardConfig) *MemoryLeaderboardCache {
	tb.Helper()
	cache, err := NewMemoryLeaderboardCache(cacheConfig, lbConfig)
	if err != nil {
		tb.Fatalf("NewMemoryLeaderboardCache: %v", err)
	}
	return cache
}
