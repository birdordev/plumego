package kvstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func BenchmarkCapacityEvictionSingle(b *testing.B) {
	for _, size := range []int{2000, 8000, 32000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			store := &KVStore{
				opts: Config{MaxEntries: size, MaxMemoryMB: 200},
				data: make(map[string]*entry, size+1),
			}
			epoch := time.Unix(1000, 0)
			for i := range size {
				store.data[fmt.Sprint(i)] = &entry{UpdatedAt: epoch.Add(time.Duration(i) * time.Millisecond), Size: 100}
			}
			oldest := &entry{UpdatedAt: epoch.Add(-time.Second), Size: 100}
			b.ReportAllocs()
			for b.Loop() {
				store.data["oldest"] = oldest
				store.evictIfNeededLocked("")
			}
			if _, present := store.data["oldest"]; present || len(store.data) != size {
				b.Fatal("unexpected eviction")
			}
		})
	}
}

func BenchmarkLoadTrimSnapshot(b *testing.B) {
	for _, size := range []int{2000, 4000, 8000} {
		for _, limit := range []int{size, size / 2} {
			b.Run(fmt.Sprintf("%d/limit-%d", size, limit), func(b *testing.B) {
				dir := b.TempDir()
				type item struct {
					Value []byte `json:"value"`
				}
				entries := make(map[string]item, size)
				value := []byte("value")
				for i := range size {
					entries[fmt.Sprintf("key-%05d", i)] = item{Value: value}
				}
				raw, err := json.Marshal(map[string]any{"entries": entries})
				if err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, stateFileName), raw, 0600); err != nil {
					b.Fatal(err)
				}
				for b.Loop() {
					store, err := NewKVStore(Config{DataDir: dir, MaxEntries: limit})
					if err != nil {
						b.Fatal(err)
					}
					if err := store.Close(); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkKVWriteSnapshotScale(b *testing.B) {
	for _, size := range []int{10, 100, 1000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			opts := DefaultConfig(b.TempDir())
			kv, err := NewKVStore(opts)
			if err != nil {
				b.Fatal(err)
			}
			defer kv.Close()
			payload := make([]byte, 128)
			for i := range size {
				key := fmt.Sprint(i)
				kv.data[key] = &entry{Value: payload, Size: entrySize(key, payload)}
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := kv.Set("updated", payload, 0); err != nil {
					b.Fatal(err)
				}
			}
			info, err := os.Stat(filepath.Join(opts.DataDir, stateFileName))
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(info.Size()), "snapshot-B")
		})
	}
}
