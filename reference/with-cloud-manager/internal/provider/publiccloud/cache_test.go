package publiccloud

import (
	"testing"

	"with-cloud-manager/internal/provider"
)

func TestHuaweiCachePageCount(t *testing.T) {
	for _, tc := range []struct {
		name          string
		offset, count int
		size          int
		next          string
		failure       provider.FailureKind
	}{
		{name: "known-total", count: 101, size: 50, next: "0:50:101"},
		{name: "ambiguous-full-page", count: 50, size: 50, next: "50"},
		{name: "page-count-continues", offset: 50, count: 50, size: 50, next: "100"},
		{name: "page-count-short", offset: 50, count: 1, size: 1},
		{name: "exact-total-boundary", offset: 50, count: 50},
		{name: "absolute-total-final", offset: 50, count: 51, size: 1},
		{name: "skips-rows", offset: 50, count: 53, size: 1, failure: provider.ExpiredCursor},
		{name: "count-less-than-row-count", count: 1, size: 2, failure: provider.InvalidResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next, err := huaweiCacheNext(tc.offset, &tc.count, tc.size)
			if next != tc.next || (tc.failure == "" && err != nil) || (tc.failure != "" && !provider.IsFailure(err, tc.failure)) {
				t.Fatalf("next=%q err=%v, want next=%q failure=%q", next, err, tc.next, tc.failure)
			}
		})
	}
}

func TestTencentCacheEngineFallback(t *testing.T) {
	for _, tc := range []struct {
		row             tencentCacheRow
		engine, version string
	}{
		{row: tencentCacheRow{Type: 18, CurrentRedisVersion: "7.0.9"}, engine: "redis", version: "7.0.9"},
		{row: tencentCacheRow{Type: 16}, engine: "redis", version: "6.2"},
		{row: tencentCacheRow{Type: 3}, engine: "ckv", version: "3.2"},
		{row: tencentCacheRow{Type: 200}, engine: "memcached", version: "1.6"},
	} {
		engine, version := tc.row.engineVersion()
		if engine != tc.engine || version != tc.version {
			t.Fatalf("engine=%q version=%q, want %q %q", engine, version, tc.engine, tc.version)
		}
	}
}
