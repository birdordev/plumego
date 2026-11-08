package contract

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

var (
	externalContractGoFilePathsOnce sync.Once
	externalContractGoFilePathsList []string
	externalContractGoFilePathsErr  error

	packageStringConstNamesMu    sync.Mutex
	packageStringConstNamesCache = map[string]map[string]struct{}{}
)

func TestExternalCodeUsesAPIErrorBuilder(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	// APIError remains an exported v1 compatibility struct, so this guard is
	// the enforcement point that keeps external non-test code builder-first.
	var violations []string
	fset := token.NewFileSet()
	err = walkExternalContractGoFiles(repoRoot, fset, func(path string, file *ast.File, contractNames map[string]struct{}) error {
		ast.Inspect(file, func(node ast.Node) bool {
			lit, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "APIError" {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if _, ok := contractNames[ident.Name]; !ok {
				return true
			}
			pos := fset.Position(lit.Pos())
			rel, err := filepath.Rel(repoRoot, pos.Filename)
			if err != nil {
				rel = pos.Filename
			}
			violations = append(violations, filepath.ToSlash(rel)+":"+strconv.Itoa(pos.Line))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan external APIError literals: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("external code must use contract.NewErrorBuilder instead of contract.APIError literals:\n%s", strings.Join(violations, "\n"))
	}
}

func walkExternalContractGoFiles(repoRoot string, fset *token.FileSet, fn func(path string, file *ast.File, contractNames map[string]struct{}) error) error {
	paths, err := externalContractGoFilePaths(repoRoot)
	if err != nil {
		return err
	}

	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return err
		}
		contractNames := contractImportNames(file)
		if len(contractNames) == 0 {
			continue
		}

		if err := fn(path, file, contractNames); err != nil {
			return err
		}
	}
	return nil
}

func externalContractGoFilePaths(repoRoot string) ([]string, error) {
	externalContractGoFilePathsOnce.Do(func() {
		externalContractGoFilePathsList, externalContractGoFilePathsErr = scanExternalContractGoFilePaths(repoRoot)
	})
	return externalContractGoFilePathsList, externalContractGoFilePathsErr
}

func scanExternalContractGoFilePaths(repoRoot string) ([]string, error) {
	var paths []string
	for _, root := range conformanceScanRoots(repoRoot) {
		rootPath := filepath.Join(repoRoot, root)
		info, err := os.Stat(rootPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if !info.IsDir() {
			return paths, nil
		}

		err = filepath.WalkDir(rootPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", ".codex", "gatus", "node_modules", "vendor":
					return filepath.SkipDir
				}
				if d.Name() == "testdata" || strings.HasPrefix(d.Name(), "testdata_") {
					return filepath.SkipDir
				}
				if filepath.Base(path) == "contract" && filepath.Dir(path) == repoRoot {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			usesContract, err := fileImportsPackage(path, "github.com/birdordev/plumego/contract")
			if err != nil {
				return err
			}
			if !usesContract {
				return nil
			}

			paths = append(paths, path)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func fileImportsPackage(path string, importPath string) (bool, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return false, err
	}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err == nil && path == importPath {
			return true, nil
		}
	}
	return false, nil
}

func conformanceScanRoots(repoRoot string) []string {
	spec, err := os.ReadFile(filepath.Join(repoRoot, "specs", "repo.yaml"))
	if err != nil {
		return []string{"cmd", "core", "health", "internal", "log", "metrics", "middleware", "reference", "router", "security", "store", "x"}
	}

	roots := map[string]struct{}{}
	inPathList := false
	for _, line := range strings.Split(string(spec), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasSuffix(trimmed, "paths:") {
			inPathList = true
			continue
		}
		if inPathList && !strings.HasPrefix(trimmed, "- ") && trimmed != "" {
			inPathList = false
		}
		if !inPathList || !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		path := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
		path = strings.Trim(path, `"'`)
		if path == "" {
			continue
		}
		root := strings.Split(path, "/")[0]
		if root != "" && root != "contract" {
			roots[root] = struct{}{}
		}
	}
	for _, root := range []string{"cmd", "internal", "reference", "x"} {
		roots[root] = struct{}{}
	}

	out := make([]string, 0, len(roots))
	for root := range roots {
		out = append(out, root)
	}
	sort.Strings(out)
	return out
}

func TestConformanceScanCoverageAndBudget(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	paths, err := externalContractGoFilePaths(repoRoot)
	if err != nil {
		t.Fatalf("scan external contract imports: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("expected external contract conformance scan to find at least one caller")
	}
	if len(paths) > 150 {
		t.Fatalf("external contract conformance scan covers %d files, above maintenance budget 150", len(paths))
	}

	required := map[string]bool{
		"x/messaging/api.go":                        false,
		"x/observability/ops/healthhttp/helpers.go": false,
	}
	for _, path := range paths {
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if _, ok := required[rel]; ok {
			required[rel] = true
		}
	}
	var missing []string
	for path, found := range required {
		if !found {
			missing = append(missing, path)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("external contract conformance scan missed required paths:\n%s", strings.Join(missing, "\n"))
	}
}

func TestExternalTypedErrorsUseCanonicalContractCodes(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	allowedCodesForType := map[string]map[string]struct{}{
		"TypeValidation": {
			"CodeValidationError": {},
			"CodeBadRequest":      {},
			"CodeInvalidRequest":  {},
			"CodeInvalidJSON":     {},
			"CodeInvalidQuery":    {},
		},
		"TypeRequired":         {"CodeRequired": {}},
		"TypeInvalidFormat":    {"CodeInvalidFormat": {}},
		"TypeOutOfRange":       {"CodeOutOfRange": {}},
		"TypeDuplicate":        {"CodeDuplicate": {}},
		"TypeUnauthorized":     {"CodeUnauthorized": {}},
		"TypeForbidden":        {"CodeForbidden": {}},
		"TypeInvalidToken":     {"CodeInvalidToken": {}},
		"TypeExpiredToken":     {"CodeExpiredToken": {}},
		"TypeNotFound":         {"CodeResourceNotFound": {}},
		"TypeConflict":         {"CodeConflict": {}},
		"TypeAlreadyExists":    {"CodeAlreadyExists": {}},
		"TypeGone":             {"CodeGone": {}},
		"TypeInternal":         {"CodeInternalError": {}},
		"TypeUnavailable":      {"CodeUnavailable": {}},
		"TypeTimeout":          {"CodeTimeout": {}},
		"TypeRateLimited":      {"CodeRateLimited": {}},
		"TypeMaintenance":      {"CodeMaintenance": {}},
		"TypeMethodNotAllowed": {"CodeMethodNotAllowed": {}},
		"TypeNotImplemented":   {"CodeNotImplemented": {}},
		"TypeBadGateway":       {"CodeBadGateway": {}},
		"TypeGatewayTimeout":   {"CodeGatewayTimeout": {}},
	}

	var violations []string
	registeredCustomCodes := loadContractErrorCodeRegistry(t, repoRoot)
	allowedDynamicCustomCodes := map[string]int{
		"x/observability/ops/ops.go#Handler.writeHookError": 1,
		"x/observability/ops/ops.go#writeNotImplemented":    1,
	}
	actualDynamicCustomCodes := map[string]int{}
	fset := token.NewFileSet()
	err = walkExternalContractGoFiles(repoRoot, fset, func(path string, file *ast.File, contractNames map[string]struct{}) error {
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		stringConsts, err := packageStringConstNames(filepath.Dir(path))
		if err != nil {
			return err
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			funcKey := rel + "#" + funcDeclName(fn)

			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Build" {
					return true
				}
				chain, ok := contractErrorBuilderChain(call, contractNames)
				if !ok {
					return true
				}

				typeName := ""
				typeIndex := -1
				codeName := ""
				customCodeRef := ""
				codeIndex := -1
				dynamicCode := false
				for i, step := range chain {
					switch step.name {
					case "Type":
						if len(step.args) != 1 {
							continue
						}
						if name, ok := contractSelector(step.args[0], contractNames, "Type"); ok {
							typeName = name
							typeIndex = i
						}
					case "Code":
						if len(step.args) != 1 {
							continue
						}
						if name, ok := contractSelector(step.args[0], contractNames, "Code"); ok {
							codeName = name
							codeIndex = i
						} else if ref, ok := customCodeReference(step.args[0], stringConsts); ok {
							customCodeRef = ref
							codeIndex = i
						} else {
							dynamicCode = true
							codeIndex = i
						}
					}
				}
				if typeName == "" || codeIndex < typeIndex {
					return true
				}
				if dynamicCode {
					actualDynamicCustomCodes[funcKey]++
					if _, ok := allowedDynamicCustomCodes[funcKey]; !ok {
						pos := fset.Position(call.Pos())
						violations = append(violations, filepath.ToSlash(rel)+":"+strconv.Itoa(pos.Line)+
							" uses a dynamic typed custom code with contract."+typeName+"; use a registered const/literal or add an explicit dynamic-code allowlist")
					}
					return true
				}
				if customCodeRef != "" {
					key := rel + "#" + customCodeRef
					registeredType, ok := registeredCustomCodes[key]
					if !ok {
						pos := fset.Position(call.Pos())
						violations = append(violations, filepath.ToSlash(rel)+":"+strconv.Itoa(pos.Line)+
							" uses unregistered extension-owned error code "+customCodeRef+" with contract."+typeName)
						return true
					}
					if registeredType != typeName {
						pos := fset.Position(call.Pos())
						violations = append(violations, filepath.ToSlash(rel)+":"+strconv.Itoa(pos.Line)+
							" uses "+customCodeRef+" with contract."+typeName+"; registry declares contract."+registeredType)
					}
					return true
				}
				if codeName == "" {
					return true
				}

				allowedCodes, ok := allowedCodesForType[typeName]
				if !ok {
					return true
				}
				if _, ok := allowedCodes[codeName]; ok {
					return true
				}
				pos := fset.Position(call.Pos())
				rel, err := filepath.Rel(repoRoot, pos.Filename)
				if err != nil {
					rel = pos.Filename
				}
				violations = append(violations, filepath.ToSlash(rel)+":"+strconv.Itoa(pos.Line)+
					" uses contract."+typeName+" with contract."+codeName+"; use a type-compatible contract code or an extension-owned code")
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan external typed error code overrides: %v", err)
	}
	for key, count := range actualDynamicCustomCodes {
		if allowed, ok := allowedDynamicCustomCodes[key]; ok && count != allowed {
			violations = append(violations, key+" uses dynamic typed custom codes "+strconv.Itoa(count)+" time(s); expected "+strconv.Itoa(allowed))
		}
	}
	for key, allowed := range allowedDynamicCustomCodes {
		if actualDynamicCustomCodes[key] != allowed {
			violations = append(violations, key+" uses dynamic typed custom codes "+strconv.Itoa(actualDynamicCustomCodes[key])+" time(s); expected "+strconv.Itoa(allowed))
		}
	}
	if len(violations) > 0 {
		t.Fatalf("typed contract errors must not override contract-owned codes across type families:\n%s", strings.Join(violations, "\n"))
	}
}

type contractErrorCodeRegistry struct {
	ExtensionCodes []contractErrorCodeEntry `json:"extension_codes"`
}

type contractErrorCodeEntry struct {
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
	Type   string `json:"type"`
}

func loadContractErrorCodeRegistry(t *testing.T, repoRoot string) map[string]string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(repoRoot, "specs", "contract-error-codes.json"))
	if err != nil {
		t.Fatalf("read contract error code registry: %v", err)
	}
	var registry contractErrorCodeRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		t.Fatalf("parse contract error code registry: %v", err)
	}

	out := map[string]string{}
	for _, entry := range registry.ExtensionCodes {
		if entry.Path == "" || entry.Symbol == "" || entry.Type == "" {
			t.Fatalf("contract error code registry entries require path, symbol, and type: %+v", entry)
		}
		key := filepath.ToSlash(entry.Path) + "#" + entry.Symbol
		if _, exists := out[key]; exists {
			t.Fatalf("duplicate contract error code registry entry: %s", key)
		}
		out[key] = entry.Type
	}
	return out
}

func stringConstNames(file *ast.File) map[string]struct{} {
	names := map[string]struct{}{}
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			continue
		}
		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range valueSpec.Names {
				if i >= len(valueSpec.Values) {
					continue
				}
				lit, ok := valueSpec.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				names[name.Name] = struct{}{}
			}
		}
	}
	return names
}

func packageStringConstNames(dir string) (map[string]struct{}, error) {
	packageStringConstNamesMu.Lock()
	if cached, ok := packageStringConstNamesCache[dir]; ok {
		packageStringConstNamesMu.Unlock()
		return cached, nil
	}
	packageStringConstNamesMu.Unlock()

	names := map[string]struct{}{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return nil, err
		}
		for name := range stringConstNames(file) {
			names[name] = struct{}{}
		}
	}

	packageStringConstNamesMu.Lock()
	packageStringConstNamesCache[dir] = names
	packageStringConstNamesMu.Unlock()
	return names, nil
}

