package commands

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/birdordev/plumego/cmd/plumego/internal/executil"
	"gopkg.in/yaml.v3"
)

const communityExtensionManifest = "community-extension.yaml"

var stableRootForbiddenImports = []string{
	"github.com/birdordev/plumego/core",
	"github.com/birdordev/plumego/router",
	"github.com/birdordev/plumego/contract",
	"github.com/birdordev/plumego/middleware",
	"github.com/birdordev/plumego/security",
	"github.com/birdordev/plumego/store",
	"github.com/birdordev/plumego/health",
	"github.com/birdordev/plumego/log",
	"github.com/birdordev/plumego/metrics",
}

type AddCmd struct {
	runner addCommandRunner
}

type addCommandRunner interface {
	Run(context.Context, executil.Options) (executil.Result, error)
}

type defaultAddRunner struct{}

func (defaultAddRunner) Run(ctx context.Context, opts executil.Options) (executil.Result, error) {
	return executil.Run(ctx, opts)
}

type addOptions struct {
	ModulePath string
	Version    string
	ProjectDir string
}

type addReport struct {
	ModulePath      string   `json:"module_path" yaml:"module_path"`
	Version         string   `json:"version" yaml:"version"`
	ManifestPath    string   `json:"manifest_path" yaml:"manifest_path"`
	FieldsValidated []string `json:"fields_validated" yaml:"fields_validated"`
	ChecksPassed    []string `json:"checks_passed" yaml:"checks_passed"`
	ModuleAdded     bool     `json:"module_added" yaml:"module_added"`
}

type goListModule struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
}

type goDownloadModule struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
	Dir     string `json:"Dir"`
	Error   string `json:"Error"`
}

type communityManifest struct {
	Name              string   `yaml:"name"`
	ModulePath        string   `yaml:"module_path"`
	Status            string   `yaml:"status"`
	HandlerShape      string   `yaml:"handler_shape"`
	TestCommands      []string `yaml:"test_commands"`
	ForbiddenImports  []string `yaml:"forbidden_imports"`
	NoInitSideEffects *bool    `yaml:"no_init_side_effects"`
	NoGlobals         *bool    `yaml:"no_globals"`
	Owner             string   `yaml:"owner"`
}

func (c *AddCmd) Name() string  { return "add" }
func (c *AddCmd) Short() string { return "Add a validated community extension module" }

func (c *AddCmd) Run(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	version := fs.String("version", "latest", "Module version")
	positionals, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}
	if len(positionals) != 1 {
		return ctx.Out.Error("usage: plumego add <module-path> [--version latest]", 1)
	}

	projectDir, err := os.Getwd()
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("resolve project directory: %v", err), 1)
	}
	opts := addOptions{
		ModulePath: strings.TrimSpace(positionals[0]),
		Version:    strings.TrimSpace(*version),
		ProjectDir: projectDir,
	}
	if opts.ModulePath == "" {
		return ctx.Out.Error("module path is required", 1)
	}
	if opts.Version == "" {
		opts.Version = "latest"
	}

	report, err := c.add(context.Background(), opts)
	if err != nil {
		return ctx.Out.Error(err.Error(), 1)
	}
	return ctx.Out.Success("Community extension added", report)
}

func (c *AddCmd) add(ctx context.Context, opts addOptions) (addReport, error) {
	runner := c.runner
	if runner == nil {
		runner = defaultAddRunner{}
	}

	target := moduleTarget(opts.ModulePath, opts.Version)
	listed, err := resolveModule(ctx, runner, opts.ProjectDir, target)
	if err != nil {
		return addReport{}, err
	}
	resolvedVersion := listed.Version
	if resolvedVersion == "" {
		resolvedVersion = opts.Version
	}

	downloaded, err := downloadModule(ctx, runner, opts.ProjectDir, moduleTarget(opts.ModulePath, resolvedVersion))
	if err != nil {
		return addReport{}, err
	}
	manifestPath := filepath.Join(downloaded.Dir, communityExtensionManifest)
	manifest, err := readCommunityManifest(manifestPath)
	if err != nil {
		return addReport{}, err
	}
	if violations := validateCommunityManifest(manifest, opts.ModulePath); len(violations) > 0 {
		return addReport{}, fmt.Errorf("schema validation failed: %s", strings.Join(violations, "; "))
	}

	if err := runGoVet(ctx, runner, downloaded.Dir); err != nil {
		return addReport{}, err
	}
	if violations, err := scanForbiddenImports(downloaded.Dir, manifest.ForbiddenImports); err != nil {
		return addReport{}, err
	} else if len(violations) > 0 {
		return addReport{}, fmt.Errorf("forbidden import detected: %s", strings.Join(violations, "; "))
	}

	if err := runGoGet(ctx, runner, opts.ProjectDir, target); err != nil {
		return addReport{}, err
	}

	return addReport{
		ModulePath:      opts.ModulePath,
		Version:         resolvedVersion,
		ManifestPath:    manifestPath,
		FieldsValidated: []string{"name", "module_path", "status", "handler_shape", "test_commands", "forbidden_imports", "no_init_side_effects", "no_globals", "owner"},
		ChecksPassed:    []string{"go list", "schema validation", "go vet", "forbidden import scan", "go get"},
		ModuleAdded:     true,
	}, nil
}

