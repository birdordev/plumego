package checkutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindDisallowedImportsReportsAllAndSkipsTests(t *testing.T) {
	repo := t.TempDir()
	writeRepoSpec(t, repo)
	writeDependencyRulesSpec(t, repo)

	writeFile(t, filepath.Join(repo, "core", "core.go"), `package core

import (
	"net/http"
	"github.com/birdordev/plumego/x/tenant/resolve"
)

func Use(_ http.ResponseWriter) {
	_ = resolve.Options{}
}
`)
	// The same disallowed import appears only in a _test.go file and must be
	// skipped, so it does not produce a second violation.
	writeFile(t, filepath.Join(repo, "core", "core_test.go"), `package core

import "github.com/birdordev/plumego/x/tenant/resolve"
`)

	violations, err := FindDisallowedImports(repo)
	if err != nil {
		t.Fatalf("FindDisallowedImports: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %v", len(violations), violations)
	}
	if got := violations[0]; got != "core/core.go|github.com/birdordev/plumego/x/tenant/resolve" {
		t.Fatalf("unexpected violation %q", got)
	}
}

func TestFindDisallowedImportsSkipsNestedModules(t *testing.T) {
	repo := t.TempDir()
	writeRepoSpec(t, repo)
	writeDependencyRulesSpec(t, repo)
	writeFile(t, filepath.Join(repo, "core", "core.go"), `package core

import "net/http"

func Use(_ http.ResponseWriter) {}
`)
	writeFile(t, filepath.Join(repo, "core", "adapter", "go.mod"), `module github.com/birdordev/plumego/core/adapter
`)
	writeFile(t, filepath.Join(repo, "core", "adapter", "adapter.go"), `package adapter

import "github.com/birdordev/plumego/x/tenant/resolve"

func Use() {
	_ = resolve.Options{}
}
`)

	violations, err := FindDisallowedImports(repo)
	if err != nil {
		t.Fatalf("FindDisallowedImports: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected nested module imports to be skipped, got %v", violations)
	}
}

func TestValidateReferenceModules(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "reference", "standard-service", "go.mod"), `module standard-service

go 1.26.0

require github.com/birdordev/plumego v0.0.0

replace github.com/birdordev/plumego => ../..
`)
	writeFile(t, filepath.Join(repo, "reference", "standard-service", "main.go"), `package main

import "github.com/birdordev/plumego/core"

func main() {
	_ = core.DefaultConfig()
}
`)

	violations, err := ValidateReferenceModules(repo)
	if err != nil {
		t.Fatalf("ValidateReferenceModules: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %v", violations)
	}

	writeFile(t, filepath.Join(repo, "reference", "missing", "main.go"), `package main

func main() {}
`)
	writeFile(t, filepath.Join(repo, "reference", "bad-module", "go.mod"), `module github.com/birdordev/plumego/reference/bad-module

go 1.26.0
`)
	writeFile(t, filepath.Join(repo, "reference", "bad-module", "main.go"), `package main

import (
	_ "github.com/birdordev/plumego/internal/config"
	_ "github.com/birdordev/plumego/reference/other/internal/app"
)
`)

	violations, err = ValidateReferenceModules(repo)
	if err != nil {
		t.Fatalf("ValidateReferenceModules: %v", err)
	}

	want := []string{
		"reference/missing missing go.mod",
		`reference/bad-module/go.mod module "github.com/birdordev/plumego/reference/bad-module" must equal "bad-module"`,
		"reference/bad-module/go.mod missing github.com/birdordev/plumego require",
		"reference/bad-module/go.mod missing local github.com/birdordev/plumego replace",
		"reference/bad-module/main.go imports repository internal path github.com/birdordev/plumego/internal/config",
		"reference/bad-module/main.go imports repository reference path github.com/birdordev/plumego/reference/other/internal/app",
	}
	for _, expected := range want {
		if !hasString(violations, expected) {
			t.Fatalf("expected violation %q in %v", expected, violations)
		}
	}
}

