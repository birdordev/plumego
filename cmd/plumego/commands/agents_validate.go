package commands

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/birdordev/plumego/cmd/plumego/internal/agentmanifest"
	"github.com/birdordev/plumego/cmd/plumego/internal/executil"
	"github.com/birdordev/plumego/cmd/plumego/internal/gateprofile"
)

// runAgentsValidateDiff inspects the current git diff, selects the minimal
// sufficient gate profile from specs/gate-profiles.yaml, and runs those gates.
func runAgentsValidateDiff(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("agents validate-diff", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	base := fs.String("base", "HEAD", "Git ref to diff against (e.g. HEAD, origin/main)")
	dir := fs.String("dir", ".", "Repository root directory")
	dryRun := fs.Bool("dry-run", false, "Print selected gate commands without executing them")
	timeoutStr := fs.String("timeout", "10m", "Per-command timeout")

	positionals, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}
	if len(positionals) > 0 {
		return ctx.Out.Error(fmt.Sprintf("unexpected arguments: %v", positionals), 1)
	}

	timeout, err := time.ParseDuration(*timeoutStr)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("invalid timeout: %v", err), 1)
	}

	repoRoot, err := agentmanifest.FindRepoRoot(*dir)
	if err != nil {
		return ctx.Out.Error(err.Error(), 1)
	}

	changedFiles, err := collectChangedFiles(repoRoot, *base)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("collect changed files: %v", err), 1)
	}

	if len(changedFiles) == 0 {
		return ctx.Out.Success("no changed files detected; nothing to gate", map[string]any{
			"base": *base,
		})
	}

	primary, multi := detectModules(changedFiles)

	gp, err := gateprofile.Load(repoRoot)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("load gate-profiles.yaml: %v", err), 1)
	}

	profileName, profile := gateprofile.Select(gp, changedFiles, primary, multi)

	// Substitute {module} placeholder in commands.
	modPath := primary
	if modPath == "" {
		modPath = "."
	}
	commands := make([]string, len(profile.Commands))
	for i, cmd := range profile.Commands {
		commands[i] = strings.ReplaceAll(cmd, "{module}", modPath)
	}

	if *dryRun {
		return ctx.Out.Success(fmt.Sprintf("selected profile: %s (dry-run)", profileName), map[string]any{
			"base":          *base,
			"profile":       profileName,
			"module":        primary,
			"multi_module":  multi,
			"changed_files": changedFiles,
			"commands":      commands,
		})
	}

	type commandResult struct {
		Command  string `json:"command" yaml:"command"`
		Status   string `json:"status" yaml:"status"`
		Output   string `json:"output,omitempty" yaml:"output,omitempty"`
		Duration string `json:"duration" yaml:"duration"`
	}

	results := make([]commandResult, 0, len(commands))
	allPassed := true

	for i, cmdStr := range commands {
		ctx.Out.Verbose(fmt.Sprintf("running: %s", cmdStr))
		name, cmdArgs := agentmanifest.SplitCommand(profile.Commands[i])
		if name == "" {
			continue
		}
		for i, arg := range cmdArgs {
			cmdArgs[i] = strings.ReplaceAll(arg, "{module}", modPath)
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
		"base":          *base,
		"profile":       profileName,
		"module":        primary,
		"multi_module":  multi,
		"changed_files": changedFiles,
		"results":       results,
	}

	if allPassed {
		return ctx.Out.Success(fmt.Sprintf("all gates passed [profile: %s]", profileName), payload)
	}
	return ctx.Out.Error(fmt.Sprintf("gates failed [profile: %s]", profileName), 1, payload)
}

// ─── git helpers ──────────────────────────────────────────────────────────────

// collectChangedFiles returns the deduplicated set of repo-relative file paths
// that differ from base, are staged, or are untracked.
func collectChangedFiles(repoRoot, base string) ([]string, error) {
	commands := [][]string{
		{"diff", "--name-only", "-z", "--no-renames", "--end-of-options", base, "--"},
		// An unstaged edit can cancel a staged change in the base-to-worktree diff.
		{"diff", "--name-only", "-z", "--no-renames", "--cached", "--"},
		{"ls-files", "-z", "--others", "--exclude-standard"},
	}
	seen := make(map[string]bool)
	var files []string
	for _, args := range commands {
		result, err := executil.Run(context.Background(), executil.Options{
			Name: "git", Args: args, Dir: repoRoot, Timeout: 15 * time.Second,
		})
		if err != nil {
			return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(result.Stderr))
		}
		if result.StdoutTruncated {
			return nil, fmt.Errorf("git %s returned an incomplete file list", args[0])
		}
		for file := range strings.SplitSeq(result.Stdout, "\x00") {
			if file != "" && !seen[file] {
				seen[file] = true
				files = append(files, file)
			}
		}
	}
	return files, nil
}

// ─── module detection ─────────────────────────────────────────────────────────

// detectModules returns the primary module name and whether multiple modules
// are involved. x/* paths are treated as a single module per x/<family>.
func detectModules(changedFiles []string) (primary string, multi bool) {
	moduleSet := map[string]bool{}
	for _, f := range changedFiles {
		if !strings.Contains(f, "/") {
			moduleSet["."] = true
			continue
		}
		if m := moduleFromFilePath(f); m != "" {
			moduleSet[m] = true
		}
	}
	if len(moduleSet) == 0 {
		return "", false
	}
	if len(moduleSet) > 1 {
		// Prefer a stable root as the primary slot for profile selection.
		for _, s := range agentmanifest.KnownStableRoots() {
			if moduleSet[s] {
				return s, true
			}
		}
		for m := range moduleSet {
			return m, true
		}
	}
	for m := range moduleSet {
		return m, false
	}
	return "", false
}

// moduleFromFilePath extracts the module prefix from a repo-relative path.
//
//	"middleware/timeout/timeout.go"                         → "middleware"
//	"x/tenant/resolver.go"                                 → "x/tenant"
//	"reference/standard-service/internal/app/app_test.go"  → "reference/standard-service"
//	"specs/gate-profiles.yaml"                             → "specs"
func moduleFromFilePath(path string) string {
	path = filepath.ToSlash(path)
	parts := strings.SplitN(path, "/", 3)
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	// Group extensions by family and standalone projects by directory.
	switch parts[0] {
	case "x", "reference", "examples":
		if len(parts) >= 2 {
			return parts[0] + "/" + parts[1]
		}
	}
	return parts[0]
}
