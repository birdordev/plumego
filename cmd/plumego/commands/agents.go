package commands

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/birdordev/plumego/cmd/plumego/internal/agentmanifest"
	"github.com/birdordev/plumego/cmd/plumego/internal/executil"
	"github.com/birdordev/plumego/cmd/plumego/internal/taskbundle"
	"gopkg.in/yaml.v3"
)

// AgentsCmd provides agent-oriented subcommands for the agent workflow.
type AgentsCmd struct{}

func (c *AgentsCmd) Name() string { return "agents" }
func (c *AgentsCmd) Short() string {
	return "Agent workflow helpers: verify, explain, bundle, validate-diff, route, list-modules"
}

func (c *AgentsCmd) Help() CommandHelp {
	return CommandHelp{
		Args: "<verify|explain|bundle|validate-diff|route|list-modules> [command-flags]",
		Subcommands: []HelpItem{
			{"verify --changed <module>", "Run validation gates for a changed module"},
			{"explain --module <module>", "Print module manifest: responsibilities, boundaries, agent hints"},
			{"bundle --task <type> --module <path>", "Generate a single-file task execution context (YAML)"},
			{"validate-diff [--base <ref>] [--dry-run]", "Auto-select and run the minimal gate profile for current git diff"},
			{"route --task <type>", "Routing oracle: start_with docs, avoid paths, and suggested recipe for a task type"},
			{"list-modules [--layer stable|extension]", "List all known modules with layer, status, risk, and summary"},
		},
		Examples: []string{
			"plumego agents verify --changed log",
			"plumego agents verify --changed middleware --dir /path/to/repo",
			"plumego agents explain --module core",
			"plumego agents explain --module x/rest",
			"plumego agents bundle --task http_endpoint --module x/tenant",
			"plumego agents bundle --task middleware --module middleware --output .agent-bundle.yaml",
			"plumego agents validate-diff",
			"plumego agents validate-diff --base origin/main",
			"plumego agents validate-diff --dry-run",
			"plumego agents route --task http_endpoint",
			"plumego agents route --task add-middleware",
			"plumego agents route --path middleware/timeout/timeout.go",
			"plumego agents list-modules",
			"plumego agents list-modules --layer stable",
		},
	}
}

func (c *AgentsCmd) Run(ctx *Context, args []string) error {
	if len(args) == 0 {
		return ctx.Out.Error("agents requires a subcommand: verify, explain, bundle, validate-diff, route, list-modules", 1, map[string]any{
			"hint": "run plumego agents --help",
		})
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "verify":
		return runAgentsVerify(ctx, rest)
	case "explain":
		return runAgentsExplain(ctx, rest)
	case "bundle":
		return runAgentsBundle(ctx, rest)
	case "validate-diff":
		return runAgentsValidateDiff(ctx, rest)
	case "route":
		return runAgentsRoute(ctx, rest)
	case "list-modules":
		return runAgentsListModules(ctx, rest)
	default:
		return ctx.Out.Error(fmt.Sprintf("unknown agents subcommand: %s", sub), 1, map[string]any{
			"hint": "run plumego agents --help",
		})
	}
}

// ─── verify ───────────────────────────────────────────────────────────────────

func runAgentsVerify(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("agents verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	changed := fs.String("changed", "", "Module path to verify (e.g. log, middleware, x/rest)")
	dir := fs.String("dir", ".", "Repository root directory")
	timeoutStr := fs.String("timeout", "120s", "Per-command timeout")

	positionals, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}
	if len(positionals) > 0 {
		return ctx.Out.Error(fmt.Sprintf("unexpected arguments: %v", positionals), 1)
	}
	if *changed == "" {
		return ctx.Out.Error("--changed is required", 1, map[string]any{
			"hint": "plumego agents verify --changed <module>",
		})
	}

	timeout, err := time.ParseDuration(*timeoutStr)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("invalid timeout: %v", err), 1)
	}

	repoRoot, err := agentmanifest.FindRepoRoot(*dir)
	if err != nil {
		return ctx.Out.Error(err.Error(), 1)
	}

	manifest, err := agentmanifest.Load(repoRoot, *changed)
	if err != nil {
		return ctx.Out.Error(err.Error(), 1)
	}

	if len(manifest.TestCommands) == 0 {
		return ctx.Out.Error(fmt.Sprintf("module %s has no test_commands in module.yaml", *changed), 1)
	}

	type commandResult struct {
		Command  string `json:"command" yaml:"command"`
		Status   string `json:"status" yaml:"status"`
		Output   string `json:"output,omitempty" yaml:"output,omitempty"`
		Duration string `json:"duration" yaml:"duration"`
	}

	results := make([]commandResult, 0, len(manifest.TestCommands))
	allPassed := true

	for _, cmdStr := range manifest.TestCommands {
		ctx.Out.Verbose(fmt.Sprintf("running: %s", cmdStr))

		name, cmdArgs := agentmanifest.SplitCommand(cmdStr)
		if name == "" {
			continue
		}

		start := time.Now()
		result, runErr := executil.Run(context.Background(), executil.Options{
			Name:    name,
			Args:    cmdArgs,
			Dir:     repoRoot,
			Timeout: timeout,
		})
		elapsed := time.Since(start)

		cr := commandResult{
			Command:  cmdStr,
			Duration: elapsed.Round(time.Millisecond).String(),
		}

		if runErr != nil || result.TimedOut {
			cr.Status = "failed"
			cr.Output = strings.TrimSpace(result.CombinedOutput())
			allPassed = false
		} else {
			cr.Status = "passed"
			if ctx.Out.IsVerbose() {
				cr.Output = strings.TrimSpace(result.CombinedOutput())
			}
		}
		results = append(results, cr)
	}

	payload := map[string]any{
		"module":   *changed,
		"commands": results,
	}

	if allPassed {
		return ctx.Out.Success(fmt.Sprintf("all gates passed for module: %s", *changed), payload)
	}
	return ctx.Out.Error(fmt.Sprintf("one or more gates failed for module: %s", *changed), 1, payload)
}

