package commands

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/birdordev/plumego/cmd/plumego/internal/output"
)

func mustNewLocalServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	defer func() {
		if rec := recover(); rec != nil {
			msg := fmt.Sprint(rec)
			if strings.Contains(msg, "failed to listen on a port") || strings.Contains(msg, "operation not permitted") {
				t.Skipf("skipping local listener test in restricted runtime: %s", msg)
			}
			panic(rec)
		}
	}()

	return httptest.NewServer(handler)
}

func runCLI(t *testing.T, args []string, cwd string) (string, string, error) {
	t.Helper()

	root := &RootCmd{
		subcommands: make(map[string]Command),
		formatter:   output.NewFormatter(),
	}

	root.Register(&NewCmd{})
	root.Register(&GenerateCmd{})
	root.Register(&AddCmd{})
	root.Register(&DevCmd{})
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
	root.Register(&VersionCmd{})

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	root.formatter.SetWriters(&outBuf, &errBuf)

	if cwd != "" {
		prev, err := os.Getwd()
		if err != nil {
			t.Fatalf("getwd failed: %v", err)
		}
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("chdir failed: %v", err)
		}
		t.Cleanup(func() {
			_ = os.Chdir(prev)
		})
	}

	err := root.Run(args)
	return outBuf.String(), errBuf.String(), err
}

func writeTinyCanonicalProject(t *testing.T, dir string) {
	t.Helper()

	appDir := filepath.Join(dir, "cmd", "app")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatalf("mkdir app dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/tiny\n\ngo 1.26.0\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	mainGo := []byte(`package main

import "fmt"

func main() {
	fmt.Println("ok")
}
`)
	if err := os.WriteFile(filepath.Join(appDir, "main.go"), mainGo, 0644); err != nil {
		t.Fatalf("write cmd/app/main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Tiny\n"), 0644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env\nbin/\n"), 0644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "env.example"), []byte("APP_ADDR=:8080\n"), 0644); err != nil {
		t.Fatalf("write env.example: %v", err)
	}
}

func writeTinyTestProject(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/testproject\n\ngo 1.26.0\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "calc.go"), []byte(`package testproject

func Add(a, b int) int {
	return a + b
}
`), 0644); err != nil {
		t.Fatalf("write calc.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "calc_test.go"), []byte(`package testproject

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatal("bad add")
	}
}
`), 0644); err != nil {
		t.Fatalf("write calc_test.go: %v", err)
	}
}

type cliJSONEnvelope struct {
	Status  string          `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	Error   json.RawMessage `json:"error,omitempty"`
}

type inspectHealthData struct {
	Endpoint string `json:"endpoint"`
}

func TestCLI_VersionJSONOutput(t *testing.T) {
	stdout, _, err := runCLI(t, []string{"--format", "json", "version"}, "")
	if err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse json output: %v\noutput: %s", err, stdout)
	}

	if payload.Status != "success" {
		t.Fatalf("expected status success, got %v", payload.Status)
	}
	if len(payload.Data) == 0 {
		t.Fatalf("expected data in output, got nil")
	}
}

func TestCLI_DefaultFormatIsText(t *testing.T) {
	stdout, _, err := runCLI(t, []string{"version"}, "")
	if err != nil {
		t.Fatalf("version command failed: %v", err)
	}
	// Default format is text; output must begin with "SUCCESS:" and must not be a JSON object.
	if !strings.HasPrefix(strings.TrimSpace(stdout), "SUCCESS:") {
		t.Fatalf("default output should be text (SUCCESS: ...), got: %s", stdout)
	}
	if strings.HasPrefix(strings.TrimSpace(stdout), "{") {
		t.Fatalf("default output must not be JSON, got: %s", stdout)
	}
}

func TestCLI_GlobalInlineFormatFlag(t *testing.T) {
	stdout, _, err := runCLI(t, []string{"--format=json", "version"}, "")
	if err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("inline format output should be json: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "success" {
		t.Fatalf("expected success status, got %q", payload.Status)
	}
}

