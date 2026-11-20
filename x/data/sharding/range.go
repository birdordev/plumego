package sharding

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"time"
)

// RangeStrategy implements range-based sharding where data is distributed
// across shards based on predefined ranges.
//
// This strategy is ideal for:
//   - Time-based data (created_at ranges)
//   - Geographic data (region IDs, zip codes)
//   - Sequential IDs with known distribution
//
// Advantages:
//   - Efficient range queries (only query relevant shards)
//   - Easy to understand data distribution
//   - Flexible range definitions
//
// Disadvantages:
//   - Requires careful range planning
//   - Can lead to uneven data distribution
//   - Needs manual range updates when scaling
type RangeStrategy struct {
	ranges []RangeDefinition
}

// RangeDefinition defines a range of values that map to a specific shard.
type RangeDefinition struct {
	// Start is the inclusive lower bound of this range
	Start any

	// End is the exclusive upper bound of this range
	End any

	// Shard is the target shard index for keys in this range
	Shard int
}

// NewRangeStrategy creates a new range-based sharding strategy.
// The ranges slice defines the mapping from value ranges to shard indices.
// Ranges must not overlap. All endpoints must use the same concrete integer,
// string, or time.Time type. A copy is sorted by Start for lookup.
//
// Example:
//
//	strategy := NewRangeStrategy([]RangeDefinition{
//	    {Start: int64(0), End: int64(10000), Shard: 0},
//	    {Start: int64(10000), End: int64(20000), Shard: 1},
//	    {Start: int64(20000), End: int64(30000), Shard: 2},
//	})
func NewRangeStrategy(ranges []RangeDefinition) (*RangeStrategy, error) {
	if len(ranges) == 0 {
		return nil, fmt.Errorf("range strategy requires at least one range definition")
	}

	sorted := slices.Clone(ranges)
	if err := validateAndSortRanges(sorted); err != nil {
		return nil, err
	}

	return &RangeStrategy{
		ranges: sorted,
	}, nil
}

// Shard finds the target shard for a given key by searching the range definitions.
// Uses binary search for efficient lookup in O(log n) time. A key with a
// different concrete type from the configured endpoints returns ErrInvalidShardKey.
//
// Example:
//
//	shardIdx, err := strategy.Shard(int64(15000), 3)
//	// Returns 1 because 15000 is in range [10000, 20000)
func (r *RangeStrategy) Shard(key any, numShards int) (int, error) {
	if err := validateShardKey(key); err != nil {
		return 0, err
	}
	if err := validateNumShards(numShards); err != nil {
		return 0, err
	}

	if err := r.validateKeyType(key); err != nil {
		return 0, err
	}

	// Binary search for the matching range
	idx := sort.Search(len(r.ranges), func(i int) bool {
		return compareValues(key, r.ranges[i].End) < 0
	})

	if idx >= len(r.ranges) {
		return 0, fmt.Errorf("%w: key %v exceeds all ranges", ErrNoMatchingRange, key)
	}

	rangedef := r.ranges[idx]

	// Verify key is within range [Start, End)
	if compareValues(key, rangedef.Start) < 0 {
		return 0, fmt.Errorf("%w: key %v below minimum range", ErrNoMatchingRange, key)
	}

	// Validate shard index
	if rangedef.Shard < 0 || rangedef.Shard >= numShards {
		return 0, fmt.Errorf("range definition has invalid shard index %d (numShards=%d)", rangedef.Shard, numShards)
	}

	return rangedef.Shard, nil
}

// ShardRange finds all shards that overlap with the given value range.
// Bounds must use the same concrete type as the configured endpoints.
//
// Example:
//
//	shards, err := strategy.ShardRange(int64(8000), int64(15000), 3)
//	// Returns [0, 1] because the range spans shards 0 and 1
func (r *RangeStrategy) ShardRange(start, end any, numShards int) ([]int, error) {
	if err := validateNumShards(numShards); err != nil {
		return nil, err
	}

	if len(r.ranges) == 0 {
		return nil, ErrNoMatchingRange
	}
	if err := r.validateKeyType(start); err != nil {
		return nil, fmt.Errorf("range start: %w", err)
	}
	if err := r.validateKeyType(end); err != nil {
		return nil, fmt.Errorf("range end: %w", err)
	}

	// Ensure start <= end
	if compareValues(start, end) > 0 {
		start, end = end, start
	}

	// Find all ranges that overlap with [start, end]
	shardSet := make(map[int]struct{})
	for _, rangedef := range r.ranges {
		// Check if ranges overlap: [start, end] and [rangedef.Start, rangedef.End)
		if compareValues(start, rangedef.End) < 0 && compareValues(end, rangedef.Start) > 0 {
			if rangedef.Shard >= 0 && rangedef.Shard < numShards {
				shardSet[rangedef.Shard] = struct{}{}
			}
		}
	}

	if len(shardSet) == 0 {
		return nil, fmt.Errorf("%w: range [%v, %v] does not match any shard", ErrNoMatchingRange, start, end)
	}

	// Convert set to sorted slice
	shards := make([]int, 0, len(shardSet))
	for i := 0; i < numShards; i++ {
		if _, exists := shardSet[i]; exists {
			shards = append(shards, i)
		}
	}

	return shards, nil
}

