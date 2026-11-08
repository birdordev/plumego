package commands

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/birdordev/plumego/cmd/plumego/internal/agentmanifest"
	"gopkg.in/yaml.v3"
)

// runAgentsRoute is a routing oracle: given a task type or file path it returns
// the owning module, the docs to read first, paths to avoid, and the suggested
// change recipe — so agents can orient without reading multiple spec files.
func runAgentsRoute(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("agents route", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	task := fs.String("task", "", "Task type to route (e.g. http_endpoint, add-middleware, bugfix_triage)")
	path := fs.String("path", "", "Repo-relative file or directory to identify module from")
	dir := fs.String("dir", ".", "Repository root directory")

	positionals, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}
	if len(positionals) > 0 {
		return ctx.Out.Error(fmt.Sprintf("unexpected arguments: %v", positionals), 1)
	}
	if *task == "" && *path == "" {
		return ctx.Out.Error("--task or --path is required", 1, map[string]any{
			"hint": "plumego agents route --task <type>  OR  plumego agents route --path <file>",
		})
	}

	repoRoot, err := agentmanifest.FindRepoRoot(*dir)
	if err != nil {
		return ctx.Out.Error(err.Error(), 1)
	}

	payload := map[string]any{}

	// Identify module from --path.
	if *path != "" {
		mod := moduleFromFilePath(*path)
		payload["detected_module"] = mod
		if mod != "" {
			payload["module_manifest"] = mod + "/module.yaml"
		}
	}

	// Look up routing entry for --task.
	if *task != "" {
		routing, loadErr := loadTaskRoutingEntries(repoRoot)
		if loadErr != nil {
			return ctx.Out.Error(fmt.Sprintf("load task-routing.yaml: %v", loadErr), 1)
		}

		key, entry, ok := resolveTaskKey(*task, routing)
		if !ok {
			available := make([]string, 0, len(routing))
			for k := range routing {
				available = append(available, k)
			}
			sort.Strings(available)
			return ctx.Out.Error(fmt.Sprintf("unknown task type: %q", *task), 1, map[string]any{
				"available_tasks": available,
				"hint":            "Use one of the task types listed, or check specs/task-routing.yaml",
			})
		}

		payload["task"] = key
		payload["intent"] = entry.Intent
		if len(entry.StartWith) > 0 {
			payload["start_with"] = entry.StartWith
		}
		if len(entry.Avoid) > 0 {
			payload["avoid"] = entry.Avoid
		}
		if recipe := suggestRecipe(*task); recipe != "" {
			payload["suggested_recipe"] = "specs/change-recipes/" + recipe + ".yaml"
		}
	}

	label := strings.TrimSpace(*task + " " + *path)
	return ctx.Out.Success(fmt.Sprintf("routing: %s", label), payload)
}

// runAgentsListModules enumerates all modules declared in specs/repo.yaml,
// loads their module.yaml, and returns a structured list for agent orientation.
func runAgentsListModules(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("agents list-modules", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	dir := fs.String("dir", ".", "Repository root directory")
	layer := fs.String("layer", "", "Filter by layer: stable, extension, tooling, reference")

	positionals, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}
	if len(positionals) > 0 {
		return ctx.Out.Error(fmt.Sprintf("unexpected arguments: %v", positionals), 1)
	}

	repoRoot, err := agentmanifest.FindRepoRoot(*dir)
	if err != nil {
		return ctx.Out.Error(err.Error(), 1)
	}

	allPaths, err := loadRepoModulePaths(repoRoot)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("load repo.yaml: %v", err), 1)
	}

	type entry struct {
		Path    string `yaml:"path" json:"path"`
		Layer   string `yaml:"layer" json:"layer"`
		Status  string `yaml:"status,omitempty" json:"status,omitempty"`
		Risk    string `yaml:"risk,omitempty" json:"risk,omitempty"`
		Owner   string `yaml:"owner,omitempty" json:"owner,omitempty"`
		Summary string `yaml:"summary,omitempty" json:"summary,omitempty"`
	}

	var entries []entry
	for _, mp := range allPaths {
		if *layer != "" && mp.layer != *layer {
			continue
		}
		m, loadErr := agentmanifest.Load(repoRoot, mp.path)
		if loadErr != nil {
			entries = append(entries, entry{Path: mp.path, Layer: mp.layer})
			continue
		}
		entries = append(entries, entry{
			Path:    mp.path,
			Layer:   m.Layer,
			Status:  m.Status,
			Risk:    m.Risk,
			Owner:   m.Owner,
			Summary: m.Summary,
		})
	}

	return ctx.Out.Success(fmt.Sprintf("%d modules", len(entries)), map[string]any{
		"modules": entries,
	})
}

// ─── helpers ──────────────────────────────────────────────────────────────────

type routingEntry struct {
	Intent    string   `yaml:"intent"`
	StartWith []string `yaml:"start_with"`
	Avoid     []string `yaml:"avoid"`
}

func loadTaskRoutingEntries(repoRoot string) (map[string]routingEntry, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "specs", "task-routing.yaml"))
	if err != nil {
		return nil, err
	}
	var doc struct {
		Tasks map[string]routingEntry `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc.Tasks, nil
}

// resolveTaskKey looks up task in the routing map, normalizing _ ↔ - separators.
func resolveTaskKey(task string, routing map[string]routingEntry) (string, routingEntry, bool) {
	if e, ok := routing[task]; ok {
		return task, e, true
	}
	alt := strings.ReplaceAll(task, "_", "-")
	if e, ok := routing[alt]; ok {
		return alt, e, true
	}
	alt2 := strings.ReplaceAll(task, "-", "_")
	if e, ok := routing[alt2]; ok {
		return alt2, e, true
	}
	return "", routingEntry{}, false
}

type modulePathEntry struct {
	path  string
	layer string
}

func loadRepoModulePaths(repoRoot string) ([]modulePathEntry, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "specs", "repo.yaml"))
	if err != nil {
		return nil, err
	}
	var doc struct {
		Layers map[string]struct {
			Paths []string `yaml:"paths"`
		} `yaml:"layers"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	order := []string{"stable", "extension", "tooling", "reference"}
	seen := map[string]bool{}
	var out []modulePathEntry
	for _, l := range order {
		layer, ok := doc.Layers[l]
		if !ok {
			continue
		}
		for _, p := range layer.Paths {
			if !seen[p] {
				seen[p] = true
				out = append(out, modulePathEntry{path: p, layer: l})
			}
		}
	}
	return out, nil
}

func suggestRecipe(task string) string {
	norm := strings.ToLower(strings.ReplaceAll(task, "_", "-"))
	recipes := map[string]string{
		"http-endpoint":               "add-http-endpoint",
		"add-http-endpoint":           "add-http-endpoint",
		"middleware":                  "add-middleware",
		"add-middleware":              "add-middleware",
		"fix-bug":                     "fix-bug",
		"bugfix-triage":               "fix-bug",
		"bugfix":                      "fix-bug",
		"http-endpoint-bugfix":        "http-endpoint-bugfix",
		"symbol-change":               "symbol-change",
		"tenant-policy-change":        "tenant-policy-change",
		"new-stable-module":           "new-stable-module",
		"new-extension-module":        "new-extension-module",
		"analysis-only":               "analysis-only",
		"review-only":                 "review-only",
		"stable-root-boundary-review": "stable-root-boundary-review",
	}
	return recipes[norm]
}
