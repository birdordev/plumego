package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spcent/plumego/internal/checks/checkutil"
)

type inventoryEntry struct {
	ID          string
	Category    string
	Status      string
	Owner       string
	Decision    string
	Replacement string
	Paths       []string
}

type marker struct {
	Path string
	Line int
	Text string
}

var skippedDirs = map[string]struct{}{
	".git":         {},
	".next":        {},
	"gatus":        {},
	"node_modules": {},
	"output":       {},
	"vendor":       {},
	"website":      {},
}

func main() {
	strict := flag.Bool("strict", false, "exit non-zero when unregistered or stale inventory markers are found")
	report := flag.Bool("report", false, "print registered marker inventory status")
	flag.Parse()

	repoRoot, err := os.Getwd()
	if err != nil {
		failf("resolve working directory: %v", err)
	}

	entries, err := readInventory(filepath.Join(repoRoot, "specs", "deprecation-inventory.yaml"))
	if err != nil {
		failf("read deprecation inventory: %v", err)
	}
	markers, err := findMarkers(repoRoot)
	if err != nil {
		failf("scan deprecation markers: %v", err)
	}

	warnings := inventoryWarnings(entries, markers)
	if *report {
		printReport(entries, markers)
	}
	if len(warnings) == 0 {
		return
	}

	fmt.Fprintln(os.Stderr, "deprecation-inventory warnings:")
	for _, warning := range warnings {
		fmt.Fprintf(os.Stderr, "- %s\n", warning)
	}
	if *strict {
		os.Exit(1)
	}
}

