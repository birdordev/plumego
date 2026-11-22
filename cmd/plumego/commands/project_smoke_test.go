package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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
		"--plumego-version", "v1.2.3",
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
	projectDir := filepath.Join(tmpDir, "stable app")
	root := detectLocalPlumegoRoot()
	if root == "" {
		t.Fatal("cannot locate Plumego checkout for generated-project test")
	}

	if !t.Run("generate outside checkout", func(t *testing.T) {
		stdout, _, err := runCLI(t, []string{
			"--format", "json",
			"new",
			"--template", "canonical",
			"--dir", projectDir,
			"--module", "example.com/stable-app",
			"--plumego-dir", root,
			"--no-git",
			"stable-app",
		}, tmpDir)
		if err != nil {
			t.Fatalf("new canonical failed: %v\noutput: %s", err, stdout)
		}
	}) {
		return
	}

	writeProbeSmokeTest(t, projectDir)
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
	if stdout, _, err := runCLI(t, []string{"generate", "spec", "--strict", "--dir", projectDir, "--output", specPath}, ""); err != nil {
		t.Fatalf("generate project spec failed: %v\n%s", err, stdout)
	}
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths smokeSpecPaths `json:"paths"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		t.Setenv("APP_SHUTDOWN_TIMEOUT_MS", "1500")
		exerciseGeneratedServer(t, binary, projectDir, "/api/v1/items", spec.Paths)
	}
}

func TestCLI_GeneratedRESTProjectStrictWorkflow(t *testing.T) {
	skipSlowCLISmoke(t)
	dir := t.TempDir()
	project := filepath.Join(dir, "rest-project")
	run := func(args ...string) {
		t.Helper()
		stdout, stderr, err := runCLI(t, args, dir)
		if err != nil {
			t.Fatalf("CLI %v: %v\n%s\n%s", args, err, stdout, stderr)
		}
	}
	root := detectLocalPlumegoRoot()
	run("new", "--template", "rest-api", "--dir", project, "--module", "example.com/rest-project", "--plumego-dir", root, "--no-git", "rest-project")
	binary := filepath.Join(project, "rest-server")
	output := filepath.Join(project, "openapi.json")
	run("generate", "spec", "--strict", "--dir", project, "--output", output)
	run("build", "--dir", project, "--output", binary)
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Paths smokeSpecPaths }
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		exerciseGeneratedServer(t, binary, project, "/api/users", doc.Paths)
	}
}

func TestCLI_NewSelectsExplicitPlumegoVersion(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pinned")
	stdout, _, err := runCLI(t, []string{"new", "pinned", "--dir", dir, "--no-git", "--plumego-version", "v1.2.3"}, "")
	if err != nil {
		t.Fatalf("generate pinned project: %v\n%s", err, stdout)
	}
	mod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mod), "require github.com/birdordev/plumego v1.2.3") || strings.Contains(string(mod), "replace ") {
		t.Fatalf("unexpected pinned go.mod:\n%s", mod)
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

type smokeSpecPaths map[string]map[string]struct {
	Responses map[string]json.RawMessage `json:"responses"`
}

func exerciseGeneratedServer(t *testing.T, binary, dir, collection string, paths smokeSpecPaths) {
	t.Helper()
	member := collection + "/{id}"
	assertStatusDocumented := func(path, method string, status int) {
		t.Helper()
		if _, ok := paths[path][strings.ToLower(method)].Responses[strconv.Itoa(status)]; !ok {
			t.Fatalf("generated spec does not declare %s %s status %d", method, path, status)
		}
	}
	ctx, client, base, shutdown := startSmokeServer(t, binary, dir)
	for _, invalid := range []struct {
		mediaType, body string
		status          int
	}{
		{"text/plain", `{"name":"rejected"}`, http.StatusUnsupportedMediaType},
		{"application/json", `null`, http.StatusBadRequest},
		{"application/json", `{"name":"rejected"}{}`, http.StatusBadRequest},
		{"application/json", `{"name":"rejected","unrecognized-input":"test"}`, http.StatusBadRequest},
		{"application/json", strings.Repeat(" ", 1<<20+1), http.StatusRequestEntityTooLarge},
	} {
		response, err := client.Post(base+collection, invalid.mediaType, strings.NewReader(invalid.body))
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != invalid.status || strings.Contains(string(data), "unrecognized-input") {
			t.Fatalf("generated endpoint accepted or echoed invalid input: status=%d body=%q error=%v", response.StatusCode, data, err)
		}
		assertStatusDocumented(collection, http.MethodPost, response.StatusCode)
	}
	response, err := client.Post(base+collection, "application/json", strings.NewReader(`{"name":"smoke"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusCreated || !strings.Contains(string(body), "smoke") {
		t.Fatalf("generated endpoint: status=%d body=%q error=%v", response.StatusCode, body, err)
	}
	assertStatusDocumented(collection, http.MethodPost, response.StatusCode)
	var created struct {
		Data struct{ ID string }
	}
	if err := json.Unmarshal(body, &created); err != nil || created.Data.ID == "" {
		t.Fatalf("generated item id: %v", err)
	}
	itemURL := base + collection + "/" + created.Data.ID
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
		if err != nil || resp.StatusCode != step.status || !strings.Contains(string(data), step.bodyContains) ||
			(step.status == http.StatusNoContent && len(data) != 0) {
			t.Fatalf("generated %s: status=%d body=%q error=%v", step.method, resp.StatusCode, data, err)
		}
		assertStatusDocumented(member, step.method, resp.StatusCode)
	}
	verifyGeneratedRequestDrain(t, ctx, base, collection, shutdown)
}