// Name returns the strategy name.
func (r *RangeStrategy) Name() string {
	return "range"
}

func (r *RangeStrategy) validateKeyType(key any) error {
	if len(r.ranges) > 0 && reflect.TypeOf(key) != reflect.TypeOf(r.ranges[0].Start) {
		return fmt.Errorf("%w: got %T, want %T", ErrInvalidShardKey, key, r.ranges[0].Start)
	}
	return nil
}

// validateAndSortRanges checks endpoints before comparing any boundaries.
func validateAndSortRanges(ranges []RangeDefinition) error {
	first := ranges[0].Start
	switch first.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, string, time.Time:
	default:
		return fmt.Errorf("%w: unsupported range endpoint type %T", ErrInvalidShardKey, first)
	}
	want := reflect.TypeOf(first)
	for i, r := range ranges {
		if reflect.TypeOf(r.Start) != want || reflect.TypeOf(r.End) != want {
			return fmt.Errorf("%w: range %d has endpoint types %T and %T, want %v", ErrInvalidShardKey, i, r.Start, r.End, want)
		}
		if compareValues(r.Start, r.End) >= 0 {
			return fmt.Errorf("%w: range %d has start >= end", ErrInvalidRangeOrder, i)
		}
		if r.Shard < 0 {
			return fmt.Errorf("range %d: shard index cannot be negative", i)
		}
	}

	sort.Slice(ranges, func(i, j int) bool {
		return compareValues(ranges[i].Start, ranges[j].Start) < 0
	})
	for i := 0; i+1 < len(ranges); i++ {
		if compareValues(ranges[i].End, ranges[i+1].Start) > 0 {
			return fmt.Errorf("%w: ranges %v and %v overlap", ErrOverlappingRanges, ranges[i], ranges[i+1])
		}
	}
	return nil
}

// compareValues compares two values of the same type.
// Returns: -1 if a < b, 0 if a == b, 1 if a > b
func compareValues(a, b any) int {
	switch va := a.(type) {
	case int:
		vb := b.(int)
		return compareInt64(int64(va), int64(vb))
	case int8:
		vb := b.(int8)
		return compareInt64(int64(va), int64(vb))
	case int16:
		vb := b.(int16)
		return compareInt64(int64(va), int64(vb))
	case int32:
		vb := b.(int32)
		return compareInt64(int64(va), int64(vb))
	case int64:
		vb := b.(int64)
		return compareInt64(va, vb)

	case uint:
		vb := b.(uint)
		return compareUint64(uint64(va), uint64(vb))
	case uint8:
		vb := b.(uint8)
		return compareUint64(uint64(va), uint64(vb))
	case uint16:
		vb := b.(uint16)
		return compareUint64(uint64(va), uint64(vb))
	case uint32:
		vb := b.(uint32)
		return compareUint64(uint64(va), uint64(vb))
	case uint64:
		vb := b.(uint64)
		return compareUint64(va, vb)

	case string:
		vb := b.(string)
		if va < vb {
			return -1
		} else if va > vb {
			return 1
		}
		return 0

	case time.Time:
		vb := b.(time.Time)
		if va.Before(vb) {
			return -1
		} else if va.After(vb) {
			return 1
		}
		return 0

	default:
		// Fallback: treat as equal (shouldn't reach here if validation works)
		return 0
	}
}

func compareInt64(a, b int64) int {
	if a < b {
		return -1
	} else if a > b {
		return 1
	}
	return 0
}

func compareUint64(a, b uint64) int {
	if a < b {
		return -1
	} else if a > b {
		return 1
	}
	return 0
}
