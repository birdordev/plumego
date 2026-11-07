package leaderboard

import (
	"fmt"
	"testing"

	storecache "github.com/spcent/plumego/store/cache"
)

func BenchmarkSkipListInsert(b *testing.B) {
	sl := newSkipList()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sl.insert(fmt.Sprintf("member%d", i), float64(i))
	}
}

func BenchmarkSkipListGetScore(b *testing.B) {
	sl := newSkipList()

	// Populate
	for i := 0; i < 10000; i++ {
		sl.insert(fmt.Sprintf("member%d", i), float64(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sl.getScore(fmt.Sprintf("member%d", i%10000))
	}
}

func BenchmarkSkipListDelete(b *testing.B) {
	b.StopTimer()
	sl := newSkipList()

	// Populate
	for i := 0; i < b.N; i++ {
		sl.insert(fmt.Sprintf("member%d", i), float64(i))
	}

	b.StartTimer()
	for i := 0; i < b.N; i++ {
		sl.delete(fmt.Sprintf("member%d", i), float64(i))
	}
}

func BenchmarkSkipListGetRange(b *testing.B) {
	sl := newSkipList()

	// Populate
	for i := 0; i < 10000; i++ {
		sl.insert(fmt.Sprintf("member%d", i), float64(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sl.getRange(0, 99, false)
	}
}

func BenchmarkSkipListGetRangeByScore(b *testing.B) {
	sl := newSkipList()

	// Populate
	for i := 0; i < 10000; i++ {
		sl.insert(fmt.Sprintf("member%d", i), float64(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sl.getRangeByScore(1000.0, 2000.0, false)
	}
}

func BenchmarkSkipListGetRank(b *testing.B) {
	sl := newSkipList()

	// Populate
	for i := 0; i < 10000; i++ {
		sl.insert(fmt.Sprintf("member%d", i), float64(i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sl.getRank(fmt.Sprintf("member%d", i%10000), float64(i%10000), false)
	}
}

func BenchmarkLeaderboardCacheZAdd(b *testing.B) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(b, config, lbConfig)
	defer lbc.Close()

	ctx := b.Context()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lbc.ZAdd(ctx, "benchmark", &ZMember{
			Member: fmt.Sprintf("member%d", i),
			Score:  float64(i),
		})
	}
}

func BenchmarkLeaderboardCacheZScore(b *testing.B) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(b, config, lbConfig)
	defer lbc.Close()

	ctx := b.Context()

	// Populate
	for i := 0; i < 10000; i++ {
		lbc.ZAdd(ctx, "benchmark", &ZMember{
			Member: fmt.Sprintf("member%d", i),
			Score:  float64(i),
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lbc.ZScore(ctx, "benchmark", fmt.Sprintf("member%d", i%10000))
	}
}

func BenchmarkLeaderboardCacheZIncrBy(b *testing.B) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(b, config, lbConfig)
	defer lbc.Close()

	ctx := b.Context()

	// Populate
	for i := 0; i < 10000; i++ {
		lbc.ZAdd(ctx, "benchmark", &ZMember{
			Member: fmt.Sprintf("member%d", i),
			Score:  float64(i),
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lbc.ZIncrBy(ctx, "benchmark", fmt.Sprintf("member%d", i%10000), 1.0)
	}
}

func BenchmarkLeaderboardCacheZRange(b *testing.B) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(b, config, lbConfig)
	defer lbc.Close()

	ctx := b.Context()

	// Populate
	for i := 0; i < 10000; i++ {
		lbc.ZAdd(ctx, "benchmark", &ZMember{
			Member: fmt.Sprintf("member%d", i),
			Score:  float64(i),
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lbc.ZRange(ctx, "benchmark", 0, 99, true)
	}
}

func BenchmarkLeaderboardCacheZRangeByScore(b *testing.B) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(b, config, lbConfig)
	defer lbc.Close()

	ctx := b.Context()

	// Populate
	for i := 0; i < 10000; i++ {
		lbc.ZAdd(ctx, "benchmark", &ZMember{
			Member: fmt.Sprintf("member%d", i),
			Score:  float64(i),
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lbc.ZRangeByScore(ctx, "benchmark", 1000.0, 2000.0, false)
	}
}

func BenchmarkLeaderboardCacheZRank(b *testing.B) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(b, config, lbConfig)
	defer lbc.Close()

	ctx := b.Context()

	// Populate
	for i := 0; i < 10000; i++ {
		lbc.ZAdd(ctx, "benchmark", &ZMember{
			Member: fmt.Sprintf("member%d", i),
			Score:  float64(i),
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lbc.ZRank(ctx, "benchmark", fmt.Sprintf("member%d", i%10000), true)
	}
}

func BenchmarkLeaderboardCacheZCard(b *testing.B) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(b, config, lbConfig)
	defer lbc.Close()

	ctx := b.Context()

	// Populate
	for i := 0; i < 10000; i++ {
		lbc.ZAdd(ctx, "benchmark", &ZMember{
			Member: fmt.Sprintf("member%d", i),
			Score:  float64(i),
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lbc.ZCard(ctx, "benchmark")
	}
}

func BenchmarkLeaderboardCacheZCount(b *testing.B) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(b, config, lbConfig)
	defer lbc.Close()

	ctx := b.Context()

	// Populate
	for i := 0; i < 10000; i++ {
		lbc.ZAdd(ctx, "benchmark", &ZMember{
			Member: fmt.Sprintf("member%d", i),
			Score:  float64(i),
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lbc.ZCount(ctx, "benchmark", 1000.0, 2000.0)
	}
}

func BenchmarkLeaderboardCacheScoreRangeFullScanBaseline(b *testing.B) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(b, config, lbConfig)
	defer lbc.Close()

	ctx := b.Context()
	for i := 0; i < lbConfig.MaxMembersPerSet; i++ {
		lbc.ZAdd(ctx, "benchmark", &ZMember{
			Member: fmt.Sprintf("member%d", i),
			Score:  float64(i),
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lbc.ZCount(ctx, "benchmark", 0, float64(lbConfig.MaxMembersPerSet))
	}
}

// Benchmark different leaderboard sizes
func BenchmarkLeaderboardCacheZAdd_SmallSet(b *testing.B) {
	benchmarkZAddWithSize(b, 100)
}

func BenchmarkLeaderboardCacheZAdd_MediumSet(b *testing.B) {
	benchmarkZAddWithSize(b, 1000)
}

func BenchmarkLeaderboardCacheZAdd_LargeSet(b *testing.B) {
	benchmarkZAddWithSize(b, 10000)
}

func benchmarkZAddWithSize(b *testing.B, size int) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()
	lbConfig.MaxMembersPerSet = size * 2

	lbc := mustNewMemoryLeaderboardCache(b, config, lbConfig)
	defer lbc.Close()

	ctx := b.Context()

	// Pre-populate
	for i := 0; i < size; i++ {
		lbc.ZAdd(ctx, "benchmark", &ZMember{
			Member: fmt.Sprintf("member%d", i),
			Score:  float64(i),
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lbc.ZAdd(ctx, "benchmark", &ZMember{
			Member: fmt.Sprintf("new%d", i),
			Score:  float64(size + i),
		})
	}
}

// Benchmark parallel operations
func BenchmarkLeaderboardCacheParallelZAdd(b *testing.B) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(b, config, lbConfig)
	defer lbc.Close()

	ctx := b.Context()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			lbc.ZAdd(ctx, "benchmark", &ZMember{
				Member: fmt.Sprintf("member%d", i),
				Score:  float64(i),
			})
			i++
		}
	})
}

func BenchmarkLeaderboardCacheParallelZScore(b *testing.B) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(b, config, lbConfig)
	defer lbc.Close()

	ctx := b.Context()

	// Populate
	for i := 0; i < 10000; i++ {
		lbc.ZAdd(ctx, "benchmark", &ZMember{
			Member: fmt.Sprintf("member%d", i),
			Score:  float64(i),
		})
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, _ = lbc.ZScore(ctx, "benchmark", fmt.Sprintf("member%d", i%10000))
			i++
		}
	})
}

func BenchmarkLeaderboardCacheParallelZRank(b *testing.B) {
	config := storecache.DefaultConfig()
	lbConfig := DefaultLeaderboardConfig()

	lbc := mustNewMemoryLeaderboardCache(b, config, lbConfig)
	defer lbc.Close()

	ctx := b.Context()

	// Populate
	for i := 0; i < 10000; i++ {
		lbc.ZAdd(ctx, "benchmark", &ZMember{
			Member: fmt.Sprintf("member%d", i),
			Score:  float64(i),
		})
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_, _ = lbc.ZRank(ctx, "benchmark", fmt.Sprintf("member%d", i%10000), true)
			i++
		}
	})
}
