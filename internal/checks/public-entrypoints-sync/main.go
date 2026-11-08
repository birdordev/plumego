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

	"github.com/birdordev/plumego/internal/checks/checkutil"
)

func main() {
	repoRoot, err := os.Getwd()
	if err != nil {
		failf("resolve working directory: %v", err)
	}

	baseline, err := checkutil.ReadBaseline(filepath.Join(repoRoot, "specs", "check-baseline", "public-entrypoints-sync.txt"))
	if err != nil {
		failf("read baseline: %v", err)
	}

	violations, err := runCheck(repoRoot, baseline)
	if err != nil {
		failf("public-entrypoints-sync: %v", err)
	}
	if len(violations) == 0 {
		return
	}

	fmt.Fprint(os.Stderr, checkutil.FormatViolations("public-entrypoints-sync", violations))
	os.Exit(1)
}

func runCheck(repoRoot string, baseline map[string]struct{}) ([]string, error) {
	var violations []string

	for _, root := range checkutil.StableRoots() {
		v, err := checkModule(repoRoot, root, baseline)
		if err != nil {
			return nil, err
		}
		violations = append(violations, v...)
	}

	xDir := filepath.Join(repoRoot, "x")
	entries, err := os.ReadDir(xDir)
	if err != nil {
		if os.IsNotExist(err) {
			return violations, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		modPath := "x/" + entry.Name()
		v, err := checkModule(repoRoot, modPath, baseline)
		if err != nil {
			return nil, err
		}
		violations = append(violations, v...)
	}

	sort.Strings(violations)
	return violations, nil
}

func checkModule(repoRoot, modPath string, baseline map[string]struct{}) ([]string, error) {
	manifestPath := filepath.Join(repoRoot, filepath.FromSlash(modPath), "module.yaml")
	declared, err := checkutil.ReadModulePublicEntrypoints(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s/module.yaml: %w", modPath, err)
	}
	if len(declared) == 0 {
		return nil, nil
	}

	syms, err := extractSymbols(filepath.Join(repoRoot, filepath.FromSlash(modPath)))
	if err != nil {
		return nil, fmt.Errorf("extract symbols from %s: %w", modPath, err)
	}

	var violations []string
	for _, entry := range declared {
		// Skip capability-label entries (lowercase = semantic labels, not Go symbols).
		if len(entry) > 0 && entry[0] >= 'a' && entry[0] <= 'z' {
			continue
		}
		// Skip wildcard patterns like "Code*", "Err*".
		if strings.Contains(entry, "*") {
			continue
		}
		if symbolExists(entry, syms) {
			continue
		}
		key := modPath + "|" + entry
		if _, ok := baseline[key]; ok {
			continue
		}
		violations = append(violations,
			fmt.Sprintf("%s/module.yaml: public_entrypoints entry %q not found in %s source", modPath, entry, modPath))
	}
	return violations, nil
}

type moduleSymbols struct {
	topLevel     map[string]struct{}
	methods      map[string]map[string]struct{}
	structFields map[string]map[string]struct{}
	allMethods   map[string]struct{} // method names across all receiver types
}

func extractSymbols(dir string) (*moduleSymbols, error) {
	s := &moduleSymbols{
		topLevel:     make(map[string]struct{}),
		methods:      make(map[string]map[string]struct{}),
		structFields: make(map[string]map[string]struct{}),
		allMethods:   make(map[string]struct{}),
	}

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkerr error) error {
		if walkerr != nil {
			return walkerr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}

		for _, decl := range node.Decls {
			switch fd := decl.(type) {
			case *ast.FuncDecl:
				if fd.Name == nil || !ast.IsExported(fd.Name.Name) {
					continue
				}
				if fd.Recv == nil || len(fd.Recv.List) == 0 {
					s.topLevel[fd.Name.Name] = struct{}{}
				} else {
					typeName := receiverTypeName(fd.Recv.List[0].Type)
					if typeName != "" {
						if s.methods[typeName] == nil {
							s.methods[typeName] = make(map[string]struct{})
						}
						s.methods[typeName][fd.Name.Name] = struct{}{}
					}
					s.allMethods[fd.Name.Name] = struct{}{}
				}
			case *ast.GenDecl:
				for _, spec := range fd.Specs {
					switch sp := spec.(type) {
					case *ast.TypeSpec:
						if ast.IsExported(sp.Name.Name) {
							s.topLevel[sp.Name.Name] = struct{}{}
							if st, ok := sp.Type.(*ast.StructType); ok {
								for _, field := range st.Fields.List {
									for _, fname := range field.Names {
										if ast.IsExported(fname.Name) {
											if s.structFields[sp.Name.Name] == nil {
												s.structFields[sp.Name.Name] = make(map[string]struct{})
											}
											s.structFields[sp.Name.Name][fname.Name] = struct{}{}
										}
									}
								}
							}
						}
					case *ast.ValueSpec:
						for _, name := range sp.Names {
							if ast.IsExported(name.Name) {
								s.topLevel[name.Name] = struct{}{}
							}
						}
					}
				}
			}
		}
		return nil
	})
	return s, err
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return ident.Name
		}
	}
	return ""
}

func symbolExists(entry string, s *moduleSymbols) bool {
	// Format: (*Type).Method or (Type).Method
	typeName, methodName, isParenMethod := parseMethodEntry(entry)
	if isParenMethod {
		methods, ok := s.methods[typeName]
		if !ok {
			return false
		}
		_, found := methods[methodName]
		return found
	}

	// Format: Type.FieldOrMethod (no parens) — e.g. APIError.Code, TraceContext.Valid
	if dotIdx := strings.Index(entry, "."); dotIdx > 0 {
		tName := entry[:dotIdx]
		member := entry[dotIdx+1:]
		if methods, ok := s.methods[tName]; ok {
			if _, found := methods[member]; found {
				return true
			}
		}
		if fields, ok := s.structFields[tName]; ok {
			if _, found := fields[member]; found {
				return true
			}
		}
		return false
	}

	// Plain name: check top-level declarations and all method names.
	if _, found := s.topLevel[entry]; found {
		return true
	}
	_, found := s.allMethods[entry]
	return found
}

func parseMethodEntry(entry string) (typeName, methodName string, isMethod bool) {
	if !strings.HasPrefix(entry, "(") {
		return "", "", false
	}
	closeIdx := strings.Index(entry, ")")
	if closeIdx < 0 {
		return "", "", false
	}
	rest := entry[closeIdx:]
	dotIdx := strings.Index(rest, ".")
	if dotIdx < 0 {
		return "", "", false
	}
	typePart := strings.TrimPrefix(entry[1:closeIdx], "*")
	method := rest[dotIdx+1:]
	if typePart == "" || method == "" {
		return "", "", false
	}
	return typePart, method, true
}

func failf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
