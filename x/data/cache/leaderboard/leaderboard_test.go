package leaderboard

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	storecache "github.com/birdordev/plumego/store/cache"
)

type sequenceRNG struct {
	values []float64
	idx    int
}

func (r *sequenceRNG) Float64() float64 {
	if r.idx >= len(r.values) {
		return 1
	}
	value := r.values[r.idx]
	r.idx++
	return value
}

func TestSkipListOwnsRandomLevelSource(t *testing.T) {
	promoted := newSkipListWithRand(&sequenceRNG{values: []float64{0.1, 0.1, 0.3}})
	promoted.insert("player1", 1)
	if promoted.level != 3 {
		t.Fatalf("promoted skiplist level = %d, want 3", promoted.level)
	}

	unpromoted := newSkipListWithRand(&sequenceRNG{values: []float64{0.3}})
	unpromoted.insert("player1", 1)
	if unpromoted.level != 1 {
		t.Fatalf("unpromoted skiplist level = %d, want 1", unpromoted.level)
	}
}

func TestSkipListBasicOperations(t *testing.T) {
	sl := newSkipList()

	// Test insert
	sl.insert("player1", 100.0)
	sl.insert("player2", 95.0)
	sl.insert("player3", 90.0)

	if sl.length != 3 {
		t.Errorf("expected length 3, got %d", sl.length)
	}

	// Test getScore
	score, ok := sl.getScore("player1")
	if !ok || score != 100.0 {
		t.Errorf("expected score 100.0, got %f, ok=%v", score, ok)
	}

	// Test delete
	if !sl.delete("player2", 95.0) {
		t.Error("failed to delete player2")
	}

	if sl.length != 2 {
		t.Errorf("expected length 2 after delete, got %d", sl.length)
	}

	// Verify deleted member is gone
	_, ok = sl.getScore("player2")
	if ok {
		t.Error("player2 should not exist after deletion")
	}
}

func TestSkipListRangeQueries(t *testing.T) {
	sl := newSkipList()

	// Insert members
	members := []struct {
		name  string
		score float64
	}{
		{"player1", 100.0},
		{"player2", 95.0},
		{"player3", 90.0},
		{"player4", 85.0},
		{"player5", 80.0},
	}

	for _, m := range members {
		sl.insert(m.name, m.score)
	}

	// Test ascending range
	nodes := sl.getRange(0, 2, false)
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}

	expected := []string{"player5", "player4", "player3"}
	for i, node := range nodes {
		if node.member != expected[i] {
			t.Errorf("position %d: expected %s, got %s", i, expected[i], node.member)
		}
	}

	// Test descending range
	nodes = sl.getRange(0, 2, true)
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}

	expected = []string{"player1", "player2", "player3"}
	for i, node := range nodes {
		if node.member != expected[i] {
			t.Errorf("position %d: expected %s, got %s", i, expected[i], node.member)
		}
	}
}

func TestSkipListRangeByScore(t *testing.T) {
	sl := newSkipList()

	sl.insert("player1", 100.0)
	sl.insert("player2", 95.0)
	sl.insert("player3", 90.0)
	sl.insert("player4", 85.0)
	sl.insert("player5", 80.0)

	// Test score range
	nodes := sl.getRangeByScore(85.0, 95.0, false)
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}

	expected := []string{"player4", "player3", "player2"}
	for i, node := range nodes {
		if node.member != expected[i] {
			t.Errorf("position %d: expected %s, got %s", i, expected[i], node.member)
		}
	}
}

func TestSkipListGetRank(t *testing.T) {
	sl := newSkipList()

	sl.insert("player1", 100.0)
	sl.insert("player2", 95.0)
	sl.insert("player3", 90.0)

	// Test ascending rank (lowest score = rank 0)
	rank := sl.getRank("player3", 90.0, false)
	if rank != 0 {
		t.Errorf("expected rank 0 for player3, got %d", rank)
	}

	rank = sl.getRank("player1", 100.0, false)
	if rank != 2 {
		t.Errorf("expected rank 2 for player1, got %d", rank)
	}

	// Test descending rank (highest score = rank 0)
	rank = sl.getRank("player1", 100.0, true)
	if rank != 0 {
		t.Errorf("expected rank 0 for player1 (desc), got %d", rank)
	}
}

func TestSkipListCount(t *testing.T) {
	sl := newSkipList()

	sl.insert("player1", 100.0)
	sl.insert("player2", 95.0)
	sl.insert("player3", 90.0)
	sl.insert("player4", 85.0)
	sl.insert("player5", 80.0)

	count := sl.count(85.0, 95.0)
	if count != 3 {
		t.Errorf("expected count 3, got %d", count)
	}

	count = sl.count(100.0, 100.0)
	if count != 1 {
		t.Errorf("expected count 1, got %d", count)
	}

	count = sl.count(200.0, 300.0)
	if count != 0 {
		t.Errorf("expected count 0, got %d", count)
	}
}

func TestSkipListDeleteRangeByRank(t *testing.T) {
	sl := newSkipList()
	sl.insert("player1", 100.0)
	sl.insert("player2", 95.0)
	sl.insert("player3", 90.0)
	sl.insert("player4", 85.0)
	sl.insert("player5", 80.0)

	deleted := sl.deleteRangeByRank(1, 3)
	if deleted != 3 {
		t.Fatalf("expected 3 deleted, got %d", deleted)
	}
	if sl.length != 2 {
		t.Fatalf("expected length 2, got %d", sl.length)
	}

	if _, ok := sl.getScore("player2"); ok {
		t.Error("player2 should be removed")
	}
	if _, ok := sl.getScore("player3"); ok {
		t.Error("player3 should be removed")
	}
	if _, ok := sl.getScore("player4"); ok {
		t.Error("player4 should be removed")
	}

	nodes := sl.getRange(0, 10, false)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 remaining nodes, got %d", len(nodes))
	}
	if nodes[0].member != "player5" || nodes[1].member != "player1" {
		t.Fatalf("unexpected remaining members: %s, %s", nodes[0].member, nodes[1].member)
	}
}