// ─── explain ──────────────────────────────────────────────────────────────────

func runAgentsExplain(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("agents explain", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	module := fs.String("module", "", "Module path to explain (e.g. core, middleware, x/rest)")
	dir := fs.String("dir", ".", "Repository root directory")

	positionals, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}
	if len(positionals) > 0 {
		return ctx.Out.Error(fmt.Sprintf("unexpected arguments: %v", positionals), 1)
	}
	if *module == "" {
		return ctx.Out.Error("--module is required", 1, map[string]any{
			"hint": "plumego agents explain --module <module>",
		})
	}

	repoRoot, err := agentmanifest.FindRepoRoot(*dir)
	if err != nil {
		return ctx.Out.Error(err.Error(), 1)
	}

	m, err := agentmanifest.Load(repoRoot, *module)
	if err != nil {
		return ctx.Out.Error(err.Error(), 1)
	}

	payload := map[string]any{
		"name":              m.Name,
		"path":              m.Path,
		"layer":             m.Layer,
		"status":            m.Status,
		"owner":             m.Owner,
		"risk":              m.Risk,
		"summary":           m.Summary,
		"responsibilities":  m.Responsibilities,
		"non_goals":         m.NonGoals,
		"allowed_imports":   m.AllowedImports,
		"forbidden_imports": m.ForbiddenImports,
		"test_commands":     m.TestCommands,
		"stop_conditions":   m.StopConditions,
		"agent_hints":       m.AgentHints,
	}

	if ctx.Out.IsVerbose() {
		payload["entrypoint_reads"] = m.EntrypointReads
		payload["review_checklist"] = m.ReviewChecklist
		payload["change_risks"] = m.ChangeRisks
		payload["public_entrypoints"] = m.PublicEntrypoints
	}

	return ctx.Out.Success(fmt.Sprintf("module manifest: %s", m.Name), payload)
}

// ─── bundle ───────────────────────────────────────────────────────────────────

func runAgentsBundle(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("agents bundle", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	taskType := fs.String("task", "", "Task type from task-routing.yaml (e.g. http_endpoint, middleware, bugfix_triage)")
	modulePath := fs.String("module", "", "Module path to work in (e.g. x/tenant, middleware, core)")
	dir := fs.String("dir", ".", "Repository root directory")
	output := fs.String("output", "", "Write bundle to file instead of stdout (e.g. .agent-bundle.yaml)")

	positionals, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}
	if len(positionals) > 0 {
		return ctx.Out.Error(fmt.Sprintf("unexpected arguments: %v", positionals), 1)
	}
	if *taskType == "" {
		return ctx.Out.Error("--task is required", 1, map[string]any{
			"hint":    "plumego agents bundle --task <type> --module <path>",
			"example": "plumego agents bundle --task http_endpoint --module x/tenant",
		})
	}
	if *modulePath == "" {
		return ctx.Out.Error("--module is required", 1, map[string]any{
			"hint":    "plumego agents bundle --task <type> --module <path>",
			"example": "plumego agents bundle --task http_endpoint --module x/tenant",
		})
	}

	repoRoot, err := agentmanifest.FindRepoRoot(*dir)
	if err != nil {
		return ctx.Out.Error(err.Error(), 1)
	}

	b, err := taskbundle.Generate(repoRoot, *taskType, *modulePath)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("generate bundle: %v", err), 1)
	}

	// Write to file when --output is specified: always raw YAML (file artifact).
	if *output != "" {
		f, err := os.Create(*output)
		if err != nil {
			return ctx.Out.Error(fmt.Sprintf("create output file: %v", err), 1)
		}
		defer f.Close()
		enc := yaml.NewEncoder(f)
		enc.SetIndent(2)
		if err := enc.Encode(b); err != nil {
			return ctx.Out.Error(fmt.Sprintf("encode bundle: %v", err), 1)
		}
		if err := enc.Close(); err != nil {
			return ctx.Out.Error(fmt.Sprintf("flush bundle: %v", err), 1)
		}
		return ctx.Out.Success(fmt.Sprintf("bundle written to %s", *output), map[string]any{
			"task":   *taskType,
			"module": *modulePath,
			"output": *output,
		})
	}

	// No --output: machine formats (json/yaml) wrap in commandResult envelope;
	// text mode streams raw YAML for human readability.
	if ctx.Out.Format() != "text" {
		return ctx.Out.Success(fmt.Sprintf("bundle: %s / %s", *taskType, *modulePath), map[string]any{
			"task":   *taskType,
			"module": *modulePath,
			"bundle": b,
		})
	}

	if ctx.Out.IsQuiet() {
		return nil
	}
	enc := yaml.NewEncoder(os.Stdout)
	enc.SetIndent(2)
	if err := enc.Encode(b); err != nil {
		return ctx.Out.Error(fmt.Sprintf("encode bundle: %v", err), 1)
	}
	return enc.Close()
}
