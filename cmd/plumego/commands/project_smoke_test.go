package commands

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/birdordev/plumego/cmd/plumego/internal/output"
)

func TestCLI_NewAcceptsScenarioTemplatesDryRun(t *testing.T) {
	templates := []string{
		"rest-api",
		"tenant-api",
		"gateway",
		"realtime",
		"ai-service",
		"ops-service",
	}

	for _, template := range templates {
		t.Run(template, func(t *testing.T) {
			stdout, _, err := runCLI(t, []string{
				"--format", "json",
				"new", "--template", template, "--dry-run", "trust-check",
			}, t.TempDir())
			if err != nil {
				t.Fatalf("new dry-run failed: %v\noutput: %s", err, stdout)
			}

			var payload struct {
				Status string `json:"status"`
				Data   struct {
					DryRun       bool     `json:"dry_run"`
					Template     string   `json:"template"`
					FilesCreated []string `json:"files_created"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
				t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
			}
			if payload.Status != "success" {
				t.Fatalf("expected success envelope, got %#v", payload)
			}
			if !payload.Data.DryRun {
				t.Fatalf("expected dry_run=true, got %#v", payload)
			}
			if payload.Data.Template != template {
				t.Fatalf("expected template %q, got %q", template, payload.Data.Template)
			}
			if len(payload.Data.FilesCreated) == 0 {
				t.Fatalf("expected file preview for %q, got none", template)
			}
		})
	}
}

func TestCLI_NewParsesFlagsAfterProjectName(t *testing.T) {
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "doc-order-app")

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"new", "doc-order-app",
		"--template", "api",
		"--dir", projectDir,
		"--module", "example.com/doc-order-app",
		"--no-git",
	}, "")
	if err != nil {
		t.Fatalf("new with flags after project name failed: %v\noutput: %s", err, stdout)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Template string `json:"template"`
			Path     string `json:"path"`
			Module   string `json:"module"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "success" || payload.Data.Template != "api" || payload.Data.Path != projectDir || payload.Data.Module != "example.com/doc-order-app" {
		t.Fatalf("unexpected new payload: %#v", payload)
	}
	if _, err := os.Stat(filepath.Join(projectDir, "internal", "resource", "users.go")); err != nil {
		t.Fatalf("expected api template resource file: %v", err)
	}
	for _, file := range []string{
		"Makefile",
		".github/workflows/ci.yml",
		"AGENTS.md",
		"CLAUDE.md",
	} {
		if _, err := os.Stat(filepath.Join(projectDir, file)); err != nil {
			t.Fatalf("expected generated project control file %s: %v", file, err)
		}
	}
}

func TestCLI_NewNextStepUsesOutputDir(t *testing.T) {
	tmpDir := t.TempDir()
	customDir := filepath.Join(tmpDir, "services", "myapp")

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"new", "myapp",
		"--dir", customDir,
		"--module", "example.com/myapp",
		"--no-git",
	}, "")
	if err != nil {
		t.Fatalf("new with custom dir failed: %v\noutput: %s", err, stdout)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			NextSteps []string `json:"next_steps"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if len(payload.Data.NextSteps) == 0 {
		t.Fatalf("expected next_steps, got none; output: %s", stdout)
	}
	cdStep := payload.Data.NextSteps[0]
	// The cd step should point to the custom output dir, not just the project name.
	if !strings.Contains(cdStep, "services") {
		t.Fatalf("cd hint should reference output dir path, got %q", cdStep)
	}
	if cdStep == "cd myapp" {
		t.Fatalf("cd hint must not use bare project name when --dir is set, got %q", cdStep)
	}
}

func TestCLI_NewNextStepUsesDefaultDir(t *testing.T) {
	tmpDir := t.TempDir()
	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"new", "myservice",
		"--module", "example.com/myservice",
		"--no-git",
	}, tmpDir)
	if err != nil {
		t.Fatalf("new with default dir failed: %v\noutput: %s", err, stdout)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			NextSteps []string `json:"next_steps"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if len(payload.Data.NextSteps) == 0 {
		t.Fatalf("expected next_steps, got none; output: %s", stdout)
	}
	cdStep := payload.Data.NextSteps[0]
	if !strings.Contains(cdStep, "myservice") {
		t.Fatalf("cd hint should reference project dir, got %q", cdStep)
	}
}

func TestCLI_NewRejectsUnexpectedArguments(t *testing.T) {
	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"new", "first", "second", "--dry-run",
	}, t.TempDir())
	if err == nil {
		t.Fatalf("expected unexpected argument error")
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" || !strings.Contains(payload.Message, "unexpected arguments") {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestCLI_NewRejectsInvalidModuleBeforeWriting(t *testing.T) {
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "bad-app")

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"new", "bad-app",
		"--dir", projectDir,
		"--module", "https://example.com/bad",
		"--no-git",
	}, "")
	if err == nil {
		t.Fatalf("expected invalid module error")
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" || !strings.Contains(payload.Message, "invalid module path") {
		t.Fatalf("unexpected new payload: %#v", payload)
	}
	if _, err := os.Stat(projectDir); !os.IsNotExist(err) {
		t.Fatalf("invalid module should not create project dir, stat err=%v", err)
	}
}

