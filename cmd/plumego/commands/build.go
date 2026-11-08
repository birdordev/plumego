package commands

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/birdordev/plumego/cmd/plumego/internal/buildtarget"
	"github.com/birdordev/plumego/cmd/plumego/internal/executil"
)

type BuildCmd struct{}

func (c *BuildCmd) Name() string  { return "build" }
func (c *BuildCmd) Short() string { return "Build application with optimizations" }

func (c *BuildCmd) Run(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	dir := fs.String("dir", ".", "Project directory")
	outputPath := fs.String("output", "./bin/app", "Output binary path")
	ldflags := fs.String("ldflags", "", "Go linker flags")
	tags := fs.String("tags", "", "Build tags")
	race := fs.Bool("race", false, "Enable race detector")
	trimpath := fs.Bool("trimpath", true, "Remove file system paths")

	positionals, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}
	if len(positionals) > 1 {
		return ctx.Out.Error(fmt.Sprintf("unexpected arguments: %v", positionals[1:]), 1)
	}

	absDir, err := resolveDir(*dir)
	if err != nil {
		return ctx.Out.Error(err.Error(), 1)
	}

	absOutput, err := filepath.Abs(*outputPath)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("invalid output path: %v", err), 1)
	}

	outputDir := filepath.Dir(absOutput)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return ctx.Out.Error(fmt.Sprintf("failed to create output directory: %v", err), 1)
	}

	buildArgs := []string{"build"}

	if *race {
		buildArgs = append(buildArgs, "-race")
	}

	if *trimpath {
		buildArgs = append(buildArgs, "-trimpath")
	}

	if *ldflags != "" {
		buildArgs = append(buildArgs, "-ldflags", *ldflags)
	}

	if *tags != "" {
		buildArgs = append(buildArgs, "-tags", *tags)
	}

	buildArgs = append(buildArgs, "-o", absOutput)
	buildTarget := buildtarget.Default(absDir)
	if len(positionals) > 0 {
		buildTarget = positionals[0]
	}
	buildArgs = append(buildArgs, buildTarget)

	goVersion, err := getGoVersion()
	if err != nil {
		goVersion = "unknown"
	}

	gitCommit := getGitCommit(absDir)
	startTime := time.Now()

	if ctx.Out.IsVerbose() {
		ctx.Out.Verbose(fmt.Sprintf("Building: go %s", strings.Join(buildArgs, " ")))
	}

	buildCommand := fmt.Sprintf("go %s", strings.Join(buildArgs, " "))
	run, err := executil.Run(context.Background(), executil.Options{
		Name:    "go",
		Args:    buildArgs,
		Dir:     absDir,
		Timeout: 10 * time.Minute,
	})
	if err != nil {
		buildOutput := strings.TrimSpace(run.CombinedOutput())
		if ctx.Out.Format() == "text" && !ctx.Out.IsQuiet() && buildOutput != "" {
			_ = ctx.Out.Textf("%s\n", buildOutput)
		}

		errData := map[string]any{
			"command": buildCommand,
		}
		if buildOutput != "" {
			errData["output"] = buildOutput
		}
		if run.OutputTruncated() {
			errData["output_truncated"] = true
		}
		return ctx.Out.Error(fmt.Sprintf("build failed: %v", err), 1, errData)
	}

	buildOutput := strings.TrimSpace(run.CombinedOutput())
	if ctx.Out.Format() == "text" && !ctx.Out.IsQuiet() && buildOutput != "" {
		_ = ctx.Out.Textf("%s\n", buildOutput)
	}

	buildTime := time.Since(startTime)

	fileInfo, err := os.Stat(absOutput)
	var sizeBytes int64
	if err == nil {
		sizeBytes = fileInfo.Size()
	}

	result := map[string]any{
		"binary":        absOutput,
		"size_bytes":    sizeBytes,
		"size_mb":       float64(sizeBytes) / (1024 * 1024),
		"build_time_ms": buildTime.Milliseconds(),
		"go_version":    goVersion,
		"git_commit":    gitCommit,
		"race_detector": *race,
		"trimpath":      *trimpath,
		"target":        buildTarget,
	}

	if *tags != "" {
		result["build_tags"] = strings.Split(*tags, ",")
	}
	if buildOutput != "" {
		result["build_output"] = buildOutput
	}
	if run.OutputTruncated() {
		result["build_output_truncated"] = true
	}

	return ctx.Out.Success("Build completed successfully", result)
}

func getGoVersion() (string, error) {
	result, err := executil.Run(context.Background(), executil.Options{
		Name:        "go",
		Args:        []string{"version"},
		Timeout:     5 * time.Second,
		OutputLimit: 8 * 1024,
	})
	if err != nil {
		return "", err
	}

	output := result.CombinedOutput()
	parts := strings.Fields(output)
	if len(parts) >= 3 {
		return strings.TrimPrefix(parts[2], "go"), nil
	}

	return output, nil
}

func getGitCommit(dir string) string {
	result, err := executil.Run(context.Background(), executil.Options{
		Name:        "git",
		Args:        []string{"rev-parse", "--short", "HEAD"},
		Dir:         dir,
		Timeout:     5 * time.Second,
		OutputLimit: 8 * 1024,
	})
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(result.CombinedOutput())
}