func TestSkipListDeleteRangeByScore(t *testing.T) {
	sl := newSkipList()
	sl.insert("player1", 100.0)
	sl.insert("player2", 95.0)
	sl.insert("player3", 90.0)
	sl.insert("player4", 85.0)
	sl.insert("player5", 80.0)

	deleted := sl.deleteRangeByScore(85.0, 95.0)
	if deleted != 3 {
		t.Fatalf("expected 3 deleted, got %d", deleted)
	}
	if sl.length != 2 {
		t.Fatalf("expected length 2, got %d", sl.length)
	}

	if _, ok := sl.getScore("player2"); ok {
		t.Error("player2 should be removed")
	}
	if _, ok := sl.getScore("player3"); ok {
		t.Error("player3 should be removed")
	}
	if _, ok := sl.getScore("player4"); ok {
		t.Error("player4 should be removed")
	}

	nodes := sl.getRange(0, 10, false)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 remaining nodes, got %d", len(nodes))
	}
	if nodes[0].member != "player5" || nodes[1].member != "player1" {
		t.Fatalf("unexpected remaining members: %s, %s", nodes[0].member, nodes[1].member)
	}
}

func TestSkipListClear(t *testing.T) {
	sl := newSkipList()
	sl.insert("player1", 100.0)
	sl.insert("player2", 90.0)

	sl.clear()

	if sl.length != 0 {
		t.Fatalf("expected length 0 after clear, got %d", sl.length)
	}
	if sl.level != 1 {
		t.Fatalf("expected level 1 after clear, got %d", sl.level)
	}
	if sl.tail != nil {
		t.Fatal("expected tail nil after clear")
	}
	if sl.header == nil {
		t.Fatal("expected header initialized after clear")
	}

	sl.insert("player3", 120.0)
	if sl.length != 1 {
		t.Fatalf("expected length 1 after reinsert, got %d", sl.length)
	}
}

func TestLeaderboardCacheBasicOperations(t *testing.T) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(t, config, lbConfig)
	defer lbc.Close()

	ctx := t.Context()

	// Test ZAdd
	err := lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player1", Score: 100.0})
	if err != nil {
		t.Errorf("ZAdd failed: %v", err)
	}

	// Test ZScore
	score, err := lbc.ZScore(ctx, "game:scores", "player1")
	if err != nil {
		t.Errorf("ZScore failed: %v", err)
	}
	if score != 100.0 {
		t.Errorf("expected score 100.0, got %f", score)
	}

	// Test ZCard
	card, err := lbc.ZCard(ctx, "game:scores")
	if err != nil {
		t.Errorf("ZCard failed: %v", err)
	}
	if card != 1 {
		t.Errorf("expected cardinality 1, got %d", card)
	}

	// Test ZRem
	err = lbc.ZRem(ctx, "game:scores", "player1")
	if err != nil {
		t.Errorf("ZRem failed: %v", err)
	}

	card, err = lbc.ZCard(ctx, "game:scores")
	if err != nil {
		t.Errorf("ZCard failed: %v", err)
	}
	if card != 0 {
		t.Errorf("expected cardinality 0 after ZRem, got %d", card)
	}
}

func TestLeaderboardCacheCloseIdempotent(t *testing.T) {
	var nilCache *MemoryLeaderboardCache
	if err := nilCache.Close(); err != nil {
		t.Fatalf("nil Close returned error: %v", err)
	}

	lbc := mustNewMemoryLeaderboardCache(t, storecache.DefaultConfig(), DefaultLeaderboardConfig())
	if err := lbc.Close(); err != nil {
		t.Fatalf("first Close failed: %v", err)
	}
	if err := lbc.Close(); err != nil {
		t.Fatalf("second Close failed: %v", err)
	}
}

func TestLeaderboardCacheDoesNotMutateCallerConfig(t *testing.T) {
	lbConfig := &LeaderboardConfig{
		MaxLeaderboards:  2,
		MaxMembersPerSet: 2,
	}

	lbc := mustNewMemoryLeaderboardCache(t, storecache.DefaultConfig(), lbConfig)
	defer lbc.Close()

	if lbConfig.DefaultTTL != 0 {
		t.Fatalf("caller DefaultTTL = %v, want unchanged zero", lbConfig.DefaultTTL)
	}
	if lbConfig.CleanupInterval != 0 {
		t.Fatalf("caller CleanupInterval = %v, want unchanged zero", lbConfig.CleanupInterval)
	}
	if lbc.config.DefaultTTL <= 0 {
		t.Fatal("expected normalized internal DefaultTTL")
	}
	if lbc.config.CleanupInterval <= 0 {
		t.Fatal("expected normalized internal CleanupInterval")
	}
}

