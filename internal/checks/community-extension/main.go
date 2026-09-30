package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spcent/plumego/internal/checks/checkutil"
)

const (
	manifestFileName = "community-extension.yaml"
	schemaRelPath    = "specs/community-extension.schema.yaml"
)

type communitySchema struct {
	Required                 []string
	StatusEnum               []string
	HandlerShapeConst        string
	ListMinLengths           map[string]int
	BooleanTrue              []string
	StableRootForbiddenPaths []string
}

type communityExtension struct {
	Name              string
	ModulePath        string
	Status            string
	HandlerShape      string
	TestCommands      []string
	ForbiddenImports  []string
	NoInitSideEffects *bool
	NoGlobals         *bool
	Owner             string
}

func main() {
	code := run(os.Args[1:], mustGetwd(), os.Stderr)
	os.Exit(code)
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func run(args []string, cwd string, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: community-extension <directory>")
		return 1
	}

	repoRoot, err := findRepoRoot(cwd)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "resolve repo root: %v\n", err)
		return 1
	}
	violations, err := validateDirectory(filepath.Join(repoRoot, schemaRelPath), args[0])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "validate community extension: %v\n", err)
		return 1
	}
	if len(violations) > 0 {
		_, _ = fmt.Fprint(stderr, checkutil.FormatViolations("community-extension", violations))
		return 1
	}
	return 0
}

func validateDirectory(schemaPath string, dir string) ([]string, error) {
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("read schema %s: %w", schemaPath, err)
	}
	schema, err := parseSchema(schemaBytes)
	if err != nil {
		return nil, fmt.Errorf("parse schema %s: %w", schemaPath, err)
	}

	manifestPath := filepath.Join(dir, manifestFileName)
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", manifestPath, err)
	}
	manifest, present, err := parseManifest(manifestBytes)
	if err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", manifestPath, err)
	}
	return validateManifest(schema, manifest, present), nil
}

func validateManifest(schema communitySchema, manifest communityExtension, present map[string]bool) []string {
	var violations []string
	for _, field := range schema.Required {
		if !present[field] {
			violations = append(violations, fmt.Sprintf("missing required field %q", field))
			continue
		}
		if isStringField(field) && stringField(manifest, field) == "" {
			violations = append(violations, fmt.Sprintf("field %q must be a non-empty string", field))
		}
	}

	if manifest.Status != "" && !contains(schema.StatusEnum, manifest.Status) {
		violations = append(violations, fmt.Sprintf("status %q is invalid; allowed values: %s", manifest.Status, strings.Join(schema.StatusEnum, ", ")))
	}
	if manifest.HandlerShape != "" && manifest.HandlerShape != schema.HandlerShapeConst {
		violations = append(violations, fmt.Sprintf("handler_shape must be %q", schema.HandlerShapeConst))
	}
	for field, minLen := range schema.ListMinLengths {
		if listLength(manifest, field) < minLen {
			violations = append(violations, fmt.Sprintf("%s must contain at least %d item(s)", field, minLen))
		}
	}
	for _, field := range schema.BooleanTrue {
		if !boolFieldIsTrue(manifest, field) {
			violations = append(violations, fmt.Sprintf("%s must be true", field))
		}
	}
	for _, stablePath := range schema.StableRootForbiddenPaths {
		if !contains(manifest.ForbiddenImports, stablePath) {
			violations = append(violations, fmt.Sprintf("forbidden_imports must include %s", stablePath))
		}
	}

	sort.Strings(violations)
	return violations
}

func parseSchema(data []byte) (communitySchema, error) {
	scalars, lists, maps, err := parseSimpleYAML(data)
	if err != nil {
		return communitySchema{}, err
	}
	schema := communitySchema{
		Required:                 lists["required"],
		StatusEnum:               lists["status_enum"],
		HandlerShapeConst:        scalars["handler_shape_const"],
		ListMinLengths:           map[string]int{},
		BooleanTrue:              lists["boolean_true"],
		StableRootForbiddenPaths: lists["stable_root_forbidden_imports"],
	}
	for field, value := range maps["list_min_lengths"] {
		minLen, err := strconv.Atoi(value)
		if err != nil {
			return communitySchema{}, fmt.Errorf("list_min_lengths.%s must be an integer", field)
		}
		schema.ListMinLengths[field] = minLen
	}
	return schema, nil
}

