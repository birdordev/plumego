package sharding

import (
	"errors"
	"slices"
	"testing"
	"time"
)

// TestNormalizeShardIndex tests the normalizeShardIndex helper function.
func TestNormalizeShardIndex(t *testing.T) {
	tests := []struct {
		name      string
		idx       int
		numShards int
		want      int
	}{
		{
			name:      "positive within range",
			idx:       2,
			numShards: 4,
			want:      2,
		},
		{
			name:      "positive exact boundary",
			idx:       4,
			numShards: 4,
			want:      0,
		},
		{
			name:      "positive exceeds range",
			idx:       7,
			numShards: 4,
			want:      3,
		},
		{
			name:      "zero index",
			idx:       0,
			numShards: 4,
			want:      0,
		},
		{
			name:      "negative index",
			idx:       -3,
			numShards: 4,
			want:      3,
		},
		{
			name:      "negative exceeds range",
			idx:       -7,
			numShards: 4,
			want:      3,
		},
		{
			name:      "large positive",
			idx:       1000003,
			numShards: 4,
			want:      3,
		},
		{
			name:      "large negative",
			idx:       -1000003,
			numShards: 4,
			want:      3,
		},
		{
			name:      "single shard",
			idx:       42,
			numShards: 1,
			want:      0,
		},
		{
			name:      "negative with single shard",
			idx:       -42,
			numShards: 1,
			want:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeShardIndex(tt.idx, tt.numShards)
			if got != tt.want {
				t.Errorf("normalizeShardIndex(%d, %d) = %d, want %d", tt.idx, tt.numShards, got, tt.want)
			}
			if got < 0 || got >= tt.numShards {
				t.Errorf("normalizeShardIndex(%d, %d) = %d, out of range [0, %d)", tt.idx, tt.numShards, got, tt.numShards)
			}
		})
	}
}

// TestValidateShardKey tests the validateShardKey helper function.
func TestValidateShardKey(t *testing.T) {
	tests := []struct {
		name    string
		key     any
		wantErr bool
	}{
		{name: "string key", key: "test", wantErr: false},
		{name: "int key", key: 42, wantErr: false},
		{name: "int64 key", key: int64(42), wantErr: false},
		{name: "uint64 key", key: uint64(42), wantErr: false},
		{name: "byte slice key", key: []byte("test"), wantErr: false},
		{name: "struct key", key: struct{}{}, wantErr: false},
		{name: "nil key", key: nil, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateShardKey(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateShardKey(%v) error = %v, wantErr %v", tt.key, err, tt.wantErr)
			}
			if tt.wantErr && err != nil && err != ErrNilShardKey {
				t.Errorf("validateShardKey(nil) should return ErrNilShardKey, got %v", err)
			}
		})
	}
}

// TestValidateNumShards tests the validateNumShards helper function.
func TestValidateNumShards(t *testing.T) {
	tests := []struct {
		name      string
		numShards int
		wantErr   bool
	}{
		{name: "positive", numShards: 4, wantErr: false},
		{name: "one", numShards: 1, wantErr: false},
		{name: "large", numShards: 1024, wantErr: false},
		{name: "zero", numShards: 0, wantErr: true},
		{name: "negative", numShards: -1, wantErr: true},
		{name: "large negative", numShards: -100, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNumShards(tt.numShards)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateNumShards(%d) error = %v, wantErr %v", tt.numShards, err, tt.wantErr)
			}
		})
	}
}

