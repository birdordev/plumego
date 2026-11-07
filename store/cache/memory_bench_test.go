package cache

import (
	"context"
	"testing"
	"time"
)

func BenchmarkMemoryCacheGet(b *testing.B) {
	ctx := context.Background()
	c := NewMemoryCache()
	_ = c.Set(ctx, "key", []byte("value"), 1*time.Hour)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Get(ctx, "key")
	}
}

func BenchmarkMemoryCacheGetMiss(b *testing.B) {
	ctx := context.Background()
	c := NewMemoryCache()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = c.Get(ctx, "nonexistent")
	}
}

func BenchmarkMemoryCacheSet(b *testing.B) {
	ctx := context.Background()
	c := NewMemoryCache()
	expiry := 1 * time.Hour
	val := []byte("value")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.Set(ctx, "key", val, expiry)
	}
}

func BenchmarkMemoryCacheSetParallel(b *testing.B) {
	ctx := context.Background()
	c := NewMemoryCache()
	expiry := 1 * time.Hour
	val := []byte("value")

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			_ = c.Set(ctx, "k", val, expiry)
			i++
		}
	})
}

func BenchmarkMemoryCacheGetSetParallel(b *testing.B) {
	ctx := context.Background()
	c := NewMemoryCache()
	expiry := 1 * time.Hour
	val := []byte("value")
	_ = c.Set(ctx, "key", val, expiry)

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = c.Set(ctx, "key", val, expiry)
			_, _ = c.Get(ctx, "key")
		}
	})
}

func BenchmarkMemoryCacheDeleteHit(b *testing.B) {
	ctx := context.Background()
	c := NewMemoryCache()
	val := []byte("value")
	_ = c.Set(ctx, "key", val, 1*time.Hour)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.Delete(ctx, "key")
		_ = c.Set(ctx, "key", val, 1*time.Hour)
	}
}