func TestLeaderboardCacheRejectsInvalidDefaultTTL(t *testing.T) {
	lbConfig := DefaultLeaderboardConfig()
	lbConfig.DefaultTTL = -2 * time.Nanosecond

	_, err := NewMemoryLeaderboardCache(storecache.DefaultConfig(), lbConfig)
	if !errors.Is(err, storecache.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestLeaderboardCacheSortedSetOperationsFailAfterClose(t *testing.T) {
	lbc := mustNewMemoryLeaderboardCache(t, storecache.DefaultConfig(), DefaultLeaderboardConfig())
	if err := lbc.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	ctx := t.Context()
	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "ZAdd",
			run: func() error {
				return lbc.ZAdd(ctx, "scores", &ZMember{Member: "player", Score: 1})
			},
		},
		{
			name: "ZRem",
			run: func() error {
				return lbc.ZRem(ctx, "scores", "player")
			},
		},
		{
			name: "ZScore",
			run: func() error {
				_, err := lbc.ZScore(ctx, "scores", "player")
				return err
			},
		},
		{
			name: "ZIncrBy",
			run: func() error {
				_, err := lbc.ZIncrBy(ctx, "scores", "player", 1)
				return err
			},
		},
		{
			name: "ZRange",
			run: func() error {
				_, err := lbc.ZRange(ctx, "scores", 0, -1, true)
				return err
			},
		},
		{
			name: "ZRangeByScore",
			run: func() error {
				_, err := lbc.ZRangeByScore(ctx, "scores", 0, 1, true)
				return err
			},
		},
		{
			name: "ZRank",
			run: func() error {
				_, err := lbc.ZRank(ctx, "scores", "player", true)
				return err
			},
		},
		{
			name: "ZCard",
			run: func() error {
				_, err := lbc.ZCard(ctx, "scores")
				return err
			},
		},
		{
			name: "ZCount",
			run: func() error {
				_, err := lbc.ZCount(ctx, "scores", 0, 1)
				return err
			},
		},
		{
			name: "ZRemRangeByRank",
			run: func() error {
				_, err := lbc.ZRemRangeByRank(ctx, "scores", 0, 1)
				return err
			},
		},
		{
			name: "ZRemRangeByScore",
			run: func() error {
				_, err := lbc.ZRemRangeByScore(ctx, "scores", 0, 1)
				return err
			},
		},
		{
			name: "Clear",
			run: func() error {
				return lbc.Clear(ctx)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, ErrClosed) {
				t.Fatalf("expected ErrClosed, got %v", err)
			}
		})
	}
}

func TestLeaderboardCacheZIncrBy(t *testing.T) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(t, config, lbConfig)
	defer lbc.Close()

	ctx := t.Context()

	// Increment non-existent member (should create with delta)
	score, err := lbc.ZIncrBy(ctx, "game:scores", "player1", 10.0)
	if err != nil {
		t.Errorf("ZIncrBy failed: %v", err)
	}
	if score != 10.0 {
		t.Errorf("expected score 10.0, got %f", score)
	}

	// Increment existing member
	score, err = lbc.ZIncrBy(ctx, "game:scores", "player1", 5.0)
	if err != nil {
		t.Errorf("ZIncrBy failed: %v", err)
	}
	if score != 15.0 {
		t.Errorf("expected score 15.0, got %f", score)
	}

	// Decrement (negative delta)
	score, err = lbc.ZIncrBy(ctx, "game:scores", "player1", -3.0)
	if err != nil {
		t.Errorf("ZIncrBy failed: %v", err)
	}
	if score != 12.0 {
		t.Errorf("expected score 12.0, got %f", score)
	}
}