func TestFindXGoModFiles(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "x", "validate", "module.yaml"), "name: x/validate\n")
	writeFile(t, filepath.Join(repo, "x", "validate", "playground", "go.mod"), "module example.com/bad\n")
	writeFile(t, filepath.Join(repo, "reference", "with-rest", "go.mod"), "module with-rest\n")

	violations, err := FindXGoModFiles(repo)
	if err != nil {
		t.Fatalf("FindXGoModFiles: %v", err)
	}
	if len(violations) != 1 || violations[0] != "x/validate/playground/go.mod" {
		t.Fatalf("unexpected violations: %#v", violations)
	}
}

func TestValidateManifestDependencyRuleConsistencyReportsContradictions(t *testing.T) {
	repo := t.TempDir()
	writeRepoSpec(t, repo)
	writeFile(t, filepath.Join(repo, "specs", "dependency-rules.yaml"), `modules:
  x/messaging:
    path: x/messaging
    allow:
      - stdlib
      - x/tenant
    deny:
      - x/ai/**
  x/fileapi:
    path: x/fileapi
    allow:
      - stdlib
      - x/tenant
    deny:
      - x/tenant/**
special_rules:
  forbidden_paths:
    - plumego.go
  forbidden_import_patterns:
    - github.com/birdordev/plumego
`)
	writeFile(t, filepath.Join(repo, "x", "messaging", "module.yaml"), `name: x/messaging
path: x/messaging
layer: extension
status: experimental
owner: messaging
risk: medium
summary: example
responsibilities:
  - keep scope tight
non_goals:
  - do not sprawl
allowed_imports:
  - stdlib
forbidden_imports:
  - x/tenant/**
test_commands:
  - go test ./...
review_checklist:
  - stay explicit
agent_hints:
  - keep modules small
`)
	writeFile(t, filepath.Join(repo, "x", "fileapi", "module.yaml"), `name: x/fileapi
path: x/fileapi
layer: extension
status: experimental
owner: persistence
risk: medium
summary: example
responsibilities:
  - keep scope tight
non_goals:
  - do not sprawl
allowed_imports:
  - stdlib
  - x/tenant
forbidden_imports:
  - x/ai/**
test_commands:
  - go test ./...
review_checklist:
  - stay explicit
agent_hints:
  - keep modules small
`)

	violations, err := ValidateManifestDependencyRuleConsistency(repo)
	if err != nil {
		t.Fatalf("ValidateManifestDependencyRuleConsistency: %v", err)
	}

	joined := strings.Join(violations, "\n")
	for _, want := range []string{
		`module x/messaging allows "x/tenant"`,
		`x/fileapi/module.yaml: allowed_imports entry "x/tenant" is denied`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected violation containing %q, got:\n%s", want, joined)
		}
	}
}

func TestValidateDeclaredInternalCallersReportsMissingAndStaleDeclarations(t *testing.T) {
	repo := t.TempDir()
	writeRepoSpec(t, repo)
	writeFile(t, filepath.Join(repo, "specs", "dependency-rules.yaml"), `modules:
  internal/httpx:
    path: internal/httpx
    allow:
      - stdlib
    declared_callers:
      - x/gateway
      - x/stale
    deny:
      - x/**
special_rules:
  forbidden_paths:
    - plumego.go
  forbidden_import_patterns:
    - github.com/birdordev/plumego
`)
	writeFile(t, filepath.Join(repo, "x", "gateway", "gateway.go"), `package gateway

import _ "github.com/birdordev/plumego/internal/httpx"
`)
	writeFile(t, filepath.Join(repo, "x", "messaging", "messaging.go"), `package messaging

import _ "github.com/birdordev/plumego/internal/httpx"
`)
	writeFile(t, filepath.Join(repo, "x", "observability", "devtools", "devtools.go"), `package devtools

import _ "github.com/birdordev/plumego/internal/config"
`)

	violations, err := ValidateDeclaredInternalCallers(repo)
	if err != nil {
		t.Fatalf("ValidateDeclaredInternalCallers: %v", err)
	}

	joined := strings.Join(violations, "\n")
	for _, want := range []string{
		`x/messaging/messaging.go imports github.com/birdordev/plumego/internal/httpx but caller "x/messaging" is not listed`,
		`x/observability/devtools/devtools.go imports github.com/birdordev/plumego/internal/config but no specs/dependency-rules.yaml internal module declares it`,
		`specs/dependency-rules.yaml module internal/httpx declares caller "x/stale" but no non-test Go import uses it`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected violation containing %q, got:\n%s", want, joined)
		}
	}
}

func TestReadBaselineAcceptsLongLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.txt")
	longEntry := "x/long|" + strings.Repeat("a", 70*1024)
	writeFile(t, path, longEntry+"\n")

	baseline, err := ReadBaseline(path)
	if err != nil {
		t.Fatalf("ReadBaseline: %v", err)
	}
	if _, ok := baseline[longEntry]; !ok {
		t.Fatal("expected long baseline entry to be preserved")
	}
}

func TestFindMissingModuleManifestsReportsXModulesWithoutManifest(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "core", "module.yaml"), validManifest("core", "stable"))
	writeFile(t, filepath.Join(repo, "router", "module.yaml"), validManifest("router", "stable"))
	writeFile(t, filepath.Join(repo, "contract", "module.yaml"), validManifest("contract", "stable"))
	writeFile(t, filepath.Join(repo, "middleware", "module.yaml"), validManifest("middleware", "stable"))
	writeFile(t, filepath.Join(repo, "security", "module.yaml"), validManifest("security", "stable"))
	writeFile(t, filepath.Join(repo, "store", "module.yaml"), validManifest("store", "stable"))
	writeFile(t, filepath.Join(repo, "health", "module.yaml"), validManifest("health", "stable"))
	writeFile(t, filepath.Join(repo, "log", "module.yaml"), validManifest("log", "stable"))
	writeFile(t, filepath.Join(repo, "metrics", "module.yaml"), validManifest("metrics", "stable"))
	writeFile(t, filepath.Join(repo, "x", "ai", "module.yaml"), validManifest("x/ai", "extension"))
	writeFile(t, filepath.Join(repo, "x", "observability", "module.yaml"), validManifest("x/observability", "extension"))
	if err := os.MkdirAll(filepath.Join(repo, "x", "scheduler"), 0o755); err != nil {
		t.Fatalf("mkdir x/scheduler: %v", err)
	}

	missing, err := FindMissingModuleManifests(repo)
	if err != nil {
		t.Fatalf("FindMissingModuleManifests: %v", err)
	}
	if len(missing) != 1 || missing[0] != "x/scheduler" {
		t.Fatalf("unexpected missing manifests: %v", missing)
	}
}

func TestValidateModuleManifestsReportsSchemaAndPathViolations(t *testing.T) {
	repo := t.TempDir()
	writeManifestSchema(t, repo)
	writeFile(t, filepath.Join(repo, "core", "module.yaml"), `name: wrong
path: wrong
layer: unstable
status: ga
owner: runtime
risk: high
summary: bad manifest
responsibilities:
  - one
non_goals:
allowed_imports:
  - stdlib
forbidden_imports:
  - x/**
test_commands:
  - go test ./...
review_checklist:
  - stay explicit
agent_hints:
  - keep boundaries
`)

	violations, err := ValidateModuleManifests(repo)
	if err != nil {
		t.Fatalf("ValidateModuleManifests: %v", err)
	}

	joined := strings.Join(violations, "\n")
	for _, want := range []string{
		`required list "non_goals" must not be empty`,
		`invalid layer "unstable"`,
		`path "wrong" does not match directory "core"`,
		`name "wrong" should match module path "core"`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected violation containing %q, got:\n%s", want, joined)
		}
	}
}