func TestCLI_GlobalFlagsStopAtCommandToken(t *testing.T) {
	// Global flags must come before the command token; --format after the command
	// must be rejected as an unknown argument rather than silently consumed.
	stdout, _, err := runCLI(t, []string{"--format", "json", "version", "--format", "text"}, "")
	if err == nil {
		t.Fatalf("expected command-local --format to fail instead of being consumed globally")
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("expected json error envelope: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" {
		t.Fatalf("expected error status, got %q", payload.Status)
	}
}

func TestCLI_JSONEnvelopeIsCommandOutput(t *testing.T) {
	stdout, _, err := runCLI(t, []string{"--format", "json", "version"}, "")
	if err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse json output: %v\noutput: %s", err, stdout)
	}

	if payload.Status != "success" {
		t.Fatalf("expected CLI status field, got %v", payload.Status)
	}
	if payload.Message == "" {
		t.Fatalf("expected CLI message field, got: %#v", payload)
	}
	if len(payload.Error) != 0 {
		t.Fatalf("CLI success output should not mimic HTTP error envelope: %#v", payload)
	}
}

func TestCLI_InvalidFormatFailsClosed(t *testing.T) {
	// When the user provides an unsupported --format value, the CLI must fail with
	// exit code 1. The error is emitted in the fallback (text) format.
	stdout, stderr, err := runCLI(t, []string{"--format", "bogus", "version"}, "")
	if err == nil {
		t.Fatalf("expected invalid format error")
	}

	code, ok := output.ExitCode(err)
	if !ok || code != 1 {
		t.Fatalf("expected exit code 1, got %d (ok=%v)", code, ok)
	}

	combined := stdout + stderr
	if !strings.Contains(combined, "unsupported output format") {
		t.Fatalf("expected 'unsupported output format' in output, got stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestCLI_UnknownCommandExitCode(t *testing.T) {
	_, _, err := runCLI(t, []string{"unknown-command"}, "")
	if err == nil {
		t.Fatalf("expected error for unknown command")
	}

	code, ok := output.ExitCode(err)
	if !ok || code != 1 {
		t.Fatalf("expected exit code 1, got %d (ok=%v)", code, ok)
	}
}

func TestCLI_GenerateHandlerUsesCanonicalDefaultPath(t *testing.T) {
	tmpDir := t.TempDir()
	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"generate", "handler", "User", "--methods", "GET",
	}, tmpDir)
	if err != nil {
		t.Fatalf("generate handler failed: %v\noutput: %s", err, stdout)
	}

	generatedPath := filepath.Join(tmpDir, "internal", "handler", "user.go")
	if _, err := os.Stat(generatedPath); err != nil {
		t.Fatalf("expected generated handler at %s: %v", generatedPath, err)
	}
}

func TestCLI_GenerateUsesDirFlagAndRelativeOutput(t *testing.T) {
	projectDir := t.TempDir()
	cwd := t.TempDir()
	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"generate", "middleware", "Audit", "--dir", projectDir, "--output", "internal/middleware/audit.go",
	}, cwd)
	if err != nil {
		t.Fatalf("generate middleware failed: %v\noutput: %s", err, stdout)
	}

	generatedPath := filepath.Join(projectDir, "internal", "middleware", "audit.go")
	if _, err := os.Stat(generatedPath); err != nil {
		t.Fatalf("expected generated middleware at %s: %v", generatedPath, err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "internal")); !os.IsNotExist(err) {
		t.Fatalf("generate should not write relative output under cwd, stat err=%v", err)
	}
}

func TestCLI_GenerateRejectsUnsupportedMethod(t *testing.T) {
	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"generate", "handler", "User", "--methods", "TRACE",
	}, t.TempDir())
	if err == nil {
		t.Fatalf("expected unsupported method to fail")
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" || !strings.Contains(payload.Message, "unsupported HTTP method") {
		t.Fatalf("unexpected generation error: %#v", payload)
	}
}

func TestCLI_GenerateRejectsUnexpectedArguments(t *testing.T) {
	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"generate", "handler", "User", "extra",
	}, t.TempDir())
	if err == nil {
		t.Fatalf("expected unexpected argument error")
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" || !strings.Contains(payload.Message, "unexpected arguments") {
		t.Fatalf("unexpected generation error: %#v", payload)
	}
}

func TestCLI_GlobalFlagsDoNotLeakAcrossRuns(t *testing.T) {
	stdout, _, err := runCLI(t, []string{"--quiet", "version"}, "")
	if err != nil {
		t.Fatalf("quiet version command failed: %v", err)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("expected no output in quiet mode, got: %s", stdout)
	}

	stdout, _, err = runCLI(t, []string{"version"}, "")
	if err != nil {
		t.Fatalf("version command failed: %v", err)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatal("expected output after quiet run, got empty output")
	}
}

