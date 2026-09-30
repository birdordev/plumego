// cross-extension-deps verifies that x/* packages do not import paths listed
// in their own module.yaml forbidden_imports field.
//
// The dependency-rules checker enforces rules from specs/dependency-rules.yaml
// (primarily preventing stable roots from importing x/*). This checker closes
// the complementary gap: it reads each x/* module.yaml directly and confirms
// that the package's actual Go imports respect its own declared boundary.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const modulePath = "github.com/spcent/plumego"

type violation struct {
	file       string
	importPath string
	pattern    string
}

func main() {
	repoRoot, err := os.Getwd()
	if err != nil {
		fatalf("resolve working directory: %v", err)
	}

	violations, err := check(repoRoot)
	if err != nil {
		fatalf("cross-extension-deps: %v", err)
	}
	if len(violations) == 0 {
		return
	}

	fmt.Fprintf(os.Stderr, "cross-extension-deps check failed: %d violation(s)\n\n", len(violations))
	for _, v := range violations {
		fmt.Fprintf(os.Stderr, "  ❌  %s\n\n     imports %q\n     forbidden by pattern %q in module.yaml\n\n", v.file, v.importPath, v.pattern)
	}
	os.Exit(1)
}

func check(repoRoot string) ([]violation, error) {
	xDir := filepath.Join(repoRoot, "x")
	if _, err := os.Stat(xDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	// Collect all x/* packages that have a module.yaml with forbidden_imports.
	type moduleEntry struct {
		dir       string
		forbidden []string
	}
	var modules []moduleEntry

	err := filepath.WalkDir(xDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		manifestPath := filepath.Join(path, "module.yaml")
		if _, err := os.Stat(manifestPath); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		forbidden, err := readForbiddenImports(manifestPath)
		if err != nil {
			return fmt.Errorf("parse %s: %w", manifestPath, err)
		}
		if len(forbidden) > 0 {
			modules = append(modules, moduleEntry{dir: path, forbidden: forbidden})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var violations []violation
	for _, mod := range modules {
		vs, err := checkModule(repoRoot, mod.dir, mod.forbidden)
		if err != nil {
			return nil, err
		}
		violations = append(violations, vs...)
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].file != violations[j].file {
			return violations[i].file < violations[j].file
		}
		return violations[i].importPath < violations[j].importPath
	})
	return violations, nil
}

func checkModule(repoRoot, dir string, forbidden []string) ([]violation, error) {
	var violations []violation

	fset := token.NewFileSet()
	files, err := parseNonTestGoFiles(fset, dir, parser.ImportsOnly)
	if err != nil {
		// Non-Go dirs or parse errors — skip gracefully.
		return nil, nil
	}

	for filename, file := range files {
		relFile, err := filepath.Rel(repoRoot, filename)
		if err != nil {
			relFile = filename
		}
		relFile = filepath.ToSlash(relFile)

		for _, imp := range file.Imports {
			if imp.Path == nil {
				continue
			}
			importPath := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(importPath, modulePath+"/") {
				continue
			}
			// Relative import path within the module, e.g. "x/ai/session".
			relImport := strings.TrimPrefix(importPath, modulePath+"/")
			for _, pattern := range forbidden {
				if matchPattern(relImport, pattern) {
					violations = append(violations, violation{
						file:       relFile,
						importPath: importPath,
						pattern:    pattern,
					})
					break
				}
			}
		}
	}
	return violations, nil
}

// parseNonTestGoFiles parses all non-test Go source files directly in dir
// (not recursing into subdirectories) and returns them keyed by absolute
// file path. It replaces the deprecated go/parser.ParseDir with the stdlib
// primitives it was a thin wrapper over.
func parseNonTestGoFiles(fset *token.FileSet, dir string, mode parser.Mode) (map[string]*ast.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	files := make(map[string]*ast.File)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, mode)
		if err != nil {
			return nil, err
		}
		files[path] = file
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no buildable Go source files in %s", dir)
	}
	return files, nil
}

// readForbiddenImports extracts the forbidden_imports list from a module.yaml
// using line-by-line scanning (no external YAML library required).
func readForbiddenImports(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var forbidden []string
	inSection := false
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(rawLine, " \t\r")
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Detect top-level section key (no leading spaces).
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			inSection = strings.HasPrefix(trimmed, "forbidden_imports:")
			continue
		}
		if !inSection {
			continue
		}
		// List item: "  - some/pattern"
		if strings.HasPrefix(trimmed, "- ") {
			value := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			value = strings.Trim(value, `"'`)
			if value != "" {
				forbidden = append(forbidden, value)
			}
		}
	}
	return forbidden, nil
}

// matchPattern checks whether relImport matches a forbidden pattern.
// Patterns may end with /** (recursive match) or /* (single-level match),
// or be a plain relative path for exact package matching.
func matchPattern(relImport, pattern string) bool {
	switch {
	case strings.HasSuffix(pattern, "/**"):
		prefix := strings.TrimSuffix(pattern, "/**")
		return relImport == prefix ||
			strings.HasPrefix(relImport, prefix+"/")
	case strings.HasSuffix(pattern, "/*"):
		prefix := strings.TrimSuffix(pattern, "/*")
		if !strings.HasPrefix(relImport, prefix+"/") {
			return false
		}
		// Only one additional path segment allowed.
		rest := strings.TrimPrefix(relImport, prefix+"/")
		return !strings.Contains(rest, "/")
	default:
		return relImport == pattern
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
