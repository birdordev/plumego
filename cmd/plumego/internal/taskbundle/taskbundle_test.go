package taskbundle_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/birdordev/plumego/cmd/plumego/internal/taskbundle"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine caller path")
	}
	// file is .../cmd/plumego/internal/taskbundle/taskbundle_test.go
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return abs
}

func TestGenerate_ExtensionModule(t *testing.T) {
	b, err := taskbundle.Generate(repoRoot(t), "http_endpoint", "x/tenant")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if b.BundleVersion != 1 {
		t.Errorf("BundleVersion: want 1, got %d", b.BundleVersion)
	}
	if b.Task.Type != "http_endpoint" {
		t.Errorf("Task.Type: want http_endpoint, got %s", b.Task.Type)
	}
	if b.Task.Module != "x/tenant" {
		t.Errorf("Task.Module: want x/tenant, got %s", b.Task.Module)
	}

	// Routing should be populated from task-routing.yaml.
	if len(b.Routing.ReadDocs) == 0 {
		t.Error("Routing.ReadDocs: want non-empty")
	}

	// Module info.
	if b.Module.Layer != "extension" {
		t.Errorf("Module.Layer: want extension, got %s", b.Module.Layer)
	}
	if b.Module.Name != "x/tenant" {
		t.Errorf("Module.Name: want x/tenant, got %s", b.Module.Name)
	}

	// Import rules should be populated.
	if len(b.ImportRules.Allow) == 0 {
		t.Error("ImportRules.Allow: want non-empty")
	}

	// Recipe should be populated (http_endpoint → add-http-endpoint).
	if b.Recipe.Name == "" {
		t.Error("Recipe.Name: want non-empty")
	}

	// Validation for extension module: single_module_behavior.
	if b.Validation.GateProfile != "single_module_behavior" {
		t.Errorf("Validation.GateProfile: want single_module_behavior, got %s", b.Validation.GateProfile)
	}
	if len(b.Validation.Commands) == 0 {
		t.Error("Validation.Commands: want non-empty")
	}
	for _, cmd := range b.Validation.Commands {
		if !strings.Contains(cmd, "x/tenant") {
			t.Errorf("command %q does not reference module path", cmd)
		}
	}

	// Preflight checklist.
	if len(b.PreflightChecklist) == 0 {
		t.Error("PreflightChecklist: want non-empty")
	}

	// Canonical patterns for http_endpoint.
	if _, ok := b.CanonicalPatterns["handler"]; !ok {
		t.Error("CanonicalPatterns: want handler pattern")
	}
	if _, ok := b.CanonicalPatterns["route_wiring"]; !ok {
		t.Error("CanonicalPatterns: want route_wiring pattern")
	}

	// Must-rules and anti-patterns.
	if len(b.MustRules) == 0 {
		t.Error("MustRules: want non-empty")
	}
	if len(b.AntiPatterns) == 0 {
		t.Error("AntiPatterns: want non-empty")
	}
}

func TestGenerate_StableRootRMS(t *testing.T) {
	b, err := taskbundle.Generate(repoRoot(t), "middleware", "middleware")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if b.Module.Layer != "stable" {
		t.Errorf("Module.Layer: want stable, got %s", b.Module.Layer)
	}
	if b.Validation.GateProfile != "router_middleware_security" {
		t.Errorf("Validation.GateProfile: want router_middleware_security, got %s", b.Validation.GateProfile)
	}

	// RMS profile requires dependency-rules check.
	hasDepCheck := false
	for _, cmd := range b.Validation.Commands {
		if strings.Contains(cmd, "dependency-rules") {
			hasDepCheck = true
			break
		}
	}
	if !hasDepCheck {
		t.Error("router_middleware_security profile must include dependency-rules check")
	}
}

func TestGenerate_StableRoot(t *testing.T) {
	b, err := taskbundle.Generate(repoRoot(t), "bugfix_triage", "core")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if b.Validation.GateProfile != "stable_root_change" {
		t.Errorf("Validation.GateProfile: want stable_root_change, got %s", b.Validation.GateProfile)
	}

	// Stable root change needs agent-workflow and module-manifests checks.
	var hasAgentWorkflow, hasModuleManifests bool
	for _, cmd := range b.Validation.Commands {
		if strings.Contains(cmd, "agent-workflow") {
			hasAgentWorkflow = true
		}
		if strings.Contains(cmd, "module-manifests") {
			hasModuleManifests = true
		}
	}
	if !hasAgentWorkflow {
		t.Error("stable_root_change must include agent-workflow check")
	}
	if !hasModuleManifests {
		t.Error("stable_root_change must include module-manifests check")
	}
}

func TestGenerate_ModuleNotFound(t *testing.T) {
	_, err := taskbundle.Generate(repoRoot(t), "http_endpoint", "x/nonexistent")
	if err == nil {
		t.Fatal("want error for nonexistent module")
	}
}

func TestGenerate_UnknownTaskType(t *testing.T) {
	// Unknown task type: routing is skipped, recipe is skipped, module still loaded.
	b, err := taskbundle.Generate(repoRoot(t), "unknown_task", "x/tenant")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(b.Routing.ReadDocs) != 0 {
		t.Error("unknown task: Routing.ReadDocs should be empty")
	}
	if b.Recipe.Name != "" {
		t.Error("unknown task: Recipe.Name should be empty")
	}
	// Module info still populated.
	if b.Module.Name == "" {
		t.Error("Module.Name should always be populated")
	}
}

func TestGenerate_SymbolChangePatterns(t *testing.T) {
	b, err := taskbundle.Generate(repoRoot(t), "symbol_change", "contract")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, ok := b.CanonicalPatterns["search_callers"]; !ok {
		t.Error("symbol_change: want search_callers pattern")
	}
	if _, ok := b.CanonicalPatterns["verify_migration"]; !ok {
		t.Error("symbol_change: want verify_migration pattern")
	}
}
