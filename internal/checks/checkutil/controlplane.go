package checkutil

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type manifestSchema struct {
	Required []string
	Enums    map[string]map[string]struct{}
	Limits   map[string]int
}

type dependencyModuleRule struct {
	Path            string
	Allow           []string
	DeclaredCallers []string
	Deny            []string
}

type dependencyRulesDoc struct {
	ModulePath              string
	Modules                 map[string]dependencyModuleRule
	ForbiddenPaths          []string
	ForbiddenImportPatterns []string
}

type extensionTaxonomyDoc struct {
	CanonicalRoots  []string
	CoveredRoots    map[string]struct{}
	RootsByFamily   map[string][]string
	RootToCanonical map[string]string
}

type taskRoutingEntry struct {
	Name      string
	StartWith []string
}

func readRepoModulePath(repoRoot string) (string, error) {
	path := filepath.Join(repoRoot, "specs", "repo.yaml")
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	scanner := NewLineScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "module:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "module:"))
		value = strings.Trim(value, "\"'")
		if value != "" {
			return value, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return modulePath, nil
}

func ReadManifestSchema(repoRoot string) (manifestSchema, error) {
	path := filepath.Join(repoRoot, "specs", "module-manifest.schema.yaml")
	file, err := os.Open(path)
	if err != nil {
		return manifestSchema{}, err
	}
	defer func() { _ = file.Close() }()

	out := manifestSchema{
		Enums:  map[string]map[string]struct{}{},
		Limits: map[string]int{},
	}

	section := ""
	currentEnum := ""
	scanner := NewLineScanner(file)
	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), " \t")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		switch {
		case indent == 0 && trimmed == "required:":
			section = "required"
			currentEnum = ""
			continue
		case indent == 0 && trimmed == "enums:":
			section = "enums"
			currentEnum = ""
			continue
		case indent == 0 && trimmed == "limits:":
			section = "limits"
			currentEnum = ""
			continue
		case indent == 0:
			section = ""
			currentEnum = ""
		}

		switch section {
		case "required":
			if indent >= 2 && strings.HasPrefix(trimmed, "- ") {
				value := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
				value = strings.Trim(value, "\"'")
				if value != "" {
					out.Required = append(out.Required, value)
				}
			}
		case "enums":
			switch {
			case indent == 2 && strings.HasSuffix(trimmed, ":"):
				currentEnum = strings.TrimSuffix(trimmed, ":")
				out.Enums[currentEnum] = map[string]struct{}{}
			case indent >= 4 && currentEnum != "" && strings.HasPrefix(trimmed, "- "):
				value := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
				value = strings.Trim(value, "\"'")
				if value != "" {
					out.Enums[currentEnum][value] = struct{}{}
				}
			}
		case "limits":
			if indent != 2 {
				continue
			}
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			key = strings.TrimSuffix(key, "_max")
			value := strings.TrimSpace(parts[1])
			n, err := strconv.Atoi(value)
			if err != nil {
				return manifestSchema{}, err
			}
			out.Limits[key] = n
		}
	}
	if err := scanner.Err(); err != nil {
		return manifestSchema{}, err
	}

	return out, nil
}

