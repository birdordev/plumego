package checkutil

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const modulePath = "github.com/spcent/plumego"
const scannerMaxTokenSize = 1024 * 1024

// NewLineScanner returns a line scanner sized for repository control-plane
// files, where long Markdown table rows or snapshot lines can exceed bufio's
// default 64 KiB token limit.
func NewLineScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), scannerMaxTokenSize)
	return scanner
}

func StableRoots() []string {
	return []string{
		"core",
		"router",
		"contract",
		"middleware",
		"security",
		"store",
		"health",
		"log",
		"metrics",
	}
}

var allowedTopLevelDirs = []string{
	"benchmark",
	"cmd",
	"contract",
	"core",
	"docs",
	"examples",
	"health",
	"internal",
	"log",
	"metrics",
	"middleware",
	"reference",
	"router",
	"security",
	"specs",
	"store",
	"scripts",
	"tasks",
	"website",
	"x",
}

var stableHTTPSurfaceExemptRoots = map[string]struct{}{
	"core":   {},
	"router": {},
}

var suspiciousHTTPSurfaceFiles = map[string]struct{}{
	"api.go":          {},
	"endpoint.go":     {},
	"endpoints.go":    {},
	"handler.go":      {},
	"handlers.go":     {},
	"http_handler.go": {},
	"route.go":        {},
	"routes.go":       {},
}

var routeRegistrationNames = map[string]struct{}{
	"Any":            {},
	"AnyNamed":       {},
	"Delete":         {},
	"DeleteNamed":    {},
	"Get":            {},
	"GetNamed":       {},
	"Handle":         {},
	"HandleFunc":     {},
	"Mount":          {},
	"Patch":          {},
	"PatchNamed":     {},
	"Post":           {},
	"PostNamed":      {},
	"Put":            {},
	"PutNamed":       {},
	"RegisterRoutes": {},
	"Routes":         {},
	"ServeHTTP":      {},
}

func AllowedTopLevelDirs() map[string]struct{} {
	out := make(map[string]struct{}, len(allowedTopLevelDirs))
	for _, dir := range allowedTopLevelDirs {
		out[dir] = struct{}{}
	}
	return out
}

func ReadRepoExtensionRoots(repoRoot string) (map[string]struct{}, error) {
	repoSpecPath := filepath.Join(repoRoot, "specs", "repo.yaml")
	file, err := os.Open(repoSpecPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	roots := map[string]struct{}{}
	scanner := NewLineScanner(file)
	inLayers := false
	inExtension := false
	inPaths := false

	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), " \t")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		switch {
		case indent == 0 && trimmed == "layers:":
			inLayers = true
			inExtension = false
			inPaths = false
			continue
		case indent == 0:
			inLayers = false
			inExtension = false
			inPaths = false
		}

		if !inLayers {
			continue
		}

		switch {
		case indent == 2 && trimmed == "extension:":
			inExtension = true
			inPaths = false
			continue
		case indent == 2:
			inExtension = false
			inPaths = false
		}

		if !inExtension {
			continue
		}

		switch {
		case indent == 4 && trimmed == "paths:":
			inPaths = true
			continue
		case indent == 4:
			inPaths = false
		}

		if !inPaths {
			continue
		}
		if indent < 6 || !strings.HasPrefix(trimmed, "- ") {
			continue
		}

		value := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
		value = strings.Trim(value, "\"'")
		if value == "" {
			continue
		}
		roots[value] = struct{}{}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return roots, nil
}

func ReadBaseline(path string) (map[string]struct{}, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]struct{}{}, nil
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()

	out := map[string]struct{}{}
	scanner := NewLineScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func walkGoImports(repoRoot, dir string, includeTests bool, visit func(relPath, importPath string)) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				} else if !os.IsNotExist(err) {
					return err
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || (!includeTests && strings.HasSuffix(path, "_test.go")) {
			return nil
		}

		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse imports %s: %w", path, err)
		}

		relPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)

		for _, imp := range node.Imports {
			importPath, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return fmt.Errorf("unquote import %s: %w", path, err)
			}
			visit(relPath, importPath)
		}

		return nil
	})
}

func FindDisallowedImports(repoRoot string) ([]string, error) {
	rules, err := ReadDependencyRules(repoRoot)
	if err != nil {
		return nil, err
	}

	var moduleNames []string
	for name := range rules.Modules {
		moduleNames = append(moduleNames, name)
	}
	sort.Strings(moduleNames)

	var violations []string

	for _, name := range moduleNames {
		rule := rules.Modules[name]
		if rule.Path == "" {
			continue
		}

		dir := filepath.Join(repoRoot, filepath.FromSlash(rule.Path))
		if _, err := os.Stat(dir); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		err := walkGoImports(repoRoot, dir, false, func(relPath, importPath string) {
			if !strings.HasPrefix(importPath, rules.ModulePath) {
				return
			}

			relImportPath := ""
			if importPath != rules.ModulePath {
				relImportPath = strings.TrimPrefix(importPath, rules.ModulePath+"/")
			}

			disallowed := false
			for _, pattern := range rule.Deny {
				if matchesRepoPattern(relImportPath, pattern) {
					disallowed = true
					break
				}
			}
			if !disallowed {
				for _, pattern := range rules.ForbiddenImportPatterns {
					if matchesForbiddenImportPattern(importPath, relImportPath, pattern, rules.ModulePath) {
						disallowed = true
						break
					}
				}
			}
			if !disallowed {
				return
			}

			key := relPath + "|" + importPath
			violations = append(violations, key)
		})
		if err != nil {
			return nil, err
		}
	}

	for _, forbiddenPath := range rules.ForbiddenPaths {
		target := filepath.Join(repoRoot, filepath.FromSlash(forbiddenPath))
		if _, err := os.Stat(target); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		key := "FORBIDDEN_PATH|" + forbiddenPath
		violations = append(violations, key)
	}

	sort.Strings(violations)
	return violations, nil
}