func TestCLI_VersionRejectsUnexpectedArguments(t *testing.T) {
	stdout, _, err := runCLI(t, []string{"--format", "json", "version", "extra"}, "")
	if err == nil {
		t.Fatalf("expected version extra argument error")
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" || !strings.Contains(payload.Message, "unexpected arguments") {
		t.Fatalf("unexpected version error payload: %#v", payload)
	}
}

func TestCLI_MigrateCreateParsesFlagsAfterSubcommand(t *testing.T) {
	tmpDir := t.TempDir()
	migrationDir := filepath.Join(tmpDir, "db", "migrations")

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"migrate", "create", "add_users_table", "--dir", migrationDir,
	}, "")
	if err != nil {
		t.Fatalf("migrate create failed: %v", err)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Directory string `json:"directory"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "success" {
		t.Fatalf("expected success status, got %q", payload.Status)
	}

	absDir, err := filepath.Abs(migrationDir)
	if err != nil {
		t.Fatalf("failed to resolve migration dir: %v", err)
	}
	if payload.Data.Directory != absDir {
		t.Fatalf("expected migration directory %q, got %q", absDir, payload.Data.Directory)
	}
}

func TestCLI_MigrateRuntimeRequiresRegisteredDriver(t *testing.T) {
	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"migrate", "status",
		"--driver", "plumego_missing_driver",
		"--db-url", "postgres://localhost/plumego",
	}, "")
	if err == nil {
		t.Fatalf("expected unsupported driver error")
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" {
		t.Fatalf("expected error status, got %q", payload.Status)
	}
	if !strings.Contains(payload.Message, "not registered") || !strings.Contains(payload.Message, "migrate create") {
		t.Fatalf("unexpected unsupported driver message: %#v", payload)
	}
}

func TestCLI_MigrateReadsConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "plumego.migrate.yaml")
	if err := os.WriteFile(configPath, []byte("driver: plumego_missing_driver\ndb_url: postgres://localhost/plumego\ndir: migrations\n"), 0644); err != nil {
		t.Fatalf("write migrate config: %v", err)
	}

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"migrate", "status",
		"--config", configPath,
	}, tmpDir)
	if err == nil {
		t.Fatalf("expected unsupported driver error")
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" || !strings.Contains(payload.Message, "plumego_missing_driver") {
		t.Fatalf("unexpected migrate config response: %#v", payload)
	}
}

func TestCLI_MigrateRejectsInvalidSteps(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		message string
	}{
		{
			name:    "negative",
			args:    []string{"migrate", "up", "--steps", "-1"},
			message: "--steps must be greater than or equal to 0",
		},
		{
			name:    "status",
			args:    []string{"migrate", "status", "--steps", "0"},
			message: "--steps is not supported for migrate status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"--format", "json"}, tt.args...)
			stdout, _, err := runCLI(t, args, "")
			if err == nil {
				t.Fatalf("expected steps error")
			}

			var payload cliJSONEnvelope
			if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
				t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
			}
			if payload.Status != "error" || !strings.Contains(payload.Message, tt.message) {
				t.Fatalf("unexpected steps error payload: %#v", payload)
			}
		})
	}
}

func TestCLI_MigrateNoOpUsesWarningEnvelope(t *testing.T) {
	cmd := &MigrateCmd{}
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	out := output.NewFormatter()
	out.SetFormat("json")
	out.SetWriters(&outBuf, &errBuf)

	var db *sql.DB
	err := cmd.applyUp(out, context.Background(), db, "", nil, nil, 0)
	if err == nil {
		t.Fatalf("expected no-op up to return warning exit")
	}
	code, ok := output.ExitCode(err)
	if !ok || code != 2 {
		t.Fatalf("expected exit code 2, got %d (ok=%v)", code, ok)
	}
	var payload cliJSONEnvelope
	if err := json.Unmarshal(outBuf.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse up warning: %v\noutput: %s", err, outBuf.String())
	}
	if payload.Status != "warning" || !strings.Contains(payload.Message, "No migrations to apply") {
		t.Fatalf("unexpected up warning payload: %#v", payload)
	}

	outBuf.Reset()
	errBuf.Reset()
	err = cmd.applyDown(out, context.Background(), db, "", nil, nil, 0)
	if err == nil {
		t.Fatalf("expected no-op down to return warning exit")
	}
	code, ok = output.ExitCode(err)
	if !ok || code != 2 {
		t.Fatalf("expected exit code 2, got %d (ok=%v)", code, ok)
	}
	if err := json.Unmarshal(outBuf.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse down warning: %v\noutput: %s", err, outBuf.String())
	}
	if payload.Status != "warning" || !strings.Contains(payload.Message, "No migrations to roll back") {
		t.Fatalf("unexpected down warning payload: %#v", payload)
	}
}

func TestCLI_InspectParsesFlagsAfterSubcommand(t *testing.T) {
	server := mustNewLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"inspect", "health", "--url", server.URL,
	}, "")
	if err != nil {
		t.Fatalf("inspect health failed: %v", err)
	}

	var payload struct {
		Status string            `json:"status"`
		Data   inspectHealthData `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "success" {
		t.Fatalf("expected success status, got %q", payload.Status)
	}
	if payload.Data.Endpoint != "/health" {
		t.Fatalf("expected endpoint /health, got %v", payload.Data.Endpoint)
	}
}