func ReadDependencyRules(repoRoot string) (dependencyRulesDoc, error) {
	modulePath, err := readRepoModulePath(repoRoot)
	if err != nil {
		return dependencyRulesDoc{}, err
	}

	path := filepath.Join(repoRoot, "specs", "dependency-rules.yaml")
	file, err := os.Open(path)
	if err != nil {
		return dependencyRulesDoc{}, err
	}
	defer func() { _ = file.Close() }()

	out := dependencyRulesDoc{
		ModulePath: modulePath,
		Modules:    map[string]dependencyModuleRule{},
	}

	section := ""
	currentModule := ""
	inAllow := false
	inDeclaredCallers := false
	inDeny := false
	inForbiddenPaths := false
	inForbiddenImportPatterns := false

	scanner := NewLineScanner(file)
	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), " \t")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		switch {
		case indent == 0 && trimmed == "modules:":
			section = "modules"
			currentModule = ""
			inAllow = false
			inDeclaredCallers = false
			inDeny = false
			inForbiddenPaths = false
			inForbiddenImportPatterns = false
			continue
		case indent == 0 && trimmed == "special_rules:":
			section = "special_rules"
			currentModule = ""
			inAllow = false
			inDeclaredCallers = false
			inDeny = false
			inForbiddenPaths = false
			inForbiddenImportPatterns = false
			continue
		case indent == 0:
			section = ""
			currentModule = ""
			inAllow = false
			inDeclaredCallers = false
			inDeny = false
			inForbiddenPaths = false
			inForbiddenImportPatterns = false
		}

		switch section {
		case "modules":
			switch {
			case indent == 2 && strings.HasSuffix(trimmed, ":"):
				currentModule = strings.TrimSuffix(trimmed, ":")
				out.Modules[currentModule] = dependencyModuleRule{}
				inAllow = false
				inDeclaredCallers = false
				inDeny = false
			case indent == 4 && strings.HasPrefix(trimmed, "path:"):
				rule := out.Modules[currentModule]
				rule.Path = cleanScalar(strings.TrimPrefix(trimmed, "path:"))
				out.Modules[currentModule] = rule
				inAllow = false
				inDeclaredCallers = false
				inDeny = false
			case indent == 4 && trimmed == "allow:":
				inAllow = true
				inDeclaredCallers = false
				inDeny = false
			case indent == 4 && trimmed == "declared_callers:":
				inAllow = false
				inDeclaredCallers = true
				inDeny = false
			case indent == 4 && trimmed == "deny:":
				inAllow = false
				inDeclaredCallers = false
				inDeny = true
			case indent == 4:
				inAllow = false
				inDeclaredCallers = false
				inDeny = false
			case indent >= 6 && inAllow && strings.HasPrefix(trimmed, "- "):
				rule := out.Modules[currentModule]
				rule.Allow = append(rule.Allow, cleanScalar(strings.TrimPrefix(trimmed, "- ")))
				out.Modules[currentModule] = rule
			case indent >= 6 && inDeclaredCallers && strings.HasPrefix(trimmed, "- "):
				rule := out.Modules[currentModule]
				rule.DeclaredCallers = append(rule.DeclaredCallers, cleanScalar(strings.TrimPrefix(trimmed, "- ")))
				out.Modules[currentModule] = rule
			case indent >= 6 && inDeny && strings.HasPrefix(trimmed, "- "):
				rule := out.Modules[currentModule]
				rule.Deny = append(rule.Deny, cleanScalar(strings.TrimPrefix(trimmed, "- ")))
				out.Modules[currentModule] = rule
			}
		case "special_rules":
			switch {
			case indent == 2 && trimmed == "forbidden_paths:":
				inForbiddenPaths = true
				inForbiddenImportPatterns = false
			case indent == 2 && trimmed == "forbidden_import_patterns:":
				inForbiddenPaths = false
				inForbiddenImportPatterns = true
			case indent == 2:
				inForbiddenPaths = false
				inForbiddenImportPatterns = false
			case indent >= 4 && inForbiddenPaths && strings.HasPrefix(trimmed, "- "):
				out.ForbiddenPaths = append(out.ForbiddenPaths, cleanScalar(strings.TrimPrefix(trimmed, "- ")))
			case indent >= 4 && inForbiddenImportPatterns && strings.HasPrefix(trimmed, "- "):
				out.ForbiddenImportPatterns = append(out.ForbiddenImportPatterns, cleanScalar(strings.TrimPrefix(trimmed, "- ")))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return dependencyRulesDoc{}, err
	}

	return out, nil
}