func TestValidateModuleManifestsRequiresDeclaredDocPathsToExist(t *testing.T) {
	repo := t.TempDir()
	writeManifestSchema(t, repo)
	writeFile(t, filepath.Join(repo, "core", "module.yaml"), validManifestWithDocPaths("core", "stable", "docs/modules/core/README.md"))
	writeFile(t, filepath.Join(repo, "docs", "modules", "core", "README.md"), "# core\n")

	violations, err := ValidateModuleManifests(repo)
	if err != nil {
		t.Fatalf("ValidateModuleManifests existing docs: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations for existing doc_paths, got %v", violations)
	}

	writeFile(t, filepath.Join(repo, "x", "messaging", "webhook", "module.yaml"), validManifestWithDocPaths("x/messaging/webhook", "extension", "docs/modules/x/messaging/webhook/README.md"))

	violations, err = ValidateModuleManifests(repo)
	if err != nil {
		t.Fatalf("ValidateModuleManifests missing docs: %v", err)
	}

	joined := strings.Join(violations, "\n")
	if !strings.Contains(joined, `doc_paths target "docs/modules/x/messaging/webhook/README.md" does not exist`) {
		t.Fatalf("expected missing doc_paths violation, got:\n%s", joined)
	}
}

func TestFindUnexpectedTopLevelDirs(t *testing.T) {
	repo := t.TempDir()
	for _, dir := range []string{"core", "docs", "x"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	unexpected, err := FindUnexpectedTopLevelDirs(repo, AllowedTopLevelDirs())
	if err != nil {
		t.Fatalf("FindUnexpectedTopLevelDirs: %v", err)
	}
	if len(unexpected) != 0 {
		t.Fatalf("expected no unexpected dirs, got %v", unexpected)
	}

	if err := os.MkdirAll(filepath.Join(repo, "rogue"), 0o755); err != nil {
		t.Fatalf("mkdir rogue: %v", err)
	}
	unexpected, err = FindUnexpectedTopLevelDirs(repo, AllowedTopLevelDirs())
	if err != nil {
		t.Fatalf("FindUnexpectedTopLevelDirs second pass: %v", err)
	}
	if len(unexpected) != 1 || unexpected[0] != "rogue" {
		t.Fatalf("unexpected dirs: %v", unexpected)
	}
}

func TestValidateStableBoundaryDeclarationsReportsWhenMissing(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "core", "module.yaml"), validManifestWithStrictBoundary("core", "stable", "kernel"))
	writeFile(t, filepath.Join(repo, "router", "module.yaml"), validManifest("router", "stable"))
	// other stable roots absent → skipped (FindMissingModuleManifests handles them)

	violations, err := ValidateStableBoundaryDeclarations(repo)
	if err != nil {
		t.Fatalf("ValidateStableBoundaryDeclarations: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %v", len(violations), violations)
	}
	if !strings.Contains(violations[0], "router/module.yaml") || !strings.Contains(violations[0], "strict_boundary") {
		t.Fatalf("unexpected violation: %q", violations[0])
	}
}

func TestValidateStableBoundaryDeclarationsPassesWhenPresent(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "core", "module.yaml"), validManifestWithStrictBoundary("core", "stable", "kernel"))
	writeFile(t, filepath.Join(repo, "router", "module.yaml"), validManifestWithStrictBoundary("router", "stable", "route_structure"))

	violations, err := ValidateStableBoundaryDeclarations(repo)
	if err != nil {
		t.Fatalf("ValidateStableBoundaryDeclarations: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got: %v", violations)
	}
}

