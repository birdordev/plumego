package commands

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/birdordev/plumego/cmd/plumego/internal/executil"
)

type TestCmd struct{}

func (c *TestCmd) Name() string  { return "test" }
func (c *TestCmd) Short() string { return "Run tests with enhanced reporting" }

func (c *TestCmd) Run(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	dir := fs.String("dir", ".", "Project directory")
	race := fs.Bool("race", false, "Enable race detector")
	cover := fs.Bool("cover", false, "Enable coverage")
	bench := fs.Bool("bench", false, "Run benchmarks")
	timeout := fs.String("timeout", "20s", "Test timeout")
	tags := fs.String("tags", "", "Build tags")
	runPattern := fs.String("run", "", "Run pattern")
	short := fs.Bool("short", false, "Short tests")
	coverProfile := fs.String("coverprofile", "", "Coverage profile path when --cover is set")

	packages, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}

	absDir, err := resolveDir(*dir)
	if err != nil {
		return ctx.Out.Error(err.Error(), 1)
	}

	testArgs := []string{"test"}

	if len(packages) == 0 {
		packages = []string{"./..."}
	}

	if *race {
		testArgs = append(testArgs, "-race")
	}

	var cleanupCoverage func()
	if *cover {
		profilePath := *coverProfile
		if profilePath == "" {
			tmpDir, err := os.MkdirTemp("", "plumego-coverage-*")
			if err != nil {
				return ctx.Out.Error(fmt.Sprintf("failed to create coverage temp dir: %v", err), 1)
			}
			cleanupCoverage = func() { _ = os.RemoveAll(tmpDir) }
			defer cleanupCoverage()
			profilePath = filepath.Join(tmpDir, "coverage.out")
		}
		testArgs = append(testArgs, "-cover", "-coverprofile="+profilePath)
	}

	if *bench {
		testArgs = append(testArgs, "-bench=.")
	}

	if *timeout != "" {
		testArgs = append(testArgs, "-timeout", *timeout)
	}

	if *tags != "" {
		testArgs = append(testArgs, "-tags", *tags)
	}

	if *runPattern != "" {
		testArgs = append(testArgs, "-run", *runPattern)
	}

	if *short {
		testArgs = append(testArgs, "-short")
	}

	testArgs = append(testArgs, "-json")
	testArgs = append(testArgs, packages...)

	startTime := time.Now()

	if ctx.Out.IsVerbose() {
		ctx.Out.Verbose(fmt.Sprintf("Running: go %s", strings.Join(testArgs, " ")))
	}

	processTimeout := testProcessTimeout(*timeout)
	run, testErr := executil.Run(context.Background(), executil.Options{
		Name:    "go",
		Args:    testArgs,
		Dir:     absDir,
		Timeout: processTimeout,
	})
	duration := time.Since(startTime)

	testResult := parseTestOutput(run.Stdout)
	testResult["duration_ms"] = duration.Milliseconds()
	testResult["race_detector"] = *race
	testResult["coverage_enabled"] = *cover
	testResult["benchmark"] = *bench
	if run.OutputTruncated() {
		testResult["output_truncated"] = true
	}

	if *cover {
		coveragePath := *coverProfile
		if coveragePath == "" {
			for _, arg := range testArgs {
				if profile, ok := strings.CutPrefix(arg, "-coverprofile="); ok {
					coveragePath = profile
					break
				}
			}
		}
		if coveragePath != "" && !filepath.IsAbs(coveragePath) {
			coveragePath = filepath.Join(absDir, coveragePath)
		}
		if coverage, err := parseCoverage(absDir, coveragePath); err == nil {
			testResult["coverage_percent"] = coverage
		}
		if *coverProfile != "" {
			testResult["coverage_profile"] = coveragePath
		}
	}

	if testErr != nil {
		testResult["status"] = "failed"
		if stderrText := strings.TrimSpace(run.Stderr); stderrText != "" {
			testResult["stderr"] = stderrText
		}
		return ctx.Out.Error("Tests failed", 1, testResult)
	}

	return ctx.Out.Success("Tests passed", testResult)
}

func testProcessTimeout(value string) time.Duration {
	if value == "" {
		return 0
	}
	timeout, err := time.ParseDuration(value)
	if err != nil {
		return 0
	}
	return timeout + 30*time.Second
}

type testEvent struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

func parseTestOutput(jsonOutput string) map[string]any {
	result := map[string]any{
		"status":  "passed",
		"tests":   0,
		"passed":  0,
		"failed":  0,
		"skipped": 0,
	}

	failures := []map[string]any{}
	packageFailures := []map[string]any{}
	lines := strings.Split(jsonOutput, "\n")

	for _, line := range lines {
		if line == "" {
			continue
		}

		var event testEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		switch event.Action {
		case "pass":
			if event.Test != "" {
				result["tests"] = result["tests"].(int) + 1
				result["passed"] = result["passed"].(int) + 1
			}
		case "fail":
			if event.Test != "" {
				result["tests"] = result["tests"].(int) + 1
				result["failed"] = result["failed"].(int) + 1
				failures = append(failures, map[string]any{
					"package": event.Package,
					"test":    event.Test,
				})
			} else if event.Package != "" {
				packageFailures = append(packageFailures, map[string]any{
					"package": event.Package,
				})
			}
		case "skip":
			if event.Test != "" {
				result["tests"] = result["tests"].(int) + 1
				result["skipped"] = result["skipped"].(int) + 1
			}
		}
	}

	if len(failures) > 0 {
		result["failures"] = failures
	}
	if len(packageFailures) > 0 {
		result["package_failures"] = packageFailures
	}

	return result
}

func parseCoverage(dir, coverageFile string) (float64, error) {
	result, err := executil.Run(context.Background(), executil.Options{
		Name:        "go",
		Args:        []string{"tool", "cover", "-func", coverageFile},
		Dir:         dir,
		Timeout:     30 * time.Second,
		OutputLimit: 64 * 1024,
	})
	if err != nil {
		return 0, err
	}

	for _, line := range strings.Split(result.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "total:" {
			continue
		}
		value := strings.TrimSuffix(fields[len(fields)-1], "%")
		var coverage float64
		if _, err := fmt.Sscanf(value, "%f", &coverage); err != nil {
			return 0, err
		}
		return coverage, nil
	}

	return 0, fmt.Errorf("total coverage not found")
}
