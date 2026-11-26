package provider

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrObjectChanged = errors.New("cloud object changed during read")
var ErrObjectSize = errors.New("cloud object size is outside the import limits")

type ObjectMeta struct {
	ETag  string `json:"etag"`
	Bytes int64  `json:"bytes"`
}

func ValidObjectKey(key string) bool {
	if key == "" || len(key) > 1024 || !utf8.ValidString(key) || strings.ContainsRune(key, '\\') ||
		strings.IndexFunc(key, unicode.IsControl) >= 0 {
		return false
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