func TestLeaderboardCacheZRange(t *testing.T) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(t, config, lbConfig)
	defer lbc.Close()

	ctx := t.Context()

	// Add multiple members
	members := []*ZMember{
		{Member: "player1", Score: 100.0},
		{Member: "player2", Score: 95.0},
		{Member: "player3", Score: 90.0},
		{Member: "player4", Score: 85.0},
		{Member: "player5", Score: 80.0},
	}

	err := lbc.ZAdd(ctx, "game:scores", members...)
	if err != nil {
		t.Fatalf("ZAdd failed: %v", err)
	}

	// Get top 3 (descending order)
	results, err := lbc.ZRange(ctx, "game:scores", 0, 2, true)
	if err != nil {
		t.Errorf("ZRange failed: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	expected := []string{"player1", "player2", "player3"}
	for i, r := range results {
		if r.Member != expected[i] {
			t.Errorf("position %d: expected %s, got %s", i, expected[i], r.Member)
		}
	}

	// Get bottom 2 (ascending order)
	results, err = lbc.ZRange(ctx, "game:scores", 0, 1, false)
	if err != nil {
		t.Errorf("ZRange failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	expected = []string{"player5", "player4"}
	for i, r := range results {
		if r.Member != expected[i] {
			t.Errorf("position %d: expected %s, got %s", i, expected[i], r.Member)
		}
	}
}

func TestLeaderboardCacheZRangeByScore(t *testing.T) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(t, config, lbConfig)
	defer lbc.Close()

	ctx := t.Context()

	// Add multiple members
	members := []*ZMember{
		{Member: "player1", Score: 100.0},
		{Member: "player2", Score: 95.0},
		{Member: "player3", Score: 90.0},
		{Member: "player4", Score: 85.0},
		{Member: "player5", Score: 80.0},
	}

	err := lbc.ZAdd(ctx, "game:scores", members...)
	if err != nil {
		t.Fatalf("ZAdd failed: %v", err)
	}

	// Get members with score between 85 and 95
	results, err := lbc.ZRangeByScore(ctx, "game:scores", 85.0, 95.0, false)
	if err != nil {
		t.Errorf("ZRangeByScore failed: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	expected := []string{"player4", "player3", "player2"}
	for i, r := range results {
		if r.Member != expected[i] {
			t.Errorf("position %d: expected %s, got %s", i, expected[i], r.Member)
		}
	}
}

func TestLeaderboardCacheZRank(t *testing.T) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(t, config, lbConfig)
	defer lbc.Close()

	ctx := t.Context()

	// Add multiple members
	members := []*ZMember{
		{Member: "player1", Score: 100.0},
		{Member: "player2", Score: 95.0},
		{Member: "player3", Score: 90.0},
	}

	err := lbc.ZAdd(ctx, "game:scores", members...)
	if err != nil {
		t.Fatalf("ZAdd failed: %v", err)
	}

	// Get descending rank (0 = highest score)
	rank, err := lbc.ZRank(ctx, "game:scores", "player1", true)
	if err != nil {
		t.Errorf("ZRank failed: %v", err)
	}
	if rank != 0 {
		t.Errorf("expected rank 0 for player1, got %d", rank)
	}

	rank, err = lbc.ZRank(ctx, "game:scores", "player2", true)
	if err != nil {
		t.Errorf("ZRank failed: %v", err)
	}
	if rank != 1 {
		t.Errorf("expected rank 1 for player2, got %d", rank)
	}

	// Test non-existent member
	_, err = lbc.ZRank(ctx, "game:scores", "nonexistent", true)
	if err == nil {
		t.Error("expected error for non-existent member")
	}
}

func TestLeaderboardCacheZCount(t *testing.T) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(t, config, lbConfig)
	defer lbc.Close()

	ctx := t.Context()

	// Add multiple members
	members := []*ZMember{
		{Member: "player1", Score: 100.0},
		{Member: "player2", Score: 95.0},
		{Member: "player3", Score: 90.0},
		{Member: "player4", Score: 85.0},
		{Member: "player5", Score: 80.0},
	}

	err := lbc.ZAdd(ctx, "game:scores", members...)
	if err != nil {
		t.Fatalf("ZAdd failed: %v", err)
	}

	// Count members in range
	count, err := lbc.ZCount(ctx, "game:scores", 85.0, 95.0)
	if err != nil {
		t.Errorf("ZCount failed: %v", err)
	}
	if count != 3 {
		t.Errorf("expected count 3, got %d", count)
	}

	// Count all
	count, err = lbc.ZCount(ctx, "game:scores", 0, 200.0)
	if err != nil {
		t.Errorf("ZCount failed: %v", err)
	}
	if count != 5 {
		t.Errorf("expected count 5, got %d", count)
	}
}

func TestLeaderboardCacheZRemRange(t *testing.T) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(t, config, lbConfig)
	defer lbc.Close()

	ctx := t.Context()

	// Add multiple members
	members := []*ZMember{
		{Member: "player1", Score: 100.0},
		{Member: "player2", Score: 95.0},
		{Member: "player3", Score: 90.0},
		{Member: "player4", Score: 85.0},
		{Member: "player5", Score: 80.0},
	}

	err := lbc.ZAdd(ctx, "game:scores", members...)
	if err != nil {
		t.Fatalf("ZAdd failed: %v", err)
	}

	// Remove by rank
	removed, err := lbc.ZRemRangeByRank(ctx, "game:scores", 0, 1)
	if err != nil {
		t.Errorf("ZRemRangeByRank failed: %v", err)
	}
	if removed != 2 {
		t.Errorf("expected 2 removed, got %d", removed)
	}

	// Verify remaining count
	card, err := lbc.ZCard(ctx, "game:scores")
	if err != nil {
		t.Errorf("ZCard failed: %v", err)
	}
	if card != 3 {
		t.Errorf("expected cardinality 3, got %d", card)
	}

	if _, err := lbc.ZScore(ctx, "game:scores", "player5"); err != ErrMemberNotFound {
		t.Errorf("expected ErrMemberNotFound for player5, got %v", err)
	}
	if _, err := lbc.ZScore(ctx, "game:scores", "player4"); err != ErrMemberNotFound {
		t.Errorf("expected ErrMemberNotFound for player4, got %v", err)
	}
	if score, err := lbc.ZScore(ctx, "game:scores", "player1"); err != nil || score != 100.0 {
		t.Errorf("expected player1 score 100.0, got score=%f err=%v", score, err)
	}
}

func TestLeaderboardCacheZRemRangeByScore(t *testing.T) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(t, config, lbConfig)
	defer lbc.Close()

	ctx := t.Context()

	// Add multiple members
	members := []*ZMember{
		{Member: "player1", Score: 100.0},
		{Member: "player2", Score: 95.0},
		{Member: "player3", Score: 90.0},
		{Member: "player4", Score: 85.0},
		{Member: "player5", Score: 80.0},
	}

	err := lbc.ZAdd(ctx, "game:scores", members...)
	if err != nil {
		t.Fatalf("ZAdd failed: %v", err)
	}

	// Remove by score range
	removed, err := lbc.ZRemRangeByScore(ctx, "game:scores", 80.0, 90.0)
	if err != nil {
		t.Errorf("ZRemRangeByScore failed: %v", err)
	}
	if removed != 3 {
		t.Errorf("expected 3 removed, got %d", removed)
	}

	// Verify remaining members
	card, err := lbc.ZCard(ctx, "game:scores")
	if err != nil {
		t.Errorf("ZCard failed: %v", err)
	}
	if card != 2 {
		t.Errorf("expected cardinality 2, got %d", card)
	}

	if _, err := lbc.ZScore(ctx, "game:scores", "player5"); err != ErrMemberNotFound {
		t.Errorf("expected ErrMemberNotFound for player5, got %v", err)
	}
	if _, err := lbc.ZScore(ctx, "game:scores", "player3"); err != ErrMemberNotFound {
		t.Errorf("expected ErrMemberNotFound for player3, got %v", err)
	}
	if score, err := lbc.ZScore(ctx, "game:scores", "player1"); err != nil || score != 100.0 {
		t.Errorf("expected player1 score 100.0, got score=%f err=%v", score, err)
	}
}

func TestLeaderboardCacheRejectsInvalidRanges(t *testing.T) {
	lbc := mustNewMemoryLeaderboardCache(t, storecache.DefaultConfig(), DefaultLeaderboardConfig())
	defer lbc.Close()

	ctx := t.Context()
	if err := lbc.ZAdd(ctx, "game:scores",
		&ZMember{Member: "player1", Score: 100},
		&ZMember{Member: "player2", Score: 90},
	); err != nil {
		t.Fatalf("ZAdd failed: %v", err)
	}

	if _, err := lbc.ZRange(ctx, "game:scores", 2, 1, true); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("expected ErrInvalidRange from ZRange, got %v", err)
	}
	if _, err := lbc.ZRemRangeByRank(ctx, "game:scores", 2, 1); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("expected ErrInvalidRange from ZRemRangeByRank, got %v", err)
	}
	if _, err := lbc.ZRangeByScore(ctx, "game:scores", 100, 90, true); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("expected ErrInvalidRange from ZRangeByScore, got %v", err)
	}
	if _, err := lbc.ZCount(ctx, "game:scores", 100, 90); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("expected ErrInvalidRange from ZCount, got %v", err)
	}
	if _, err := lbc.ZRemRangeByScore(ctx, "game:scores", 100, 90); !errors.Is(err, ErrInvalidRange) {
		t.Fatalf("expected ErrInvalidRange from ZRemRangeByScore, got %v", err)
	}
}

