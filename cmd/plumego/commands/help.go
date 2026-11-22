package commands

import (
	"fmt"
	"strings"
)

var stableCommandOrder = []string{
	"new",
	"generate",
	"add",
	"dev",
	"routes",
	"check",
	"lint",
	"config",
	"migrate",
	"test",
	"build",
	"inspect",
	"serve",
	"agents",
	"doctor",
	"version",
}

type commandHelpProvider interface {
	Help() CommandHelp
}

type CommandHelp struct {
	Args        string
	Subcommands []HelpItem
	Flags       []HelpItem
	Examples    []string
}

type HelpItem struct {
	Name        string
	Description string
}

func commandHelp(cmd Command) string {
	spec := CommandHelp{Args: "[command-flags] [args]"}
	if provider, ok := cmd.(commandHelpProvider); ok {
		spec = provider.Help()
	}
	args := strings.TrimSpace(spec.Args)
	if args == "" {
		args = "[command-flags] [args]"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Usage:\n  plumego [global-flags] %s %s\n\n", cmd.Name(), args)
	fmt.Fprintf(&b, "Summary:\n  %s\n", cmd.Short())
	writeHelpItems(&b, "Subcommands", spec.Subcommands)
	writeHelpItems(&b, "Command Flags", spec.Flags)
	writeExamples(&b, spec.Examples)
	b.WriteString(globalHelpFooter())
	return b.String()
}

func writeHelpItems(b *strings.Builder, title string, items []HelpItem) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s:\n", title)
	width := 0
	for _, item := range items {
		if len(item.Name) > width {
			width = len(item.Name)
		}
	}
	for _, item := range items {
		fmt.Fprintf(b, "  %-*s  %s\n", width, item.Name, item.Description)
	}
}

func writeExamples(b *strings.Builder, examples []string) {
	if len(examples) == 0 {
		return
	}
	b.WriteString("\nExamples:\n")
	for _, example := range examples {
		fmt.Fprintf(b, "  %s\n", example)
	}
}

func (c *NewCmd) Help() CommandHelp {
	return CommandHelp{
		Args: "<project-name> [command-flags]",
		Flags: []HelpItem{
			{"--template <name>", "canonical (default), minimal, api, fullstack, microservice, rest-api, tenant-api, gateway, realtime, ai-service, ops-service"},
			{"--module <path>", "Go module path (default: project name)"},
			{"--dir <path>", "Output directory (default: ./<project-name>)"},
			{"--force", "Overwrite existing directory"},
			{"--no-git", "Skip git initialization"},
			{"--dry-run", "Preview files without creating"},
		},
		Examples: []string{
			"plumego new myapp",
			"plumego new myapp --template api",
			"plumego new myapp --module github.com/me/myapp",
			"plumego new myapp --template tenant-api --dry-run",
			"plumego new myapp --template rest-api --module github.com/org/myapp --dir ./services/myapp",
		},
	}
}

func (c *GenerateCmd) Help() CommandHelp {
	return CommandHelp{
		Args: "<type> [name] [command-flags]",
		Subcommands: []HelpItem{
			{"handler <Name>", "Struct-based HTTP handler with contract.WriteResponse"},
			{"endpoint <Name>", "Functional HTTP handler (canonical plumego shape)"},
			{"middleware <Name>", "Transport-only HTTP middleware func"},
			{"health-handler", "Health check handler using health.ComponentChecker"},
			{"metrics-middleware", "Metrics-recording middleware using metrics.HTTPObserver"},
			{"model <Name>", "Domain entity struct"},
			{"service <Name>", "Domain service interface + stub implementation"},
			{"repo <Name>", "Repository interface + no-op stub"},
			{"spec", "OpenAPI 3.1 document from registered routes"},
		},
		Flags: []HelpItem{
			{"--dir <path>", "Project directory (default: .)"},
			{"--output <path>", "Output file path"},
			{"--format <json|yaml>", "Spec output format (spec only)"},
			{"--app <package>", "Application package to introspect (spec only)"},
			{"--strict", "Require explicit responses and security definitions (spec only)"},
			{"--package <name>", "Package name for generated file"},
			{"--methods <list>", "HTTP methods, comma-separated: GET, POST, PUT, PATCH, DELETE (handler only, default: GET)"},
			{"--with-tests", "Also generate test file"},
			{"--with-validation", "Add Validate() method (model only)"},
			{"--force", "Overwrite existing files"},
		},
		Examples: []string{
			"plumego generate handler User --methods GET,POST,PUT,DELETE --with-tests",
			"plumego generate endpoint Item --with-tests",
			"plumego generate middleware RateLimit",
			"plumego generate health-handler",
			"plumego generate metrics-middleware",
			"plumego generate model Invoice --with-validation",
			"plumego generate spec --output openapi.yaml --format yaml",
		},
	}
}

