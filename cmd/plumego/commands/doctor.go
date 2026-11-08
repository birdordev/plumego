package commands

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/birdordev/plumego/cmd/plumego/internal/agentmanifest"
)

// DoctorCmd checks repository agent-readiness.
type DoctorCmd struct{}

func (c *DoctorCmd) Name() string  { return "doctor" }
func (c *DoctorCmd) Short() string { return "Check repository agent-readiness" }

func (c *DoctorCmd) Help() CommandHelp {
	return CommandHelp{
		Args: "[command-flags]",
		Flags: []HelpItem{
			{"--dir <path>", "Repository root directory (default: auto-detected)"},
		},
		Examples: []string{
			"plumego doctor",
			"plumego doctor --dir /path/to/repo",
			"plumego --format text doctor",
		},
	}
}

// doctorCheck is a single readiness check result.
type doctorCheck struct {
	Name    string `json:"name" yaml:"name"`
	Status  string `json:"status" yaml:"status"` // passed, warning, failed
	Message string `json:"message" yaml:"message"`
	Fix     string `json:"fix,omitempty" yaml:"fix,omitempty"`
}

func (c *DoctorCmd) Run(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	dir := fs.String("dir", ".", "Repository root directory")

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

	ctx.Out.Verbose(fmt.Sprintf("checking repo at: %s", repoRoot))

	var checks []doctorCheck

	// ── 1. Required spec files ────────────────────────────────────────────────
	for _, specPath := range agentmanifest.KnownSpecFiles() {
		full := filepath.Join(repoRoot, specPath)
		if _, statErr := os.Stat(full); statErr == nil {
			checks = append(checks, doctorCheck{
				Name:    fmt.Sprintf("spec: %s", specPath),
				Status:  "passed",
				Message: "present",
			})
		} else {
			checks = append(checks, doctorCheck{
				Name:    fmt.Sprintf("spec: %s", specPath),
				Status:  "failed",
				Message: "missing",
				Fix:     fmt.Sprintf("create %s — see docs/concepts/agent-first.md for the required format", specPath),
			})
		}
	}

	// ── 2. Stable root module.yaml files ─────────────────────────────────────
	for _, root := range agentmanifest.KnownStableRoots() {
		manifestPath := filepath.Join(repoRoot, root, "module.yaml")
		if _, statErr := os.Stat(manifestPath); statErr == nil {
			checks = append(checks, doctorCheck{
				Name:    fmt.Sprintf("module.yaml: %s", root),
				Status:  "passed",
				Message: "present",
			})
		} else {
			checks = append(checks, doctorCheck{
				Name:    fmt.Sprintf("module.yaml: %s", root),
				Status:  "failed",
				Message: "missing",
				Fix:     fmt.Sprintf("add %s/module.yaml with name, status, responsibilities, test_commands, and agent_hints", root),
			})
		}
	}

	// ── 3. Reference app AGENT_TASKS.md files ────────────────────────────────
	for _, ref := range agentmanifest.KnownReferenceApps() {
		tasksPath := filepath.Join(repoRoot, ref, "AGENT_TASKS.md")
		if _, statErr := os.Stat(tasksPath); statErr == nil {
			checks = append(checks, doctorCheck{
				Name:    fmt.Sprintf("AGENT_TASKS.md: %s", ref),
				Status:  "passed",
				Message: "present",
			})
		} else {
			checks = append(checks, doctorCheck{
				Name:    fmt.Sprintf("AGENT_TASKS.md: %s", ref),
				Status:  "warning",
				Message: "missing",
				Fix:     fmt.Sprintf("add %s/AGENT_TASKS.md with zone classification and task recipes", ref),
			})
		}
	}

	// ── 4. Agent-first docs ───────────────────────────────────────────────────
	agentDocs := []string{
		"docs/concepts/agent-first.md",
		"docs/concepts/core-boundary.md",
		"docs/concepts/extension-boundary.md",
	}
	for _, docPath := range agentDocs {
		full := filepath.Join(repoRoot, docPath)
		if _, statErr := os.Stat(full); statErr == nil {
			checks = append(checks, doctorCheck{
				Name:    fmt.Sprintf("doc: %s", docPath),
				Status:  "passed",
				Message: "present",
			})
		} else {
			checks = append(checks, doctorCheck{
				Name:    fmt.Sprintf("doc: %s", docPath),
				Status:  "warning",
				Message: "missing",
				Fix:     fmt.Sprintf("create %s — see docs/concepts/agent-first.md for recommended content", docPath),
			})
		}
	}

	// ── Summarise ─────────────────────────────────────────────────────────────
	var failed, warned, passed int
	for _, ch := range checks {
		switch ch.Status {
		case "failed":
			failed++
		case "warning":
			warned++
		default:
			passed++
		}
	}

	overallStatus := "ready"
	if failed > 0 {
		overallStatus = "not-ready"
	} else if warned > 0 {
		overallStatus = "partial"
	}

	payload := map[string]any{
		"status": overallStatus,
		"passed": passed,
		"warned": warned,
		"failed": failed,
		"checks": checks,
		"repo":   repoRoot,
	}

	msg := fmt.Sprintf("doctor: %d passed, %d warnings, %d failed", passed, warned, failed)

	switch overallStatus {
	case "not-ready":
		return ctx.Out.Error(msg, 1, payload)
	case "partial":
		return ctx.Out.Warning(msg, 2, payload)
	default:
		return ctx.Out.Success(msg, payload)
	}
}