func TestCLI_GeneratedCanonicalProjectStableWorkflow(t *testing.T) {
	skipSlowCLISmoke(t)

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "stable-app")

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"new",
		"--template", "canonical",
		"--dir", projectDir,
		"--module", "example.com/stable-app",
		"--no-git",
		"stable-app",
	}, "")
	if err != nil {
		t.Fatalf("new canonical failed: %v\noutput: %s", err, stdout)
	}

	runGoCommand(t, projectDir, "mod", "tidy")

	binary := filepath.Join(projectDir, "bin", "app")
	if stdout, _, err := runCLI(t, []string{"--format", "json", "build", "--dir", projectDir, "--output", binary}, ""); err != nil {
		t.Fatalf("build generated project failed: %v\noutput: %s", err, stdout)
	}
	if stdout, _, err := runCLI(t, []string{"--format", "json", "test", "--dir", projectDir}, ""); err != nil {
		t.Fatalf("test generated project failed: %v\noutput: %s", err, stdout)
	}
	if stdout, _, err := runCLI(t, []string{"--format", "json", "check"}, projectDir); err != nil {
		t.Fatalf("check generated project failed: %v\noutput: %s", err, stdout)
	}
	specPath := filepath.Join(projectDir, "openapi.json")
	if stdout, _, err := runCLI(t, []string{"generate", "spec", "--dir", projectDir, "--output", specPath}, ""); err != nil {
		t.Fatalf("generate project spec failed: %v\n%s", err, stdout)
	}
	if spec, err := os.ReadFile(specPath); err != nil || !strings.Contains(string(spec), "/api/v1/items") {
		t.Fatalf("generated project spec missing item route: %v", err)
	}
	if runtime.GOOS != "windows" {
		exerciseGeneratedServer(t, binary, projectDir)
	}
}

func TestCLI_NewRejectsInvalidTemplate(t *testing.T) {
	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"new", "--template", "invalid-template-name", "--dry-run", "trust-check",
	}, t.TempDir())
	if err == nil {
		t.Fatalf("expected invalid template error")
	}

	code, ok := output.ExitCode(err)
	if !ok || code != 3 {
		t.Fatalf("expected exit code 3, got %d (ok=%v)", code, ok)
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" {
		t.Fatalf("expected error status, got %q", payload.Status)
	}
	if !strings.Contains(payload.Message, "invalid-template-name") {
		t.Fatalf("expected invalid template in message, got %q", payload.Message)
	}
	if !strings.Contains(payload.Message, "rest-api") {
		t.Fatalf("expected supported scenario templates in message, got %q", payload.Message)
	}
}

func runGoCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go %s failed: %v\n%s", strings.Join(args, " "), err, string(output))
	}
}

func skipSlowCLISmoke(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping slow CLI smoke test in short mode")
	}
}

func TestCLIDocsExplainTestLayersAndBuildArtifacts(t *testing.T) {
	readDoc := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return string(data)
	}

	docs := readDoc(filepath.Join("..", "README.md")) + "\n" + readDoc(filepath.Join("..", "MODULE.md"))
	for _, want := range []string{
		"go test -short ./commands",
		"fast command-contract gate",
		"go test ./...",
		"slow generated-project smoke layer",
		"go build -o ../../bin/plumego .",
		"cmd/plumego/plumego",
		"stale binary",
	} {
		if !strings.Contains(docs, want) {
			t.Fatalf("CLI docs missing %q", want)
		}
	}
}

func TestCLI_NewWritesLocalPlumegoReplaceWhenRunFromCheckout(t *testing.T) {
	root := findPlumegoModuleRoot(".")
	if root == "" {
		t.Skip("not running from a plumego checkout")
	}

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "demo")
	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"new", "--dir", projectDir, "--no-git", "demo",
	}, "")
	if err != nil {
		t.Fatalf("new failed: %v\noutput: %s", err, stdout)
	}

	data, err := os.ReadFile(filepath.Join(projectDir, "go.mod"))
	if err != nil {
		t.Fatalf("read generated go.mod: %v", err)
	}
	want := "replace github.com/birdordev/plumego => " + filepath.ToSlash(root)
	if !strings.Contains(string(data), want) {
		t.Fatalf("generated go.mod missing local replace %q:\n%s", want, string(data))
	}
}

func exerciseGeneratedServer(t *testing.T, binary, dir string) {
	t.Helper()
	bind, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := bind.Addr().String()
	if err := bind.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-addr", address)
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		cancel()
		if done != nil {
			<-done
		}
	}()
	client := &http.Client{Timeout: time.Second}
	base := "http://" + address
	for {
		response, err := client.Get(base + "/readyz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("generated app readiness = %d", response.StatusCode)
			}
			break
		}
		select {
		case err := <-done:
			done = nil
			t.Fatalf("generated server exited early: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	response, err := client.Post(base+"/api/v1/items", "application/json", strings.NewReader(`{"name":"smoke"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusCreated || !strings.Contains(string(body), "smoke") {
		t.Fatalf("generated endpoint: status=%d body=%q error=%v", response.StatusCode, body, err)
	}
	var created struct {
		Data struct{ ID string }
	}
	if err := json.Unmarshal(body, &created); err != nil || created.Data.ID == "" {
		t.Fatalf("generated item id: %v", err)
	}
	itemURL := base + "/api/v1/items/" + created.Data.ID
	for _, step := range []struct {
		method, input, bodyContains string
		status                      int
	}{
		{http.MethodGet, "", "smoke", 200},
		{http.MethodPut, `{"name":"changed"}`, "changed", 200},
		{http.MethodDelete, "", "", 204},
		{http.MethodGet, "", "", 404},
	} {
		req, err := http.NewRequestWithContext(ctx, step.method, itemURL, strings.NewReader(step.input))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil || resp.StatusCode != step.status || !strings.Contains(string(data), step.bodyContains) {
			t.Fatalf("generated %s: status=%d body=%q error=%v", step.method, resp.StatusCode, data, err)
		}
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		done = nil
		if err != nil {
			t.Fatalf("generated app failed graceful shutdown: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