func TestLeaderboardCacheClear(t *testing.T) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(t, config, lbConfig)
	defer lbc.Close()

	ctx := t.Context()

	if err := lbc.Set(ctx, "plain:key", []byte("value"), time.Minute); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	if err := lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player1", Score: 100.0}); err != nil {
		t.Fatalf("ZAdd failed: %v", err)
	}

	if err := lbc.Clear(ctx); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	if _, err := lbc.Get(ctx, "plain:key"); err != storecache.ErrNotFound {
		t.Errorf("expected ErrNotFound for plain:key, got %v", err)
	}

	card, err := lbc.ZCard(ctx, "game:scores")
	if err != nil {
		t.Fatalf("ZCard failed: %v", err)
	}
	if card != 0 {
		t.Fatalf("expected cardinality 0 after clear, got %d", card)
	}

	if _, err := lbc.ZScore(ctx, "game:scores", "player1"); err != ErrLeaderboardNotFound {
		t.Errorf("expected ErrLeaderboardNotFound after clear, got %v", err)
	}
}

func TestLeaderboardCacheTTL(t *testing.T) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()
	lbConfig.DefaultTTL = 100 * time.Millisecond
	lbConfig.CleanupInterval = 50 * time.Millisecond

	lbc := mustNewMemoryLeaderboardCache(t, config, lbConfig)
	defer lbc.Close()

	ctx := t.Context()

	// Add member
	err := lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player1", Score: 100.0})
	if err != nil {
		t.Fatalf("ZAdd failed: %v", err)
	}

	// Wait for expiration
	time.Sleep(200 * time.Millisecond)

	// Try to access expired leaderboard
	_, err = lbc.ZScore(ctx, "game:scores", "player1")
	if err == nil {
		t.Error("expected error for expired leaderboard")
	}
}

func TestLeaderboardCacheNoExpirationTTL(t *testing.T) {
	lbConfig := DefaultLeaderboardConfig()
	lbConfig.DefaultTTL = NoExpirationTTL
	lbConfig.CleanupInterval = time.Nanosecond

	lbc := mustNewMemoryLeaderboardCache(t, storecache.DefaultConfig(), lbConfig)
	defer lbc.Close()

	if lbc.config.DefaultTTL != 0 {
		t.Fatalf("normalized DefaultTTL = %v, want no-expiration zero", lbc.config.DefaultTTL)
	}

	ctx := t.Context()
	if err := lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player1", Score: 100}); err != nil {
		t.Fatalf("ZAdd failed: %v", err)
	}

	time.Sleep(time.Millisecond)
	lbc.cleanupExpiredLeaderboards()

	card, err := lbc.ZCard(ctx, "game:scores")
	if err != nil {
		t.Fatalf("ZCard failed: %v", err)
	}
	if card != 1 {
		t.Fatalf("cardinality = %d, want 1 for no-expiration leaderboard", card)
	}
}

func TestLeaderboardCacheInvalidScore(t *testing.T) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(t, config, lbConfig)
	defer lbc.Close()

	ctx := t.Context()

	// Test NaN
	err := lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player1", Score: math.NaN()})
	if err != ErrInvalidScore {
		t.Errorf("expected ErrInvalidScore for NaN, got %v", err)
	}

	// Test Inf
	err = lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player1", Score: math.Inf(1)})
	if err != ErrInvalidScore {
		t.Errorf("expected ErrInvalidScore for Inf, got %v", err)
	}

	// Test -Inf
	err = lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player1", Score: math.Inf(-1)})
	if err != ErrInvalidScore {
		t.Errorf("expected ErrInvalidScore for -Inf, got %v", err)
	}
}

func TestLeaderboardCacheMemberLimit(t *testing.T) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()
	lbConfig.MaxMembersPerSet = 5

	lbc := mustNewMemoryLeaderboardCache(t, config, lbConfig)
	defer lbc.Close()

	ctx := t.Context()

	// Add up to limit
	var err error
	for i := 0; i < 5; i++ {
		err = lbc.ZAdd(ctx, "game:scores", &ZMember{
			Member: fmt.Sprintf("player%d", i),
			Score:  float64(i),
		})
		if err != nil {
			t.Fatalf("ZAdd failed at %d: %v", i, err)
		}
	}

	// Try to add one more (should fail)
	err = lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player5", Score: 5.0})
	if err != ErrLeaderboardFull {
		t.Errorf("expected ErrLeaderboardFull, got %v", err)
	}
}

func TestLeaderboardCacheFailedFirstWriteDoesNotLeaveEmptyLeaderboard(t *testing.T) {
	lbConfig := DefaultLeaderboardConfig()
	lbConfig.MaxMembersPerSet = 1
	lbc := mustNewMemoryLeaderboardCache(t, storecache.DefaultConfig(), lbConfig)
	defer lbc.Close()

	err := lbc.ZAdd(t.Context(), "game:scores",
		&ZMember{Member: "player1", Score: 1},
		&ZMember{Member: "player2", Score: 2},
	)
	if !errors.Is(err, ErrLeaderboardFull) {
		t.Fatalf("expected ErrLeaderboardFull, got %v", err)
	}

	metrics := lbc.GetLeaderboardMetrics()
	if metrics.TotalLeaderboards != 0 {
		t.Fatalf("total leaderboards = %d, want 0", metrics.TotalLeaderboards)
	}
}

