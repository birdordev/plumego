package commands

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/birdordev/plumego/cmd/plumego/internal/output"
)

// Context carries shared CLI dependencies and configuration.
type Context struct {
	Out     *output.Formatter
	EnvFile string
}

// resolveDir converts a directory flag to an absolute path and verifies it exists.
func resolveDir(dir string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("invalid directory: %w", err)
	}

	info, err := os.Stat(absDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("directory not found: %s", absDir)
		}
		return "", fmt.Errorf("failed to inspect directory %s: %w", absDir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", absDir)
	}

	return absDir, nil
}

type boolFlag interface {
	IsBoolFlag() bool
}

// parseInterspersedFlags parses flags even when positional arguments are mixed
// between them, and returns the positional arguments in original order.
func parseInterspersedFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	flagArgs := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))
	stopParsingFlags := false

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if stopParsingFlags {
			positionals = append(positionals, arg)
			continue
		}

		if arg == "--" {
			stopParsingFlags = true
			continue
		}

		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}

		name := strings.TrimLeft(arg, "-")
		hasInlineValue := false
		if idx := strings.Index(name, "="); idx >= 0 {
			name = name[:idx]
			hasInlineValue = true
		}

		fl := fs.Lookup(name)
		if fl == nil {
			flagArgs = append(flagArgs, arg)
			continue
		}

		flagArgs = append(flagArgs, arg)

		if hasInlineValue {
			continue
		}

		if bf, ok := fl.Value.(boolFlag); ok && bf.IsBoolFlag() {
			continue
		}

		if i+1 >= len(args) {
			return nil, fmt.Errorf("flag needs an argument: %s", arg)
		}

		i++
		flagArgs = append(flagArgs, args[i])
	}

	if err := fs.Parse(flagArgs); err != nil {
		return nil, err
	}

	if tail := fs.Args(); len(tail) > 0 {
		positionals = append(positionals, tail...)
	}

	return positionals, nil
}