func (c *AddCmd) Help() CommandHelp {
	return CommandHelp{
		Args: "<module-path> [command-flags]",
		Flags: []HelpItem{
			{"--version <tag>", "Module version (default: latest)"},
		},
		Subcommands: []HelpItem{
			{"validation pipeline", "go list → manifest schema check → go vet → forbidden-import scan → go get"},
		},
		Examples: []string{
			"plumego add github.com/acme/plumego-cache",
			"plumego add github.com/acme/plumego-cache --version v1.2.0",
			"plumego --format json add github.com/acme/plumego-cache",
		},
	}
}

func (c *DevCmd) Help() CommandHelp {
	return CommandHelp{
		Args: "[command-flags]",
		Flags: []HelpItem{
			{"--addr <addr>", "Application listen address (default: :8080)"},
			{"-p, --port <port>", "Listen port shorthand (e.g. 3000); sets --addr to :PORT"},
			{"--dashboard-addr <addr>", "Dashboard listen address (default: 127.0.0.1:9999)"},
			{"--dashboard-token <token>", "Auth token for action APIs (required when dashboard is non-loopback)"},
			{"--dir <path>", "Project directory (default: .)"},
			{"--watch <patterns>", "Comma-separated watch patterns (default: **/*.go)"},
			{"--exclude <patterns>", "Comma-separated exclude patterns"},
			{"--debounce <duration>", "Rebuild debounce delay (default: 500ms)"},
			{"--no-reload", "Disable hot reload (run once and stay)"},
			{"--build-cmd <cmd>", "Custom build command (replaces default go build)"},
			{"--run-cmd <cmd>", "Custom run command (replaces running the built binary)"},
		},
		Examples: []string{
			"plumego dev",
			"plumego dev --port 3000",
			"plumego dev --addr :3000",
			"plumego dev --dashboard-addr :9090 --dashboard-token secret",
			"plumego dev --no-reload",
			"plumego --format json dev --port 3000",
		},
	}
}

func (c *RoutesCmd) Help() CommandHelp {
	return CommandHelp{
		Args: "[command-flags]",
		Flags: []HelpItem{
			{"--dir <path>", "Project directory (default: .)"},
			{"--method <method>", "Filter by HTTP method (GET, POST, PUT, DELETE, …)"},
			{"--pattern <text>", "Filter routes by path substring"},
			{"--sort <field>", "Sort by: path (default) or method"},
			{"--middleware", "(not yet supported) Show middleware chains per route"},
			{"--group <name>", "(not yet supported) Filter by route group"},
		},
		Examples: []string{
			"plumego routes",
			"plumego routes --method GET",
			"plumego routes --pattern '/api/'",
			"plumego --format json routes",
		},
	}
}

func (c *CheckCmd) Help() CommandHelp {
	return CommandHelp{
		Args: "[command-flags]",
		Flags: []HelpItem{
			{"--dir <path>", "Project directory (default: .)"},
			{"--config-only", "Only run configuration checks"},
			{"--deps-only", "Only run dependency checks"},
			{"--security", "Also run security checks (opt-in)"},
			{"--updates", "Check for available dependency updates"},
		},
		Examples: []string{
			"plumego check",
			"plumego check --security",
			"plumego check --deps-only --updates",
			"plumego --format json check --security",
		},
	}
}

func (c *LintCmd) Help() CommandHelp {
	return CommandHelp{
		Args: "[command-flags]",
		Flags: []HelpItem{
			{"--dir <path>", "Project directory (default: .)"},
			{"--check <list>", "Checks to run, comma-separated: boundaries, globals, init-effects, handlers, all (default: all)"},
			{"--strict", "Treat warnings as errors (exit 1)"},
		},
		Subcommands: []HelpItem{
			{"boundaries", "Flag middleware importing service/domain/dto packages (transport-only rule)"},
			{"globals", "Detect mutable package-level variables in handler/middleware packages"},
			{"init-effects", "Detect init() functions that launch goroutines, assign globals, or send on channels"},
			{"handlers", "Warn about functions named *Handler that lack net/http signatures"},
		},
		Examples: []string{
			"plumego lint",
			"plumego lint --check globals,init-effects",
			"plumego lint --strict",
			"plumego --format json lint",
		},
	}
}

func (c *ConfigCmd) Help() CommandHelp {
	return CommandHelp{
		Args: "<show|validate|init|env> [command-flags]",
		Subcommands: []HelpItem{
			{"show", "Display current configuration (sensitive values redacted by default)"},
			{"validate", "Validate configuration against required fields"},
			{"init", "Generate default .env and plumego.yaml files"},
			{"env", "List resolved environment variables"},
		},
		Flags: []HelpItem{
			{"--dir <path>", "Project directory (default: .)"},
			{"--resolve", "Resolve env var references in values (show only)"},
			{"--show-secrets", "Show raw sensitive values without redaction (show only)"},
		},
		Examples: []string{
			"plumego config show",
			"plumego config show --resolve",
			"plumego config validate",
			"plumego config init",
			"plumego config env",
			"plumego --format json config show",
		},
	}
}

