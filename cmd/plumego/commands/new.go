package commands

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/birdordev/plumego/cmd/plumego/internal/scaffold"
)

var newProjectTemplates = []string{
	"canonical",
	"minimal",
	"api",
	"fullstack",
	"microservice",
	"rest-api",
	"tenant-api",
	"gateway",
	"realtime",
	"ai-service",
	"ops-service",
}

// NewCmd creates a new project
type NewCmd struct{}

func (c *NewCmd) Name() string  { return "new" }
func (c *NewCmd) Short() string { return "Create new project from template" }

func (c *NewCmd) Run(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	templateFlag := fs.String("template", "canonical", fmt.Sprintf("Project template (%s)", strings.Join(newProjectTemplates, ", ")))
	moduleFlag := fs.String("module", "", "Go module path")
	dirFlag := fs.String("dir", "", "Output directory")
	force := fs.Bool("force", false, "Overwrite existing directory")
	noGit := fs.Bool("no-git", false, "Skip git initialization")
	dryRun := fs.Bool("dry-run", false, "Preview without creating")

	positionals, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}

	if len(positionals) == 0 {
		return ctx.Out.Error("project name required", 1)
	}
	if len(positionals) > 1 {
		return ctx.Out.Error(fmt.Sprintf("unexpected arguments: %v", positionals[1:]), 1)
	}

	projectName := positionals[0]
	template := *templateFlag
	module := *moduleFlag
	dir := *dirFlag

	if dir == "" {
		dir = filepath.Join(".", projectName)
	}
	if module == "" {
		module = projectName
	}

	ctx.Out.Verbose(fmt.Sprintf("Creating project: %s", projectName))
	ctx.Out.Verbose(fmt.Sprintf("Template: %s", template))
	ctx.Out.Verbose(fmt.Sprintf("Module: %s", module))
	ctx.Out.Verbose(fmt.Sprintf("Directory: %s", dir))

	if _, err := os.Stat(dir); err == nil && !*force {
		return ctx.Out.Error(fmt.Sprintf("directory %s already exists (use --force to overwrite)", dir), 2)
	}

	valid := false
	for _, t := range newProjectTemplates {
		if template == t {
			valid = true
			break
		}
	}
	if !valid {
		return ctx.Out.Error(fmt.Sprintf("invalid template: %s (valid: %s)", template, strings.Join(newProjectTemplates, ", ")), 3)
	}

	if *dryRun {
		files := scaffold.GetTemplateFiles(template)
		return ctx.Out.Success("Project creation preview", map[string]any{
			"dry_run":       true,
			"project":       projectName,
			"path":          dir,
			"module":        module,
			"template":      template,
			"files_created": files,
		})
	}

	projectOptions := scaffold.ProjectOptions{
		PlumegoReplace: detectLocalPlumegoRoot(),
		CleanExisting:  *force,
	}
	files, err := scaffold.CreateProject(dir, projectName, module, template, !*noGit, projectOptions)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("failed to create project: %v", err), 1)
	}

	return ctx.Out.Success("Project created successfully", map[string]any{
		"project":       projectName,
		"path":          dir,
		"module":        module,
		"template":      template,
		"files_created": files,
		"next_steps": []string{
			fmt.Sprintf("cd %s", dir),
			"go mod tidy",
			"plumego dev",
		},
	})
}

func detectLocalPlumegoRoot() string {
	candidates := []string{}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	candidates = append(candidates, getExecutableDir())

	for _, candidate := range candidates {
		if root := findPlumegoModuleRoot(candidate); root != "" {
			return root
		}
	}
	return ""
}

func findPlumegoModuleRoot(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		goMod := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(goMod); err == nil {
			if strings.Contains(string(data), "module github.com/birdordev/plumego\n") {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
