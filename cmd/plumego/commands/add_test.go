package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/birdordev/plumego/cmd/plumego/internal/executil"
	"github.com/birdordev/plumego/cmd/plumego/internal/output"
)

func TestAddValidExtensionRunsGoGet(t *testing.T) {
	moduleDir := writeExtensionModule(t, extensionModuleOptions{})
	runner := &fakeAddRunner{moduleDir: moduleDir}
	code, stdout := runAddCommand(t, runner, []string{"github.com/acme/plumego-example", "--version", "v0.1.0"})

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; output=%s", code, stdout)
	}
	if !runner.goGetRan {
		t.Fatal("expected go get to run")
	}
	if got, want := runner.commands, []string{
		"go list -m -json github.com/acme/plumego-example@v0.1.0",
		"go mod download -json github.com/acme/plumego-example@v0.1.0",
		"go vet ./...",
		"go get github.com/acme/plumego-example@v0.1.0",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %#v, want %#v", got, want)
	}
	if !strings.Contains(stdout, `"module_added": true`) || !strings.Contains(stdout, `"schema validation"`) {
		t.Fatalf("expected compliance report, got: %s", stdout)
	}
}

func TestAddMissingSchemaExitsBeforeGoGet(t *testing.T) {
	moduleDir := t.TempDir()
	runner := &fakeAddRunner{moduleDir: moduleDir}
	code, stdout := runAddCommand(t, runner, []string{"github.com/acme/plumego-example"})

	if code != 1 {
		t.Fatalf("exit code = %d, want 1; output=%s", code, stdout)
	}
	if runner.goGetRan {
		t.Fatal("go get ran before schema validation passed")
	}
	if !strings.Contains(stdout, "schema not found") {
		t.Fatalf("expected schema not found message, got: %s", stdout)
	}
}

func TestAddInvalidSchemaExitsBeforeGoGet(t *testing.T) {
	moduleDir := writeExtensionModule(t, extensionModuleOptions{omitName: true})
	runner := &fakeAddRunner{moduleDir: moduleDir}
	code, stdout := runAddCommand(t, runner, []string{"github.com/acme/plumego-example"})

	if code != 1 {
		t.Fatalf("exit code = %d, want 1; output=%s", code, stdout)
	}
	if runner.goGetRan {
		t.Fatal("go get ran before schema validation passed")
	}
	if !strings.Contains(stdout, "missing required field") || !strings.Contains(stdout, "name") {
		t.Fatalf("expected missing name message, got: %s", stdout)
	}
}

func TestAddForbiddenImportExitsBeforeGoGet(t *testing.T) {
	moduleDir := writeExtensionModule(t, extensionModuleOptions{forbiddenImport: true})
	runner := &fakeAddRunner{moduleDir: moduleDir}
	code, stdout := runAddCommand(t, runner, []string{"github.com/acme/plumego-example"})

	if code != 1 {
		t.Fatalf("exit code = %d, want 1; output=%s", code, stdout)
	}
	if runner.goGetRan {
		t.Fatal("go get ran before forbidden import scan passed")
	}
	if !strings.Contains(stdout, "forbidden import detected") || !strings.Contains(stdout, "github.com/birdordev/plumego/contract") {
		t.Fatalf("expected forbidden import message, got: %s", stdout)
	}
}

func TestAddGoListNetworkFailureExitsWithRetrySuggestion(t *testing.T) {
	runner := &fakeAddRunner{goListErr: errors.New("network unavailable")}
	code, stdout := runAddCommand(t, runner, []string{"github.com/acme/plumego-example"})

	if code != 1 {
		t.Fatalf("exit code = %d, want 1; output=%s", code, stdout)
	}
	if runner.goGetRan {
		t.Fatal("go get ran after go list failure")
	}
	if !strings.Contains(stdout, "retry after checking network access and GOPROXY") {
		t.Fatalf("expected retry suggestion, got: %s", stdout)
	}
}

type fakeAddRunner struct {
	moduleDir string
	goListErr error
	commands  []string
	goGetRan  bool
}

func (r *fakeAddRunner) Run(_ context.Context, opts executil.Options) (executil.Result, error) {
	command := opts.Name + " " + strings.Join(opts.Args, " ")
	r.commands = append(r.commands, command)
	switch {
	case hasArgsPrefix(opts.Args, []string{"list", "-m", "-json"}):
		if r.goListErr != nil {
			return executil.Result{Stderr: r.goListErr.Error()}, r.goListErr
		}
		payload, _ := json.Marshal(goListModule{Path: "github.com/acme/plumego-example", Version: "v0.1.0"})
		return executil.Result{Stdout: string(payload)}, nil
	case hasArgsPrefix(opts.Args, []string{"mod", "download", "-json"}):
		payload, _ := json.Marshal(goDownloadModule{Path: "github.com/acme/plumego-example", Version: "v0.1.0", Dir: r.moduleDir})
		return executil.Result{Stdout: string(payload)}, nil
	case reflect.DeepEqual(opts.Args, []string{"vet", "./..."}):
		return executil.Result{}, nil
	case len(opts.Args) == 2 && opts.Args[0] == "get":
		r.goGetRan = true
		return executil.Result{}, nil
	default:
		return executil.Result{}, errors.New("unexpected command: " + command)
	}
}

func hasArgsPrefix(args []string, prefix []string) bool {
	return len(args) >= len(prefix) && reflect.DeepEqual(args[:len(prefix)], prefix)
}

type extensionModuleOptions struct {
	omitName        bool
	forbiddenImport bool
}

func writeExtensionModule(t *testing.T, opts extensionModuleOptions) string {
	t.Helper()
	dir := t.TempDir()
	name := "name: example\n"
	if opts.omitName {
		name = ""
	}
	manifest := name + `module_path: github.com/acme/plumego-example
status: experimental
handler_shape: "func(http.ResponseWriter, *http.Request)"
test_commands:
  - go test ./...
forbidden_imports:
  - github.com/birdordev/plumego/core
  - github.com/birdordev/plumego/router
  - github.com/birdordev/plumego/contract
  - github.com/birdordev/plumego/middleware
  - github.com/birdordev/plumego/security
  - github.com/birdordev/plumego/store
  - github.com/birdordev/plumego/health
  - github.com/birdordev/plumego/log
  - github.com/birdordev/plumego/metrics
no_init_side_effects: true
no_globals: true
owner: platform
`
	if err := os.WriteFile(filepath.Join(dir, communityExtensionManifest), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	source := `package example

import "net/http"

func Handler(w http.ResponseWriter, r *http.Request) {}
`
	if opts.forbiddenImport {
		source = `package example

import (
	"net/http"
	"github.com/birdordev/plumego/contract"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	_ = contract.ContentTypeJSON
}
`
	}
	if err := os.WriteFile(filepath.Join(dir, "handler.go"), []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return dir
}

func runAddCommand(t *testing.T, runner *fakeAddRunner, args []string) (int, string) {
	t.Helper()
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module example.com/app\n\ngo 1.26.0\n"), 0o644); err != nil {
		t.Fatalf("write project go.mod: %v", err)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prev)
	})

	formatter := output.NewFormatter()
	formatter.SetFormat("json")
	var out bytes.Buffer
	var errOut bytes.Buffer
	formatter.SetWriters(&out, &errOut)
	ctx := &Context{Out: formatter}
	err = (&AddCmd{runner: runner}).Run(ctx, args)
	if err == nil {
		return 0, out.String()
	}
	if code, ok := output.ExitCode(err); ok {
		return code, out.String() + errOut.String()
	}
	return 1, out.String() + errOut.String()
}
