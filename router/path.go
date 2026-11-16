package router

// This file contains low-level path helpers used at two distinct call sites:
//
//   - fastNormalizePath — hot dispatch path (ServeHTTP); zero-allocation, no
//     internal-double-slash detection, used only to key the match cache and
//     split the trie traversal input.
//   - canonicalRoutePath / joinRoutePath — cold registration path (AddRoute,
//     Group); allocating is fine; responsible for deduplicating leading slashes
//     and joining prefix + relative path into the authoritative stored pattern.
//
// Registration validates and stores canonical patterns. Dispatch preserves
// internal repeated slashes in the request path for the matcher to handle.

// fastNormalizePath normalizes the request path for cache keying and trie
// traversal. It preserves exactly one leading slash, strips trailing slashes,
// and collapses duplicate leading slashes — all as zero-allocation substring
// operations. The root "/" is returned unchanged.
func fastNormalizePath(path string) string {
	if len(path) == 0 {
		return "/"
	}

	end := len(path)
	for end > 1 && path[end-1] == '/' {
		end--
	}

	// Collapse duplicate leading slashes to one via a zero-allocation slice.
	if len(path) >= 2 && path[0] == '/' && path[1] == '/' {
		start := 1
		for start < end && path[start] == '/' {
			start++
		}
		if start >= end {
			return "/"
		}
		return path[start-1 : end] // e.g. "//users" → "/users"
	}

	if end == len(path) {
		return path
	}
	return path[:end]
}