func TestLeaderboardCacheConcurrentMaxLeaderboards(t *testing.T) {
	lbConfig := DefaultLeaderboardConfig()
	lbConfig.MaxLeaderboards = 1
	lbc := mustNewMemoryLeaderboardCache(t, storecache.DefaultConfig(), lbConfig)
	defer lbc.Close()

	ctx := t.Context()
	const workers = 32

	var wg sync.WaitGroup
	var success atomic.Int64
	var limitErrors atomic.Int64
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(id int) {
			defer wg.Done()
			err := lbc.ZAdd(ctx, fmt.Sprintf("game:scores:%d", id), &ZMember{
				Member: "player",
				Score:  float64(id),
			})
			switch {
			case err == nil:
				success.Add(1)
			case errors.Is(err, ErrLeaderboardFull):
				limitErrors.Add(1)
			default:
				t.Errorf("unexpected ZAdd error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if success.Load() != 1 {
		t.Fatalf("successful leaderboard creations = %d, want 1", success.Load())
	}
	if limitErrors.Load() != workers-1 {
		t.Fatalf("limit errors = %d, want %d", limitErrors.Load(), workers-1)
	}
	metrics := lbc.GetLeaderboardMetrics()
	if metrics.TotalLeaderboards != 1 {
		t.Fatalf("total leaderboards = %d, want 1", metrics.TotalLeaderboards)
	}
}

func TestLeaderboardCacheConcurrency(t *testing.T) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(t, config, lbConfig)
	defer lbc.Close()

	ctx := t.Context()
	const numGoroutines = 100
	const numOpsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			for j := 0; j < numOpsPerGoroutine; j++ {
				member := fmt.Sprintf("player%d", id)

				// ZIncrBy
				_, _ = lbc.ZIncrBy(ctx, "game:scores", member, 1.0)

				// ZScore
				_, _ = lbc.ZScore(ctx, "game:scores", member)

				// ZRank
				_, _ = lbc.ZRank(ctx, "game:scores", member, true)
			}
		}(i)
	}

	wg.Wait()

	// Verify final state
	card, err := lbc.ZCard(ctx, "game:scores")
	if err != nil {
		t.Errorf("ZCard failed: %v", err)
	}

	if card != numGoroutines {
		t.Errorf("expected cardinality %d, got %d", numGoroutines, card)
	}

	// Verify each player has correct score
	for i := 0; i < numGoroutines; i++ {
		member := fmt.Sprintf("player%d", i)
		score, err := lbc.ZScore(ctx, "game:scores", member)
		if err != nil {
			t.Errorf("ZScore failed for %s: %v", member, err)
		}
		if score != float64(numOpsPerGoroutine) {
			t.Errorf("expected score %d for %s, got %f", numOpsPerGoroutine, member, score)
		}
	}
}

func TestLeaderboardCacheMetrics(t *testing.T) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(t, config, lbConfig)
	defer lbc.Close()

	ctx := t.Context()

	// Perform operations
	_ = lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player1", Score: 100.0})
	_, _ = lbc.ZIncrBy(ctx, "game:scores", "player1", 1.0)
	_, _ = lbc.ZScore(ctx, "game:scores", "player1")
	_, _ = lbc.ZRank(ctx, "game:scores", "player1", true)
	_, _ = lbc.ZRange(ctx, "game:scores", 0, 10, true)
	_ = lbc.ZRem(ctx, "game:scores", "player1")

	// Get metrics
	metrics := lbc.GetLeaderboardMetrics()

	if metrics.ZAdds != 1 {
		t.Errorf("expected 1 ZAdd, got %d", metrics.ZAdds)
	}
	if metrics.ZIncrements != 1 {
		t.Errorf("expected 1 ZIncrement, got %d", metrics.ZIncrements)
	}
	if metrics.ZScoreLookups != 1 {
		t.Errorf("expected 1 ZScoreLookup, got %d", metrics.ZScoreLookups)
	}
	if metrics.ZRankCalculations != 1 {
		t.Errorf("expected 1 ZRankCalculation, got %d", metrics.ZRankCalculations)
	}
	if metrics.ZRangeQueries != 1 {
		t.Errorf("expected 1 ZRangeQuery, got %d", metrics.ZRangeQueries)
	}
	if metrics.ZRems != 1 {
		t.Errorf("expected 1 ZRem, got %d", metrics.ZRems)
	}
}

func TestLeaderboardCacheMetricsCanBeDisabledWithCustomConfig(t *testing.T) {
	lbConfig := &LeaderboardConfig{
		MaxLeaderboards:  1000,
		MaxMembersPerSet: 10000,
		DefaultTTL:       NoExpirationTTL,
		CleanupInterval:  time.Hour,
		EnableMetrics:    false,
	}
	lbc := mustNewMemoryLeaderboardCache(t, storecache.DefaultConfig(), lbConfig)
	defer lbc.Close()

	ctx := t.Context()
	if err := lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player1", Score: 100}); err != nil {
		t.Fatalf("ZAdd failed: %v", err)
	}
	if _, err := lbc.ZRange(ctx, "game:scores", 0, 10, true); err != nil {
		t.Fatalf("ZRange failed: %v", err)
	}
	if err := lbc.ZRem(ctx, "game:scores", "player1"); err != nil {
		t.Fatalf("ZRem failed: %v", err)
	}

	metrics := lbc.GetLeaderboardMetrics()
	if metrics.ZAdds != 0 || metrics.ZRangeQueries != 0 || metrics.ZRems != 0 {
		t.Fatalf("operation counters = %+v, want disabled zero counters", metrics)
	}
	if metrics.TotalLeaderboards != 1 {
		t.Fatalf("TotalLeaderboards = %d, want operational total", metrics.TotalLeaderboards)
	}
}