// TestHashStrategy tests the hash-based sharding strategy.
func TestHashStrategy(t *testing.T) {
	strategy := NewHashStrategy()

	tests := []struct {
		name      string
		key       any
		numShards int
		wantErr   bool
	}{
		{
			name:      "string key",
			key:       "user123",
			numShards: 4,
			wantErr:   false,
		},
		{
			name:      "int key",
			key:       12345,
			numShards: 4,
			wantErr:   false,
		},
		{
			name:      "int64 key",
			key:       int64(99999),
			numShards: 8,
			wantErr:   false,
		},
		{
			name:      "uint64 key",
			key:       uint64(123456789),
			numShards: 16,
			wantErr:   false,
		},
		{
			name:      "byte slice key",
			key:       []byte("test"),
			numShards: 4,
			wantErr:   false,
		},
		{
			name:      "nil key",
			key:       nil,
			numShards: 4,
			wantErr:   true,
		},
		{
			name:      "zero shards",
			key:       "test",
			numShards: 0,
			wantErr:   true,
		},
		{
			name:      "negative shards",
			key:       "test",
			numShards: -1,
			wantErr:   true,
		},
		{
			name:      "unsupported key type",
			key:       struct{}{},
			numShards: 4,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shard, err := strategy.Shard(tt.key, tt.numShards)
			if (err != nil) != tt.wantErr {
				t.Errorf("Shard() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if shard < 0 || shard >= tt.numShards {
					t.Errorf("Shard() = %v, want value in range [0, %d)", shard, tt.numShards)
				}
			}
		})
	}
}

// TestHashStrategyConsistency tests that the same key always maps to the same shard.
func TestHashStrategyConsistency(t *testing.T) {
	strategy := NewHashStrategy()
	key := "user123"
	numShards := 4

	shard1, err := strategy.Shard(key, numShards)
	if err != nil {
		t.Fatalf("Shard() error = %v", err)
	}

	// Call multiple times - should return same shard
	for i := 0; i < 100; i++ {
		shard2, err := strategy.Shard(key, numShards)
		if err != nil {
			t.Fatalf("Shard() error = %v", err)
		}
		if shard1 != shard2 {
			t.Errorf("Inconsistent sharding: got %v, want %v", shard2, shard1)
		}
	}
}

// TestHashStrategyDistribution tests that keys are distributed reasonably across shards.
func TestHashStrategyDistribution(t *testing.T) {
	strategy := NewHashStrategy()
	numShards := 4
	numKeys := 1000
	distribution := make(map[int]int)

	for i := 0; i < numKeys; i++ {
		key := "user" + string(rune(i))
		shard, err := strategy.Shard(key, numShards)
		if err != nil {
			t.Fatalf("Shard() error = %v", err)
		}
		distribution[shard]++
	}

	// Check that all shards got at least some keys
	for i := 0; i < numShards; i++ {
		count := distribution[i]
		if count == 0 {
			t.Errorf("Shard %d got no keys", i)
		}
		// Check distribution is reasonable (within 40% of average)
		avg := numKeys / numShards
		if count < avg*6/10 || count > avg*14/10 {
			t.Logf("Warning: Shard %d got %d keys (expected ~%d)", i, count, avg)
		}
	}
}

// TestHashStrategyShardRange tests range queries.
func TestHashStrategyShardRange(t *testing.T) {
	strategy := NewHashStrategy()
	numShards := 4

	shards, err := strategy.ShardRange("start", "end", numShards)
	if err != nil {
		t.Fatalf("ShardRange() error = %v", err)
	}

	// Hash doesn't preserve order, so should return all shards
	if len(shards) != numShards {
		t.Errorf("ShardRange() returned %d shards, want %d", len(shards), numShards)
	}

	// Check all shards are included
	shardSet := make(map[int]bool)
	for _, s := range shards {
		shardSet[s] = true
	}
	for i := 0; i < numShards; i++ {
		if !shardSet[i] {
			t.Errorf("Shard %d missing from range query", i)
		}
	}
}

