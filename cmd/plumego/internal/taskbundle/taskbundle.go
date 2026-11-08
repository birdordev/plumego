// Package taskbundle synthesizes a single-file task execution context from the
// repository control plane so agents do not need to read multiple spec documents
// before every task.
package taskbundle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/birdordev/plumego/cmd/plumego/internal/agentmanifest"
	"gopkg.in/yaml.v3"
)

// Bundle is the synthesized task execution context written to a single YAML file.
type Bundle struct {
	BundleVersion int    `yaml:"bundle_version"`
	GeneratedAt   string `yaml:"generated_at"`

	Task struct {
		Type   string `yaml:"type"`
		Module string `yaml:"module"`
	} `yaml:"task"`

	Routing struct {
		ReadDocs []string `yaml:"read_docs,omitempty"`
		Avoid    []string `yaml:"avoid,omitempty"`
	} `yaml:"routing"`

	Module      moduleInfo  `yaml:"module"`
	ImportRules importRules `yaml:"import_rules"`
	Recipe      recipeInfo  `yaml:"recipe"`
	Validation  validation  `yaml:"validation"`

	PreflightChecklist []string          `yaml:"preflight_checklist"`
	CanonicalPatterns  map[string]string `yaml:"canonical_patterns,omitempty"`
	MustRules          []string          `yaml:"must_rules"`
	AntiPatterns       []string          `yaml:"anti_patterns"`
}

type moduleInfo struct {
	Name             string   `yaml:"name"`
	Layer            string   `yaml:"layer"`
	Status           string   `yaml:"status"`
	Risk             string   `yaml:"risk"`
	Summary          string   `yaml:"summary"`
	Responsibilities []string `yaml:"responsibilities,omitempty"`
	NonGoals         []string `yaml:"non_goals,omitempty"`
	StopConditions   []string `yaml:"stop_conditions,omitempty"`
	AgentHints       []string `yaml:"agent_hints,omitempty"`
}

type importRules struct {
	Allow []string `yaml:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty"`
}

type recipeInfo struct {
	Name           string   `yaml:"name"`
	Scope          string   `yaml:"scope,omitempty"`
	Steps          []string `yaml:"steps,omitempty"`
	StopConditions []string `yaml:"stop_conditions,omitempty"`
}

type validation struct {
	GateProfile string   `yaml:"gate_profile"`
	Commands    []string `yaml:"commands"`
}

// ─── YAML source types ────────────────────────────────────────────────────────

type taskRoutingEntry struct {
	Intent    string   `yaml:"intent"`
	StartWith []string `yaml:"start_with"`
	Avoid     []string `yaml:"avoid"`
}

type taskRoutingDoc struct {
	Tasks map[string]taskRoutingEntry `yaml:"tasks"`
}

