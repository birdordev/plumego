package main

import (
	"fmt"
	"os"

	"github.com/birdordev/plumego/internal/checks/checkutil"
)

func main() {
	repoRoot, err := os.Getwd()
	if err != nil {
		failf("resolve working directory: %v", err)
	}

	missing, err := checkutil.FindMissingModuleManifests(repoRoot)
	if err != nil {
		failf("find missing module manifests: %v", err)
	}
	violations, err := checkutil.ValidateModuleManifests(repoRoot)
	if err != nil {
		failf("validate module manifests: %v", err)
	}
	boundaryViolations, err := checkutil.ValidateStableBoundaryDeclarations(repoRoot)
	if err != nil {
		failf("validate stable boundary declarations: %v", err)
	}
	violations = append(violations, boundaryViolations...)
	xGoModFiles, err := checkutil.FindXGoModFiles(repoRoot)
	if err != nil {
		failf("find x go.mod files: %v", err)
	}
	for _, path := range xGoModFiles {
		violations = append(violations, path+": x/* packages must not contain go.mod")
	}

	if len(missing) == 0 && len(violations) == 0 {
		return
	}

	all := make([]string, 0, len(missing)+len(violations))
	for _, item := range missing {
		all = append(all, "missing module.yaml for "+item)
	}
	all = append(all, violations...)
	fmt.Fprint(os.Stderr, checkutil.FormatViolations("module-manifests", all))
	os.Exit(1)
}

func failf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