func resolveModule(ctx context.Context, runner addCommandRunner, dir string, target string) (goListModule, error) {
	result, err := runner.Run(ctx, executil.Options{
		Name:    "go",
		Args:    []string{"list", "-m", "-json", target},
		Dir:     dir,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		output := strings.TrimSpace(result.CombinedOutput())
		if output != "" {
			output = ": " + output
		}
		return goListModule{}, fmt.Errorf("go list failed for %s%s; retry after checking network access and GOPROXY", target, output)
	}
	var listed goListModule
	if err := json.Unmarshal([]byte(result.Stdout), &listed); err != nil {
		return goListModule{}, fmt.Errorf("parse go list response: %w", err)
	}
	return listed, nil
}

func downloadModule(ctx context.Context, runner addCommandRunner, dir string, target string) (goDownloadModule, error) {
	result, err := runner.Run(ctx, executil.Options{
		Name:    "go",
		Args:    []string{"mod", "download", "-json", target},
		Dir:     dir,
		Timeout: 60 * time.Second,
	})
	if err != nil {
		output := strings.TrimSpace(result.CombinedOutput())
		if output != "" {
			output = ": " + output
		}
		return goDownloadModule{}, fmt.Errorf("download module %s failed%s", target, output)
	}
	var downloaded goDownloadModule
	if err := json.Unmarshal([]byte(result.Stdout), &downloaded); err != nil {
		return goDownloadModule{}, fmt.Errorf("parse go mod download response: %w", err)
	}
	if downloaded.Error != "" {
		return goDownloadModule{}, fmt.Errorf("download module %s failed: %s", target, downloaded.Error)
	}
	if downloaded.Dir == "" {
		return goDownloadModule{}, fmt.Errorf("download module %s did not return a module directory", target)
	}
	return downloaded, nil
}

func readCommunityManifest(path string) (communityManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return communityManifest{}, fmt.Errorf("schema not found: %s", path)
		}
		return communityManifest{}, fmt.Errorf("read schema %s: %w", path, err)
	}
	var manifest communityManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return communityManifest{}, fmt.Errorf("parse schema %s: %w", path, err)
	}
	return manifest, nil
}

func validateCommunityManifest(manifest communityManifest, expectedModulePath string) []string {
	var violations []string
	if strings.TrimSpace(manifest.Name) == "" {
		violations = append(violations, "missing required field \"name\"")
	}
	if strings.TrimSpace(manifest.ModulePath) == "" {
		violations = append(violations, "missing required field \"module_path\"")
	} else if manifest.ModulePath != expectedModulePath {
		violations = append(violations, fmt.Sprintf("module_path %q does not match requested module %q", manifest.ModulePath, expectedModulePath))
	}
	if !containsString([]string{"experimental", "beta", "ga"}, manifest.Status) {
		violations = append(violations, fmt.Sprintf("status %q is invalid", manifest.Status))
	}
	if manifest.HandlerShape != "func(http.ResponseWriter, *http.Request)" {
		violations = append(violations, "handler_shape must be \"func(http.ResponseWriter, *http.Request)\"")
	}
	if len(manifest.TestCommands) == 0 {
		violations = append(violations, "missing required field \"test_commands\"")
	}
	if len(manifest.ForbiddenImports) == 0 {
		violations = append(violations, "missing required field \"forbidden_imports\"")
	}
	for _, stablePath := range stableRootForbiddenImports {
		if !containsString(manifest.ForbiddenImports, stablePath) {
			violations = append(violations, fmt.Sprintf("forbidden_imports must include %s", stablePath))
		}
	}
	if manifest.NoInitSideEffects == nil || !*manifest.NoInitSideEffects {
		violations = append(violations, "no_init_side_effects must be true")
	}
	if manifest.NoGlobals == nil || !*manifest.NoGlobals {
		violations = append(violations, "no_globals must be true")
	}
	if strings.TrimSpace(manifest.Owner) == "" {
		violations = append(violations, "missing required field \"owner\"")
	}
	sort.Strings(violations)
	return violations
}

func runGoVet(ctx context.Context, runner addCommandRunner, dir string) error {
	result, err := runner.Run(ctx, executil.Options{
		Name:    "go",
		Args:    []string{"vet", "./..."},
		Dir:     dir,
		Timeout: 60 * time.Second,
	})
	if err != nil {
		output := strings.TrimSpace(result.CombinedOutput())
		if output != "" {
			return fmt.Errorf("go vet failed: %s", output)
		}
		return fmt.Errorf("go vet failed: %w", err)
	}
	return nil
}

func runGoGet(ctx context.Context, runner addCommandRunner, dir string, target string) error {
	result, err := runner.Run(ctx, executil.Options{
		Name:    "go",
		Args:    []string{"get", target},
		Dir:     dir,
		Timeout: 60 * time.Second,
	})
	if err != nil {
		output := strings.TrimSpace(result.CombinedOutput())
		if output != "" {
			return fmt.Errorf("go get failed: %s", output)
		}
		return fmt.Errorf("go get failed: %w", err)
	}
	return nil
}

func scanForbiddenImports(dir string, forbidden []string) ([]string, error) {
	forbiddenSet := make(map[string]struct{}, len(forbidden))
	for _, item := range forbidden {
		forbiddenSet[item] = struct{}{}
	}

	var violations []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse imports in %s: %w", path, err)
		}
		rel, _ := filepath.Rel(dir, path)
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, "\"")
			if _, forbidden := forbiddenSet[importPath]; forbidden {
				violations = append(violations, fmt.Sprintf("%s imports %s", filepath.ToSlash(rel), importPath))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(violations)
	return violations, nil
}

func moduleTarget(modulePath string, version string) string {
	if version == "" {
		version = "latest"
	}
	return modulePath + "@" + version
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
