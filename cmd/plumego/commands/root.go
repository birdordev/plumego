package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/birdordev/plumego/cmd/plumego/internal/output"
)

// RootCmd represents the base command
type RootCmd struct {
	subcommands map[string]Command
	formatter   *output.Formatter
}

// Command interface for all CLI commands
type Command interface {
	Name() string
	Short() string
	Run(ctx *Context, args []string) error
}

// BuildInfo holds build-time version metadata injected from main.
type BuildInfo struct {
	Version   string
	GitCommit string
	BuildDate string
}

// Execute runs the root command
func Execute(info BuildInfo) error {
	root := &RootCmd{
		subcommands: make(map[string]Command),
		formatter:   output.NewFormatter(),
	}

	// Register commands
	root.Register(&NewCmd{})
	root.Register(&GenerateCmd{})
	root.Register(&AddCmd{})
	root.Register(NewDevCmd())
	root.Register(&RoutesCmd{})
	root.Register(&CheckCmd{})
	root.Register(&LintCmd{})
	root.Register(&ConfigCmd{})
	root.Register(&MigrateCmd{})
	root.Register(&TestCmd{})
	root.Register(&BuildCmd{})
	root.Register(&InspectCmd{})
	root.Register(&ServeCmd{})
	root.Register(&AgentsCmd{})
	root.Register(&DoctorCmd{})
	root.Register(&VersionCmd{
		Version:   info.Version,
		GitCommit: info.GitCommit,
		BuildDate: info.BuildDate,
	})

	return root.Run(os.Args[1:])
}

// Register adds a command to the root
func (r *RootCmd) Register(cmd Command) {
	r.subcommands[cmd.Name()] = cmd
}

// Run executes the command
func (r *RootCmd) Run(args []string) error {
	// Parse global flags
	global, args, err := r.parseGlobalFlags(args)
	if err != nil {
		return r.formatter.Error(fmt.Sprintf("invalid global flags: %v", err), 1)
	}
	if !output.IsSupportedFormat(global.Format) {
		r.formatter.SetFormat(defaultGlobalFlags().Format)
		return r.formatter.Error(fmt.Sprintf("unsupported output format: %s", global.Format), 1, map[string]any{
			"format":            global.Format,
			"supported_formats": []string{"json", "yaml", "text"},
		})
	}

	// Configure formatter
	r.formatter.SetFormat(global.Format)
	r.formatter.SetQuiet(global.Quiet)
	r.formatter.SetVerbose(global.Verbose)
	r.formatter.SetColor(!global.NoColor)

	if len(args) == 0 {
		return r.showHelp()
	}

	cmdName := args[0]
	if cmdName == "--help" || cmdName == "-h" {
		return r.showHelp()
	}
	if cmdName == "help" {
		if len(args) > 1 {
			return r.showCommandHelp(args[1])
		}
		return r.showHelp()
	}

	cmd, ok := r.subcommands[cmdName]
	if !ok {
		return r.formatter.Error(fmt.Sprintf("unknown command: %s", cmdName), 1, map[string]any{
			"command": cmdName,
			"hint":    "run plumego --help",
		})
	}
	if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
		return r.showCommandHelp(cmdName)
	}

	ctx := &Context{
		Out:     r.formatter,
		EnvFile: global.EnvFile,
	}

	return cmd.Run(ctx, args[1:])
}

type globalFlags struct {
	Format  string
	Quiet   bool
	Verbose bool
	NoColor bool
	EnvFile string
}

func defaultGlobalFlags() globalFlags {
	return globalFlags{
		Format:  "text",
		EnvFile: ".env",
	}
}

func (r *RootCmd) parseGlobalFlags(args []string) (globalFlags, []string, error) {
	global := defaultGlobalFlags()

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--help" || arg == "-h" {
			return global, args[i:], nil
		}
		if arg == "--" {
			return global, args[i+1:], nil
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			return global, args[i:], nil
		}

		name, inlineValue, hasInlineValue := strings.Cut(arg, "=")
		switch arg {
		case "--format", "-f":
			if i+1 >= len(args) {
				return global, nil, fmt.Errorf("%s requires a value", arg)
			}
			global.Format = args[i+1]
			i++
		case "--env-file":
			if i+1 >= len(args) {
				return global, nil, fmt.Errorf("%s requires a value", arg)
			}
			global.EnvFile = args[i+1]
			i++
		case "--quiet", "-q":
			global.Quiet = true
		case "--verbose", "-v":
			global.Verbose = true
		case "--no-color":
			global.NoColor = true
		default:
			switch name {
			case "--format", "-f":
				if !hasInlineValue || inlineValue == "" {
					return global, nil, fmt.Errorf("%s requires a value", name)
				}
				global.Format = inlineValue
			case "--env-file":
				if !hasInlineValue || inlineValue == "" {
					return global, nil, fmt.Errorf("%s requires a value", name)
				}
				global.EnvFile = inlineValue
			default:
				return global, nil, fmt.Errorf("unknown global flag: %s", arg)
			}
		}
	}

	return global, nil, nil
}

func (r *RootCmd) showCommandHelp(name string) error {
	cmd, ok := r.subcommands[name]
	if !ok {
		return r.formatter.Error(fmt.Sprintf("unknown command: %s", name), 1, map[string]any{
			"command": name,
			"hint":    "run plumego --help",
		})
	}

	return printCommandHelp(r.formatter, cmd)
}

func globalHelpFooter() string {
	return `
Global Flags:
  -f, --format <type>    Output format: text, json, yaml (default: text)
  -q, --quiet            Suppress non-essential output
  -v, --verbose          Detailed logging
      --no-color         Disable color output
      --env-file <path>  Environment file path (default: .env)
`
}

func (r *RootCmd) showHelp() error {
	var b strings.Builder
	b.WriteString(`Plumego CLI - Code Agent Friendly

Usage:
  plumego [global-flags] <command> [command-flags] [args]

Global Flags:
  -f, --format <type>    Output format: text, json, yaml (default: text)
  -q, --quiet            Suppress non-essential output
  -v, --verbose          Detailed logging
      --no-color         Disable color output
      --env-file <path>  Environment file path (default: .env)

Available Commands:
`)
	for _, name := range stableCommandOrder {
		cmd := r.subcommands[name]
		if cmd == nil {
			continue
		}
		fmt.Fprintf(&b, "  %-11s %s\n", name, cmd.Short())
	}
	b.WriteString(`

Use "plumego <command> --help" for more information about a command.

Examples:
  plumego new myapp --template api
  plumego generate handler Auth
  plumego dev --addr :3000
  plumego check --security
  plumego lint
  plumego --format json routes
  plumego agents verify --changed log
  plumego agents explain --module middleware
  plumego doctor
  plumego serve ./public --addr :3000

Documentation:
  https://github.com/birdordev/plumego/tree/main/docs
`)
	return r.printHelp("Plumego CLI help", map[string]any{
		"kind": "root",
		"help": b.String(),
	})
}

func (r *RootCmd) printHelp(message string, data map[string]any) error {
	return printHelp(r.formatter, message, data)
}

func printCommandHelp(out *output.Formatter, cmd Command) error {
	return printHelp(out, "Command help", map[string]any{
		"kind":    "command",
		"command": cmd.Name(),
		"help":    commandHelp(cmd),
	})
}

func printHelp(out *output.Formatter, message string, data map[string]any) error {
	if out.Format() == "text" {
		if help, ok := data["help"].(string); ok {
			return out.Print(help)
		}
	}
	return out.Success(message, data)
}
