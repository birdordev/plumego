"""Exercise Makefile commands in a disposable checkout with stubbed external tools."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class WorkflowTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="plumego-make-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        for name in ("Makefile", "internal/tools/task-workflow", ".githooks/pre-push"):
            destination = self.root / name
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy(ROOT / name, destination)
        shutil.copytree(ROOT / "tasks/milestones", self.root / "tasks/milestones",
                        ignore=shutil.ignore_patterns("active", "done"))
        shutil.copytree(ROOT / "tasks/cards", self.root / "tasks/cards",
                        ignore=shutil.ignore_patterns("active", "done", "blocked"))
        for name in ("tasks/milestones/active", "tasks/milestones/done", "tasks/cards/active",
                     "specs/change-recipes", "cmd/plumego", "website", "bin"):
            (self.root / name).mkdir(parents=True, exist_ok=True)
        self.env = dict(os.environ, PATH=f"{self.root}/bin:{os.environ['PATH']}",
                        CALLS=str(self.root / "calls"))
        self.stub("internal/tools/check-spec", 'test -f "$1" || exit 1\nprintf "%s\\n" "$1" >> "$CALLS"')
        self.stub("bin/codex", 'printf "codex\\n" >> "$CALLS"')
        self.stub("bin/go", 'printf "%s\\n" "$*" >> "$CALLS"\ncase "$*" in *"$FAIL_PATTERN"*) test -z "${FAIL_PATTERN:-}" || exit 17 ;; esac')
        self.env["FAIL_PATTERN"] = ""
        (self.root / "internal/tools/task-workflow-test.py").write_text("# Isolated gate orchestration stub.\n")

    def stub(self, name, body):
        path = self.root / name
        path.write_text("#!/bin/sh\nset -e\n" + body + "\n")
        path.chmod(0o755)

    def make(self, *args, succeeds=True):
        result = subprocess.run(["make", "--no-print-directory", *args], cwd=self.root,
                                env=self.env, text=True, capture_output=True)
        self.assertEqual(result.returncode == 0, succeeds, result.stdout + result.stderr)
        return result.stdout + result.stderr

    def calls(self):
        return (self.root / "calls").read_text().splitlines()

    def test_help_and_phony(self):
        output = self.make()
        for target in ("lint", "new-plan", "website-sync", "gates"):
            self.assertIn(target, output)
        (self.root / "lint").touch()
        self.assertIn("golangci-lint run", self.make("-n", "lint"))

    def test_artifacts_and_resolution(self):
        title = r"Storage & IO / retry \ test"
        self.make("new-milestone", "N=901", f"TITLE={title}")
        folder = next((self.root / "tasks/milestones/active").iterdir())
        spec = folder / "M-901.md"
        self.assertIn(title, spec.read_text())
        self.make("new-plan", f"M=active/{folder.name}")
        self.make("new-verify", "M=M-901")
        for kind in ("plan", "verify"):
            content = (folder / f"{kind}-M-901.md").read_text()
            self.assertIn(title, content)
            self.assertNotIn("M-XXX", content)
        self.make("new-plan", "M=M-901", succeeds=False)
        for argument in (f"active/{folder.name}", "M-901", f"active/{folder.name}/M-901.md"):
            self.make("check-spec", f"M={argument}")
        self.make("check-plan", "M=M-901")
        self.make("check-verify", "M=M-901")
        self.make("milestone", "M=M-901")
        self.assertEqual(self.calls()[-1], "codex")
        shutil.move(folder, self.root / "tasks/milestones/done" / folder.name)
        self.make("check-spec", "M=M-901")
        self.make("milestone-status", "M=M-901")

    def test_card_extension_and_invalid_recipe(self):
        self.make("new-card", "ID=9001", "SLUG=sample", "M=M-901", "R=missing", succeeds=False)
        self.assertFalse((self.root / "tasks/cards/active/9001-sample.md").exists())
        (self.root / "specs/change-recipes/fix-bug.yaml").touch()
        self.make("new-card", "ID=9001", "SLUG=sample", "M=M-901", "R=fix-bug")
        card = self.root / "tasks/cards/active/9001-sample.md"
        card.write_text(card.read_text().replace("Primary Module:", "Primary Module: core"))
        for argument in ("active/9001-sample", "active/9001-sample.md"):
            self.make("check-card", f"C={argument}")
        self.make("run-card", "C=active/9001-sample.md")
        self.assertIn("agents bundle --task fix-bug --module core", self.calls()[-2])
        self.assertEqual(self.calls()[-1], "codex")

    def test_missing_inputs_and_failed_validation(self):
        for target in ("check-spec", "check-card", "new-card", "new-milestone", "bundle"):
            self.make(target, succeeds=False)
        self.make("check-plan", "M=invalid", succeeds=False)
        self.make("run-card", "C=active/missing", succeeds=False)
        self.assertFalse((self.root / "calls").exists())

    def test_milestone_validation_uses_the_shared_gate(self):
        shutil.copy(ROOT / "internal/tools/check-spec", self.root / "internal/tools/check-spec")
        self.make("new-milestone", "N=901", "TITLE=Validation")
        folder = next((self.root / "tasks/milestones/active").iterdir())
        self.make("new-verify", "M=M-901")
        verify = folder / "verify-M-901.md"
        self.make("check-spec", "M=M-901")
        self.make("check-verify", "M=M-901")
        verify.write_text(verify.read_text().replace("make gates", "go test -race ./..."))
        self.make("check-verify", "M=M-901", succeeds=False)
        shutil.move(folder, self.root / "tasks/milestones/done" / folder.name)
        self.make("check-verify", "M=M-901")

    def test_hook_covers_all_branch_names_and_propagates_failure(self):
        self.stub("bin/git", 'case "$1" in rev-parse) pwd ;; symbolic-ref) echo "$TEST_BRANCH" ;; esac')
        self.stub("bin/make", 'printf "%s\n" "$*" >> "$CALLS"; exit "${MAKE_STATUS:-0}"')
        for branch in ("main", "feat/test", "codex/test", "release/v2", "milestone/M-901"):
            self.env["TEST_BRANCH"] = branch
            result = subprocess.run(["bash", ".githooks/pre-push"], cwd=self.root, env=self.env)
            self.assertEqual(result.returncode, 0)
            self.assertEqual(self.calls()[-1], "pre-push-check")
        self.env["MAKE_STATUS"] = "23"
        result = subprocess.run(["bash", ".githooks/pre-push"], cwd=self.root, env=self.env)
        self.assertEqual(result.returncode, 23)

    def test_api_check_uses_one_snapshot_tool_invocation(self):
        self.make("api-check")
        self.assertEqual(self.calls(), ["run ./internal/checks/extension-api-snapshot -check-stable"])

    def test_gate_failure_stops_even_with_parallel_make(self):
        self.env["FAIL_PATTERN"] = "cross-extension-deps"
        self.make("-j4", "gates", succeeds=False)
        self.assertEqual(self.calls(), ["run ./internal/checks/dependency-rules",
                                       "run ./internal/checks/cross-extension-deps"])

    def test_go_and_website_gate_compositions_are_separate(self):
        root_gates = self.make("-n", "go-gates")
        full_gates = self.make("-n", "gates")
        self.assertNotIn("pnpm", full_gates)
        self.assertNotIn("cd cmd/plumego && go vet", root_gates)
        self.assertIn("cd cmd/plumego && go vet", full_gates)
        self.assertEqual(root_gates.count("go test -race -timeout 60s ./...\n"), 1)
        self.assertEqual(full_gates.count("go test -race -timeout 60s ./...\n"), 2)
        for gates in (root_gates, full_gates):
            self.assertNotIn("go test -timeout 20s ./...\n", gates)
        self.stub("bin/git", "exit 0")
        self.stub("bin/pnpm", 'printf "pnpm %s\\n" "$*" >> "$CALLS"')
        self.make("website-gates")
        self.assertEqual(self.calls(), [
            "pnpm translations:check", "pnpm sync", "pnpm check:site", "pnpm build:site",
        ])

    def test_reference_targets_run_in_each_module(self):
        for module in ("reference/first", "reference/second"):
            directory = self.root / module
            directory.mkdir(parents=True)
            (directory / "go.mod").touch()
        self.make("reference-vet", "reference-test")
        self.assertEqual(self.calls(), [
            "-C reference/first vet ./...",
            "-C reference/second vet ./...",
            "-C reference/first test ./...",
            "-C reference/second test ./...",
        ])

    def test_coverage_exports_only_a_passing_profile(self):
        self.stub("bin/go", r'''case "$1" in
  test)
    for arg in "$@"; do
      case "$arg" in -coverprofile=*) printf 'mode: set\n' > "${arg#-coverprofile=}" ;; esac
    done ;;
  tool) printf 'total: (statements) %s%%\n' "$COVERAGE_PERCENT" ;;
esac''')
        destination = self.root / "measured.cover"
        self.env["COVERAGE_PERCENT"] = "89.9"
        self.make("stable-coverage", f"COVERAGE_FILE={destination}", succeeds=False)
        self.assertFalse(destination.exists())
        self.env["COVERAGE_PERCENT"] = "90.0"
        self.make("stable-coverage", f"COVERAGE_FILE={destination}")
        self.assertEqual(destination.read_text(), "mode: set\n")

    def test_generation_failure_is_not_reported_as_success(self):
        self.stub("bin/git", "exit 0")
        self.stub("bin/pnpm", "exit 19")
        self.make("website-check-generated", succeeds=False)

    def test_website_sync_does_not_stage(self):
        self.stub("bin/pnpm", 'printf "pnpm %s\\n" "$*" >> "$CALLS"')
        self.stub("bin/git", 'echo "unexpected git operation" >&2; exit 1')
        self.make("website-sync")
        self.assertEqual(self.calls(), ["pnpm sync"])


if __name__ == "__main__":
    unittest.main()