func TestLeaderboardCacheMetricsConcurrentMutationDoesNotDeadlock(t *testing.T) {
	lbc := mustNewMemoryLeaderboardCache(t, storecache.DefaultConfig(), DefaultLeaderboardConfig())
	defer lbc.Close()

	ctx := t.Context()
	if err := lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player", Score: 0}); err != nil {
		t.Fatalf("initial ZAdd failed: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		for i := 0; i < 1000; i++ {
			if err := lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player", Score: float64(i)}); err != nil {
				errCh <- err
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = lbc.GetLeaderboardMetrics()
			}
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case err := <-errCh:
		t.Fatalf("mutation failed: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for concurrent metrics collection and mutation")
	}
}

func TestLeaderboardCacheZRemMetricsCountActualRemovals(t *testing.T) {
	lbc := mustNewMemoryLeaderboardCache(t, storecache.DefaultConfig(), DefaultLeaderboardConfig())
	defer lbc.Close()

	ctx := t.Context()
	if err := lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player1", Score: 100}); err != nil {
		t.Fatalf("ZAdd failed: %v", err)
	}
	if err := lbc.ZRem(ctx, "game:scores", "player1", "missing"); err != nil {
		t.Fatalf("ZRem failed: %v", err)
	}

	metrics := lbc.GetLeaderboardMetrics()
	if metrics.ZRems != 1 {
		t.Fatalf("ZRems = %d, want 1 actual removal", metrics.ZRems)
	}
}

func TestLeaderboardCacheUpdateScore(t *testing.T) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(t, config, lbConfig)
	defer lbc.Close()

	ctx := t.Context()

	// Add initial score
	err := lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player1", Score: 100.0})
	if err != nil {
		t.Fatalf("ZAdd failed: %v", err)
	}

	// Update with different score
	err = lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player1", Score: 150.0})
	if err != nil {
		t.Fatalf("ZAdd update failed: %v", err)
	}

	// Verify updated score
	score, err := lbc.ZScore(ctx, "game:scores", "player1")
	if err != nil {
		t.Errorf("ZScore failed: %v", err)
	}
	if score != 150.0 {
		t.Errorf("expected updated score 150.0, got %f", score)
	}

	// Verify cardinality is still 1
	card, err := lbc.ZCard(ctx, "game:scores")
	if err != nil {
		t.Errorf("ZCard failed: %v", err)
	}
	if card != 1 {
		t.Errorf("expected cardinality 1, got %d", card)
	}
}

func TestLeaderboardCacheRejectsCanceledContext(t *testing.T) {
	lbc := mustNewMemoryLeaderboardCache(t, storecache.DefaultConfig(), DefaultLeaderboardConfig())
	defer lbc.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player1", Score: 100.0})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from ZAdd, got %v", err)
	}

	if card, err := lbc.ZCard(t.Context(), "game:scores"); err != nil || card != 0 {
		t.Fatalf("cardinality = %d, %v; want 0, nil", card, err)
	}
}

func TestLeaderboardCacheValidatesKeys(t *testing.T) {
	config := storecache.DefaultConfig()
	config.MaxKeyLength = 10
	lbc := mustNewMemoryLeaderboardCache(t, config, DefaultLeaderboardConfig())
	defer lbc.Close()

	tests := []struct {
		name    string
		key     string
		wantErr error
	}{
		{
			name:    "empty key",
			key:     "",
			wantErr: storecache.ErrInvalidConfig,
		},
		{
			name:    "control character",
			key:     "bad\nkey",
			wantErr: storecache.ErrInvalidConfig,
		},
		{
			name:    "too long",
			key:     "leaderboard:key",
			wantErr: storecache.ErrKeyTooLong,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := lbc.ZAdd(t.Context(), tc.key, &ZMember{Member: "player1", Score: 1})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}

	metrics := lbc.GetLeaderboardMetrics()
	if metrics.TotalLeaderboards != 0 {
		t.Fatalf("invalid key operations created %d leaderboards, want 0", metrics.TotalLeaderboards)
	}
}

func TestLeaderboardCacheTracksCreateCountAcrossCleanupAndClear(t *testing.T) {
	lbConfig := DefaultLeaderboardConfig()
	lbConfig.MaxLeaderboards = 1
	lbConfig.DefaultTTL = time.Nanosecond
	lbConfig.CleanupInterval = time.Hour
	lbc := mustNewMemoryLeaderboardCache(t, storecache.DefaultConfig(), lbConfig)
	defer lbc.Close()

	ctx := t.Context()
	if err := lbc.ZAdd(ctx, "expired", &ZMember{Member: "player1", Score: 1}); err != nil {
		t.Fatalf("ZAdd expired failed: %v", err)
	}
	if metrics := lbc.GetLeaderboardMetrics(); metrics.TotalLeaderboards != 1 {
		t.Fatalf("TotalLeaderboards = %d, want 1", metrics.TotalLeaderboards)
	}

	time.Sleep(time.Millisecond)
	lbc.cleanupExpiredLeaderboards()
	if metrics := lbc.GetLeaderboardMetrics(); metrics.TotalLeaderboards != 0 {
		t.Fatalf("TotalLeaderboards after cleanup = %d, want 0", metrics.TotalLeaderboards)
	}
	if err := lbc.ZAdd(ctx, "after-cleanup", &ZMember{Member: "player1", Score: 1}); err != nil {
		t.Fatalf("ZAdd after cleanup failed: %v", err)
	}

	if err := lbc.Clear(ctx); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}
	if metrics := lbc.GetLeaderboardMetrics(); metrics.TotalLeaderboards != 0 {
		t.Fatalf("TotalLeaderboards after clear = %d, want 0", metrics.TotalLeaderboards)
	}
	if err := lbc.ZAdd(ctx, "after-clear", &ZMember{Member: "player1", Score: 1}); err != nil {
		t.Fatalf("ZAdd after clear failed: %v", err)
	}
}

