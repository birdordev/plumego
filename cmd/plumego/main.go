package main

import (
	"fmt"
	"os"

	"github.com/birdordev/plumego/cmd/plumego/commands"
	"github.com/birdordev/plumego/cmd/plumego/internal/output"
)

// Build-time variables injected via -ldflags.
var (
	version   = "dev"
	gitCommit = "unknown"
	buildDate = "unknown"
)

func main() {
	if err := commands.Execute(commands.BuildInfo{
		Version:   version,
		GitCommit: gitCommit,
		BuildDate: buildDate,
	}); err != nil {
		if code, ok := output.ExitCode(err); ok {
			os.Exit(code)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