func (c *MigrateCmd) Help() CommandHelp {
	return CommandHelp{
		Args: "<create|status|up|down> [command-flags]",
		Subcommands: []HelpItem{
			{"create <name>", "Create up/down migration file pair (offline — no DB connection required)"},
			{"status", "Show applied and pending migrations"},
			{"up", "Apply pending migrations"},
			{"down", "Roll back applied migrations"},
		},
		Flags: []HelpItem{
			{"--dir <path>", "Migrations directory (default: ./migrations)"},
			{"--db-url <url>", "Database connection string (status/up/down)"},
			{"--driver <name>", "Registered database/sql driver name (status/up/down)"},
			{"--steps <n>", "Number of migrations to apply/rollback; 0 = all (up/down only)"},
			{"--config <path>", "Migration config file (default: plumego.migrate.yaml)"},
		},
		Examples: []string{
			"plumego migrate create add_users_table",
			"plumego migrate status --db-url postgres://... --driver postgres",
			"plumego migrate up --db-url postgres://... --driver postgres",
			"plumego migrate down --steps 1 --db-url postgres://... --driver postgres",
			"plumego --format json migrate status --db-url postgres://... --driver postgres",
		},
	}
}

func (c *TestCmd) Help() CommandHelp {
	return CommandHelp{
		Args: "[packages...] [command-flags]",
		Flags: []HelpItem{
			{"--dir <path>", "Project directory (default: .)"},
			{"--race", "Enable race detector"},
			{"--cover", "Enable coverage reporting"},
			{"--coverprofile <path>", "Write coverage profile to file"},
			{"--bench", "Run benchmarks"},
			{"--timeout <duration>", "Per-test timeout (default: 20s)"},
			{"--tags <list>", "Build tags"},
			{"--run <pattern>", "Run tests matching pattern"},
			{"--short", "Run only short tests"},
		},
		Examples: []string{
			"plumego test",
			"plumego test ./internal/...",
			"plumego test --race --cover",
			"plumego test --run TestAuth --cover",
			"plumego --format json test --cover",
		},
	}
}

func (c *BuildCmd) Help() CommandHelp {
	return CommandHelp{
		Args: "[build-target] [command-flags]",
		Flags: []HelpItem{
			{"--output <path>", "Output binary path (default: ./bin/app)"},
			{"--dir <path>", "Project directory (default: .)"},
			{"--ldflags <flags>", "Go linker flags (e.g. -s -w)"},
			{"--tags <list>", "Build tags, comma-separated"},
			{"--race", "Enable race detector"},
			{"--trimpath", "Remove file system paths from binary (default: true)"},
		},
		Examples: []string{
			"plumego build",
			"plumego build --output ./bin/myapp",
			`plumego build --ldflags "-s -w" --output ./bin/myapp`,
			"plumego build --race --output ./bin/myapp-race",
			"plumego --format json build",
		},
	}
}

func (c *InspectCmd) Help() CommandHelp {
	return CommandHelp{
		Args: "[health|metrics|routes|config|info] [command-flags]",
		Subcommands: []HelpItem{
			{"health (default)", "Probe /health, /healthz, /ready, /livez"},
			{"metrics", "Fetch /metrics or /debug/metrics"},
			{"routes", "Fetch /_debug/routes.json"},
			{"config", "Fetch /_debug/config"},
			{"info", "Fetch /_debug/info"},
		},
		Flags: []HelpItem{
			{"--url <url>", "Application base URL (default: http://localhost:8080)"},
			{"--auth <value>", "Authorization header value"},
			{"--timeout <duration>", "Request timeout (default: 10s)"},
		},
		Examples: []string{
			"plumego inspect",
			"plumego inspect health --url http://localhost:3000",
			"plumego inspect metrics",
			"plumego inspect routes",
			"plumego --format json inspect health",
		},
	}
}

func (c *ServeCmd) Help() CommandHelp {
	return CommandHelp{
		Args: "[directory] [command-flags]",
		Flags: []HelpItem{
			{"-a, --addr <addr>", "Listen address (default: :8080)"},
			{"-p, --port <port>", "Listen port shorthand (e.g. 3000); sets --addr to :PORT"},
		},
		Examples: []string{
			"plumego serve",
			"plumego serve ./public",
			"plumego serve ./dist --port 3000",
			"plumego serve ./dist --addr :3000",
			"plumego --format json serve ./public --addr :4000",
		},
	}
}

func (c *VersionCmd) Help() CommandHelp {
	return CommandHelp{
		Args: "",
		Flags: []HelpItem{
			{"(none)", "Version takes no command-specific flags; use global --format for output format"},
		},
		Examples: []string{
			"plumego version",
			"plumego --format json version",
		},
	}
}
