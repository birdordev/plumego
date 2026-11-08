// Package gateprofile loads specs/gate-profiles.yaml and selects the minimal
// sufficient gate profile for a set of changed files.
package gateprofile

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Profile is one named gate configuration.
type Profile struct {
	Description     string   `yaml:"description"`
	GoGatesRequired bool     `yaml:"go_gates_required"`
	Commands        []string `yaml:"commands"`
	Notes           string   `yaml:"notes"`
}

// SelectionRule maps a set of conditions to a profile name.
type SelectionRule struct {
	ID               string   `yaml:"id"`
	Description      string   `yaml:"description"`
	PathPatterns     []string `yaml:"path_patterns"`
	ExcludeGoChanges bool     `yaml:"exclude_go_changes"`
	Modules          []string `yaml:"modules"`
	Use              string   `yaml:"use"`
}

// Doc is the decoded gate-profiles.yaml.
type Doc struct {
	Version        int                `yaml:"version"`
	Profiles       map[string]Profile `yaml:"profiles"`
	SelectionRules []SelectionRule    `yaml:"selection_rules"`
}

// Load reads gate-profiles.yaml from repoRoot/specs/.
func Load(repoRoot string) (*Doc, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "specs", "gate-profiles.yaml"))
	if err != nil {
		return nil, err
	}
	var doc Doc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// Select returns the minimal sufficient profile name and profile for the given
// inputs. primaryModule is the detected owning module (e.g. "middleware");
// multiModule is true when changes span more than one recognized module.
// The selection_rules in the doc are evaluated in order; the first match wins.
// Stable-root rules are eligible only for single-module changes.
func Select(doc *Doc, changedFiles []string, primaryModule string, multiModule bool) (string, Profile) {
	hasGo := false
	for _, f := range changedFiles {
		if strings.HasSuffix(f, ".go") {
			hasGo = true
			break
		}
	}

	for _, rule := range doc.SelectionRules {
		switch rule.ID {
		case "docs_only":
			if !hasGo && rule.ExcludeGoChanges && allMatch(changedFiles, rule.PathPatterns) {
				return applyRule(doc, rule.Use)
			}
		case "rms_stable", "other_stable":
			// A primary module cannot represent the full scope of a multi-module diff.
			if multiModule {
				continue
			}
			for _, m := range rule.Modules {
				if m == primaryModule {
					return applyRule(doc, rule.Use)
				}
			}
		case "cross_module":
			if multiModule {
				return applyRule(doc, rule.Use)
			}
		case "default":
			return applyRule(doc, rule.Use)
		}
	}
	return "single_module_behavior", fallbackProfile(primaryModule)
}

func applyRule(doc *Doc, profileName string) (string, Profile) {
	if p, ok := doc.Profiles[profileName]; ok {
		return profileName, p
	}
	return profileName, Profile{}
}

// allMatch returns true only when every file in files matches at least one pattern.
func allMatch(files, patterns []string) bool {
	for _, f := range files {
		matched := false
		for _, pat := range patterns {
			if matchGlob(pat, f) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// matchGlob handles "dir/**", "*.ext", and exact patterns.
func matchGlob(pattern, path string) bool {
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return strings.HasPrefix(path, prefix+"/") || path == prefix
	}
	if strings.HasPrefix(pattern, "*.") {
		return strings.HasSuffix(path, strings.TrimPrefix(pattern, "*"))
	}
	return path == pattern
}

func fallbackProfile(module string) Profile {
	// reference/* live in nested go.mod directories that cannot
	// be tested via "./reference/..." from the root module. Use "go -C <dir>" to
	// change into the module directory before running — supported since Go 1.21.
	if strings.Contains(module, "/") {
		return Profile{
			Description: "nested module (fallback)",
			Commands: []string{
				"go test -race -timeout 60s -C " + module + " ./...",
				"go test -timeout 20s -C " + module + " ./...",
				"go vet -C " + module + " ./...",
			},
		}
	}
	arg := "./" + module + "/..."
	if module == "" {
		arg = "./..."
	}
	return Profile{
		Description: "single module behavior (fallback)",
		Commands: []string{
			"go test -race -timeout 60s " + arg,
			"go test -timeout 20s " + arg,
			"go vet " + arg,
		},
	}
}
