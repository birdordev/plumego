package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidCommunityExtensionPasses(t *testing.T) {
	code, stderr := runCheck(t, validManifest())
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr)
	}
}

func TestMissingNameFieldExitsOne(t *testing.T) {
	manifest := strings.Replace(validManifest(), "name: example\n", "", 1)
	code, stderr := runCheck(t, manifest)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, `missing required field "name"`) {
		t.Fatalf("stderr missing name violation: %s", stderr)
	}
}

func TestInvalidStatusValueExitsOne(t *testing.T) {
	manifest := strings.Replace(validManifest(), "status: experimental", "status: preview", 1)
	code, stderr := runCheck(t, manifest)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, `status "preview" is invalid`) {
		t.Fatalf("stderr missing invalid status violation: %s", stderr)
	}
}

func TestMissingTestCommandsExitsOne(t *testing.T) {
	manifest := strings.Replace(validManifest(), "test_commands:\n  - go test ./...\n", "", 1)
	code, stderr := runCheck(t, manifest)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, `missing required field "test_commands"`) {
		t.Fatalf("stderr missing test_commands violation: %s", stderr)
	}
}

func TestNoInitSideEffectsFalseExitsOne(t *testing.T) {
	manifest := strings.Replace(validManifest(), "no_init_side_effects: true", "no_init_side_effects: false", 1)
	code, stderr := runCheck(t, manifest)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "no_init_side_effects must be true") {
		t.Fatalf("stderr missing no_init_side_effects violation: %s", stderr)
	}
}

func runCheck(t *testing.T, manifest string) (int, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "specs"), 0o755); err != nil {
		t.Fatalf("mkdir specs: %v", err)
	}
	schemaBytes, err := os.ReadFile(filepath.Join("..", "..", "..", "specs", "community-extension.schema.yaml"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, schemaRelPath), schemaBytes, 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	extDir := filepath.Join(root, "extension")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatalf("mkdir extension: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extDir, manifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	var stderr bytes.Buffer
	code := run([]string{extDir}, root, &stderr)
	return code, stderr.String()
}

func validManifest() string {
	return `name: example
module_path: github.com/acme/plumego-example
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
}
