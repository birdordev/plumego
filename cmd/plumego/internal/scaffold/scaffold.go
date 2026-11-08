package scaffold

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/birdordev/plumego/cmd/plumego/internal/executil"
)

var scenarioProfiles = map[string]struct{}{
	"rest-api":    {},
	"tenant-api":  {},
	"gateway":     {},
	"realtime":    {},
	"ai-service":  {},
	"ops-service": {},
}

const defaultPlumegoVersion = "v0.0.0"

// ProjectOptions configures project-level scaffold output.
type ProjectOptions struct {
	PlumegoVersion string
	PlumegoReplace string
	CleanExisting  bool
}

// GetTemplateFiles returns the files that would be created for a template.
func GetTemplateFiles(template string) []string {
	switch template {
	case "canonical":
		// Mirrors reference/standard-service exactly: explicit bootstrap,
		// stable-root-only imports, constructor injection, explicit routes.
		return canonicalTemplateFiles()
	case "minimal", "fullstack", "microservice":
		return canonicalTemplateFiles()
	case "api":
		files := []string{
			"cmd/app/main.go",
			"internal/app/app.go",
			"internal/app/routes.go",
			"internal/handler/api.go",
			"internal/handler/health.go",
			"internal/config/config.go",
			"internal/resource/users.go",
			"go.mod",
			"env.example",
			".gitignore",
			"README.md",
		}
		return appendTemplateControlFiles(files)
	case "rest-api":
		files := []string{
			"cmd/app/main.go",
			"internal/app/app.go",
			"internal/app/routes.go",
			"internal/handler/api.go",
			"internal/handler/health.go",
			"internal/config/config.go",
			"internal/resource/users.go",
			"internal/scenario/profile.go",
			"go.mod",
			"env.example",
			".gitignore",
			"README.md",
		}
		return appendTemplateControlFiles(files)
	case "tenant-api", "gateway", "realtime", "ai-service", "ops-service":
		files := []string{
			"cmd/app/main.go",
			"internal/app/app.go",
			"internal/app/routes.go",
			"internal/handler/api.go",
			"internal/handler/health.go",
			"internal/config/config.go",
			"internal/scenario/profile.go",
			"go.mod",
			"env.example",
			".gitignore",
			"README.md",
		}
		return appendTemplateControlFiles(files)
	default:
		return nil
	}
}

func canonicalTemplateFiles() []string {
	files := []string{
		"cmd/app/main.go",
		"internal/app/app.go",
		"internal/app/routes.go",
		"internal/domain/item/item.go",
		"internal/handler/api.go",
		"internal/handler/health.go",
		"internal/handler/items.go",
		"internal/config/config.go",
		"go.mod",
		"env.example",
		".gitignore",
		"README.md",
	}
	return appendTemplateControlFiles(files)
}

func appendTemplateControlFiles(files []string) []string {
	out := append([]string{}, files...)
	out = append(out,
		"Makefile",
		".github/workflows/ci.yml",
		"AGENTS.md",
		"CLAUDE.md",
	)
	return out
}

// CreateProject creates a new project from a template.
func CreateProject(dir, name, module, template string, initGit bool, options ...ProjectOptions) ([]string, error) {
	if err := validateProjectName(name); err != nil {
		return nil, err
	}
	if err := validateModulePath(module); err != nil {
		return nil, err
	}

	files := GetTemplateFiles(template)
	if len(files) == 0 {
		return nil, fmt.Errorf("unknown project template: %s", template)
	}
	projectOptions := resolveProjectOptions(options)
	if projectOptions.CleanExisting {
		if err := cleanKnownTemplateFiles(dir); err != nil {
			return nil, err
		}
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	created := []string{}

	for _, file := range files {
		path := filepath.Join(dir, file)

		parent := filepath.Dir(path)
		if err := os.MkdirAll(parent, 0755); err != nil {
			return created, fmt.Errorf("failed to create directory %s: %w", parent, err)
		}

		content := getTemplateContent(file, name, module, template)
		if file == "go.mod" {
			content = getGoModContent(module, projectOptions)
		}

		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return created, fmt.Errorf("failed to write file %s: %w", path, err)
		}

		created = append(created, file)
	}

	if initGit {
		result, err := executil.Run(context.Background(), executil.Options{
			Name:        "git",
			Args:        []string{"init"},
			Dir:         dir,
			Timeout:     30 * time.Second,
			OutputLimit: 16 * 1024,
		})
		if err == nil {
			created = append(created, ".git/")
		} else {
			output := strings.TrimSpace(result.CombinedOutput())
			if output == "" {
				return created, fmt.Errorf("failed to initialize git repository: %w", err)
			}
			return created, fmt.Errorf("failed to initialize git repository: %w: %s", err, output)
		}
	}

	return created, nil
}

func cleanKnownTemplateFiles(dir string) error {
	for _, file := range allTemplateFiles() {
		path := filepath.Join(dir, file)
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("failed to inspect stale template file %s: %w", path, err)
		}
		if info.IsDir() {
			continue
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("failed to remove stale template file %s: %w", path, err)
		}
	}
	return nil
}

func allTemplateFiles() []string {
	seen := make(map[string]struct{})
	var files []string
	for _, template := range []string{
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
	} {
		for _, file := range GetTemplateFiles(template) {
			if _, ok := seen[file]; ok {
				continue
			}
			seen[file] = struct{}{}
			files = append(files, file)
		}
	}
	return files
}

func validateProjectName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("project name is required")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid project name: %s", name)
	}
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("invalid project name: %s", name)
	}
	for _, r := range name {
		if r == '-' || r == '_' || r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		return fmt.Errorf("invalid project name: %s", name)
	}
	return nil
}

func validateModulePath(module string) error {
	module = strings.TrimSpace(module)
	if module == "" {
		return fmt.Errorf("module path is required")
	}
	if strings.ContainsAny(module, "\\ \t\r\n") || strings.Contains(module, "://") {
		return fmt.Errorf("invalid module path: %s", module)
	}
	if strings.HasPrefix(module, "/") || strings.HasSuffix(module, "/") || strings.Contains(module, "//") {
		return fmt.Errorf("invalid module path: %s", module)
	}
	for _, segment := range strings.Split(module, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.HasPrefix(segment, "-") {
			return fmt.Errorf("invalid module path: %s", module)
		}
		for _, r := range segment {
			if r == '-' || r == '_' || r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r) {
				continue
			}
			return fmt.Errorf("invalid module path: %s", module)
		}
	}
	return nil
}

func resolveProjectOptions(options []ProjectOptions) ProjectOptions {
	resolved := ProjectOptions{PlumegoVersion: defaultPlumegoVersion}
	if len(options) > 0 {
		resolved = options[0]
	}
	if resolved.PlumegoVersion == "" {
		resolved.PlumegoVersion = defaultPlumegoVersion
	}
	return resolved
}

func getGoModContent(module string, opts ProjectOptions) string {
	opts = resolveProjectOptions([]ProjectOptions{opts})
	content := fmt.Sprintf("module %s\n\ngo 1.26.0\n\nrequire github.com/birdordev/plumego %s\n", module, opts.PlumegoVersion)
	if opts.PlumegoReplace != "" {
		content += fmt.Sprintf("\nreplace github.com/birdordev/plumego => %s\n", filepath.ToSlash(opts.PlumegoReplace))
	}
	return content
}