func TestValidateXFamilyTaxonomyDetectsViolations(t *testing.T) {
	repo := t.TempDir()
	writeExtensionTaxonomySpec(t, repo)

	// x/messaging lacks subordinate_families → violation
	writeFile(t, filepath.Join(repo, "x", "messaging", "module.yaml"), validManifest("x/messaging", "extension"))

	// x/messaging/mq declares an unknown parent_family → violation
	writeFile(t, filepath.Join(repo, "x", "messaging", "mq", "module.yaml"), validManifestWithParentFamily("x/messaging/mq", "extension", "x/nonexistent"))

	// x/gateway declares subordinate_families listing x/nonexistent-sub → Check 1 fires
	writeFile(t, filepath.Join(repo, "x", "gateway", "module.yaml"), validManifestWithSubordinateFamilies("x/gateway", "extension", "x/nonexistent-sub"))

	// x/messaging/pubsub declares a valid parent_family → no violation
	writeFile(t, filepath.Join(repo, "x", "messaging", "pubsub", "module.yaml"), validManifestWithParentFamily("x/messaging/pubsub", "extension", "x/messaging"))

	violations, err := ValidateXFamilyTaxonomy(repo)
	if err != nil {
		t.Fatalf("ValidateXFamilyTaxonomy: %v", err)
	}
	// expect exactly 4: x/messaging missing subordinate_families,
	// x/messaging/mq points at the wrong parent, that parent is unknown,
	// and x/gateway lists a non-existent subordinate.
	if len(violations) != 4 {
		t.Fatalf("expected 4 violations, got %d: %v", len(violations), violations)
	}
	joined := strings.Join(violations, "\n")
	if !strings.Contains(joined, "x/messaging/module.yaml") || !strings.Contains(joined, "subordinate_families") {
		t.Fatalf("expected subordinate_families violation for x/messaging, got:\n%s", joined)
	}
	if !strings.Contains(joined, `parent_family "x/nonexistent" must be "x/messaging"`) {
		t.Fatalf("expected exact parent_family violation for x/messaging/mq, got:\n%s", joined)
	}
	if !strings.Contains(joined, "x/messaging/mq/module.yaml") || !strings.Contains(joined, "x/nonexistent") {
		t.Fatalf("expected parent_family violation for x/messaging/mq, got:\n%s", joined)
	}
	if !strings.Contains(joined, "x/gateway/module.yaml") || !strings.Contains(joined, "x/nonexistent-sub") {
		t.Fatalf("expected subordinate_families missing-child violation for x/gateway, got:\n%s", joined)
	}
}

func TestValidateXFamilyTaxonomyPassesForValidSetup(t *testing.T) {
	repo := t.TempDir()
	writeExtensionTaxonomySpec(t, repo)

	writeFile(t, filepath.Join(repo, "x", "messaging", "module.yaml"), validManifestWithSubordinateFamilies("x/messaging", "extension", "x/messaging/mq"))
	writeFile(t, filepath.Join(repo, "x", "messaging", "mq", "module.yaml"), validManifestWithParentFamily("x/messaging/mq", "extension", "x/messaging"))
	writeFile(t, filepath.Join(repo, "x", "rest", "module.yaml"), validManifest("x/rest", "extension"))

	violations, err := ValidateXFamilyTaxonomy(repo)
	if err != nil {
		t.Fatalf("ValidateXFamilyTaxonomy: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got: %v", violations)
	}
}

func TestFindExtensionHotspotCoverageViolationsRequiresFamilyEntries(t *testing.T) {
	doc := extensionTaxonomyDoc{
		RootsByFamily: map[string][]string{
			"messaging": {"x/messaging", "x/messaging/mq", "x/messaging/pubsub"},
			"rest":      {"x/rest"},
		},
		RootToCanonical: map[string]string{
			"x/messaging":        "x/messaging",
			"x/messaging/mq":     "x/messaging",
			"x/messaging/pubsub": "x/messaging",
			"x/rest":             "x/rest",
		},
	}

	violations := FindExtensionHotspotCoverageViolations(doc, map[string]packageIndexEntry{
		"x/messaging":    {Path: "x/messaging", StartWith: []string{"x/messaging/module.yaml"}},
		"x/messaging/mq": {Path: "x/messaging/mq", StartWith: []string{"x/messaging/mq/module.yaml"}},
	})

	joined := strings.Join(violations, "\n")
	if len(violations) != 1 || !strings.Contains(joined, "x/messaging/pubsub") {
		t.Fatalf("expected missing x/messaging/pubsub hotspot violation, got:\n%s", joined)
	}
}

