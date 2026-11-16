package kvstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

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