type depModuleEntry struct {
	Path  string   `yaml:"path"`
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

type depRulesDoc struct {
	Modules map[string]depModuleEntry `yaml:"modules"`
}

type recipeFile struct {
	Name           string   `yaml:"name"`
	Scope          string   `yaml:"scope"`
	Steps          []string `yaml:"steps"`
	StopConditions []string `yaml:"stop_conditions"`
}

type qualityRulesFile struct {
	RuleLevels struct {
		Must []string `yaml:"must"`
	} `yaml:"rule_levels"`
	AntiPatterns []string `yaml:"anti_patterns"`
}

// ─── static maps ──────────────────────────────────────────────────────────────

var stableRoots = map[string]bool{
	"core": true, "router": true, "contract": true,
	"middleware": true, "security": true, "store": true,
	"health": true, "log": true, "metrics": true,
}

var rmsRoots = map[string]bool{
	"router": true, "middleware": true, "security": true,
}

// taskToRecipe maps task-routing.yaml keys to change-recipe file names.
var taskToRecipe = map[string]string{
	"http_endpoint":               "add-http-endpoint",
	"http_endpoint_bugfix":        "http-endpoint-bugfix",
	"bugfix_triage":               "fix-bug",
	"symbol_change":               "symbol-change",
	"middleware":                  "add-middleware",
	"stable_root_boundary_review": "stable-root-boundary-review",
	"tenant_policy_change":        "tenant-policy-change",
	"control_plane":               "analysis-only",
	"tenant":                      "tenant-policy-change",
}

// ─── Generate ─────────────────────────────────────────────────────────────────

// Generate synthesizes a Bundle from the repository control plane.
// modulePath is relative to repoRoot (e.g. "x/tenant", "middleware").
func Generate(repoRoot, taskType, modulePath string) (*Bundle, error) {
	b := &Bundle{}
	b.BundleVersion = 1
	b.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	b.Task.Type = taskType
	b.Task.Module = modulePath

	// Routing (best-effort – skip if task type not in task-routing.yaml).
	if entry, err := loadRoutingEntry(repoRoot, taskType); err == nil {
		b.Routing.ReadDocs = entry.StartWith
		b.Routing.Avoid = entry.Avoid
	}

	// Module manifest (required).
	manifest, err := agentmanifest.Load(repoRoot, modulePath)
	if err != nil {
		return nil, err
	}
	b.Module = moduleInfo{
		Name:             manifest.Name,
		Layer:            manifest.Layer,
		Status:           manifest.Status,
		Risk:             manifest.Risk,
		Summary:          manifest.Summary,
		Responsibilities: manifest.Responsibilities,
		NonGoals:         manifest.NonGoals,
		StopConditions:   manifest.StopConditions,
		AgentHints:       manifest.AgentHints,
	}

	// Import rules: prefer dependency-rules.yaml, fall back to module.yaml.
	if rules, err := loadImportRules(repoRoot, modulePath); err == nil {
		b.ImportRules = rules
	} else {
		b.ImportRules = importRules{
			Allow: manifest.AllowedImports,
			Deny:  manifest.ForbiddenImports,
		}
	}

	// Recipe (best-effort).
	if recipeName, ok := taskToRecipe[taskType]; ok {
		if recipe, err := loadRecipe(repoRoot, recipeName); err == nil {
			b.Recipe = recipe
		}
	}

	// Validation profile and commands.
	b.Validation = selectValidation(manifest, modulePath)

	// Preflight checklist.
	b.PreflightChecklist = buildPreflight(manifest, modulePath)

	// Canonical patterns for the task type.
	b.CanonicalPatterns = canonicalPatterns(taskType)

	// Must-rules and anti-patterns from agent-quality-rules.yaml.
	if must, anti, err := loadQualityRules(repoRoot); err == nil {
		b.MustRules = must
		b.AntiPatterns = anti
	}

	return b, nil
}

// ─── loaders ──────────────────────────────────────────────────────────────────

func loadRoutingEntry(repoRoot, taskType string) (taskRoutingEntry, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "specs", "task-routing.yaml"))
	if err != nil {
		return taskRoutingEntry{}, err
	}
	var doc taskRoutingDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return taskRoutingEntry{}, err
	}
	entry, ok := doc.Tasks[taskType]
	if !ok {
		return taskRoutingEntry{}, fmt.Errorf("task type %q not found in task-routing.yaml", taskType)
	}
	return entry, nil
}

func loadImportRules(repoRoot, modulePath string) (importRules, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "specs", "dependency-rules.yaml"))
	if err != nil {
		return importRules{}, err
	}
	var doc depRulesDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return importRules{}, err
	}

	// Look up by key (keys in dependency-rules.yaml match module paths).
	entry, ok := doc.Modules[modulePath]
	if !ok {
		// Fall back: scan by path field.
		for _, m := range doc.Modules {
			if m.Path == modulePath {
				entry = m
				ok = true
				break
			}
		}
	}
	if !ok {
		return importRules{}, fmt.Errorf("module %q not found in dependency-rules.yaml", modulePath)
	}
	return importRules{Allow: entry.Allow, Deny: entry.Deny}, nil
}

func loadRecipe(repoRoot, recipeName string) (recipeInfo, error) {
	path := filepath.Join(repoRoot, "specs", "change-recipes", recipeName+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return recipeInfo{}, err
	}
	var r recipeFile
	if err := yaml.Unmarshal(data, &r); err != nil {
		return recipeInfo{}, err
	}
	return recipeInfo{
		Name:           r.Name,
		Scope:          r.Scope,
		Steps:          r.Steps,
		StopConditions: r.StopConditions,
	}, nil
}

func loadQualityRules(repoRoot string) (mustRules, antiPatterns []string, err error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "specs", "agent-quality-rules.yaml"))
	if err != nil {
		return nil, nil, err
	}
	var q qualityRulesFile
	if err := yaml.Unmarshal(data, &q); err != nil {
		return nil, nil, err
	}
	return q.RuleLevels.Must, q.AntiPatterns, nil
}

