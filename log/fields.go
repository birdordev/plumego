package log

import (
	"maps"
	"math/bits"
)

const maxMapCapacity = int(^uint(0) >> 1)

func safeMapCapacity(lengths ...int) int {
	total := 0
	for _, length := range lengths {
		next, ok := addMapCapacity(total, length)
		if !ok {
			return 0
		}
		total = next
	}
	return total
}

func addMapCapacity(base, extra int) (int, bool) {
	if base < 0 || extra < 0 {
		return 0, false
	}
	sum, carry := bits.Add(uint(base), uint(extra), 0)
	if carry != 0 || sum > uint(maxMapCapacity) {
		return 0, false
	}
	return int(sum), true
}

func fieldSetCapacity(total int, fieldSets []Fields) int {
	for _, fields := range fieldSets {
		next, ok := addMapCapacity(total, len(fields))
		if !ok {
			return 0
		}
		total = next
	}
	return total
}

func fieldSetsEmpty(fieldSets []Fields) bool {
	for _, fields := range fieldSets {
		if len(fields) > 0 {
			return false
		}
	}
	return true
}

// cloneFields returns a shallow copy of fields.
func cloneFields(fields Fields) Fields {
	if len(fields) == 0 {
		return Fields{}
	}
	return maps.Clone(fields)
}

// mergeFields copies the base and applies each subsequent field set in order.
func mergeFields(base Fields, fieldSets ...Fields) Fields {
	capacity := fieldSetCapacity(len(base), fieldSets)
	if capacity == 0 && len(base) == 0 && fieldSetsEmpty(fieldSets) {
		return Fields{}
	}
	return mergeFieldsWithCapacity(base, fieldSets, capacity)
}

func mergeFieldsWithCapacity(base Fields, fieldSets []Fields, capacity int) Fields {
	merged := make(Fields, capacity)
	for k, v := range base {
		merged[k] = v
	}
	for _, fields := range fieldSets {
		for k, v := range fields {
			merged[k] = v
		}
	}
	return merged
}