func customCodeReference(expr ast.Expr, stringConsts map[string]struct{}) (string, bool) {
	switch value := expr.(type) {
	case *ast.Ident:
		if _, ok := stringConsts[value.Name]; !ok {
			return "", false
		}
		return value.Name, true
	case *ast.BasicLit:
		if value.Kind == token.STRING {
			text, err := strconv.Unquote(value.Value)
			if err != nil {
				return value.Value, true
			}
			return "literal:" + text, true
		}
		return "", false
	case *ast.SelectorExpr:
		ident, ok := value.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		return ident.Name + "." + value.Sel.Name, true
	default:
		return "", false
	}
}

func TestExternalWriteResponseUsesSuccessStatuses(t *testing.T) {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	allowedDynamicStatusCalls := map[string]int{}
	actualDynamicStatusCalls := map[string]int{}
	var violations []string
	fset := token.NewFileSet()
	err = walkExternalContractGoFiles(repoRoot, fset, func(path string, file *ast.File, contractNames map[string]struct{}) error {
		httpNames := packageImportNames(file, "net/http")

		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			key := rel + "#" + funcDeclName(fn)
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) < 3 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "WriteResponse" {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				if _, ok := contractNames[ident.Name]; !ok {
					return true
				}
				if statusIsKnownNonSuccess(call.Args[2], httpNames) {
					pos := fset.Position(call.Args[2].Pos())
					violations = append(violations, filepath.ToSlash(rel)+":"+strconv.Itoa(pos.Line)+
						" uses a known non-2xx status; use WriteError for errors or a module-owned status document writer")
					return true
				}
				if statusIsKnownSuccess(call.Args[2], httpNames) {
					return true
				}
				actualDynamicStatusCalls[key]++
				if _, ok := allowedDynamicStatusCalls[key]; !ok {
					pos := fset.Position(call.Args[2].Pos())
					violations = append(violations, filepath.ToSlash(rel)+":"+strconv.Itoa(pos.Line)+
						" uses a dynamic status; keep contract.WriteResponse on known 2xx success statuses")
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan external WriteResponse statuses: %v", err)
	}
	for key, count := range actualDynamicStatusCalls {
		if allowed, ok := allowedDynamicStatusCalls[key]; ok && count != allowed {
			violations = append(violations, key+" uses dynamic contract.WriteResponse status "+strconv.Itoa(count)+" time(s); expected "+strconv.Itoa(allowed))
		}
	}
	for key, allowed := range allowedDynamicStatusCalls {
		if actualDynamicStatusCalls[key] != allowed {
			violations = append(violations, key+" uses dynamic contract.WriteResponse status "+strconv.Itoa(actualDynamicStatusCalls[key])+" time(s); expected "+strconv.Itoa(allowed))
		}
	}
	if len(violations) > 0 {
		t.Fatalf("external contract.WriteResponse calls must use known 2xx statuses:\n%s", strings.Join(violations, "\n"))
	}
}

func statusIsKnownSuccess(expr ast.Expr, httpNames map[string]struct{}) bool {
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.INT {
		status, err := strconv.Atoi(lit.Value)
		return err == nil && status >= 200 && status <= 299
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	if _, ok := httpNames[ident.Name]; !ok {
		return false
	}
	return strings.HasPrefix(sel.Sel.Name, "StatusOK") ||
		strings.HasPrefix(sel.Sel.Name, "StatusCreated") ||
		strings.HasPrefix(sel.Sel.Name, "StatusAccepted") ||
		strings.HasPrefix(sel.Sel.Name, "StatusNonAuthoritativeInfo") ||
		strings.HasPrefix(sel.Sel.Name, "StatusNoContent") ||
		strings.HasPrefix(sel.Sel.Name, "StatusResetContent") ||
		strings.HasPrefix(sel.Sel.Name, "StatusPartialContent") ||
		strings.HasPrefix(sel.Sel.Name, "StatusMultiStatus") ||
		strings.HasPrefix(sel.Sel.Name, "StatusAlreadyReported") ||
		strings.HasPrefix(sel.Sel.Name, "StatusIMUsed")
}

func statusIsKnownNonSuccess(expr ast.Expr, httpNames map[string]struct{}) bool {
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.INT {
		status, err := strconv.Atoi(lit.Value)
		return err == nil && (status < 200 || status > 299)
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	if _, ok := httpNames[ident.Name]; !ok {
		return false
	}
	return !strings.HasPrefix(sel.Sel.Name, "StatusOK") &&
		!strings.HasPrefix(sel.Sel.Name, "StatusCreated") &&
		!strings.HasPrefix(sel.Sel.Name, "StatusAccepted") &&
		!strings.HasPrefix(sel.Sel.Name, "StatusNonAuthoritativeInfo") &&
		!strings.HasPrefix(sel.Sel.Name, "StatusNoContent") &&
		!strings.HasPrefix(sel.Sel.Name, "StatusResetContent") &&
		!strings.HasPrefix(sel.Sel.Name, "StatusPartialContent") &&
		!strings.HasPrefix(sel.Sel.Name, "StatusMultiStatus") &&
		!strings.HasPrefix(sel.Sel.Name, "StatusAlreadyReported") &&
		!strings.HasPrefix(sel.Sel.Name, "StatusIMUsed")
}

func funcDeclName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return typeExprName(fn.Recv.List[0].Type) + "." + fn.Name.Name
}

func typeExprName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return typeExprName(t.X)
	case *ast.IndexExpr:
		return typeExprName(t.X)
	case *ast.IndexListExpr:
		return typeExprName(t.X)
	default:
		return "unknown"
	}
}

type errorBuilderStep struct {
	name string
	args []ast.Expr
}

func contractErrorBuilderChain(call *ast.CallExpr, contractNames map[string]struct{}) ([]errorBuilderStep, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	if sel.Sel.Name == "NewErrorBuilder" {
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return nil, false
		}
		_, ok = contractNames[ident.Name]
		return nil, ok
	}

	prevCall, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	chain, ok := contractErrorBuilderChain(prevCall, contractNames)
	if !ok {
		return nil, false
	}
	return append(chain, errorBuilderStep{name: sel.Sel.Name, args: call.Args}), true
}

func contractSelector(expr ast.Expr, contractNames map[string]struct{}, prefix string) (string, bool) {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || !strings.HasPrefix(sel.Sel.Name, prefix) {
		return "", false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	if _, ok := contractNames[ident.Name]; !ok {
		return "", false
	}
	return sel.Sel.Name, true
}

func contractImportNames(file *ast.File) map[string]struct{} {
	return packageImportNames(file, "github.com/birdordev/plumego/contract")
}

func packageImportNames(file *ast.File, importPath string) map[string]struct{} {
	names := map[string]struct{}{}
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != importPath {
			continue
		}
		name := filepath.Base(importPath)
		if imp.Name != nil {
			if imp.Name.Name == "." || imp.Name.Name == "_" {
				continue
			}
			name = imp.Name.Name
		}
		names[name] = struct{}{}
	}
	return names
}
