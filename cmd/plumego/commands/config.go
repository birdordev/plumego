package commands

import (
	"flag"
	"fmt"
	"io"

	"github.com/birdordev/plumego/cmd/plumego/internal/configmgr"
	"github.com/birdordev/plumego/cmd/plumego/internal/output"
)

// ConfigCmd manages configuration
type ConfigCmd struct{}

type configCommandOptions struct {
	dir         string
	resolve     bool
	showSecrets bool
}

func (c *ConfigCmd) Name() string  { return "config" }
func (c *ConfigCmd) Short() string { return "Configuration management" }

func (c *ConfigCmd) Run(ctx *Context, args []string) error {
	if len(args) == 0 {
		return ctx.Out.Error("config command required (show, validate, init, env)", 1)
	}

	subCmd := args[0]
	subArgs := args[1:]

	switch subCmd {
	case "show":
		return c.runShow(ctx.Out, ctx.EnvFile, subArgs)
	case "validate":
		return c.runValidate(ctx.Out, ctx.EnvFile, subArgs)
	case "init":
		return c.runInit(ctx.Out, subArgs)
	case "env":
		return c.runEnv(ctx.Out, ctx.EnvFile, subArgs)
	default:
		return ctx.Out.Error(fmt.Sprintf("unknown config command: %s", subCmd), 1)
	}
}

func (c *ConfigCmd) runShow(out *output.Formatter, envFile string, args []string) error {
	opts, err := parseConfigCommandArgs("config show", args, func(fs *flag.FlagSet, opts *configCommandOptions) {
		fs.BoolVar(&opts.resolve, "resolve", false, "Resolve environment variables")
		fs.Bool("redact", true, "Deprecated: sensitive values are redacted unless --show-secrets is set")
		fs.BoolVar(&opts.showSecrets, "show-secrets", false, "Show raw sensitive values")
	})
	if err != nil {
		return out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}

	projectDir, err := resolveDir(opts.dir)
	if err != nil {
		return out.Error(err.Error(), 1)
	}

	out.Verbose("Loading configuration...")
	config, err := configmgr.LoadConfig(projectDir, envFile, opts.resolve)
	if err != nil {
		return out.Error(fmt.Sprintf("failed to load config: %v", err), 1)
	}

	if !opts.showSecrets {
		config = configmgr.RedactSensitive(config)
	}

	return out.Success("Configuration loaded", config)
}

func (c *ConfigCmd) runValidate(out *output.Formatter, envFile string, args []string) error {
	opts, err := parseConfigCommandArgs("config validate", args, nil)
	if err != nil {
		return out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}

	projectDir, err := resolveDir(opts.dir)
	if err != nil {
		return out.Error(err.Error(), 1)
	}

	out.Verbose("Validating configuration...")
	result := configmgr.ValidateConfig(projectDir, envFile)

	if len(result.Errors) > 0 {
		return out.Error("Configuration is invalid", 1, result)
	}

	if len(result.Warnings) > 0 {
		return out.Warning("Configuration has warnings", 1, result)
	}

	return out.Success("Configuration is valid", result)
}

func (c *ConfigCmd) runInit(out *output.Formatter, args []string) error {
	opts, err := parseConfigCommandArgs("config init", args, nil)
	if err != nil {
		return out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}

	projectDir, err := resolveDir(opts.dir)
	if err != nil {
		return out.Error(err.Error(), 1)
	}

	out.Verbose("Generating default configuration files...")
	files, err := configmgr.InitConfig(projectDir)
	if err != nil {
		return out.Error(fmt.Sprintf("failed to init config: %v", err), 1)
	}

	return out.Success("Configuration files created", map[string]any{
		"files_created": files,
	})
}

func (c *ConfigCmd) runEnv(out *output.Formatter, envFile string, args []string) error {
	opts, err := parseConfigCommandArgs("config env", args, nil)
	if err != nil {
		return out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}

	projectDir, err := resolveDir(opts.dir)
	if err != nil {
		return out.Error(err.Error(), 1)
	}

	out.Verbose("Loading environment variables...")
	envVars, err := configmgr.GetEnvVars(projectDir, envFile)
	if err != nil {
		return out.Error(fmt.Sprintf("failed to load environment variables: %v", err), 1)
	}

	return out.Success("Environment variables loaded", envVars)
}

func parseConfigCommandArgs(name string, args []string, register func(*flag.FlagSet, *configCommandOptions)) (configCommandOptions, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	opts := configCommandOptions{dir: "."}
	fs.StringVar(&opts.dir, "dir", ".", "Project directory")
	if register != nil {
		register(fs, &opts)
	}

	positionals, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return configCommandOptions{}, err
	}
	if len(positionals) > 0 {
		return configCommandOptions{}, fmt.Errorf("unexpected arguments: %v", positionals)
	}

	return opts, nil
}