func readInventory(path string) ([]inventoryEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var entries []inventoryEntry
	var current *inventoryEntry
	inEntries := false
	inPaths := false
	scanner := checkutil.NewLineScanner(file)
	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), " \t")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))

		if indent == 0 && trimmed == "entries:" {
			inEntries = true
			continue
		}
		if !inEntries {
			continue
		}
		if indent == 0 {
			break
		}
		if indent == 2 && strings.HasPrefix(trimmed, "- id:") {
			if current != nil {
				entries = append(entries, *current)
			}
			current = &inventoryEntry{ID: yamlScalar(trimmed)}
			inPaths = false
			continue
		}
		if current == nil {
			continue
		}
		if indent == 4 && strings.HasPrefix(trimmed, "status:") {
			current.Status = yamlScalar(trimmed)
			inPaths = false
			continue
		}
		if indent == 4 && strings.HasPrefix(trimmed, "category:") {
			current.Category = yamlScalar(trimmed)
			inPaths = false
			continue
		}
		if indent == 4 && strings.HasPrefix(trimmed, "owner:") {
			current.Owner = yamlScalar(trimmed)
			inPaths = false
			continue
		}
		if indent == 4 && strings.HasPrefix(trimmed, "decision:") {
			current.Decision = yamlScalar(trimmed)
			inPaths = false
			continue
		}
		if indent == 4 && strings.HasPrefix(trimmed, "replacement:") {
			current.Replacement = yamlScalar(trimmed)
			inPaths = false
			continue
		}
		if indent == 4 && trimmed == "paths:" {
			inPaths = true
			continue
		}
		if indent == 4 {
			inPaths = false
		}
		if inPaths && indent == 6 && strings.HasPrefix(trimmed, "- ") {
			path := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			path = strings.Trim(path, "\"'")
			if path != "" {
				current.Paths = append(current.Paths, filepath.ToSlash(path))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if current != nil {
		entries = append(entries, *current)
	}
	return entries, nil
}

func findMarkers(repoRoot string) ([]marker, error) {
	var markers []marker
	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if _, ok := skippedDirs[name]; ok {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "internal/checks/deprecation-inventory/") {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()

		scanner := checkutil.NewLineScanner(file)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if containsMarker(line) {
				markers = append(markers, marker{Path: rel, Line: lineNo, Text: strings.TrimSpace(line)})
			}
		}
		return scanner.Err()
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(markers, func(i, j int) bool {
		if markers[i].Path == markers[j].Path {
			return markers[i].Line < markers[j].Line
		}
		return markers[i].Path < markers[j].Path
	})
	return markers, nil
}

func inventoryWarnings(entries []inventoryEntry, markers []marker) []string {
	allowed := map[string][]inventoryEntry{}
	for _, entry := range entries {
		for _, path := range entry.Paths {
			allowed[path] = append(allowed[path], entry)
		}
	}

	var warnings []string
	seenPath := map[string]bool{}
	for _, entry := range entries {
		if entry.Category == "" {
			warnings = append(warnings, fmt.Sprintf("inventory entry %s is missing category", entry.ID))
		}
		if entry.Status == "" {
			warnings = append(warnings, fmt.Sprintf("inventory entry %s is missing status", entry.ID))
		}
		if entry.Status == "decision_required" {
			warnings = append(warnings, fmt.Sprintf("inventory entry %s still requires a keep/remove decision", entry.ID))
		}
		if entry.Owner == "" {
			warnings = append(warnings, fmt.Sprintf("inventory entry %s is missing owner", entry.ID))
		}
		if entry.Decision == "" {
			warnings = append(warnings, fmt.Sprintf("inventory entry %s is missing decision", entry.ID))
		}
		if entry.Replacement == "" {
			warnings = append(warnings, fmt.Sprintf("inventory entry %s is missing replacement", entry.ID))
		}
	}
	for _, marker := range markers {
		entriesForPath := allowed[marker.Path]
		if len(entriesForPath) == 0 {
			warnings = append(warnings, fmt.Sprintf("unregistered marker at %s:%d: %s", marker.Path, marker.Line, marker.Text))
			continue
		}
		seenPath[marker.Path] = true
		for _, entry := range entriesForPath {
			if entry.Status == "removed" {
				warnings = append(warnings, fmt.Sprintf("removed inventory entry %s still has marker at %s:%d", entry.ID, marker.Path, marker.Line))
			}
		}
	}
	for _, entry := range entries {
		for _, path := range entry.Paths {
			if !seenPath[path] && entry.Status != "removed" {
				warnings = append(warnings, fmt.Sprintf("inventory entry %s references %s but no marker was found", entry.ID, path))
			}
		}
	}
	sort.Strings(warnings)
	return warnings
}

func printReport(entries []inventoryEntry, markers []marker) {
	fmt.Printf("inventory_entries=%d markers=%d\n", len(entries), len(markers))
	for _, entry := range entries {
		fmt.Printf("%s\tcategory=%s\tstatus=%s\towner=%s\treplacement=%s\tpaths=%d\n",
			entry.ID,
			entry.Category,
			entry.Status,
			entry.Owner,
			entry.Replacement,
			len(entry.Paths),
		)
	}
}

func containsMarker(line string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.Contains(line, "TODO:") || strings.Contains(line, "// TODO") ||
		strings.Contains(line, "FIXME") || strings.Contains(line, "HACK") {
		return true
	}
	if strings.HasPrefix(trimmed, "type ") && strings.Contains(trimmed, " = ") {
		return true
	}

	lower := strings.ToLower(line)
	for _, needle := range []string{
		"Deprecated:",
		"deprecated",
		"backward compatibility",
		"backwards compatibility",
		"not implemented",
	} {
		if strings.Contains(lower, strings.ToLower(needle)) {
			return true
		}
	}
	if strings.HasPrefix(trimmed, "//") {
		for _, needle := range []string{
			"compatibility alias",
			"legacy alias",
			"alias for",
			"is an alias",
			"type alias",
			"re-exports",
		} {
			if strings.Contains(lower, needle) {
				return true
			}
		}
	}
	for _, needle := range []string{
		"compatibility alias",
		"legacy alias",
		"alias for",
		"is an alias",
		"re-exports",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

func yamlScalar(line string) string {
	_, value, ok := strings.Cut(line, ":")
	if !ok {
		return ""
	}
	return strings.Trim(strings.TrimSpace(value), "\"'")
}

func failf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
