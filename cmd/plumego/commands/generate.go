package commands

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/birdordev/plumego/cmd/plumego/internal/codegen"
)

// GenerateCmd generates code
type GenerateCmd struct{}

func (c *GenerateCmd) Name() string  { return "generate" }
func (c *GenerateCmd) Short() string { return "Generate handlers, middleware, models, and more" }

// noNameGenerateTypes are types that do not require a name positional argument.
var noNameGenerateTypes = map[string]bool{
	"spec":               true,
	"health-handler":     true,
	"metrics-middleware": true,
}

func (c *GenerateCmd) Run(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	outputPath := fs.String("output", "", "Output file path")
	dir := fs.String("dir", ".", "Project directory")
	format := fs.String("format", "json", "Spec output format: json or yaml")
	appPath := fs.String("app", ".", "Application package path to introspect")
	packageName := fs.String("package", "", "Package name")
	methods := fs.String("methods", "GET", "HTTP methods (comma-separated)")
	withTests := fs.Bool("with-tests", false, "Generate test file")
	withValidation := fs.Bool("with-validation", false, "Generate validation")
	force := fs.Bool("force", false, "Overwrite existing files")

	positionals, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}

	if len(positionals) == 0 {
		return ctx.Out.Error("generate type required (e.g., plumego generate handler Auth)", 1)
	}

	genType := positionals[0]

	// Types that do not require a name argument.
	if noNameGenerateTypes[genType] {
		if len(positionals) > 1 {
			return ctx.Out.Error(fmt.Sprintf("unexpected arguments for generate %s: %v", genType, positionals[1:]), 1)
		}
		if genType == "spec" {
			absDir, err := resolveDir(*dir)
			if err != nil {
				return ctx.Out.Error(err.Error(), 1)
			}
			return runGenerateSpec(ctx, generateSpecOptions{
				Dir:        absDir,
				OutputPath: *outputPath,
				Format:     *format,
				AppPath:    *appPath,
			})
		}
		// health-handler, metrics-middleware: name is empty, generators use defaults.
		absDir, err := resolveDir(*dir)
		if err != nil {
			return ctx.Out.Error(err.Error(), 1)
		}
		resolvedOutputPath := *outputPath
		if resolvedOutputPath != "" && !filepath.IsAbs(resolvedOutputPath) {
			resolvedOutputPath = filepath.Join(absDir, resolvedOutputPath)
		}
		ctx.Out.Verbose(fmt.Sprintf("Generating %s", genType))
		result, err := codegen.Generate(absDir, codegen.GenerateOptions{
			Type:        genType,
			OutputPath:  resolvedOutputPath,
			PackageName: *packageName,
			WithTests:   *withTests,
			Force:       *force,
		})
		if err != nil {
			return ctx.Out.Error(fmt.Sprintf("generation failed: %v", err), 1)
		}
		return ctx.Out.Success(fmt.Sprintf("%s generated successfully", genType), result)
	}

	// All other types require <type> <name>.
	if len(positionals) < 2 {
		return ctx.Out.Error(fmt.Sprintf("generate %s requires a name (e.g., plumego generate %s MyName)", genType, genType), 1)
	}
	if len(positionals) > 2 {
		return ctx.Out.Error(fmt.Sprintf("unexpected arguments: %v", positionals[2:]), 1)
	}

	name := positionals[1]

	absDir, err := resolveDir(*dir)
	if err != nil {
		return ctx.Out.Error(err.Error(), 1)
	}

	resolvedOutputPath := *outputPath
	if resolvedOutputPath != "" && !filepath.IsAbs(resolvedOutputPath) {
		resolvedOutputPath = filepath.Join(absDir, resolvedOutputPath)
	}

	ctx.Out.Verbose(fmt.Sprintf("Generating %s: %s", genType, name))

	result, err := codegen.Generate(absDir, codegen.GenerateOptions{
		Type:           genType,
		Name:           name,
		OutputPath:     resolvedOutputPath,
		PackageName:    *packageName,
		Methods:        *methods,
		WithTests:      *withTests,
		WithValidation: *withValidation,
		Force:          *force,
	})
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("generation failed: %v", err), 1)
	}

	return ctx.Out.Success(fmt.Sprintf("%s generated successfully", genType), result)
}
