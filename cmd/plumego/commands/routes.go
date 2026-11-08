package commands

import (
	"flag"
	"fmt"
	"io"

	"github.com/birdordev/plumego/cmd/plumego/internal/routeanalyzer"
)

type RoutesCmd struct{}

func (c *RoutesCmd) Name() string  { return "routes" }
func (c *RoutesCmd) Short() string { return "List routes via static analysis (best-effort)" }

func (c *RoutesCmd) Run(ctx *Context, args []string) error {
	fs := flag.NewFlagSet("routes", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	dir := fs.String("dir", ".", "Project directory")
	method := fs.String("method", "", "Filter by HTTP method")
	pattern := fs.String("pattern", "", "Filter routes by pattern")
	showMiddleware := fs.Bool("middleware", false, "Not yet supported by the static analyzer")
	group := fs.String("group", "", "Not yet supported by the static analyzer")
	sortBy := fs.String("sort", "path", "Sort by: path, method")

	positionals, err := parseInterspersedFlags(fs, args)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("invalid flags: %v", err), 1)
	}
	if len(positionals) > 0 {
		return ctx.Out.Error(fmt.Sprintf("unexpected arguments: %v", positionals), 1)
	}
	if *showMiddleware {
		return ctx.Out.Error("middleware extraction is not supported by the static analyzer yet", 1)
	}
	if *group != "" {
		return ctx.Out.Error("route group filtering is not supported by the static analyzer yet", 1)
	}
	if err := routeanalyzer.ValidateSortBy(*sortBy); err != nil {
		return ctx.Out.Error(err.Error(), 1)
	}

	absDir, err := resolveDir(*dir)
	if err != nil {
		return ctx.Out.Error(err.Error(), 1)
	}

	routes, err := routeanalyzer.AnalyzeRoutes(absDir, routeanalyzer.AnalyzeOptions{
		Method:  *method,
		Pattern: *pattern,
		SortBy:  *sortBy,
	})
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("failed to analyze routes: %v", err), 1)
	}

	result := map[string]any{
		"routes": routes.Routes,
		"total":  routes.Total,
		"note":   "static analysis only; route groups, middleware chains, dynamic path patterns, and factory-wrapped registrations are not detected",
	}

	return ctx.Out.Success("Routes analyzed successfully", result)
}