func TestReadRepoExtensionRootsParsesDeclaredExtensionPaths(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "specs", "repo.yaml"), `layers:
  stable:
    paths:
      - core
  extension:
    paths:
      - x/ai
      - x/fileapi
      - x/websocket
`)

	roots, err := ReadRepoExtensionRoots(repo)
	if err != nil {
		t.Fatalf("ReadRepoExtensionRoots: %v", err)
	}

	for _, want := range []string{"x/ai", "x/fileapi", "x/websocket"} {
		if _, ok := roots[want]; !ok {
			t.Fatalf("expected %s in parsed extension roots, got %v", want, roots)
		}
	}
	if len(roots) != 3 {
		t.Fatalf("expected exactly 3 roots, got %d: %v", len(roots), roots)
	}
}

func TestFindOrphanedExtensionRootsReportsUndeclaredXDirs(t *testing.T) {
	repo := t.TempDir()
	for _, dir := range []string{"x/ai", "x/fileapi", "x/websocket"} {
		if err := os.MkdirAll(filepath.Join(repo, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	orphans, err := FindOrphanedExtensionRoots(repo, map[string]struct{}{
		"x/ai":        {},
		"x/websocket": {},
	})
	if err != nil {
		t.Fatalf("FindOrphanedExtensionRoots: %v", err)
	}
	if len(orphans) != 1 || orphans[0] != "x/fileapi" {
		t.Fatalf("unexpected orphaned extension roots: %v", orphans)
	}
}

func TestFindEmptyMisleadingDirsFlagsEmptyPackageLikeDirs(t *testing.T) {
	repo := t.TempDir()
	for _, dir := range []string{
		"contract/protocol",
		"x/fileapi",
		"x/data/file/migrations",
		"x/data/file/testdata",
	} {
		if err := os.MkdirAll(filepath.Join(repo, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	writeFile(t, filepath.Join(repo, "x", "fileapi", "handler.go"), "package fileapi\n")

	empty, err := FindEmptyMisleadingDirs(repo)
	if err != nil {
		t.Fatalf("FindEmptyMisleadingDirs: %v", err)
	}

	if len(empty) != 1 || empty[0] != "contract/protocol" {
		t.Fatalf("unexpected empty misleading dirs: %v", empty)
	}
}

func TestReadCanonicalExtensionEntrypointsParsesCanonicalRoots(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "specs", "extension-taxonomy.yaml"), `families:
  gateway:
    canonical_root: x/gateway
    roots:
      - x/gateway
  resource_api:
    canonical_root: x/rest
    roots:
      - x/rest
`)

	roots, err := ReadCanonicalExtensionEntrypoints(repo)
	if err != nil {
		t.Fatalf("ReadCanonicalExtensionEntrypoints: %v", err)
	}
	if len(roots) != 2 || roots[0] != "x/gateway" || roots[1] != "x/rest" {
		t.Fatalf("unexpected canonical entrypoints: %v", roots)
	}
}

func TestFindExtensionPrimerCoverageViolationsRequiresDocPaths(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "x", "gateway", "module.yaml"), validManifestWithDocPaths("x/gateway", "extension", "docs/modules/x/gateway/README.md"))
	writeFile(t, filepath.Join(repo, "docs", "modules", "x", "gateway", "README.md"), "# x/gateway\n")
	writeFile(t, filepath.Join(repo, "x", "rest", "module.yaml"), validManifest("x/rest", "extension"))

	violations, err := FindExtensionPrimerCoverageViolations(repo, []string{"x/gateway", "x/rest", "x/fileapi"})
	if err != nil {
		t.Fatalf("FindExtensionPrimerCoverageViolations: %v", err)
	}

	joined := strings.Join(violations, "\n")
	if !strings.Contains(joined, `x/rest is a canonical extension entrypoint`) {
		t.Fatalf("expected missing doc_paths violation for x/rest, got:\n%s", joined)
	}
	if !strings.Contains(joined, `x/fileapi is a canonical extension entrypoint but has no module.yaml`) {
		t.Fatalf("expected missing module.yaml violation for x/fileapi, got:\n%s", joined)
	}
	if strings.Contains(joined, "x/gateway") && !strings.Contains(joined, "x/fileapi") && !strings.Contains(joined, "x/rest") {
		t.Fatalf("unexpected violation set: %s", joined)
	}
}

func TestReadPackageIndexParsesPackagesAndStartPaths(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "specs", "package-hotspots.yaml"), `packages:
  x/fileapi:
    task_families:
      - file_api
    start_with:
      - x/fileapi/module.yaml
      - x/fileapi/handler.go
  contract:
    task_families:
      - http_endpoint
    start_with:
      - contract/module.yaml
`)

	entries, err := ReadPackageIndex(repo)
	if err != nil {
		t.Fatalf("ReadPackageIndex: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 package index entries, got %d: %v", len(entries), entries)
	}
	if got := entries["x/fileapi"].TaskFamilies; len(got) != 1 || got[0] != "file_api" {
		t.Fatalf("expected file_api task family for x/fileapi, got %v", got)
	}
	if got := entries["x/fileapi"].StartWith; len(got) != 2 {
		t.Fatalf("expected 2 start_with paths for x/fileapi, got %v", got)
	}
}