// TestModStrategy tests the modulo-based sharding strategy.
func TestModStrategy(t *testing.T) {
	strategy := NewModStrategy()

	tests := []struct {
		name      string
		key       any
		numShards int
		wantShard int
		wantErr   bool
	}{
		{
			name:      "simple positive",
			key:       int64(12345),
			numShards: 4,
			wantShard: 1, // 12345 % 4 = 1
			wantErr:   false,
		},
		{
			name:      "negative key",
			key:       int64(-12345),
			numShards: 4,
			wantShard: 1, // abs(-12345) % 4 = 1
			wantErr:   false,
		},
		{
			name:      "zero key",
			key:       int64(0),
			numShards: 4,
			wantShard: 0,
			wantErr:   false,
		},
		{
			name:      "int type",
			key:       100,
			numShards: 3,
			wantShard: 1, // 100 % 3 = 1
			wantErr:   false,
		},
		{
			name:      "uint type",
			key:       uint64(50),
			numShards: 7,
			wantShard: 1, // 50 % 7 = 1
			wantErr:   false,
		},
		{
			name:      "nil key",
			key:       nil,
			numShards: 4,
			wantErr:   true,
		},
		{
			name:      "non-integer key",
			key:       "string",
			numShards: 4,
			wantErr:   true,
		},
		{
			name:      "zero shards",
			key:       int64(100),
			numShards: 0,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shard, err := strategy.Shard(tt.key, tt.numShards)
			if (err != nil) != tt.wantErr {
				t.Errorf("Shard() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && shard != tt.wantShard {
				t.Errorf("Shard() = %v, want %v", shard, tt.wantShard)
			}
		})
	}
}

// TestModStrategyShardRange tests range queries for modulo sharding.
func TestModStrategyShardRange(t *testing.T) {
	strategy := NewModStrategy()

	tests := []struct {
		name      string
		start     any
		end       any
		numShards int
		wantLen   int
		wantErr   bool
	}{
		{
			name:      "small range",
			start:     int64(10),
			end:       int64(13),
			numShards: 4,
			wantLen:   4, // 10%4=2, 11%4=3, 12%4=0, 13%4=1 -> all 4 shards
			wantErr:   false,
		},
		{
			name:      "large range",
			start:     int64(0),
			end:       int64(100),
			numShards: 4,
			wantLen:   4, // Range > numShards -> all shards
			wantErr:   false,
		},
		{
			name:      "reversed range",
			start:     int64(20),
			end:       int64(10),
			numShards: 4,
			wantLen:   4,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shards, err := strategy.ShardRange(tt.start, tt.end, tt.numShards)
			if (err != nil) != tt.wantErr {
				t.Errorf("ShardRange() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(shards) != tt.wantLen {
				t.Errorf("ShardRange() returned %d shards, want %d", len(shards), tt.wantLen)
			}
		})
	}
}

// TestRangeStrategy tests the range-based sharding strategy.
func TestRangeStrategy(t *testing.T) {
	ranges := []RangeDefinition{
		{Start: int64(0), End: int64(10000), Shard: 0},
		{Start: int64(10000), End: int64(20000), Shard: 1},
		{Start: int64(20000), End: int64(30000), Shard: 2},
	}

	strategy, err := NewRangeStrategy(ranges)
	if err != nil {
		t.Fatalf("NewRangeStrategy() error = %v", err)
	}

	tests := []struct {
		name      string
		key       any
		numShards int
		wantShard int
		wantErr   bool
	}{
		{
			name:      "first shard",
			key:       int64(5000),
			numShards: 3,
			wantShard: 0,
			wantErr:   false,
		},
		{
			name:      "second shard",
			key:       int64(15000),
			numShards: 3,
			wantShard: 1,
			wantErr:   false,
		},
		{
			name:      "third shard",
			key:       int64(25000),
			numShards: 3,
			wantShard: 2,
			wantErr:   false,
		},
		{
			name:      "boundary start",
			key:       int64(10000),
			numShards: 3,
			wantShard: 1,
			wantErr:   false,
		},
		{
			name:      "boundary end",
			key:       int64(19999),
			numShards: 3,
			wantShard: 1,
			wantErr:   false,
		},
		{
			name:      "below range",
			key:       int64(-1),
			numShards: 3,
			wantErr:   true,
		},
		{
			name:      "above range",
			key:       int64(30000),
			numShards: 3,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shard, err := strategy.Shard(tt.key, tt.numShards)
			if (err != nil) != tt.wantErr {
				t.Errorf("Shard() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && shard != tt.wantShard {
				t.Errorf("Shard() = %v, want %v", shard, tt.wantShard)
			}
		})
	}
}