func ValidateManifestDependencyRuleConsistency(repoRoot string) ([]string, error) {
	rules, err := ReadDependencyRules(repoRoot)
	if err != nil {
		return nil, err
	}

	var moduleNames []string
	for name := range rules.Modules {
		moduleNames = append(moduleNames, name)
	}
	sort.Strings(moduleNames)

	var violations []string
	for _, name := range moduleNames {
		rule := rules.Modules[name]
		if rule.Path == "" {
			continue
		}
		if !strings.HasPrefix(rule.Path, "x/") {
			continue
		}

		manifestPath := filepath.Join(repoRoot, filepath.FromSlash(rule.Path), "module.yaml")
		doc, err := parseManifest(manifestPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		manifestAllows := doc.Lists["allowed_imports"]
		manifestDenies := doc.Lists["forbidden_imports"]

		for _, allowed := range manifestAllows {
			allowed = cleanScalar(allowed)
			if allowed == "" || allowed == "stdlib" {
				continue
			}
			if matchesAnyRepoPattern(allowed, rule.Deny) {
				violations = append(violations, fmt.Sprintf("%s: allowed_imports entry %q is denied by specs/dependency-rules.yaml module %s", filepath.ToSlash(manifestPath), allowed, name))
			}
		}

		for _, allowed := range rule.Allow {
			allowed = cleanScalar(allowed)
			if allowed == "" || allowed == "stdlib" {
				continue
			}
			if matchesAnyRepoPattern(allowed, manifestDenies) {
				violations = append(violations, fmt.Sprintf("specs/dependency-rules.yaml module %s allows %q but %s forbids it", name, allowed, filepath.ToSlash(manifestPath)))
			}
		}
	}

	sort.Strings(violations)
	return violations, nil
}

func ValidateDeclaredInternalCallers(repoRoot string) ([]string, error) {
	rules, err := ReadDependencyRules(repoRoot)
	if err != nil {
		return nil, err
	}

	var internalModuleNames []string
	for name, rule := range rules.Modules {
		if strings.HasPrefix(rule.Path, "internal/") {
			internalModuleNames = append(internalModuleNames, name)
		}
	}
	sort.Strings(internalModuleNames)

	actualCallers := map[string]map[string]struct{}{}
	for _, name := range internalModuleNames {
		actualCallers[name] = map[string]struct{}{}
	}

	var violations []string
	err = walkGoImports(repoRoot, repoRoot, false, func(relPath, importPath string) {
		if !strings.HasPrefix(importPath, rules.ModulePath+"/internal/") {
			return
		}

		caller := filepath.ToSlash(filepath.Dir(relPath))
		relImportPath := strings.TrimPrefix(importPath, rules.ModulePath+"/")
		if strings.HasPrefix(caller, "internal/") {
			return
		}

		name, rule, ok := matchingInternalRule(relImportPath, rules, internalModuleNames)
		if !ok {
			violations = append(violations, fmt.Sprintf("%s imports %s but no specs/dependency-rules.yaml internal module declares it", relPath, importPath))
			return
		}

		if len(rule.DeclaredCallers) == 0 {
			violations = append(violations, fmt.Sprintf("%s imports %s but specs/dependency-rules.yaml module %s has no declared_callers", relPath, importPath, name))
			return
		}

		if !matchesAnyDeclaredCaller(caller, rule.DeclaredCallers) {
			violations = append(violations, fmt.Sprintf("%s imports %s but caller %q is not listed in specs/dependency-rules.yaml module %s declared_callers", relPath, importPath, caller, name))
			return
		}
		actualCallers[name][caller] = struct{}{}
	})
	if err != nil {
		return nil, err
	}

	for _, name := range internalModuleNames {
		rule := rules.Modules[name]
		for _, declared := range rule.DeclaredCallers {
			if declared == "" {
				continue
			}
			if !hasActualDeclaredCaller(declared, actualCallers[name]) {
				violations = append(violations, fmt.Sprintf("specs/dependency-rules.yaml module %s declares caller %q but no non-test Go import uses it", name, declared))
			}
		}
	}

	sort.Strings(violations)
	return violations, nil
}

func matchingInternalRule(relImportPath string, rules dependencyRulesDoc, internalModuleNames []string) (string, dependencyModuleRule, bool) {
	var matchedName string
	var matchedRule dependencyModuleRule
	for _, name := range internalModuleNames {
		rule := rules.Modules[name]
		if !matchesRepoPattern(relImportPath, rule.Path) {
			continue
		}
		if len(rule.Path) <= len(matchedRule.Path) {
			continue
		}
		matchedName = name
		matchedRule = rule
	}
	return matchedName, matchedRule, matchedName != ""
}

func matchesAnyDeclaredCaller(caller string, declaredCallers []string) bool {
	for _, declared := range declaredCallers {
		if caller == declared || strings.HasPrefix(caller, declared+"/") {
			return true
		}
	}
	return false
}

func hasActualDeclaredCaller(declared string, actual map[string]struct{}) bool {
	for caller := range actual {
		if caller == declared || strings.HasPrefix(caller, declared+"/") {
			return true
		}
	}
	return false
}

func matchesAnyRepoPattern(relPath string, patterns []string) bool {
	for _, pattern := range patterns {
		if matchesRepoPattern(relPath, pattern) {
			return true
		}
	}
	return false
}

func FindMissingModuleManifests(repoRoot string) ([]string, error) {
	var missing []string

	for _, root := range StableRoots() {
		path := filepath.Join(repoRoot, root, "module.yaml")
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, filepath.ToSlash(filepath.Join(root, "module.yaml")))
				continue
			}
			return nil, err
		}
	}

	xDir := filepath.Join(repoRoot, "x")
	entries, err := os.ReadDir(xDir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		relModule := filepath.ToSlash(filepath.Join("x", entry.Name()))
		modulePath := filepath.Join(xDir, entry.Name(), "module.yaml")
		if _, err := os.Stat(modulePath); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return nil, err
		}

		missing = append(missing, relModule)
	}

	sort.Strings(missing)
	return missing, nil
}