func ReadExtensionTaxonomy(repoRoot string) (extensionTaxonomyDoc, error) {
	path := filepath.Join(repoRoot, "specs", "extension-taxonomy.yaml")
	file, err := os.Open(path)
	if err != nil {
		return extensionTaxonomyDoc{}, err
	}
	defer func() { _ = file.Close() }()

	out := extensionTaxonomyDoc{
		CoveredRoots:    map[string]struct{}{},
		RootsByFamily:   map[string][]string{},
		RootToCanonical: map[string]string{},
	}

	inFamilies := false
	currentFamily := ""
	currentCanonical := ""
	inRoots := false
	scanner := NewLineScanner(file)
	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), " \t")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		switch {
		case indent == 0 && trimmed == "families:":
			inFamilies = true
			currentFamily = ""
			currentCanonical = ""
			inRoots = false
			continue
		case indent == 0:
			inFamilies = false
			currentFamily = ""
			currentCanonical = ""
			inRoots = false
		}

		if !inFamilies {
			continue
		}

		switch {
		case indent == 2 && strings.HasSuffix(trimmed, ":"):
			currentFamily = strings.TrimSuffix(trimmed, ":")
			currentCanonical = ""
			inRoots = false
			continue
		case indent == 2:
			currentFamily = ""
			currentCanonical = ""
			inRoots = false
		}

		if currentFamily == "" {
			continue
		}

		switch {
		case indent == 4 && strings.HasPrefix(trimmed, "canonical_root:"):
			value := cleanScalar(strings.TrimPrefix(trimmed, "canonical_root:"))
			if value != "" {
				currentCanonical = value
				out.CanonicalRoots = append(out.CanonicalRoots, value)
				out.CoveredRoots[value] = struct{}{}
				out.RootToCanonical[value] = value
			}
			inRoots = false
		case indent == 4 && trimmed == "roots:":
			inRoots = true
		case indent == 4:
			inRoots = false
		case indent >= 6 && inRoots && strings.HasPrefix(trimmed, "- "):
			value := cleanScalar(strings.TrimPrefix(trimmed, "- "))
			if value == "" {
				continue
			}
			out.RootsByFamily[currentFamily] = append(out.RootsByFamily[currentFamily], value)
			out.CoveredRoots[value] = struct{}{}
			if currentCanonical != "" {
				out.RootToCanonical[value] = currentCanonical
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return extensionTaxonomyDoc{}, err
	}

	sort.Strings(out.CanonicalRoots)
	return out, nil
}

func ReadTaskRouting(repoRoot string) (map[string]taskRoutingEntry, error) {
	path := filepath.Join(repoRoot, "specs", "task-routing.yaml")
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	out := map[string]taskRoutingEntry{}
	inTasks := false
	currentTask := ""
	inStartWith := false

	scanner := NewLineScanner(file)
	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), " \t")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		switch {
		case indent == 0 && trimmed == "tasks:":
			inTasks = true
			currentTask = ""
			inStartWith = false
			continue
		case indent == 0:
			inTasks = false
			currentTask = ""
			inStartWith = false
		}

		if !inTasks {
			continue
		}

		switch {
		case indent == 2 && strings.HasSuffix(trimmed, ":"):
			currentTask = strings.TrimSuffix(trimmed, ":")
			out[currentTask] = taskRoutingEntry{Name: currentTask}
			inStartWith = false
			continue
		case indent == 2:
			currentTask = ""
			inStartWith = false
		}

		if currentTask == "" {
			continue
		}

		switch {
		case indent == 4 && trimmed == "start_with:":
			inStartWith = true
		case indent == 4:
			inStartWith = false
		case indent >= 6 && inStartWith && strings.HasPrefix(trimmed, "- "):
			entry := out[currentTask]
			entry.StartWith = append(entry.StartWith, cleanScalar(strings.TrimPrefix(trimmed, "- ")))
			out[currentTask] = entry
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return out, nil
}

func cleanScalar(value string) string {
	value = strings.TrimSpace(value)
	return strings.Trim(value, "\"'")
}

func matchesRepoPattern(relPath, pattern string) bool {
	pattern = cleanScalar(pattern)
	if pattern == "" || relPath == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/**") {
		base := strings.TrimSuffix(pattern, "/**")
		return relPath == base || strings.HasPrefix(relPath, base+"/")
	}
	return relPath == pattern || strings.HasPrefix(relPath, pattern+"/")
}

func matchesForbiddenImportPattern(importPath, relPath, pattern, repoModulePath string) bool {
	pattern = cleanScalar(pattern)
	if pattern == "" {
		return false
	}
	if pattern == repoModulePath {
		return importPath == pattern
	}
	if strings.HasPrefix(pattern, repoModulePath+"/") {
		return matchesRepoPattern(relPath, strings.TrimPrefix(pattern, repoModulePath+"/"))
	}
	return matchesRepoPattern(relPath, pattern)
}
