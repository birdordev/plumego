package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spcent/plumego/internal/checks/checkutil"
)

const snapshotHeader = "# plumego extension api snapshot v1"

func main() {
	modulePattern := flag.String("module", "", "package pattern to snapshot, for example ./x/rest or ./x/rest/...")
	outPath := flag.String("out", "", "file to write; stdout is used when empty")
	compare := flag.Bool("compare", false, "compare two snapshot files passed as positional arguments")
	flag.Parse()

	if *compare {
		if flag.NArg() != 2 {
			failf("-compare requires two snapshot file arguments")
		}
		if err := compareSnapshots(flag.Arg(0), flag.Arg(1), os.Stdout); err != nil {
			failf("%v", err)
		}
		return
	}

	if *modulePattern == "" {
		failf("-module is required when not comparing snapshots")
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		failf("resolve working directory: %v", err)
	}

	modulePath, err := readModulePath(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		failf("read module path: %v", err)
	}

	lines, err := snapshotPattern(repoRoot, modulePath, *modulePattern)
	if err != nil {
		failf("snapshot %s: %v", *modulePattern, err)
	}

	var buf bytes.Buffer
	fmt.Fprintln(&buf, snapshotHeader)
	fmt.Fprintf(&buf, "module\t%s\n", modulePath)
	fmt.Fprintf(&buf, "pattern\t%s\n", *modulePattern)
	for _, line := range lines {
		fmt.Fprintln(&buf, line)
	}

	if *outPath == "" {
		_, _ = os.Stdout.Write(buf.Bytes())
		return
	}
	if err := os.WriteFile(*outPath, buf.Bytes(), 0o644); err != nil {
		failf("write snapshot: %v", err)
	}
}

func snapshotPattern(repoRoot, modulePath, pattern string) ([]string, error) {
	dirs, err := packageDirs(repoRoot, pattern)
	if err != nil {
		return nil, err
	}

	var lines []string
	for _, dir := range dirs {
		pkgLines, err := snapshotDir(repoRoot, modulePath, dir)
		if err != nil {
			return nil, err
		}
		lines = append(lines, pkgLines...)
	}
	sort.Strings(lines)
	return lines, nil
}

func packageDirs(repoRoot, pattern string) ([]string, error) {
	cleaned := filepath.Clean(strings.TrimPrefix(pattern, "./"))
	if cleaned == "." || strings.HasPrefix(cleaned, "..") {
		return nil, fmt.Errorf("pattern must be repo-relative: %s", pattern)
	}

	if strings.HasSuffix(pattern, "/...") {
		root := filepath.Join(repoRoot, strings.TrimSuffix(cleaned, string(filepath.Separator)+"..."))
		var dirs []string
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				return nil
			}
			if shouldSkipDir(entry.Name()) && path != root {
				return filepath.SkipDir
			}
			hasGo, err := hasNonTestGoFile(path)
			if err != nil {
				return err
			}
			if hasGo {
				dirs = append(dirs, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		sort.Strings(dirs)
		return dirs, nil
	}

	dir := filepath.Join(repoRoot, cleaned)
	hasGo, err := hasNonTestGoFile(dir)
	if err != nil {
		return nil, err
	}
	if !hasGo {
		return nil, fmt.Errorf("no non-test Go files in %s", pattern)
	}
	return []string{dir}, nil
}

func shouldSkipDir(name string) bool {
	return name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func hasNonTestGoFile(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			return true, nil
		}
	}
	return false, nil
}

// parsePackageFiles parses non-test Go files directly in dir and returns
// them keyed by file path, along with the distinct package names found.
func parsePackageFiles(fset *token.FileSet, dir string) (map[string]*ast.File, map[string]struct{}, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}

	files := make(map[string]*ast.File)
	pkgNames := make(map[string]struct{})
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		path := filepath.Join(dir, name)
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, nil, err
		}
		files[path] = file
		pkgNames[file.Name.Name] = struct{}{}
	}
	return files, pkgNames, nil
}

func snapshotDir(repoRoot, modulePath, dir string) ([]string, error) {
	fset := token.NewFileSet()
	files, pkgNames, err := parsePackageFiles(fset, dir)
	if err != nil {
		return nil, err
	}
	if len(pkgNames) != 1 {
		return nil, fmt.Errorf("%s contains %d packages", relPath(repoRoot, dir), len(pkgNames))
	}

	importPath := modulePath + "/" + filepath.ToSlash(relPath(repoRoot, dir))
	var lines []string
	for _, file := range files {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				lines = append(lines, snapshotGenDecl(fset, importPath, d)...)
			case *ast.FuncDecl:
				line, ok := snapshotFuncDecl(fset, importPath, d)
				if ok {
					lines = append(lines, line)
				}
			}
		}
	}
	sort.Strings(lines)
	return lines, nil
}

func snapshotGenDecl(fset *token.FileSet, importPath string, decl *ast.GenDecl) []string {
	var lines []string
	for _, spec := range decl.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if !s.Name.IsExported() {
				continue
			}
			prefix := "type " + s.Name.Name + " "
			if s.Assign.IsValid() {
				prefix = "type " + s.Name.Name + " = "
			}
			lines = append(lines, symbolLine(importPath, "type", s.Name.Name, prefix+typeSnapshotString(fset, s.Type)))
		case *ast.ValueSpec:
			kind := strings.ToLower(decl.Tok.String())
			for i, name := range s.Names {
				if !name.IsExported() {
					continue
				}
				lines = append(lines, symbolLine(importPath, kind, name.Name, valueSpecString(fset, kind, name.Name, s, i)))
			}
		}
	}
	return lines
}