func TestFindPackageIndexCoverageViolationsRequiresExistingPackageAndStartFiles(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "x", "fileapi", "module.yaml"), "name: x/fileapi\n")
	writeFile(t, filepath.Join(repo, "contract", "module.yaml"), "name: contract\n")

	violations, err := FindPackageIndexCoverageViolations(repo, map[string]packageIndexEntry{
		"x/fileapi": {
			Path:      "x/fileapi",
			StartWith: []string{"x/fileapi/module.yaml", "x/fileapi/handler.go"},
		},
		"contract": {
			Path:      "contract",
			StartWith: nil,
		},
		"x/missing": {
			Path:      "x/missing",
			StartWith: []string{"x/missing/module.yaml"},
		},
	})
	if err != nil {
		t.Fatalf("FindPackageIndexCoverageViolations: %v", err)
	}

	joined := strings.Join(violations, "\n")
	for _, want := range []string{
		`specs/package-hotspots.yaml package x/fileapi references missing start_with path x/fileapi/handler.go`,
		`specs/package-hotspots.yaml package contract must declare at least one start_with path`,
		`specs/package-hotspots.yaml package x/missing does not exist in the repository`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected violation containing %q, got:\n%s", want, joined)
		}
	}
}

func TestFindPackageIndexTaskFamilyViolations(t *testing.T) {
	violations := FindPackageIndexTaskFamilyViolations(
		map[string]packageIndexEntry{
			"x/fileapi": {
				Path:         "x/fileapi",
				TaskFamilies: []string{"file_api", "missing_family"},
			},
			"contract": {
				Path: "contract",
			},
		},
		map[string]taskRoutingEntry{
			"file_api": {},
		},
	)

	joined := strings.Join(violations, "\n")
	for _, want := range []string{
		`specs/package-hotspots.yaml package contract must declare at least one task_families entry`,
		`specs/package-hotspots.yaml package x/fileapi references unknown task_families entry missing_family`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected violation containing %q, got:\n%s", want, joined)
		}
	}
}

func TestFindStableHTTPSurfaceViolationsFlagsAppFacingHandlersOutsideCoreAndRouter(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "store", "blob", "handler.go"), `package blob

import "net/http"

func Upload(w http.ResponseWriter, r *http.Request) {}
`)
	writeFile(t, filepath.Join(repo, "health", "admin", "routes.go"), `package admin

import "github.com/birdordev/plumego/router"

func RegisterRoutes(r *router.Router) {}
`)
	writeFile(t, filepath.Join(repo, "core", "routing.go"), `package core

import "net/http"

type App struct{}

func (a *App) HandleFunc(pattern string, handler http.HandlerFunc) {}
`)
	writeFile(t, filepath.Join(repo, "metrics", "exporter.go"), `package metrics

import "net/http"

type Exporter struct{}

func (e *Exporter) Handler() http.Handler { return nil }
`)

	violations, err := FindStableHTTPSurfaceViolations(repo)
	if err != nil {
		t.Fatalf("FindStableHTTPSurfaceViolations: %v", err)
	}

	joined := strings.Join(violations, "\n")
	for _, want := range []string{
		`stable package health/admin/routes.go exposes route registration helper RegisterRoutes`,
		`stable package store/blob/handler.go exposes app-facing HTTP handler surface Upload`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected violation containing %q, got:\n%s", want, joined)
		}
	}
	for _, unwanted := range []string{
		"core/routing.go",
		"metrics/exporter.go",
	} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("did not expect %q in violations:\n%s", unwanted, joined)
		}
	}
}