func verifyGeneratedRequestDrain(t *testing.T, ctx context.Context, base, collection string, shutdown func(func())) {
	t.Helper()
	address := strings.TrimPrefix(base, "http://")
	conn, err := new(net.Dialer).DialContext(ctx, "tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			t.Fatal(err)
		}
	}
	const payload = `{"name":"drained"}`
	if _, err := fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: %s\r\n"+
		"Content-Type: application/json\r\nContent-Length: %d\r\nExpect: 100-continue\r\n\r\n", collection, address, len(payload)); err != nil {
		t.Fatal(err)
	}
	wire := bufio.NewReader(conn)
	req := &http.Request{Method: http.MethodPost}
	interim, err := http.ReadResponse(wire, req)
	if err != nil || interim.StatusCode != http.StatusContinue {
		t.Fatalf("generated handler did not begin reading the body: response=%v err=%v", interim, err)
	}
	shutdown(func() {
		for {
			c, err := net.DialTimeout("tcp", address, 50*time.Millisecond)
			if err != nil {
				break
			}
			_ = c.Close()
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(5 * time.Millisecond):
			}
		}
		if _, err := io.WriteString(conn, payload); err != nil {
			t.Fatal(err)
		}
		response, err := http.ReadResponse(wire, req)
		if err != nil {
			t.Fatalf("generated process exited before draining the request: %v", err)
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != http.StatusCreated || !strings.Contains(string(body), "drained") {
			t.Fatalf("in-flight request did not complete: status=%d body=%s err=%v", response.StatusCode, body, err)
		}
	})
}

func startSmokeServer(t *testing.T, binary, dir string) (context.Context, *http.Client, string, func(func())) {
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
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, binary, "-addr", address)
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		cancel()
		if done != nil {
			<-done
		}
	})
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
	return ctx, client, base, func(afterSignal func()) {
		t.Helper()
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		if afterSignal != nil {
			afterSignal()
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
}

func writeProbeSmokeTest(t *testing.T, dir string) {
	t.Helper()
	const source = `package handler

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/health"
)

type failedDependency struct{}

func (failedDependency) Name() string { return "store" }
func (failedDependency) Check(context.Context) error { return errors.New("fixture private error") }

func TestGeneratedProbeRedactsDependencyError(t *testing.T) {
	handler := HealthHandler{Checkers: []health.ComponentChecker{failedDependency{}}}
	ready, live := httptest.NewRecorder(), httptest.NewRecorder()
	handler.Ready(ready, httptest.NewRequest("GET", "/readyz", nil))
	handler.Live(live, httptest.NewRequest("GET", "/healthz", nil))
	if ready.Code != 503 || live.Code != 200 ||
		strings.Contains(ready.Body.String(), "fixture private error") ||
		!strings.Contains(ready.Body.String(), ` + "`" + `"store":false` + "`" + `) {
		t.Fatalf("ready=%d %s live=%d", ready.Code, ready.Body, live.Code)
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "internal", "handler", "probe_smoke_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}