func snapshotFuncDecl(fset *token.FileSet, importPath string, decl *ast.FuncDecl) (string, bool) {
	if !decl.Name.IsExported() {
		return "", false
	}
	if decl.Recv == nil {
		return symbolLine(importPath, "func", decl.Name.Name, "func "+decl.Name.Name+nodeString(fset, decl.Type)), true
	}
	recv := receiverName(fset, decl.Recv)
	if recv == "" || !ast.IsExported(recv) {
		return "", false
	}
	name := recv + "." + decl.Name.Name
	return symbolLine(importPath, "method", name, "func ("+recv+") "+decl.Name.Name+nodeString(fset, decl.Type)), true
}

func valueSpecString(fset *token.FileSet, kind, name string, spec *ast.ValueSpec, index int) string {
	var b strings.Builder
	b.WriteString(kind)
	b.WriteString(" ")
	b.WriteString(name)
	if spec.Type != nil {
		b.WriteString(" ")
		b.WriteString(nodeString(fset, spec.Type))
	}
	if index < len(spec.Values) {
		b.WriteString(" = ")
		b.WriteString(nodeString(fset, spec.Values[index]))
	}
	return b.String()
}

func typeSnapshotString(fset *token.FileSet, expr ast.Expr) string {
	if structType, ok := expr.(*ast.StructType); ok {
		return nodeString(fset, exportedStructType(structType))
	}
	return nodeString(fset, expr)
}

func exportedStructType(in *ast.StructType) *ast.StructType {
	if in == nil || in.Fields == nil {
		return in
	}
	fields := make([]*ast.Field, 0, len(in.Fields.List))
	for _, field := range in.Fields.List {
		if field == nil {
			continue
		}
		if len(field.Names) == 0 {
			if !embeddedFieldExported(field.Type) {
				continue
			}
			fields = append(fields, cloneFieldWithNames(field, nil))
			continue
		}

		names := make([]*ast.Ident, 0, len(field.Names))
		for _, name := range field.Names {
			if name != nil && name.IsExported() {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			continue
		}
		fields = append(fields, cloneFieldWithNames(field, names))
	}
	return &ast.StructType{
		Struct: fieldPos(in.Struct, token.NoPos),
		Fields: &ast.FieldList{
			Opening: fieldPos(in.Fields.Opening, token.NoPos),
			List:    fields,
			Closing: fieldPos(in.Fields.Closing, token.NoPos),
		},
		Incomplete: in.Incomplete,
	}
}

func cloneFieldWithNames(field *ast.Field, names []*ast.Ident) *ast.Field {
	return &ast.Field{
		Doc:     field.Doc,
		Names:   names,
		Type:    field.Type,
		Tag:     field.Tag,
		Comment: field.Comment,
	}
}

func embeddedFieldExported(expr ast.Expr) bool {
	for {
		switch t := expr.(type) {
		case *ast.StarExpr:
			expr = t.X
		case *ast.SelectorExpr:
			return t.Sel != nil && t.Sel.IsExported()
		case *ast.Ident:
			return t.IsExported()
		default:
			return false
		}
	}
}

func fieldPos(pos token.Pos, fallback token.Pos) token.Pos {
	if pos.IsValid() {
		return pos
	}
	return fallback
}

func receiverName(fset *token.FileSet, recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	expr := recv.List[0].Type
	for {
		switch t := expr.(type) {
		case *ast.StarExpr:
			expr = t.X
		case *ast.IndexExpr:
			expr = t.X
		case *ast.IndexListExpr:
			expr = t.X
		case *ast.Ident:
			return t.Name
		default:
			return nodeString(fset, expr)
		}
	}
}

func symbolLine(importPath, kind, name, signature string) string {
	return strings.Join([]string{"symbol", importPath, kind, name, compact(signature)}, "\t")
}

func nodeString(fset *token.FileSet, node any) string {
	var b bytes.Buffer
	_ = printer.Fprint(&b, fset, node)
	return b.String()
}

func compact(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func compareSnapshots(leftPath, rightPath string, out io.Writer) error {
	left, err := readSnapshot(leftPath)
	if err != nil {
		return err
	}
	right, err := readSnapshot(rightPath)
	if err != nil {
		return err
	}

	var diffs []string
	for key, before := range left {
		after, ok := right[key]
		if !ok {
			diffs = append(diffs, "- removed "+key+" "+before)
			continue
		}
		if before != after {
			diffs = append(diffs, "~ changed "+key)
			diffs = append(diffs, "  before "+before)
			diffs = append(diffs, "  after  "+after)
		}
	}
	for key, after := range right {
		if _, ok := left[key]; !ok {
			diffs = append(diffs, "+ added "+key+" "+after)
		}
	}
	sort.Strings(diffs)
	if len(diffs) == 0 {
		_, _ = fmt.Fprintln(out, "snapshots match")
		return nil
	}
	for _, diff := range diffs {
		_, _ = fmt.Fprintln(out, diff)
	}
	return errors.New("snapshots differ")
}

func readSnapshot(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	symbols := make(map[string]string)
	scanner := checkutil.NewLineScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "symbol\t") {
			continue
		}
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) != 5 {
			return nil, fmt.Errorf("invalid snapshot line in %s: %s", path, line)
		}
		key := strings.Join(parts[:4], "\t")
		symbols[key] = parts[4]
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return symbols, nil
}

func readModulePath(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	scanner := checkutil.NewLineScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("module directive not found in %s", path)
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.Clean(path)
	}
	return rel
}

func failf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