func validManifest(path, layer string) string {
	return "name: " + path + "\n" +
		"path: " + path + "\n" +
		"layer: " + layer + "\n" +
		"status: ga\n" +
		"owner: runtime\n" +
		"risk: medium\n" +
		"summary: example\n" +
		"responsibilities:\n  - keep scope tight\n" +
		"non_goals:\n  - do not sprawl\n" +
		"allowed_imports:\n  - stdlib\n" +
		"forbidden_imports:\n  - x/**\n" +
		"test_commands:\n  - go test ./...\n" +
		"review_checklist:\n  - stay explicit\n" +
		"agent_hints:\n  - keep modules small\n"
}

func validManifestWithDocPaths(path, layer string, docPaths ...string) string {
	manifest := validManifest(path, layer)
	if len(docPaths) == 0 {
		return manifest
	}
	manifest += "doc_paths:\n"
	for _, docPath := range docPaths {
		manifest += "  - " + docPath + "\n"
	}
	return manifest
}

func validManifestWithStrictBoundary(path, layer, boundary string) string {
	return validManifest(path, layer) + "strict_boundary: " + boundary + "\n"
}

func validManifestWithParentFamily(path, layer, parent string) string {
	return validManifest(path, layer) + "parent_family: " + parent + "\n"
}

func validManifestWithSubordinateFamilies(path, layer, sub string) string {
	return validManifest(path, layer) + "subordinate_families:\n  - " + sub + "\n"
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writeRepoSpec(t *testing.T, repo string) {
	t.Helper()
	writeFile(t, filepath.Join(repo, "specs", "repo.yaml"), `repo:
  module: github.com/birdordev/plumego
layers:
  stable:
    paths:
      - core
      - router
      - contract
      - middleware
      - security
      - store
      - health
      - log
      - metrics
  extension:
    paths:
      - x/ai
      - x/fileapi
      - x/websocket
`)
}

func writeManifestSchema(t *testing.T, repo string) {
	t.Helper()
	writeFile(t, filepath.Join(repo, "specs", "module-manifest.schema.yaml"), `required:
  - name
  - path
  - layer
  - status
  - owner
  - risk
  - summary
  - responsibilities
  - non_goals
  - allowed_imports
  - forbidden_imports
  - test_commands
  - review_checklist
  - agent_hints

enums:
  layer:
    - stable
    - extension
    - tooling
    - reference
  status:
    - ga
    - beta
    - experimental
  risk:
    - low
    - medium
    - high
    - critical

limits:
  responsibilities_max: 7
  non_goals_max: 7
  allowed_imports_max: 12
  forbidden_imports_max: 20
  test_commands_max: 3
  review_checklist_max: 5
  agent_hints_max: 3
`)
}

func writeDependencyRulesSpec(t *testing.T, repo string) {
	t.Helper()
	writeFile(t, filepath.Join(repo, "specs", "dependency-rules.yaml"), `modules:
  core:
    path: core
    deny:
      - x/**
special_rules:
  forbidden_paths:
    - plumego.go
  forbidden_import_patterns:
    - github.com/birdordev/plumego
`)
}

func writeExtensionTaxonomySpec(t *testing.T, repo string) {
	t.Helper()
	writeFile(t, filepath.Join(repo, "specs", "extension-taxonomy.yaml"), `families:
  tenant:
    canonical_root: x/tenant
    roots:
      - x/tenant
  messaging:
    canonical_root: x/messaging
    roots:
      - x/messaging
      - x/messaging/mq
  resource_api:
    canonical_root: x/rest
    roots:
      - x/rest
  gateway:
    canonical_root: x/gateway
    roots:
      - x/gateway
`)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
