package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestModuleFromFilePath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		// stable roots — single-level
		{"middleware/timeout/timeout.go", "middleware"},
		{"core/app.go", "core"},
		{"router/router.go", "router"},
		// x/* — two-level (existing behaviour)
		{"x/tenant/resolver.go", "x/tenant"},
		{"x/observability/tracer/tracer.go", "x/observability"},
		// reference/* — two-level (new behaviour)
		{"reference/standard-service/internal/app/app_test.go", "reference/standard-service"},
		{"reference/with-ai/main.go", "reference/with-ai"},
		{"examples/standard-api/main.go", "examples/standard-api"},
		// other top-level paths
		{"specs/gate-profiles.yaml", "specs"},
		{"docs/reference/style-guide.md", "docs"},
		{"cmd/plumego/commands/agents_validate.go", "cmd"},
		// empty / root
		{"", ""},
	}
	for _, tt := range tests {
		got := moduleFromFilePath(tt.path)
		if got != tt.want {
			t.Errorf("moduleFromFilePath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestCollectChangedFilesPreservesStagedAndUntrackedPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	write := func(name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("staged.md", "original\n")
	git("add", "--", "staged.md")
	git("-c", "user.name=Gate Test", "-c", "user.email=gate-test@example.com",
		"-c", "commit.gpgsign=false", "-c", "core.hooksPath="+root, "commit", "-qm", "Initial")

	write("staged.md", "staged\n")
	git("add", "--", "staged.md")
	write("staged.md", "original\n")
	write("caf\u00e9 notes.md", "untracked\n")

	files, err := collectChangedFiles(root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"staged.md", "caf\u00e9 notes.md"}; !slices.Equal(files, want) {
		t.Fatalf("changed files = %q, want %q", files, want)
	}
	if _, err := collectChangedFiles(root, "missing-base"); err == nil {
		t.Fatal("invalid base was accepted")
	}
}