// ─── validation profile selection ─────────────────────────────────────────────

func selectValidation(m *agentmanifest.Manifest, modulePath string) validation {
	modArg := "./" + strings.TrimSuffix(modulePath, "/") + "/..."

	switch {
	case rmsRoots[m.Name]:
		return validation{
			GateProfile: "router_middleware_security",
			Commands: []string{
				"go test -race -timeout 60s " + modArg,
				"go test -timeout 20s " + modArg,
				"go vet " + modArg,
				"go run ./internal/checks/dependency-rules",
			},
		}
	case stableRoots[m.Name]:
		return validation{
			GateProfile: "stable_root_change",
			Commands: []string{
				"go test -race -timeout 60s " + modArg,
				"go test -timeout 20s " + modArg,
				"go vet " + modArg,
				"go run ./internal/checks/dependency-rules",
				"go run ./internal/checks/agent-workflow",
				"go run ./internal/checks/module-manifests",
			},
		}
	default:
		return validation{
			GateProfile: "single_module_behavior",
			Commands: []string{
				"go test -race -timeout 60s " + modArg,
				"go test -timeout 20s " + modArg,
				"go vet " + modArg,
			},
		}
	}
}

// ─── preflight checklist ──────────────────────────────────────────────────────

func buildPreflight(m *agentmanifest.Manifest, modulePath string) []string {
	docHint := ""
	if len(m.DocPaths) > 0 {
		docHint = " (update " + m.DocPaths[0] + " if behavior changes)"
	}

	return []string{
		"[ ] Owning module: " + modulePath,
		"[ ] Target module.yaml read: " + modulePath + "/module.yaml",
		"[ ] In-scope paths: (specify sub-path within " + modulePath + "/)",
		"[ ] Out-of-scope paths: (list what must NOT change)",
		"[ ] Public API impact: none (state yes if different)",
		"[ ] Dependency impact: none (state yes if adding imports)",
		"[ ] Behavior impact: yes",
		"[ ] Security impact: none (verify for auth, token, or input-handling changes)",
		"[ ] Docs impact: none" + docHint,
		"[ ] Validation plan: run commands listed in validation section",
	}
}

// ─── canonical patterns ───────────────────────────────────────────────────────

func canonicalPatterns(taskType string) map[string]string {
	switch taskType {
	case "http_endpoint", "add-http-endpoint", "http_endpoint_bugfix":
		return map[string]string{
			"handler": `// Constructor-injected handler returning http.HandlerFunc.
func HandleXxx(svc XxxService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req XxxRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			contract.WriteError(w, contract.ErrInvalidInput.Build())
			return
		}
		result, err := svc.DoXxx(r.Context(), req)
		if err != nil {
			contract.WriteError(w, contract.ErrInternal.Build())
			return
		}
		contract.WriteResponse(w, http.StatusOK, result)
	}
}`,
			"route_wiring": `// One method + one path + one handler per line. No auto-discovery.
app.Post("/api/v1/xxx", HandleXxx(deps.XxxService))`,
			"handler_test": `func TestHandleXxx(t *testing.T) {
	svc := &mockXxxService{}
	h := HandleXxx(svc)
	body := strings.NewReader(` + "`" + `{"key":"value"}` + "`" + `)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/xxx", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
}`,
		}

	case "add-middleware", "middleware":
		return map[string]string{
			"middleware": `// Fallible constructor – never panic for missing config.
func NewXxxMiddleware(param string) (func(http.Handler) http.Handler, error) {
	if param == "" {
		return nil, errors.New("param required")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// transport-only logic here; no business DTOs, no service calls
			next.ServeHTTP(w, r)
		})
	}, nil
}`,
			"middleware_test": `func TestXxxMiddleware_CallsNext(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	m, err := NewXxxMiddleware("value")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	m(next).ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Fatal("next not called")
	}
}

func TestXxxMiddleware_ErrorPath(t *testing.T) {
	_, err := NewXxxMiddleware("")
	if err == nil {
		t.Fatal("want error for empty param")
	}
}`,
		}

	case "symbol_change", "symbol-change":
		return map[string]string{
			"search_callers":   `rg -n --glob '*.go' 'OldSymbolName' .`,
			"verify_migration": `rg -n --glob '*.go' 'OldSymbolName' .  # must return zero results after migration`,
		}

	default:
		return nil
	}
}