func TestCLI_InspectUsesCanonicalDebugEndpoints(t *testing.T) {
	tests := []struct {
		name        string
		subcommand  string
		wantPath    string
		wantMessage string
	}{
		{name: "routes", subcommand: "routes", wantPath: "/_debug/routes.json", wantMessage: "Routes retrieved"},
		{name: "config", subcommand: "config", wantPath: "/_debug/config", wantMessage: "Configuration retrieved"},
		{name: "info", subcommand: "info", wantPath: "/_debug/info", wantMessage: "Application info retrieved"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := mustNewLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.wantPath {
					t.Fatalf("unexpected path %q", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
			}))
			defer server.Close()

			stdout, _, err := runCLI(t, []string{
				"--format", "json",
				"inspect", tt.subcommand, "--url", server.URL,
			}, "")
			if err != nil {
				t.Fatalf("inspect %s failed: %v", tt.subcommand, err)
			}

			var payload struct {
				Status  string `json:"status"`
				Message string `json:"message"`
				Data    struct {
					OK bool `json:"ok"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
				t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
			}
			if payload.Status != "success" {
				t.Fatalf("expected success status, got %q", payload.Status)
			}
			if payload.Message != tt.wantMessage {
				t.Fatalf("expected message %q, got %q", tt.wantMessage, payload.Message)
			}
		})
	}
}

func TestCLI_InspectPassesAuthorizationHeaderValue(t *testing.T) {
	server := mustNewLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer local-token" {
			t.Fatalf("unexpected Authorization header %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"inspect", "health", "--url", server.URL, "--auth", "Bearer local-token",
	}, "")
	if err != nil {
		t.Fatalf("inspect auth failed: %v\noutput: %s", err, stdout)
	}
}

func TestCLI_InspectRejectsOversizedResponse(t *testing.T) {
	largeBody := strings.Repeat("x", maxInspectResponseBytes+1)
	server := mustNewLocalServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(largeBody))
	}))
	defer server.Close()

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"inspect", "health", "--url", server.URL,
	}, "")
	if err == nil {
		t.Fatalf("expected oversized inspect response error")
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" || !strings.Contains(payload.Message, "exceeds") {
		t.Fatalf("unexpected inspect payload: %#v", payload)
	}
}

func TestCLI_RoutesRejectsUnexpectedArguments(t *testing.T) {
	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"routes", "extra",
	}, "")
	if err == nil {
		t.Fatalf("expected unexpected argument error")
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" || !strings.Contains(payload.Message, "unexpected arguments") {
		t.Fatalf("unexpected routes payload: %#v", payload)
	}
}

func TestCLI_RoutesRejectsUnsupportedOptions(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{name: "middleware", args: []string{"routes", "--middleware"}, want: "middleware extraction is not supported"},
		{name: "group", args: []string{"routes", "--group", "api"}, want: "group filtering is not supported"},
		{name: "sort", args: []string{"routes", "--sort", "group"}, want: "unsupported route sort field"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stdout, _, err := runCLI(t, append([]string{"--format", "json"}, tt.args...), "")
			if err == nil {
				t.Fatalf("expected routes option error")
			}

			var payload cliJSONEnvelope
			if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
				t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
			}
			if payload.Status != "error" || !strings.Contains(payload.Message, tt.want) {
				t.Fatalf("unexpected routes payload: %#v", payload)
			}
		})
	}
}

func TestCLI_BuildDefaultsToCanonicalCmdApp(t *testing.T) {
	tmpDir := t.TempDir()
	writeTinyCanonicalProject(t, tmpDir)
	outputPath := filepath.Join(tmpDir, "bin", "tiny")

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"build", "--dir", tmpDir, "--output", outputPath,
	}, "")
	if err != nil {
		t.Fatalf("build failed: %v\noutput: %s", err, stdout)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Target string `json:"target"`
			Binary string `json:"binary"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "success" {
		t.Fatalf("expected success status, got %q", payload.Status)
	}
	if payload.Data.Target != "./cmd/app" {
		t.Fatalf("expected build target ./cmd/app, got %q", payload.Data.Target)
	}
	if payload.Data.Binary != outputPath {
		t.Fatalf("expected binary %q, got %q", outputPath, payload.Data.Binary)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("expected build output at %s: %v", outputPath, err)
	}
}

func TestCLI_BuildParsesTargetBeforeFlagsAndRejectsExtras(t *testing.T) {
	tmpDir := t.TempDir()
	writeTinyCanonicalProject(t, tmpDir)
	outputPath := filepath.Join(tmpDir, "bin", "tiny")

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"build", "./cmd/app", "--dir", tmpDir, "--output", outputPath,
	}, "")
	if err != nil {
		t.Fatalf("build with target before flags failed: %v\noutput: %s", err, stdout)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Target string `json:"target"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "success" || payload.Data.Target != "./cmd/app" {
		t.Fatalf("unexpected build payload: %#v", payload)
	}

	stdout, _, err = runCLI(t, []string{
		"--format", "json",
		"build", "./cmd/app", "./extra", "--dir", tmpDir, "--output", outputPath,
	}, "")
	if err == nil {
		t.Fatalf("expected extra target error")
	}

	var errorPayload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &errorPayload); err != nil {
		t.Fatalf("failed to parse error output: %v\noutput: %s", err, stdout)
	}
	if errorPayload.Status != "error" || !strings.Contains(errorPayload.Message, "unexpected arguments") {
		t.Fatalf("unexpected build error payload: %#v", errorPayload)
	}
}

func TestCLI_TestCoverUsesTempProfileByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	writeTinyTestProject(t, tmpDir)

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"test", "--dir", tmpDir, "--cover",
	}, "")
	if err != nil {
		t.Fatalf("test --cover failed: %v\noutput: %s", err, stdout)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			CoveragePercent float64 `json:"coverage_percent"`
			CoverageProfile string  `json:"coverage_profile"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "success" || payload.Data.CoveragePercent <= 0 {
		t.Fatalf("unexpected coverage payload: %#v", payload)
	}
	if payload.Data.CoverageProfile != "" {
		t.Fatalf("default temp coverage profile should not be exposed, got %q", payload.Data.CoverageProfile)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "coverage.out")); !os.IsNotExist(err) {
		t.Fatalf("default coverage should not write coverage.out in project root")
	}
}

func TestCLI_TestParsesPackagesBeforeFlags(t *testing.T) {
	tmpDir := t.TempDir()
	writeTinyTestProject(t, tmpDir)

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"test", "./...", "--dir", tmpDir, "--run", "TestAdd",
	}, "")
	if err != nil {
		t.Fatalf("test with package before flags failed: %v\noutput: %s", err, stdout)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Tests int `json:"tests"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "success" || payload.Data.Tests != 1 {
		t.Fatalf("unexpected test payload: %#v", payload)
	}
}

func TestCLI_TestFailureIncludesStructuredFailures(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module example.com/failtest\n\ngo 1.26.0\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "fail_test.go"), []byte(`package failtest

import "testing"

func TestFailure(t *testing.T) {
	t.Fatal("boom")
}
`), 0644); err != nil {
		t.Fatalf("write fail_test.go: %v", err)
	}

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"test", "--dir", tmpDir,
	}, "")
	if err == nil {
		t.Fatalf("expected failing tests")
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Status   string `json:"status"`
			Failures []struct {
				Package string `json:"package"`
				Test    string `json:"test"`
			} `json:"failures"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" || payload.Data.Status != "failed" || len(payload.Data.Failures) == 0 {
		t.Fatalf("unexpected failure payload: %#v", payload)
	}
	if payload.Data.Failures[0].Test != "TestFailure" {
		t.Fatalf("expected TestFailure in structured failures, got %#v", payload.Data.Failures)
	}
}

