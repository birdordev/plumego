package middleware_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestMiddlewareAdaptersRespectLayerBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		dir       string
		allowList []string
	}{
		{
			name:      "proxy adapter imports only gateway",
			dir:       "proxy",
			allowList: []string{"github.com/birdordev/plumego/x/gateway"},
		},
		{
			name:      "cache adapter imports only gateway cache",
			dir:       "cache",
			allowList: []string{"github.com/birdordev/plumego/x/gateway/cache"},
		},
		{
			name:      "circuit adapter imports only resilience circuit",
			dir:       "circuitbreaker",
			allowList: []string{"github.com/birdordev/plumego/x/resilience/circuitbreaker"},
		},
		{
			name: "tenant adapter imports only transport and tenant modules",
			dir:  "tenant",
			allowList: []string{
				"github.com/birdordev/plumego/contract",
				"github.com/birdordev/plumego/middleware",
				"github.com/birdordev/plumego/x/tenant/core",
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			matches, err := filepath.Glob(filepath.Join(tc.dir, "*.go"))
			if err != nil {
				t.Fatalf("glob %s: %v", tc.dir, err)
			}

			fset := token.NewFileSet()
			for _, file := range matches {
				if strings.HasSuffix(file, "_test.go") {
					continue
				}

				node, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
				if err != nil {
					t.Fatalf("parse %s: %v", file, err)
				}

				for _, imp := range node.Imports {
					path, err := strconv.Unquote(imp.Path.Value)
					if err != nil {
						t.Fatalf("unquote import path in %s: %v", file, err)
					}

					if allowedImport(path, tc.allowList) {
						continue
					}

					t.Fatalf("forbidden import %q in %s", path, file)
				}
			}
		})
	}
}

func allowedImport(path string, allowList []string) bool {
	if !strings.Contains(path, ".") {
		return true // standard library import
	}

	for _, allowed := range allowList {
		if path == allowed || strings.HasPrefix(path, allowed+"/") {
			return true
		}
	}

	return false
}