// TestRangeStrategyWithTime tests range sharding with time.Time keys.
func TestRangeStrategyWithTime(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	ranges := []RangeDefinition{
		{Start: base, End: base.AddDate(0, 3, 0), Shard: 0},                   // Q1
		{Start: base.AddDate(0, 3, 0), End: base.AddDate(0, 6, 0), Shard: 1},  // Q2
		{Start: base.AddDate(0, 6, 0), End: base.AddDate(0, 9, 0), Shard: 2},  // Q3
		{Start: base.AddDate(0, 9, 0), End: base.AddDate(0, 12, 0), Shard: 3}, // Q4
	}

	strategy, err := NewRangeStrategy(ranges)
	if err != nil {
		t.Fatalf("NewRangeStrategy() error = %v", err)
	}

	// Test February (Q1)
	feb := time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC)
	shard, err := strategy.Shard(feb, 4)
	if err != nil {
		t.Fatalf("Shard() error = %v", err)
	}
	if shard != 0 {
		t.Errorf("February should be in shard 0, got %d", shard)
	}

	// Test July (Q3)
	july := time.Date(2024, 7, 15, 0, 0, 0, 0, time.UTC)
	shard, err = strategy.Shard(july, 4)
	if err != nil {
		t.Fatalf("Shard() error = %v", err)
	}
	if shard != 2 {
		t.Errorf("July should be in shard 2, got %d", shard)
	}
}

// TestRangeStrategyShardRange tests range queries.
func TestRangeStrategyShardRange(t *testing.T) {
	ranges := []RangeDefinition{
		{Start: int64(0), End: int64(10000), Shard: 0},
		{Start: int64(10000), End: int64(20000), Shard: 1},
		{Start: int64(20000), End: int64(30000), Shard: 2},
	}

	strategy, err := NewRangeStrategy(ranges)
	if err != nil {
		t.Fatalf("NewRangeStrategy() error = %v", err)
	}

	tests := []struct {
		name       string
		start      any
		end        any
		numShards  int
		wantShards []int
		wantErr    bool
	}{
		{
			name:       "single shard",
			start:      int64(5000),
			end:        int64(8000),
			numShards:  3,
			wantShards: []int{0},
			wantErr:    false,
		},
		{
			name:       "two shards",
			start:      int64(8000),
			end:        int64(15000),
			numShards:  3,
			wantShards: []int{0, 1},
			wantErr:    false,
		},
		{
			name:       "all shards",
			start:      int64(5000),
			end:        int64(25000),
			numShards:  3,
			wantShards: []int{0, 1, 2},
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shards, err := strategy.ShardRange(tt.start, tt.end, tt.numShards)
			if (err != nil) != tt.wantErr {
				t.Errorf("ShardRange() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if len(shards) != len(tt.wantShards) {
					t.Errorf("ShardRange() returned %d shards, want %d", len(shards), len(tt.wantShards))
					return
				}
				for i, want := range tt.wantShards {
					if shards[i] != want {
						t.Errorf("ShardRange()[%d] = %v, want %v", i, shards[i], want)
					}
				}
			}
		})
	}
}

// TestListStrategy tests the list-based sharding strategy.
func TestListStrategy(t *testing.T) {
	mapping := map[any]int{
		"US": 0,
		"EU": 1,
		"CN": 2,
		"JP": 3,
	}

	strategy := NewListStrategy(mapping)

	tests := []struct {
		name      string
		key       any
		numShards int
		wantShard int
		wantErr   bool
	}{
		{
			name:      "US",
			key:       "US",
			numShards: 4,
			wantShard: 0,
			wantErr:   false,
		},
		{
			name:      "EU",
			key:       "EU",
			numShards: 4,
			wantShard: 1,
			wantErr:   false,
		},
		{
			name:      "unmapped key",
			key:       "UK",
			numShards: 4,
			wantErr:   true,
		},
		{
			name:      "nil key",
			key:       nil,
			numShards: 4,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shard, err := strategy.Shard(tt.key, tt.numShards)
			if (err != nil) != tt.wantErr {
				t.Errorf("Shard() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && shard != tt.wantShard {
				t.Errorf("Shard() = %v, want %v", shard, tt.wantShard)
			}
		})
	}
}

