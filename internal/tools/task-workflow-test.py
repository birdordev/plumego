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
        for name in ("Makefile", "internal/tools/task-workflow"):
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

    def test_snapshot_patterns(self):
        self.make("api-check")
        calls = self.calls()
        self.assertEqual(len(calls), 18)
        for index, module in enumerate("core router contract middleware security store health log metrics".split()):
            pattern = f"./{module}" + ("" if module in ("core", "router") else "/...")
            self.assertIn(f"-module {pattern} -out", calls[index * 2])
            self.assertIn(f"snapshots/{module}-head.snapshot", calls[index * 2 + 1])

    def test_gate_failure_stops_even_with_parallel_make(self):
        self.env["FAIL_PATTERN"] = "cross-extension-deps"
        self.make("-j4", "gates", succeeds=False)
        self.assertEqual(self.calls(), ["run ./internal/checks/dependency-rules",
                                       "run ./internal/checks/cross-extension-deps"])

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