// FindReferenceXImports scans all .go files under refDir (relative to repoRoot)
// and returns violations for any file that imports from the x/ extension layer.
// This enforces that the canonical reference app stays free of extension imports.
func FindReferenceXImports(repoRoot, refDir string) ([]string, error) {
	dir := filepath.Join(repoRoot, refDir)
	blockedPrefix := modulePath + "/x/"

	var violations []string
	err := walkGoImports(repoRoot, dir, true, func(relPath, importPath string) {
		if strings.HasPrefix(importPath, blockedPrefix) {
			violations = append(violations, relPath+"|"+importPath)
		}
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	sort.Strings(violations)
	return violations, nil
}

// FindXGoModFiles returns all go.mod files nested under x/. Extension packages
// are part of the main module; optional third-party adapters must live in
// reference applications or external modules instead of adding x/* submodules.
func FindXGoModFiles(repoRoot string) ([]string, error) {
	xDir := filepath.Join(repoRoot, "x")
	var violations []string
	err := filepath.WalkDir(xDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		violations = append(violations, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Strings(violations)
	return violations, nil
}

// ValidateReferenceModules verifies that every top-level reference example is a
// standalone module with local module naming and no imports back through the
// repository's reference or internal paths.
func ValidateReferenceModules(repoRoot string) ([]string, error) {
	referenceDir := filepath.Join(repoRoot, "reference")
	entries, err := os.ReadDir(referenceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var violations []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		relDir := filepath.ToSlash(filepath.Join("reference", entry.Name()))
		dir := filepath.Join(referenceDir, entry.Name())
		modPath := filepath.Join(dir, "go.mod")
		data, err := os.ReadFile(modPath)
		if err != nil {
			if os.IsNotExist(err) {
				violations = append(violations, relDir+" missing go.mod")
				continue
			}
			return nil, err
		}

		moduleName := parseModuleName(string(data))
		if moduleName == "" {
			violations = append(violations, relDir+"/go.mod missing module directive")
		} else if moduleName != entry.Name() {
			violations = append(violations, fmt.Sprintf("%s/go.mod module %q must equal %q", relDir, moduleName, entry.Name()))
		}
		if !strings.Contains(string(data), "require github.com/spcent/plumego ") &&
			!strings.Contains(string(data), "\n\tgithub.com/spcent/plumego ") {
			violations = append(violations, relDir+"/go.mod missing github.com/spcent/plumego require")
		}
		if !strings.Contains(string(data), "replace github.com/spcent/plumego => ../..") {
			violations = append(violations, relDir+"/go.mod missing local github.com/spcent/plumego replace")
		}

		err = walkGoImports(repoRoot, dir, true, func(relPath, importPath string) {
			switch {
			case strings.HasPrefix(importPath, modulePath+"/reference/"):
				violations = append(violations, relPath+" imports repository reference path "+importPath)
			case strings.HasPrefix(importPath, modulePath+"/internal/"):
				violations = append(violations, relPath+" imports repository internal path "+importPath)
			}
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Strings(violations)
	return violations, nil
}

func parseModuleName(data string) string {
	scanner := NewLineScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

func FindUnexpectedTopLevelDirs(repoRoot string, allowed map[string]struct{}) ([]string, error) {
	entries, err := os.ReadDir(repoRoot)
	if err != nil {
		return nil, err
	}

	var unexpected []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		if _, ok := allowed[name]; ok {
			continue
		}
		unexpected = append(unexpected, name)
	}

	sort.Strings(unexpected)
	return unexpected, nil
}

func FindOrphanedExtensionRoots(repoRoot string, declared map[string]struct{}) ([]string, error) {
	xDir := filepath.Join(repoRoot, "x")
	entries, err := os.ReadDir(xDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var orphans []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		rel := filepath.ToSlash(filepath.Join("x", entry.Name()))
		if _, ok := declared[rel]; ok {
			continue
		}
		orphans = append(orphans, rel)
	}

	sort.Strings(orphans)
	return orphans, nil
}

func FindEmptyMisleadingDirs(repoRoot string) ([]string, error) {
	roots := append([]string{}, StableRoots()...)
	roots = append(roots, "x")

	var empty []string
	for _, root := range roots {
		rootPath := filepath.Join(repoRoot, root)
		if _, err := os.Stat(rootPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		err := filepath.WalkDir(rootPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			if path == rootPath {
				return nil
			}

			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "testdata" || name == "migrations" {
				return nil
			}

			entries, err := os.ReadDir(path)
			if err != nil {
				return err
			}
			if len(entries) != 0 {
				return nil
			}

			rel, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			empty = append(empty, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Strings(empty)
	return empty, nil
}

func ValidateModuleManifests(repoRoot string) ([]string, error) {
	schema, err := ReadManifestSchema(repoRoot)
	if err != nil {
		return nil, err
	}

	var paths []string
	err = filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "module.yaml" {
			return nil
		}

		relPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)
		if strings.Count(relPath, "/") == 1 {
			root := strings.Split(relPath, "/")[0]
			if isStableRoot(root) {
				paths = append(paths, path)
			}
			return nil
		}
		if strings.HasPrefix(relPath, "x/") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)

	var violations []string
	for _, path := range paths {
		items, err := validateModuleManifest(repoRoot, path, schema)
		if err != nil {
			return nil, err
		}
		violations = append(violations, items...)
	}

	sort.Strings(violations)
	return violations, nil
}

type manifestDoc struct {
	Seen       map[string]bool
	Scalars    map[string]string
	ListCounts map[string]int
	Lists      map[string][]string
}

type packageIndexEntry struct {
	Path         string
	TaskFamilies []string
	StartWith    []string
}

func ReadCanonicalExtensionEntrypoints(repoRoot string) ([]string, error) {
	doc, err := ReadExtensionTaxonomy(repoRoot)
	if err != nil {
		return nil, err
	}
	return doc.CanonicalRoots, nil
}

func FindExtensionPrimerCoverageViolations(repoRoot string, roots []string) ([]string, error) {
	var violations []string
	for _, root := range roots {
		manifestPath := filepath.Join(repoRoot, filepath.FromSlash(root), "module.yaml")
		if _, err := os.Stat(manifestPath); err != nil {
			if os.IsNotExist(err) {
				violations = append(violations, fmt.Sprintf("%s is a canonical extension entrypoint but has no module.yaml", root))
				continue
			}
			return nil, err
		}

		doc, err := parseManifest(manifestPath)
		if err != nil {
			return nil, err
		}
		if len(doc.Lists["doc_paths"]) == 0 {
			violations = append(violations, fmt.Sprintf("%s is a canonical extension entrypoint but %s does not declare doc_paths primer coverage", root, filepath.ToSlash(manifestPath)))
		}
	}

	sort.Strings(violations)
	return violations, nil
}

func ReadPackageIndex(repoRoot string) (map[string]packageIndexEntry, error) {
	path := filepath.Join(repoRoot, "specs", "package-hotspots.yaml")
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	out := map[string]packageIndexEntry{}
	scanner := NewLineScanner(file)
	inPackages := false
	inTaskFamilies := false
	inStartWith := false
	currentPath := ""

	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), " \t")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		switch {
		case indent == 0 && trimmed == "packages:":
			inPackages = true
			inTaskFamilies = false
			inStartWith = false
			currentPath = ""
			continue
		case indent == 0:
			inPackages = false
			inTaskFamilies = false
			inStartWith = false
			currentPath = ""
		}

		if !inPackages {
			continue
		}

		switch {
		case indent == 2 && strings.HasSuffix(trimmed, ":"):
			currentPath = strings.TrimSuffix(trimmed, ":")
			out[currentPath] = packageIndexEntry{Path: currentPath}
			inTaskFamilies = false
			inStartWith = false
			continue
		case indent == 2:
			currentPath = ""
			inTaskFamilies = false
			inStartWith = false
		}

		if currentPath == "" {
			continue
		}

		switch {
		case indent == 4 && trimmed == "task_families:":
			inTaskFamilies = true
			inStartWith = false
			continue
		case indent == 4 && trimmed == "start_with:":
			inTaskFamilies = false
			inStartWith = true
			continue
		case indent == 4:
			inTaskFamilies = false
			inStartWith = false
		}

		if !inTaskFamilies && !inStartWith {
			continue
		}
		if indent < 6 || !strings.HasPrefix(trimmed, "- ") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
		value = strings.Trim(value, "\"'")
		if value == "" {
			continue
		}
		entry := out[currentPath]
		if inTaskFamilies {
			entry.TaskFamilies = append(entry.TaskFamilies, value)
		} else {
			entry.StartWith = append(entry.StartWith, value)
		}
		out[currentPath] = entry
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return out, nil
}

func FindPackageIndexCoverageViolations(repoRoot string, entries map[string]packageIndexEntry) ([]string, error) {
	var violations []string
	for pkgPath, entry := range entries {
		dirPath := filepath.Join(repoRoot, filepath.FromSlash(pkgPath))
		info, err := os.Stat(dirPath)
		if err != nil {
			if os.IsNotExist(err) {
				violations = append(violations, fmt.Sprintf("specs/package-hotspots.yaml package %s does not exist in the repository", pkgPath))
				continue
			}
			return nil, err
		}
		if !info.IsDir() {
			violations = append(violations, fmt.Sprintf("specs/package-hotspots.yaml package %s is not a directory", pkgPath))
			continue
		}

		if len(entry.StartWith) == 0 {
			violations = append(violations, fmt.Sprintf("specs/package-hotspots.yaml package %s must declare at least one start_with path", pkgPath))
			continue
		}

		for _, startPath := range entry.StartWith {
			target := filepath.Join(repoRoot, filepath.FromSlash(startPath))
			if _, err := os.Stat(target); err != nil {
				if os.IsNotExist(err) {
					violations = append(violations, fmt.Sprintf("specs/package-hotspots.yaml package %s references missing start_with path %s", pkgPath, startPath))
					continue
				}
				return nil, err
			}
		}
	}

	sort.Strings(violations)
	return violations, nil
}

func FindPackageIndexTaskFamilyViolations(entries map[string]packageIndexEntry, taskRouting map[string]taskRoutingEntry) []string {
	var violations []string
	for pkgPath, entry := range entries {
		if len(entry.TaskFamilies) == 0 {
			violations = append(violations, fmt.Sprintf("specs/package-hotspots.yaml package %s must declare at least one task_families entry", pkgPath))
			continue
		}
		for _, family := range entry.TaskFamilies {
			if _, ok := taskRouting[family]; ok {
				continue
			}
			violations = append(violations, fmt.Sprintf("specs/package-hotspots.yaml package %s references unknown task_families entry %s", pkgPath, family))
		}
	}
	sort.Strings(violations)
	return violations
}

func FindExtensionHotspotCoverageViolations(doc extensionTaxonomyDoc, entries map[string]packageIndexEntry) []string {
	var violations []string
	for family, roots := range doc.RootsByFamily {
		if len(roots) <= 1 {
			continue
		}

		canonicalRoot := ""
		for _, root := range roots {
			if doc.RootToCanonical[root] == root {
				canonicalRoot = root
				break
			}
		}
		if canonicalRoot == "" {
			continue
		}
		if _, ok := entries[canonicalRoot]; !ok {
			violations = append(violations, fmt.Sprintf("specs/package-hotspots.yaml must index canonical extension family %s for taxonomy family %s", canonicalRoot, family))
		}

		for _, root := range roots {
			if root == canonicalRoot {
				continue
			}
			if _, ok := entries[root]; ok {
				continue
			}
			violations = append(violations, fmt.Sprintf("specs/package-hotspots.yaml must index subordinate extension root %s under canonical family %s", root, canonicalRoot))
		}
	}
	sort.Strings(violations)
	return violations
}

func FindTaskRoutingCoverageViolations(repoRoot string, entries map[string]taskRoutingEntry) ([]string, error) {
	var violations []string
	for taskName, entry := range entries {
		if len(entry.StartWith) == 0 {
			violations = append(violations, fmt.Sprintf("specs/task-routing.yaml task %s must declare at least one start_with path", taskName))
			continue
		}
		for _, startPath := range entry.StartWith {
			target := filepath.Join(repoRoot, filepath.FromSlash(startPath))
			if _, err := os.Stat(target); err != nil {
				if os.IsNotExist(err) {
					violations = append(violations, fmt.Sprintf("specs/task-routing.yaml task %s references missing start_with path %s", taskName, startPath))
					continue
				}
				return nil, err
			}
		}
	}

	sort.Strings(violations)
	return violations, nil
}

func FindExtensionTaxonomyCoverageViolations(repoRoot string, declared map[string]struct{}, doc extensionTaxonomyDoc) ([]string, error) {
	var violations []string

	for _, root := range doc.CanonicalRoots {
		target := filepath.Join(repoRoot, filepath.FromSlash(root))
		if _, err := os.Stat(target); err != nil {
			if os.IsNotExist(err) {
				violations = append(violations, fmt.Sprintf("specs/extension-taxonomy.yaml canonical_root %s does not exist in the repository", root))
				continue
			}
			return nil, err
		}
	}

	for root := range doc.CoveredRoots {
		target := filepath.Join(repoRoot, filepath.FromSlash(root))
		if _, err := os.Stat(target); err != nil {
			if os.IsNotExist(err) {
				violations = append(violations, fmt.Sprintf("specs/extension-taxonomy.yaml root %s does not exist in the repository", root))
				continue
			}
			return nil, err
		}
	}

	for root := range declared {
		if _, ok := doc.CoveredRoots[root]; ok {
			continue
		}
		violations = append(violations, fmt.Sprintf("extension root %s exists in x/ but is not declared in specs/extension-taxonomy.yaml", root))
	}

	sort.Strings(violations)
	return violations, nil
}

func FindStableHTTPSurfaceViolations(repoRoot string) ([]string, error) {
	var violations []string
	for _, root := range StableRoots() {
		if _, exempt := stableHTTPSurfaceExemptRoots[root]; exempt {
			continue
		}

		rootPath := filepath.Join(repoRoot, root)
		if _, err := os.Stat(rootPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		err := filepath.WalkDir(rootPath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
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
				return fmt.Errorf("parse file %s: %w", path, err)
			}

			relPath, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			relPath = filepath.ToSlash(relPath)
			fileSuspicious := isSuspiciousHTTPSurfaceFile(filepath.Base(path))

			for _, decl := range node.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name == nil {
					continue
				}

				name := fn.Name.Name
				switch {
				case fileSuspicious && (hasStandardHTTPHandlerSignature(fn.Type) || returnsHandlerLike(fn.Type)):
					violations = append(violations, fmt.Sprintf("stable package %s exposes app-facing HTTP handler surface %s; move handlers to core, reference/standard-service, or x/*", relPath, name))
				case isRouteRegistrationSurface(fn):
					violations = append(violations, fmt.Sprintf("stable package %s exposes route registration helper %s; move app-facing route wiring to core, router, reference/standard-service, or x/*", relPath, name))
				}
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Strings(violations)
	return violations, nil
}

func isSuspiciousHTTPSurfaceFile(name string) bool {
	_, ok := suspiciousHTTPSurfaceFiles[name]
	return ok
}

func isRouteRegistrationSurface(fn *ast.FuncDecl) bool {
	if fn == nil || fn.Name == nil {
		return false
	}
	name := fn.Name.Name
	if name == "ServeHTTP" {
		return true
	}
	if _, ok := routeRegistrationNames[name]; !ok {
		return false
	}

	return hasRouterLikeParam(fn.Type) || (hasStringParam(fn.Type) && hasHandlerLikeParam(fn.Type))
}

func hasStandardHTTPHandlerSignature(fn *ast.FuncType) bool {
	if fn == nil || fn.Params == nil || len(fn.Params.List) != 2 {
		return false
	}

	return isHTTPResponseWriter(fn.Params.List[0].Type) && isPointerToSelector(fn.Params.List[1].Type, "http", "Request")
}

func hasHandlerLikeParam(fn *ast.FuncType) bool {
	if fn == nil || fn.Params == nil {
		return false
	}
	for _, field := range fn.Params.List {
		if isSelector(field.Type, "http", "Handler") || isSelector(field.Type, "http", "HandlerFunc") {
			return true
		}
	}
	return false
}

func hasRouterLikeParam(fn *ast.FuncType) bool {
	if fn == nil || fn.Params == nil {
		return false
	}
	for _, field := range fn.Params.List {
		switch {
		case isSelector(field.Type, "router", "Router"):
			return true
		case isPointerToSelector(field.Type, "router", "Router"):
			return true
		case isSelector(field.Type, "http", "ServeMux"):
			return true
		case isPointerToSelector(field.Type, "http", "ServeMux"):
			return true
		}
	}
	return false
}

func hasStringParam(fn *ast.FuncType) bool {
	if fn == nil || fn.Params == nil {
		return false
	}
	for _, field := range fn.Params.List {
		if ident, ok := field.Type.(*ast.Ident); ok && ident.Name == "string" {
			return true
		}
	}
	return false
}

func returnsHandlerLike(fn *ast.FuncType) bool {
	if fn == nil || fn.Results == nil {
		return false
	}
	for _, field := range fn.Results.List {
		if isSelector(field.Type, "http", "Handler") || isSelector(field.Type, "http", "HandlerFunc") {
			return true
		}
	}
	return false
}

func isHTTPResponseWriter(expr ast.Expr) bool {
	return isSelector(expr, "http", "ResponseWriter")
}

func isSelector(expr ast.Expr, pkg, name string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == pkg && selector.Sel.Name == name
}

func isPointerToSelector(expr ast.Expr, pkg, name string) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	return isSelector(star.X, pkg, name)
}

func validateModuleManifest(repoRoot, path string, schema manifestSchema) ([]string, error) {
	doc, err := parseManifest(path)
	if err != nil {
		return nil, err
	}

	relPath, err := filepath.Rel(repoRoot, filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	relPath = filepath.ToSlash(relPath)

	var violations []string
	for _, key := range schema.Required {
		if !doc.Seen[key] {
			violations = append(violations, fmt.Sprintf("%s: missing required key %q", filepath.ToSlash(path), key))
			continue
		}

		if _, isEnum := schema.Enums[key]; isEnum {
			continue
		}
		if maxCount, isList := schema.Limits[key]; isList {
			if doc.ListCounts[key] == 0 {
				violations = append(violations, fmt.Sprintf("%s: required list %q must not be empty", filepath.ToSlash(path), key))
			}
			if doc.ListCounts[key] > maxCount {
				violations = append(violations, fmt.Sprintf("%s: list %q exceeds limit %d", filepath.ToSlash(path), key, maxCount))
			}
			continue
		}
		if strings.TrimSpace(doc.Scalars[key]) == "" {
			violations = append(violations, fmt.Sprintf("%s: required scalar %q must not be empty", filepath.ToSlash(path), key))
		}
	}

	for key, allowed := range schema.Enums {
		value := strings.TrimSpace(doc.Scalars[key])
		if value == "" {
			continue
		}
		if _, ok := allowed[value]; !ok {
			violations = append(violations, fmt.Sprintf("%s: invalid %s %q", filepath.ToSlash(path), key, value))
		}
	}

	if pathValue := strings.TrimSpace(doc.Scalars["path"]); pathValue != "" && pathValue != relPath {
		violations = append(violations, fmt.Sprintf("%s: path %q does not match directory %q", filepath.ToSlash(path), pathValue, relPath))
	}
	if nameValue := strings.TrimSpace(doc.Scalars["name"]); nameValue != "" && nameValue != relPath {
		violations = append(violations, fmt.Sprintf("%s: name %q should match module path %q", filepath.ToSlash(path), nameValue, relPath))
	}

	if strings.HasPrefix(relPath, "x/") {
		if layer := strings.TrimSpace(doc.Scalars["layer"]); layer != "" && layer != "extension" {
			violations = append(violations, fmt.Sprintf("%s: x/* modules must declare layer \"extension\"", filepath.ToSlash(path)))
		}
	} else if isStableRoot(relPath) {
		if layer := strings.TrimSpace(doc.Scalars["layer"]); layer != "" && layer != "stable" {
			violations = append(violations, fmt.Sprintf("%s: stable root modules must declare layer \"stable\"", filepath.ToSlash(path)))
		}
	}

	for _, docPath := range doc.Lists["doc_paths"] {
		targetPath := filepath.Join(repoRoot, filepath.FromSlash(docPath))
		if _, err := os.Stat(targetPath); err != nil {
			if os.IsNotExist(err) {
				violations = append(violations, fmt.Sprintf("%s: doc_paths target %q does not exist", filepath.ToSlash(path), docPath))
				continue
			}
			return nil, err
		}
	}

	return violations, nil
}

func parseManifest(path string) (manifestDoc, error) {
	file, err := os.Open(path)
	if err != nil {
		return manifestDoc{}, err
	}
	defer func() { _ = file.Close() }()

	doc := manifestDoc{
		Seen:       map[string]bool{},
		Scalars:    map[string]string{},
		ListCounts: map[string]int{},
		Lists:      map[string][]string{},
	}

	currentKey := ""
	scanner := NewLineScanner(file)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), " \t")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}
			currentKey = strings.TrimSpace(parts[0])
			doc.Seen[currentKey] = true
			value := strings.TrimSpace(parts[1])
			if value != "" {
				doc.Scalars[currentKey] = strings.Trim(value, "\"'")
			}
			continue
		}

		if currentKey == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "- ") {
			doc.ListCounts[currentKey]++
			value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
			value = strings.Trim(value, "\"'")
			if value != "" {
				doc.Lists[currentKey] = append(doc.Lists[currentKey], value)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return manifestDoc{}, err
	}
	return doc, nil
}

func isStableRoot(path string) bool {
	for _, root := range StableRoots() {
		if path == root {
			return true
		}
	}
	return false
}

// ReadModulePublicEntrypoints reads the public_entrypoints list from a module.yaml file.
// Returns nil if the file does not exist or declares no public_entrypoints.
func ReadModulePublicEntrypoints(manifestPath string) ([]string, error) {
	doc, err := parseManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	return doc.Lists["public_entrypoints"], nil
}

// ValidateStableBoundaryDeclarations checks that every stable root module.yaml
// declares a non-empty strict_boundary field.
func ValidateStableBoundaryDeclarations(repoRoot string) ([]string, error) {
	var violations []string
	for _, root := range StableRoots() {
		path := filepath.Join(repoRoot, root, "module.yaml")
		doc, err := parseManifest(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue // missing manifest is reported by FindMissingModuleManifests
			}
			return nil, err
		}
		if !doc.Seen["strict_boundary"] || strings.TrimSpace(doc.Scalars["strict_boundary"]) == "" {
			violations = append(violations, filepath.ToSlash(filepath.Join(root, "module.yaml"))+": stable root is missing strict_boundary declaration")
		}
	}
	sort.Strings(violations)
	return violations, nil
}

// extractSubordinatePackagePath extracts the package path from a
// subordinate_families list entry. Handles both plain string ("x/foo/bar")
// and YAML-object prefix ("package: x/foo/bar") formats.
func extractSubordinatePackagePath(raw string) string {
	raw = strings.TrimSpace(raw)
	if after, found := strings.CutPrefix(raw, "package:"); found {
		return strings.TrimSpace(after)
	}
	return raw
}

// ValidateXFamilyTaxonomy checks that:
//   - x/* primary families that coordinate subordinates declare subordinate_families
//   - x/* subordinate roots declare the exact canonical parent_family
//   - x/* packages that declare parent_family reference a recognized primary family
//   - each package listed in subordinate_families declares the correct parent_family
//   - each taxonomy-level non-canonical root is listed in its canonical parent's subordinate_families
func ValidateXFamilyTaxonomy(repoRoot string) ([]string, error) {
	taxonomy, err := ReadExtensionTaxonomy(repoRoot)
	if err != nil {
		return nil, err
	}

	primaryFamilies := map[string]struct{}{}
	familiesWithSubordinates := map[string]struct{}{}
	for _, canonicalRoot := range taxonomy.CanonicalRoots {
		primaryFamilies[canonicalRoot] = struct{}{}
		for _, roots := range taxonomy.RootsByFamily {
			if len(roots) <= 1 {
				continue
			}
			if len(roots) > 1 {
				for _, root := range roots {
					if root == canonicalRoot {
						familiesWithSubordinates[canonicalRoot] = struct{}{}
						break
					}
				}
			}
		}
	}

	// moduleData collects per-module info for cross-validation.
	type moduleData struct {
		path                 string
		parentFamily         string
		subordinates         []string // extracted package paths from subordinate_families
		hasSubordinatesField bool     // true when subordinate_families key is declared
	}
	collected := map[string]*moduleData{}

	var violations []string
	xDir := filepath.Join(repoRoot, "x")
	err = filepath.WalkDir(xDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "module.yaml" {
			return nil
		}
		doc, err := parseManifest(path)
		if err != nil {
			return err
		}

		pkg := strings.TrimSpace(doc.Scalars["path"])
		if pkg == "" {
			rel, err := filepath.Rel(repoRoot, filepath.Dir(path))
			if err != nil {
				return err
			}
			pkg = filepath.ToSlash(rel)
		}

		if _, ok := familiesWithSubordinates[pkg]; ok {
			if !doc.Seen["subordinate_families"] {
				violations = append(violations, pkg+"/module.yaml: primary family must declare subordinate_families")
			}
		}

		parentFamily := strings.TrimSpace(doc.Scalars["parent_family"])
		if expectedParent := taxonomy.RootToCanonical[pkg]; expectedParent != "" && expectedParent != pkg {
			if parentFamily == "" {
				violations = append(violations, pkg+"/module.yaml: subordinate family must declare parent_family "+strconv.Quote(expectedParent))
			} else if parentFamily != expectedParent {
				violations = append(violations, pkg+"/module.yaml: parent_family "+strconv.Quote(parentFamily)+" must be "+strconv.Quote(expectedParent))
			}
		}

		if parentFamily != "" {
			if _, ok := primaryFamilies[parentFamily]; !ok {
				violations = append(violations, pkg+"/module.yaml: parent_family "+strconv.Quote(parentFamily)+" is not a recognized primary x/* family")
			}
		}

		// Collect data for cross-validation passes below.
		md := &moduleData{
			path:                 pkg,
			parentFamily:         parentFamily,
			hasSubordinatesField: doc.Seen["subordinate_families"],
		}
		for _, raw := range doc.Lists["subordinate_families"] {
			if sub := extractSubordinatePackagePath(raw); sub != "" {
				md.subordinates = append(md.subordinates, sub)
			}
		}
		collected[pkg] = md
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	// Cross-validation pass 1: each explicit subordinate_families entry must
	// have its child module declare parent_family pointing to the canonical root.
	for pkg, md := range collected {
		canonicalOfPkg := taxonomy.RootToCanonical[pkg]
		if canonicalOfPkg == "" {
			canonicalOfPkg = pkg // pkg is itself a canonical root
		}
		for _, sub := range md.subordinates {
			childMd, ok := collected[sub]
			if !ok {
				violations = append(violations, pkg+"/module.yaml: subordinate_families lists "+strconv.Quote(sub)+" but "+sub+"/module.yaml was not found")
				continue
			}
			if childMd.parentFamily == "" {
				violations = append(violations, sub+"/module.yaml: listed as subordinate of "+pkg+" but does not declare parent_family "+strconv.Quote(canonicalOfPkg))
			} else if childMd.parentFamily != canonicalOfPkg {
				violations = append(violations, sub+"/module.yaml: parent_family "+strconv.Quote(childMd.parentFamily)+" does not match canonical root "+strconv.Quote(canonicalOfPkg)+" declared by "+pkg+"/module.yaml")
			}
		}
	}

	// Cross-validation pass 2: each taxonomy-level non-canonical root must be
	// listed in its canonical parent's subordinate_families. This only fires
	// when the parent has declared subordinate_families (if the field is absent
	// entirely, the "primary family must declare subordinate_families" check
	// above already covers that case).
	for pkg := range taxonomy.RootToCanonical {
		expectedParent := taxonomy.RootToCanonical[pkg]
		if expectedParent == "" || expectedParent == pkg {
			continue
		}
		parentMd, ok := collected[expectedParent]
		if !ok {
			continue
		}
		if !parentMd.hasSubordinatesField {
			continue // missing field already reported by the check above
		}
		found := false
		for _, sub := range parentMd.subordinates {
			if sub == pkg {
				found = true
				break
			}
		}
		if !found {
			violations = append(violations, expectedParent+"/module.yaml: taxonomy lists "+strconv.Quote(pkg)+" as subordinate but it is not in subordinate_families")
		}
	}

	sort.Strings(violations)
	return violations, nil
}