func TestLeaderboardCacheRejectsInvalidMembers(t *testing.T) {
	lbc := mustNewMemoryLeaderboardCache(t, storecache.DefaultConfig(), DefaultLeaderboardConfig())
	defer lbc.Close()

	err := lbc.ZAdd(t.Context(), "game:scores", nil)
	if !errors.Is(err, ErrInvalidMember) {
		t.Fatalf("expected ErrInvalidMember for nil member, got %v", err)
	}

	err = lbc.ZAdd(t.Context(), "game:scores", &ZMember{Member: "", Score: 1})
	if !errors.Is(err, ErrInvalidMember) {
		t.Fatalf("expected ErrInvalidMember for empty member, got %v", err)
	}

	if _, err := lbc.ZIncrBy(t.Context(), "game:scores", "", 1); !errors.Is(err, ErrInvalidMember) {
		t.Fatalf("expected ErrInvalidMember from ZIncrBy, got %v", err)
	}
}

func TestLeaderboardCacheMissingKeyContract(t *testing.T) {
	lbc := mustNewMemoryLeaderboardCache(t, storecache.DefaultConfig(), DefaultLeaderboardConfig())
	defer lbc.Close()

	ctx := t.Context()
	if card, err := lbc.ZCard(ctx, "missing"); err != nil || card != 0 {
		t.Fatalf("ZCard missing = %d, %v; want 0, nil", card, err)
	}
	if count, err := lbc.ZCount(ctx, "missing", 0, 10); err != nil || count != 0 {
		t.Fatalf("ZCount missing = %d, %v; want 0, nil", count, err)
	}
	if removed, err := lbc.ZRemRangeByRank(ctx, "missing", 0, 1); err != nil || removed != 0 {
		t.Fatalf("ZRemRangeByRank missing = %d, %v; want 0, nil", removed, err)
	}
	if removed, err := lbc.ZRemRangeByScore(ctx, "missing", 0, 10); err != nil || removed != 0 {
		t.Fatalf("ZRemRangeByScore missing = %d, %v; want 0, nil", removed, err)
	}

	if _, err := lbc.ZScore(ctx, "missing", "player"); !errors.Is(err, ErrLeaderboardNotFound) {
		t.Fatalf("expected ErrLeaderboardNotFound from ZScore, got %v", err)
	}
	if _, err := lbc.ZRange(ctx, "missing", 0, 1, true); !errors.Is(err, ErrLeaderboardNotFound) {
		t.Fatalf("expected ErrLeaderboardNotFound from ZRange, got %v", err)
	}
	if _, err := lbc.ZRangeByScore(ctx, "missing", 0, 10, true); !errors.Is(err, ErrLeaderboardNotFound) {
		t.Fatalf("expected ErrLeaderboardNotFound from ZRangeByScore, got %v", err)
	}
	if err := lbc.ZRem(ctx, "missing", "player"); !errors.Is(err, ErrLeaderboardNotFound) {
		t.Fatalf("expected ErrLeaderboardNotFound from ZRem, got %v", err)
	}
	if _, err := lbc.ZRank(ctx, "missing", "player", true); !errors.Is(err, ErrLeaderboardNotFound) {
		t.Fatalf("expected ErrLeaderboardNotFound from ZRank, got %v", err)
	}
}

func TestLeaderboardCacheSameScoreUpdateDoesNotDuplicate(t *testing.T) {
	lbc := mustNewMemoryLeaderboardCache(t, storecache.DefaultConfig(), DefaultLeaderboardConfig())
	defer lbc.Close()

	ctx := t.Context()
	if err := lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player1", Score: 100}); err != nil {
		t.Fatalf("initial ZAdd failed: %v", err)
	}
	if err := lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player1", Score: 100}); err != nil {
		t.Fatalf("same-score ZAdd failed: %v", err)
	}

	card, err := lbc.ZCard(ctx, "game:scores")
	if err != nil {
		t.Fatalf("ZCard failed: %v", err)
	}
	if card != 1 {
		t.Fatalf("cardinality = %d, want 1", card)
	}

	results, err := lbc.ZRange(ctx, "game:scores", 0, -1, true)
	if err != nil {
		t.Fatalf("ZRange failed: %v", err)
	}
	if len(results) != 1 || results[0].Member != "player1" {
		t.Fatalf("results = %#v, want one player1", results)
	}
}

func TestLeaderboardCacheInvalidIncrementKeepsStateConsistent(t *testing.T) {
	lbc := mustNewMemoryLeaderboardCache(t, storecache.DefaultConfig(), DefaultLeaderboardConfig())
	defer lbc.Close()

	ctx := t.Context()
	if err := lbc.ZAdd(ctx, "game:scores", &ZMember{Member: "player1", Score: math.MaxFloat64}); err != nil {
		t.Fatalf("ZAdd failed: %v", err)
	}

	if _, err := lbc.ZIncrBy(ctx, "game:scores", "player1", math.MaxFloat64); !errors.Is(err, ErrInvalidScore) {
		t.Fatalf("expected ErrInvalidScore, got %v", err)
	}

	score, err := lbc.ZScore(ctx, "game:scores", "player1")
	if err != nil {
		t.Fatalf("ZScore failed: %v", err)
	}
	if score != math.MaxFloat64 {
		t.Fatalf("score = %f, want MaxFloat64", score)
	}

	card, err := lbc.ZCard(ctx, "game:scores")
	if err != nil {
		t.Fatalf("ZCard failed: %v", err)
	}
	if card != 1 {
		t.Fatalf("cardinality = %d, want 1", card)
	}

	rank, err := lbc.ZRank(ctx, "game:scores", "player1", false)
	if err != nil {
		t.Fatalf("ZRank failed: %v", err)
	}
	if rank != 0 {
		t.Fatalf("rank = %d, want 0", rank)
	}

	results, err := lbc.ZRange(ctx, "game:scores", 0, -1, false)
	if err != nil {
		t.Fatalf("ZRange failed: %v", err)
	}
	if len(results) != 1 || results[0].Member != "player1" || results[0].Score != math.MaxFloat64 {
		t.Fatalf("range after invalid increment = %#v, want one unchanged player1", results)
	}
}