// TestListStrategyWithDefault tests list strategy with a default shard.
func TestListStrategyWithDefault(t *testing.T) {
	mapping := map[any]int{
		"US": 0,
		"EU": 1,
	}

	strategy := NewListStrategyWithDefault(mapping, 2)

	// Mapped key
	shard, err := strategy.Shard("US", 3)
	if err != nil {
		t.Fatalf("Shard() error = %v", err)
	}
	if shard != 0 {
		t.Errorf("Shard() = %v, want 0", shard)
	}

	// Unmapped key should use default
	shard, err = strategy.Shard("UK", 3)
	if err != nil {
		t.Fatalf("Shard() error = %v", err)
	}
	if shard != 2 {
		t.Errorf("Shard() = %v, want 2 (default)", shard)
	}
}

// TestListStrategyDynamicMapping tests adding and removing mappings.
func TestListStrategyDynamicMapping(t *testing.T) {
	strategy := NewListStrategy(map[any]int{
		"US": 0,
	})

	// Add a new mapping
	strategy.AddMapping("EU", 1)
	shard, err := strategy.Shard("EU", 2)
	if err != nil {
		t.Fatalf("Shard() error = %v", err)
	}
	if shard != 1 {
		t.Errorf("Shard() = %v, want 1", shard)
	}

	// Remove a mapping
	strategy.RemoveMapping("US")
	_, err = strategy.Shard("US", 2)
	if err == nil {
		t.Error("Shard() should error for removed mapping")
	}
}

