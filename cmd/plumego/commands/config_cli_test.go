package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/birdordev/plumego/cmd/plumego/internal/output"
)

func TestCLI_ConfigShowJSONOutput(t *testing.T) {
	tmpDir := t.TempDir()

	stdout, _, err := runCLI(t, []string{"--format", "json", "config", "show"}, tmpDir)
	if err != nil {
		t.Fatalf("config show failed: %v", err)
	}

	var payload struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Config map[string]any    `json:"config"`
			Source map[string]string `json:"source"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse json output: %v\noutput: %s", err, stdout)
	}

	if payload.Status != "success" || payload.Message == "" {
		t.Fatalf("expected success envelope, got: %#v", payload)
	}
	if _, ok := payload.Data.Config["app"]; !ok {
		t.Fatalf("expected app config, got: %#v", payload.Data.Config)
	}
	if _, ok := payload.Data.Config["security"]; !ok {
		t.Fatalf("expected security config, got: %#v", payload.Data.Config)
	}
}

func TestCLI_ConfigShowRedactsSecretsByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("WS_SECRET", "")

	if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte("WS_SECRET=super-secret-value\n"), 0644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	stdout, _, err := runCLI(t, []string{"--format", "json", "config", "show", "--resolve"}, tmpDir)
	if err != nil {
		t.Fatalf("config show failed: %v", err)
	}
	if strings.Contains(stdout, "super-secret-value") {
		t.Fatalf("config show leaked secret: %s", stdout)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Config struct {
				Security map[string]any `json:"security"`
			} `json:"config"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "success" {
		t.Fatalf("expected success envelope, got %#v", payload)
	}
	if payload.Data.Config.Security["ws_secret"] != "***REDACTED***" {
		t.Fatalf("expected redacted ws_secret, got %#v", payload.Data.Config.Security["ws_secret"])
	}
}

func TestCLI_ConfigShowSecretsRequiresExplicitFlag(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("WS_SECRET", "")

	if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte("WS_SECRET=super-secret-value\n"), 0644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	stdout, _, err := runCLI(t, []string{"--format", "json", "config", "show", "--resolve", "--show-secrets"}, tmpDir)
	if err != nil {
		t.Fatalf("config show --show-secrets failed: %v", err)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Config struct {
				Security map[string]any `json:"security"`
			} `json:"config"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "success" {
		t.Fatalf("expected success envelope, got %#v", payload)
	}
	if payload.Data.Config.Security["ws_secret"] != "super-secret-value" {
		t.Fatalf("expected raw ws_secret, got %#v", payload.Data.Config.Security["ws_secret"])
	}
}

func TestCLI_ConfigEnvUsesSuccessEnvelope(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte("APP_ADDR=:8081\n"), 0644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	stdout, _, err := runCLI(t, []string{"--format", "json", "config", "env"}, tmpDir)
	if err != nil {
		t.Fatalf("config env failed: %v", err)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			File map[string]string `json:"file"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "success" || payload.Data.File["APP_ADDR"] != ":8081" {
		t.Fatalf("unexpected config env payload: %#v", payload)
	}
}

func TestCLI_ConfigEnvSupportsDirFlag(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte("APP_ADDR=:9090\n"), 0644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	stdout, _, err := runCLI(t, []string{"--format", "json", "config", "env", "--dir", tmpDir}, "")
	if err != nil {
		t.Fatalf("config env --dir failed: %v", err)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			File map[string]string `json:"file"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "success" || payload.Data.File["APP_ADDR"] != ":9090" {
		t.Fatalf("unexpected config env payload: %#v", payload)
	}
}

func TestCLI_ConfigRejectsUnexpectedArguments(t *testing.T) {
	stdout, _, err := runCLI(t, []string{"--format", "json", "config", "env", "extra"}, t.TempDir())
	if err == nil {
		t.Fatalf("expected unexpected argument error")
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" || !strings.Contains(payload.Message, "unexpected arguments") {
		t.Fatalf("unexpected config error payload: %#v", payload)
	}
}

func TestCLI_ConfigEnvReportsInvalidEnvFile(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte("INVALID\n"), 0644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	stdout, _, err := runCLI(t, []string{"--format", "json", "config", "env"}, tmpDir)
	if err == nil {
		t.Fatalf("expected config env to fail for invalid .env")
	}

	var payload cliJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" || !strings.Contains(payload.Message, "invalid env line") {
		t.Fatalf("unexpected config env payload: %#v", payload)
	}
}

func TestCLI_ConfigValidateExitCode(t *testing.T) {
	tmpDir := t.TempDir()

	stdout, _, err := runCLI(t, []string{"--format", "json", "config", "validate"}, tmpDir)
	if err == nil {
		t.Fatalf("expected validation error, got nil")
	}

	code, ok := output.ExitCode(err)
	if !ok || code != 1 {
		t.Fatalf("expected exit code 1, got %v (ok=%v)", code, ok)
	}

	var payload struct {
		Status   string `json:"status"`
		ExitCode int    `json:"exit_code"`
		Data     struct {
			Valid  bool `json:"valid"`
			Errors []struct {
				Field string `json:"field"`
			} `json:"errors"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("failed to parse json output: %v\noutput: %s", err, stdout)
	}
	if payload.Status != "error" || payload.ExitCode != 1 {
		t.Fatalf("unexpected validation envelope: %#v", payload)
	}
	if payload.Data.Valid {
		t.Fatalf("expected valid=false, got true")
	}
	if len(payload.Data.Errors) == 0 {
		t.Fatalf("expected at least one error, got none")
	}
}