func TestParseServeArgsInterspersedFlags(t *testing.T) {
	opts, err := parseServeArgs([]string{"./public", "--addr", "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("parseServeArgs failed: %v", err)
	}
	if opts.dir != "./public" || opts.addr != "127.0.0.1:0" {
		t.Fatalf("unexpected serve options: %#v", opts)
	}
}

func TestCLI_ServeInvalidDirectoryUsesErrorEnvelope(t *testing.T) {
	tmpDir := t.TempDir()
	missingDir := filepath.Join(tmpDir, "missing")

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"serve", "--addr", "127.0.0.1:0", missingDir,
	}, "")
	if err == nil {
		t.Fatalf("expected invalid directory error")
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" || !strings.Contains(payload.Message, "directory does not exist") {
		t.Fatalf("unexpected serve error payload: %#v", payload)
	}
}

func TestCLI_ServeRejectsFileDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "index.html")
	if err := os.WriteFile(filePath, []byte("ok\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"serve", "--addr", "127.0.0.1:0", filePath,
	}, "")
	if err == nil {
		t.Fatalf("expected file path to fail directory validation")
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" || !strings.Contains(payload.Message, "path is not a directory") {
		t.Fatalf("unexpected serve error payload: %#v", payload)
	}
}

func TestCLI_CheckAcceptsCanonicalCmdAppEntrypoint(t *testing.T) {
	tmpDir := t.TempDir()
	writeTinyCanonicalProject(t, tmpDir)

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"check",
	}, tmpDir)
	if err != nil {
		t.Fatalf("check failed: %v\noutput: %s", err, stdout)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Status string `json:"status"`
			Checks struct {
				Structure struct {
					Status string `json:"status"`
					Issues []struct {
						Message string `json:"message"`
					} `json:"issues"`
				} `json:"structure"`
			} `json:"checks"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "success" || payload.Data.Status != "healthy" {
		t.Fatalf("expected healthy success, got %#v", payload)
	}
	if payload.Data.Checks.Structure.Status != "passed" {
		t.Fatalf("expected structure passed, got %#v", payload.Data.Checks.Structure)
	}
	for _, issue := range payload.Data.Checks.Structure.Issues {
		if strings.Contains(issue.Message, "entrypoint") || strings.Contains(issue.Message, "main.go") {
			t.Fatalf("canonical cmd/app entrypoint should not warn, got issue %q", issue.Message)
		}
	}
}

func TestCLI_CheckAcceptsDirFlag(t *testing.T) {
	tmpDir := t.TempDir()
	writeTinyCanonicalProject(t, tmpDir)

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"check", "--dir", tmpDir,
	}, "")
	if err != nil {
		t.Fatalf("check --dir failed: %v\noutput: %s", err, stdout)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "success" || payload.Data.Status != "healthy" {
		t.Fatalf("unexpected check payload: %#v", payload)
	}
}

func TestCLI_CheckRejectsUnexpectedArguments(t *testing.T) {
	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"check", "extra",
	}, "")
	if err == nil {
		t.Fatalf("expected unexpected argument error")
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" || !strings.Contains(payload.Message, "unexpected arguments") {
		t.Fatalf("unexpected check payload: %#v", payload)
	}
}

func TestCLI_CheckDegradedUsesWarningEnvelope(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module example.com/degraded\n\ngo 1.26.0\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	stdout, _, err := runCLI(t, []string{
		"--format", "json",
		"check",
	}, tmpDir)
	if err == nil {
		t.Fatalf("expected degraded check to return exit error")
	}
	code, ok := output.ExitCode(err)
	if !ok || code != 2 {
		t.Fatalf("expected exit code 2, got %d (ok=%v)", code, ok)
	}

	var payload struct {
		Status   string `json:"status"`
		ExitCode int    `json:"exit_code"`
		Data     struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "warning" || payload.ExitCode != 2 || payload.Data.Status != "degraded" {
		t.Fatalf("unexpected degraded envelope: %#v", payload)
	}
}
