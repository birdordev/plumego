package checker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/birdordev/plumego/cmd/plumego/internal/executil"
)

func writeCheckerFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestCheckStructureAcceptsCanonicalCmdAppEntrypoint(t *testing.T) {
	tmp := t.TempDir()
	writeCheckerFile(t, filepath.Join(tmp, "cmd", "app", "main.go"), "package main\nfunc main() {}\n")
	writeCheckerFile(t, filepath.Join(tmp, "README.md"), "# App\n")
	writeCheckerFile(t, filepath.Join(tmp, ".gitignore"), ".env\n")

	detail := CheckStructure(tmp)
	if detail.Status != "passed" {
		t.Fatalf("status = %q, want passed; issues: %#v", detail.Status, detail.Issues)
	}
	for _, issue := range detail.Issues {
		if issue.Message == "application entrypoint not found" {
			t.Fatalf("canonical cmd/app entrypoint should not warn: %#v", detail.Issues)
		}
	}
}

func TestCheckStructureWarnsWhenEntrypointMissing(t *testing.T) {
	tmp := t.TempDir()
	writeCheckerFile(t, filepath.Join(tmp, "README.md"), "# App\n")
	writeCheckerFile(t, filepath.Join(tmp, ".gitignore"), ".env\n")

	detail := CheckStructure(tmp)
	if detail.Status != "warning" {
		t.Fatalf("status = %q, want warning", detail.Status)
	}
	if len(detail.Issues) != 1 || detail.Issues[0].Message != "application entrypoint not found" {
		t.Fatalf("expected entrypoint issue, got %#v", detail.Issues)
	}
}

func TestCheckSecurityReportsEnvParseError(t *testing.T) {
	tmp := t.TempDir()
	writeCheckerFile(t, filepath.Join(tmp, ".env"), "INVALID\n")

	detail := CheckSecurity(tmp, ".env")
	if detail.Status != "failed" {
		t.Fatalf("status = %q, want failed", detail.Status)
	}
	if len(detail.Issues) != 1 || detail.Issues[0].Severity != "high" {
		t.Fatalf("expected one high-severity parse issue, got %#v", detail.Issues)
	}
}

func TestCheckSecurityUsesSharedRequiredSecrets(t *testing.T) {
	tmp := t.TempDir()
	writeCheckerFile(t, filepath.Join(tmp, ".env"), "WS_SECRET=secret\n")
	t.Setenv("JWT_SECRET", "")

	detail := CheckSecurity(tmp, ".env")
	foundJWT := false
	for _, issue := range detail.Issues {
		if issue.Message == "JWT_SECRET not set in environment or .env" {
			foundJWT = true
		}
	}
	if !foundJWT {
		t.Fatalf("expected JWT_SECRET issue, got %#v", detail.Issues)
	}
}

func TestCheckDependenciesSkipsUpdatesByDefault(t *testing.T) {
	tmp := t.TempDir()
	writeCheckerFile(t, filepath.Join(tmp, "go.mod"), "module example.com/checkdeps\n\ngo 1.26.0\n")

	var calls [][]string
	run := func(_ context.Context, opts executil.Options) (executil.Result, error) {
		calls = append(calls, append([]string{opts.Name}, opts.Args...))
		return executil.Result{Stdout: "all modules verified\n"}, nil
	}

	detail := checkDependencies(tmp, DependencyOptions{}, run)
	if detail.Status != "passed" {
		t.Fatalf("status = %q, want passed; issues: %#v", detail.Status, detail.Issues)
	}
	if len(calls) != 1 {
		t.Fatalf("expected only go mod verify call, got %#v", calls)
	}
	if got := calls[0]; len(got) != 3 || got[0] != "go" || got[1] != "mod" || got[2] != "verify" {
		t.Fatalf("unexpected dependency command: %#v", got)
	}
}

func TestCheckDependenciesRunsUpdatesWhenRequested(t *testing.T) {
	tmp := t.TempDir()
	writeCheckerFile(t, filepath.Join(tmp, "go.mod"), "module example.com/checkdeps\n\ngo 1.26.0\n")

	var calls [][]string
	run := func(_ context.Context, opts executil.Options) (executil.Result, error) {
		calls = append(calls, append([]string{opts.Name}, opts.Args...))
		if len(opts.Args) >= 4 && opts.Args[0] == "list" {
			return executil.Result{Stdout: "example.com/mod v1.0.0 [v1.1.0]\n"}, nil
		}
		return executil.Result{Stdout: "all modules verified\n"}, nil
	}

	detail := checkDependencies(tmp, DependencyOptions{CheckUpdates: true}, run)
	if detail.Status != "warning" {
		t.Fatalf("status = %q, want warning; issues: %#v", detail.Status, detail.Issues)
	}
	if len(calls) != 2 {
		t.Fatalf("expected verify and update calls, got %#v", calls)
	}
	updateCall := calls[1]
	want := []string{"go", "list", "-u", "-m", "all"}
	if len(updateCall) != len(want) {
		t.Fatalf("update call = %#v, want %#v", updateCall, want)
	}
	for i := range want {
		if updateCall[i] != want[i] {
			t.Fatalf("update call = %#v, want %#v", updateCall, want)
		}
	}
	if len(detail.Outdated) != 1 || detail.Outdated[0] != "example.com/mod v1.0.0 [v1.1.0]" {
		t.Fatalf("unexpected outdated modules: %#v", detail.Outdated)
	}
}
