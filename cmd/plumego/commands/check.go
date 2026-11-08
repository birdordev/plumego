package commands

import (
	"flag"
	"fmt"
	"io"

	"github.com/birdordev/plumego/cmd/plumego/internal/checker"
)

// CheckCmd validates project health
type CheckCmd struct{}

func (c *CheckCmd) Name() string  { return "check" }
func (c *CheckCmd) Short() string { return "Validate project health" }

func (c *CheckCmd) Run(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	configOnly := fs.Bool("config-only", false, "Only check configuration")
	depsOnly := fs.Bool("deps-only", false, "Only check dependencies")
	security := fs.Bool("security", false, "Run security checks")
	updates := fs.Bool("updates", false, "Check for available dependency updates")
	dir := fs.String("dir", ".", "Project directory")

	positionals, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}
	if len(positionals) > 0 {
		return ctx.Out.Error(fmt.Sprintf("unexpected arguments: %v", positionals), 1)
	}

	projectDir, err := resolveDir(*dir)
	if err != nil {
		return ctx.Out.Error(err.Error(), 1)
	}

	ctx.Out.Verbose(fmt.Sprintf("Checking project at: %s", projectDir))

	checks := &checker.CheckResult{
		Status: "healthy",
		Checks: make(map[string]checker.CheckDetail),
	}

	if !*depsOnly {
		ctx.Out.Verbose("Running configuration checks...")
		configCheck := checker.CheckConfig(projectDir, ctx.EnvFile)
		checks.Checks["config"] = configCheck
		if configCheck.Status == "failed" {
			checks.Status = "unhealthy"
		}
	}

	if !*configOnly {
		ctx.Out.Verbose("Running dependency checks...")
		depsCheck := checker.CheckDependencies(projectDir, checker.DependencyOptions{CheckUpdates: *updates})
		checks.Checks["dependencies"] = depsCheck
		if depsCheck.Status == "failed" {
			checks.Status = "unhealthy"
		} else if depsCheck.Status == "warning" && checks.Status == "healthy" {
			checks.Status = "degraded"
		}
	}

	if *security && !*configOnly && !*depsOnly {
		ctx.Out.Verbose("Running security checks...")
		securityCheck := checker.CheckSecurity(projectDir, ctx.EnvFile)
		checks.Checks["security"] = securityCheck
		if securityCheck.Status == "failed" {
			checks.Status = "unhealthy"
		} else if securityCheck.Status == "warning" && checks.Status == "healthy" {
			checks.Status = "degraded"
		}
	}

	if !*configOnly && !*depsOnly {
		ctx.Out.Verbose("Running project structure checks...")
		structureCheck := checker.CheckStructure(projectDir)
		checks.Checks["structure"] = structureCheck
		if structureCheck.Status == "warning" && checks.Status == "healthy" {
			checks.Status = "degraded"
		}
	}

	switch checks.Status {
	case "healthy":
		return ctx.Out.Success("All checks passed", checks)
	case "degraded":
		return ctx.Out.Warning("Checks completed with warnings", 2, checks)
	default:
		return ctx.Out.Error("Checks failed", 1, checks)
	}
}
