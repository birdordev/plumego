package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/birdordev/plumego/internal/checks/checkutil"
)

func main() {
	repoRoot, err := os.Getwd()
	if err != nil {
		failf("resolve working directory: %v", err)
	}

	violations, err := checkutil.FindUnexpectedTopLevelDirs(repoRoot, checkutil.AllowedTopLevelDirs())
	if err != nil {
		failf("check top-level layout: %v", err)
	}
	violations = append(violations, requiredPathViolations(repoRoot)...)

	moduleViolations, err := checkutil.ValidateReferenceModules(repoRoot)
	if err != nil {
		failf("validate reference modules: %v", err)
	}
	for _, v := range moduleViolations {
		violations = append(violations, "reference module: "+v)
	}

	// Verify reference/standard-service has no x/* imports (canonical drift check).
	xViolations, err := checkutil.FindReferenceXImports(repoRoot, "reference/standard-service")
	if err != nil {
		failf("check canonical reference x/* drift: %v", err)
	}
	for _, v := range xViolations {
		violations = append(violations, "canonical reference imports x/*: "+v)
	}

	// Verify x/* family taxonomy: subordinate_families on primary families,
	// valid parent_family references on subordinate packages.
	taxonomyViolations, err := checkutil.ValidateXFamilyTaxonomy(repoRoot)
	if err != nil {
		failf("validate x/* family taxonomy: %v", err)
	}
	for _, v := range taxonomyViolations {
		violations = append(violations, "x/* taxonomy: "+v)
	}

	if len(violations) == 0 {
		return
	}

	fmt.Fprint(os.Stderr, checkutil.FormatViolations("reference-layout", violations))
	os.Exit(1)
}

func requiredPathViolations(repoRoot string) []string {
	required := []string{
		"docs/reference/canonical-style-guide.md",
		"docs/start/adoption-path.md",
		"docs/reference/scaffold-reference-contract.md",
		"docs/concepts/agent-first-repo-blueprint.md",
		"docs/release/PRE_V1_RELEASE_CHECKLIST.md",
		"docs/evidence/stable-api/README.md",
		"docs/evidence/stable-api/snapshots/contract-head.snapshot",
		"docs/evidence/stable-api/snapshots/core-head.snapshot",
		"docs/evidence/stable-api/snapshots/health-head.snapshot",
		"docs/evidence/stable-api/snapshots/log-head.snapshot",
		"docs/evidence/stable-api/snapshots/metrics-head.snapshot",
		"docs/evidence/stable-api/snapshots/middleware-head.snapshot",
		"docs/evidence/stable-api/snapshots/router-head.snapshot",
		"docs/evidence/stable-api/snapshots/security-head.snapshot",
		"docs/evidence/stable-api/snapshots/store-head.snapshot",
		"reference/standard-service",
		"reference/with-messaging",
		"reference/with-gateway",
		"reference/with-websocket",
		"reference/with-webhook",
		"specs/repo.yaml",
		"specs/checks.yaml",
		"specs/task-routing.yaml",
		"specs/extension-taxonomy.yaml",
		"specs/package-hotspots.yaml",
		"specs/dependency-rules.yaml",
		"tasks/milestones/STATUS.md",
	}

	var violations []string
	for _, rel := range required {
		if _, err := os.Stat(filepath.Join(repoRoot, rel)); err != nil {
			if os.IsNotExist(err) {
				violations = append(violations, "missing required path "+rel)
				continue
			}
			violations = append(violations, fmt.Sprintf("stat %s: %v", rel, err))
		}
	}
	return violations
}

func failf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
