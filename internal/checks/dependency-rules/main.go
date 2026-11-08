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

	violations, err := checkutil.FindDisallowedImports(repoRoot)
	if err != nil {
		failf("run dependency rules check: %v", err)
	}
	controlPlaneViolations, err := checkutil.ValidateManifestDependencyRuleConsistency(repoRoot)
	if err != nil {
		failf("validate dependency rule control plane: %v", err)
	}
	violations = append(violations, controlPlaneViolations...)
	internalCallerViolations, err := checkutil.ValidateDeclaredInternalCallers(repoRoot)
	if err != nil {
		failf("validate declared internal callers: %v", err)
	}
	violations = append(violations, internalCallerViolations...)
	if len(violations) == 0 {
		return
	}

	fmt.Fprint(os.Stderr, checkutil.FormatViolations("dependency-rules", violations))
	os.Exit(1)
}

func failf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
