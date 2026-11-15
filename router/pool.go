package router

import (
	"sync"
)

// Object pools for reducing allocations during request processing.
// All pool helpers are unexported; they are implementation details.

// Bound the retained storage even when an unusually deep path grows a slice.
const maxPooledRouteSegments = 64

var paramValuesPool = sync.Pool{
	New: func() any {
		s := make([]string, 0, defaultPoolSliceCap)
		return &s
	},
}

func getParamValues() *[]string {
	return paramValuesPool.Get().(*[]string)
}

func putParamValues(s *[]string) {
	putRouteStrings(&paramValuesPool, s)
}

var pathPartsPool = sync.Pool{
	New: func() any {
		s := make([]string, 0, defaultPathPartsCap)
		return &s
	},
}

func getPathParts() *[]string {
	return pathPartsPool.Get().(*[]string)
}

func putPathParts(s *[]string) {
	putRouteStrings(&pathPartsPool, s)
}

func putRouteStrings(pool *sync.Pool, s *[]string) {
	if s == nil {
		return
	}
	if cap(*s) > maxPooledRouteSegments {
		*s = nil
		return
	}
	// Backtracking may shorten the slice while old path strings remain in its tail.
	clear((*s)[:cap(*s)])
	*s = (*s)[:0]
	pool.Put(s)
}

// splitPathToParts splits a path into parts using a pooled slice.
// This preserves empty segments to properly reject paths like /users//123.
// Caller must call putPathParts when done.
func splitPathToParts(path string) *[]string {
	parts := getPathParts()
	if path == "" || path == "/" {
		return parts
	}

	start := 0
	end := len(path)
	if path[0] == '/' {
		start = 1
	}
	if end > start && path[end-1] == '/' {
		end--
	}

	for i := start; i < end; i++ {
		if path[i] == '/' {
			*parts = append(*parts, path[start:i])
			start = i + 1
		}
	}
	if start <= end {
		*parts = append(*parts, path[start:end])
	}

	return parts
}
