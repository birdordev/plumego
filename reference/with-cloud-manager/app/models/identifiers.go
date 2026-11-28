package models

import (
	"strings"
	"unicode"
)

func ValidRequestSegment(segment string) bool {
	return segment != "" && len(segment) <= 256 && segment != "." && segment != ".." &&
		!strings.ContainsAny(segment, "/\\") && strings.IndexFunc(segment, unicode.IsControl) < 0
}