// TestRangeStrategyValidation tests validation of range definitions.
func TestRangeStrategyValidation(t *testing.T) {
	tests := []struct {
		name    string
		ranges  []RangeDefinition
		wantErr bool
	}{
		{
			name:    "empty ranges",
			ranges:  []RangeDefinition{},
			wantErr: true,
		},
		{
			name: "overlapping ranges",
			ranges: []RangeDefinition{
				{Start: int64(0), End: int64(100), Shard: 0},
				{Start: int64(50), End: int64(150), Shard: 1},
			},
			wantErr: true,
		},
		{
			name: "invalid order",
			ranges: []RangeDefinition{
				{Start: int64(100), End: int64(0), Shard: 0},
			},
			wantErr: true,
		},
		{
			name: "valid non-overlapping",
			ranges: []RangeDefinition{
				{Start: int64(0), End: int64(100), Shard: 0},
				{Start: int64(100), End: int64(200), Shard: 1},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRangeStrategy(tt.ranges)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewRangeStrategy() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRangeStrategyRejectsIncompatibleEndpointTypes(t *testing.T) {
	for _, tt := range []struct {
		name   string
		ranges []RangeDefinition
	}{
		{"mixed endpoints", []RangeDefinition{{Start: 0, End: int64(10)}}},
		{"mixed ranges", []RangeDefinition{{Start: 0, End: 10}, {Start: int64(10), End: int64(20)}}},
		{"unsupported", []RangeDefinition{{Start: 0.0, End: 1.0}}},
		{"nil", []RangeDefinition{{Start: nil, End: nil}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if strategy, err := NewRangeStrategy(tt.ranges); strategy != nil || !errors.Is(err, ErrInvalidShardKey) {
				t.Fatalf("NewRangeStrategy = %v, %v", strategy, err)
			}
		})
	}
}

func TestRangeStrategyChecksQueryTypesBeforeComparing(t *testing.T) {
	ranges := []RangeDefinition{
		{Start: int64(10), End: int64(20), Shard: 1},
		{Start: int64(0), End: int64(10), Shard: 0},
	}
	strategy, err := NewRangeStrategy(ranges)
	if err != nil {
		t.Fatal(err)
	}
	if ranges[0].Shard != 1 {
		t.Fatal("constructor sorted the caller's slice")
	}
	for _, key := range []any{1, "1", []int{1}} {
		if _, err := strategy.Shard(key, 2); !errors.Is(err, ErrInvalidShardKey) {
			t.Errorf("Shard(%T) = %v", key, err)
		}
	}
	if _, err := strategy.Shard(nil, 2); !errors.Is(err, ErrNilShardKey) {
		t.Errorf("Shard(nil) = %v", err)
	}
	for _, bounds := range [][2]any{{1, int64(15)}, {int64(1), 15}, {nil, nil}} {
		if _, err := strategy.ShardRange(bounds[0], bounds[1], 2); !errors.Is(err, ErrInvalidShardKey) {
			t.Errorf("ShardRange(%T, %T) = %v", bounds[0], bounds[1], err)
		}
	}
	if shard, err := strategy.Shard(int64(10), 2); err != nil || shard != 1 {
		t.Errorf("Shard at boundary = %d, %v", shard, err)
	}
	if shards, err := strategy.ShardRange(int64(15), int64(1), 2); err != nil || len(shards) != 2 || shards[0] != 0 || shards[1] != 1 {
		t.Errorf("ShardRange with reversed bounds = %v, %v", shards, err)
	}
}

func TestListStrategySnapshotAndDefaultRoute(t *testing.T) {
	s := NewListStrategyWithDefault(map[any]int{"explicit": 1, "other": 0}, 0)
	snapshot := s.GetMapping()
	snapshot["explicit"] = 2
	delete(snapshot, "other")
	s.SetDefaultShard(2)
	if got := s.GetDefaultShard(); got != 2 {
		t.Errorf("default shard = %d", got)
	}
	for key, want := range map[string]int{"explicit": 1, "other": 0, "unmapped": 2} {
		if got, err := s.Shard(key, 3); err != nil || got != want {
			t.Errorf("Shard(%s) = %d, %v; want %d", key, got, err, want)
		}
	}
	s.SetDefaultShard(-1)
	if got := s.GetDefaultShard(); got != -1 {
		t.Errorf("disabled default = %d", got)
	}
	if _, err := s.Shard("unmapped", 3); !errors.Is(err, ErrNoMatchingList) {
		t.Errorf("unmapped error = %v", err)
	}
	if got, err := s.Shard("explicit", 3); err != nil || got != 1 {
		t.Errorf("explicit shard = %d, %v", got, err)
	}
}

func TestListStrategyShardRangeFansOut(t *testing.T) {
	s := NewListStrategy(map[any]int{"begin": 1, "end": 1})
	if got, err := s.ShardRange("begin", "end", 3); err != nil || !slices.Equal(got, []int{0, 1, 2}) {
		t.Errorf("range = %v, %v", got, err)
	}
	for _, n := range []int{0, -1} {
		if got, err := s.ShardRange("begin", "end", n); len(got) != 0 || !errors.Is(err, ErrInvalidNumShards) {
			t.Errorf("range with %d shards = %v, %v", n, got, err)
		}
	}
}

func TestCustomHashStrategyReceivesKeyBytes(t *testing.T) {
	calls := 0
	s := NewHashStrategyWithFunc(func(key []byte) uint64 {
		calls++
		if string(key) != "shard-key" {
			t.Errorf("hash input=%q", key)
		}
		return 11
	})
	if _, err := s.Shard("shard-key", 0); !errors.Is(err, ErrInvalidNumShards) || calls != 0 {
		t.Errorf("invalid count=%v, calls=%d", err, calls)
	}
	if got, err := s.Shard("shard-key", 4); got != 3 || err != nil || calls != 1 {
		t.Errorf("Shard=%d, %v, calls=%d", got, err, calls)
	}
}

func TestRangeStrategyPreservesUint64Boundaries(t *testing.T) {
	const base = uint64(1 << 63)
	strategy, err := NewRangeStrategy([]RangeDefinition{
		{Start: base + 10, End: base + 20, Shard: 1},
		{Start: base, End: base + 10, Shard: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key  uint64
		want int
	}{
		{base, 0}, {base + 9, 0}, {base + 10, 1}, {base + 19, 1},
	} {
		if got, err := strategy.Shard(tc.key, 2); got != tc.want || err != nil {
			t.Errorf("Shard(%d) = %d, %v; want %d", tc.key, got, err, tc.want)
		}
	}
	if _, err := strategy.Shard(base+20, 2); !errors.Is(err, ErrNoMatchingRange) {
		t.Errorf("exclusive upper bound error = %v", err)
	}
	if _, err := strategy.Shard(int64(10), 2); !errors.Is(err, ErrInvalidShardKey) {
		t.Errorf("mismatched key type error = %v", err)
	}
}
