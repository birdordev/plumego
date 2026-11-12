package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestCanonicalDocumentationChecks(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":       "module example.com/toolkit\n",
		"README.md":    "# Toolkit\nexample.com/toolkit/core/components\n",
		"README_CN.md": "",
		"docs/reference/canonical-style-guide.md":     "",
		"docs/concepts/agent-first-repo-blueprint.md": "",
		"reference/standard-service/README.md":        "",
	}
	for path, content := range files {
		file := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	violations, err := staleAPIViolations(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"README.md:2: stale API reference example.com/toolkit/core/components"}
	if !slices.Equal(violations, want) {
		t.Fatalf("stale references = %v, want %v", violations, want)
	}
	if !slices.Contains(requiredPathViolations(root), "missing required path reference/standard-service/internal/app/routes.go") {
		t.Fatal("missing canonical routes were accepted")
	}
}