func parseManifest(data []byte) (communityExtension, map[string]bool, error) {
	scalars, lists, _, err := parseSimpleYAML(data)
	if err != nil {
		return communityExtension{}, nil, err
	}
	manifest := communityExtension{
		Name:             scalars["name"],
		ModulePath:       scalars["module_path"],
		Status:           scalars["status"],
		HandlerShape:     scalars["handler_shape"],
		TestCommands:     lists["test_commands"],
		ForbiddenImports: lists["forbidden_imports"],
		Owner:            scalars["owner"],
	}
	if value, ok := scalars["no_init_side_effects"]; ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return communityExtension{}, nil, fmt.Errorf("no_init_side_effects must be boolean")
		}
		manifest.NoInitSideEffects = &parsed
	}
	if value, ok := scalars["no_globals"]; ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return communityExtension{}, nil, fmt.Errorf("no_globals must be boolean")
		}
		manifest.NoGlobals = &parsed
	}

	present := map[string]bool{}
	for key := range scalars {
		present[key] = true
	}
	for key := range lists {
		present[key] = true
	}
	return manifest, present, nil
}

func parseSimpleYAML(data []byte) (map[string]string, map[string][]string, map[string]map[string]string, error) {
	scalars := map[string]string{}
	lists := map[string][]string{}
	maps := map[string]map[string]string{}

	var section string
	scanner := checkutil.NewLineScanner(bytes.NewReader(data))
	for scanner.Scan() {
		raw := stripComment(scanner.Text())
		if strings.TrimSpace(raw) == "" {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		trimmed := strings.TrimSpace(raw)

		if indent == 0 {
			key, value, ok := strings.Cut(trimmed, ":")
			if !ok {
				return nil, nil, nil, fmt.Errorf("invalid top-level line %q", trimmed)
			}
			section = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if value != "" {
				scalars[section] = yamlScalar(value)
				section = ""
			}
			continue
		}

		if section == "" {
			return nil, nil, nil, fmt.Errorf("indented line outside section %q", trimmed)
		}
		if strings.HasPrefix(trimmed, "- ") {
			lists[section] = append(lists[section], yamlScalar(strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))))
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			return nil, nil, nil, fmt.Errorf("invalid map line %q", trimmed)
		}
		if maps[section] == nil {
			maps[section] = map[string]string{}
		}
		maps[section][strings.TrimSpace(key)] = yamlScalar(strings.TrimSpace(value))
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, nil, err
	}
	return scalars, lists, maps, nil
}

func stripComment(line string) string {
	inSingle := false
	inDouble := false
	for i, r := range line {
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble {
				return strings.TrimRight(line[:i], " \t")
			}
		}
	}
	return strings.TrimRight(line, " \t")
}

func yamlScalar(value string) string {
	return strings.Trim(strings.TrimSpace(value), "\"'")
}

func findRepoRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, schemaRelPath)); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repository root from %s", start)
		}
		dir = parent
	}
}

func isStringField(field string) bool {
	switch field {
	case "name", "module_path", "status", "handler_shape", "owner":
		return true
	default:
		return false
	}
}

func stringField(manifest communityExtension, field string) string {
	switch field {
	case "name":
		return manifest.Name
	case "module_path":
		return manifest.ModulePath
	case "status":
		return manifest.Status
	case "handler_shape":
		return manifest.HandlerShape
	case "owner":
		return manifest.Owner
	default:
		return ""
	}
}

func listLength(manifest communityExtension, field string) int {
	switch field {
	case "test_commands":
		return len(manifest.TestCommands)
	case "forbidden_imports":
		return len(manifest.ForbiddenImports)
	default:
		return 0
	}
}

func boolFieldIsTrue(manifest communityExtension, field string) bool {
	switch field {
	case "no_init_side_effects":
		return manifest.NoInitSideEffects != nil && *manifest.NoInitSideEffects
	case "no_globals":
		return manifest.NoGlobals != nil && *manifest.NoGlobals
	default:
		return false
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
