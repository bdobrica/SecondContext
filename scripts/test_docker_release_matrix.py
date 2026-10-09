"""Exercise release selection against real, disposable Git histories."""

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from docker_release_matrix import IMAGES, release_matrix

SCRIPT = Path(__file__).with_name("docker_release_matrix.py").resolve()
ZERO = "0" * 40


class ReleaseMatrixTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="secondcontext-release-")
        self.addCleanup(self.temp.cleanup)
        self.previous_cwd = Path.cwd()
        os.chdir(self.temp.name)
        self.addCleanup(os.chdir, self.previous_cwd)
        self.git("init", "--quiet")
        self.git("config", "user.email", "release-test@example.invalid")
        self.git("config", "user.name", "Release Test")
        self.git("config", "commit.gpgsign", "false")
        self.empty = self.commit()
        self.write_version(0, "0.1.0")
        self.write_version(1, "0.1.0")
        self.baseline = self.commit()

    def git(self, *args):
        return subprocess.check_output(
            ["git", "-c", "core.hooksPath=/dev/null", *args],
            text=True,
            stderr=subprocess.PIPE,
        ).strip()

    def commit(self):
        self.git("add", ".")
        self.git("commit", "--quiet", "--allow-empty", "-m", "Fixture")
        return self.git("rev-parse", "HEAD")

    def write_version(self, index, version):
        path = Path(IMAGES[index][0])
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(
            f"[bumpversion]\ncurrent_version = {version}\ncommit = False\ntag = False\n"
        )

    def selected(self, before=None, after=None):
        return release_matrix(
            before or self.baseline, after or self.git("rev-parse", "HEAD")
        )["include"]

    def test_bootstrap_is_not_a_bump(self):
        self.assertEqual(self.selected(self.empty, self.baseline), [])

    def test_new_branch_has_no_bump_baseline(self):
        self.assertEqual(self.selected(ZERO, self.baseline), [])

    def test_unchanged_versions_and_ordinary_source_edits_skip(self):
        Path("source.go").write_text("package main\n")
        self.commit()
        self.assertEqual(self.selected(), [])

    def test_config_edit_without_version_change_skips(self):
        with Path(IMAGES[0][0]).open("a") as handle:
            handle.write("# A comment does not release an image.\n")
        self.commit()
        self.assertEqual(self.selected(), [])

    def test_gateway_only(self):
        self.write_version(0, "0.1.1")
        self.commit()
        self.assertEqual(
            self.selected(),
            [
                {
                    "image": "secondcontext-gateway",
                    "context": ".",
                    "dockerfile": "Dockerfile",
                    "version": "0.1.1",
                }
            ],
        )

    def test_knowledge_only(self):
        self.write_version(1, "0.2.0")
        self.commit()
        self.assertEqual(
            self.selected(),
            [
                {
                    "image": "secondcontext-knowledge",
                    "context": "services/knowledge-bootstrap",
                    "dockerfile": "services/knowledge-bootstrap/Dockerfile",
                    "version": "0.2.0",
                }
            ],
        )

    def test_both_images_across_multiple_commits(self):
        self.write_version(0, "0.1.1")
        self.commit()
        self.write_version(1, "1.0.0")
        self.commit()
        self.write_version(0, "0.1.2")
        self.commit()
        Path("notes.txt").write_text("Last commit does not contain the bumps.\n")
        self.commit()
        self.assertEqual(
            [row["version"] for row in self.selected()], ["0.1.2", "1.0.0"]
        )

    def test_bump_reverted_before_push_skips(self):
        self.write_version(0, "0.1.1")
        self.commit()
        self.write_version(0, "0.1.0")
        self.commit()
        self.assertEqual(self.selected(), [])

    def test_working_tree_changes_are_not_releases(self):
        self.write_version(0, "9.9.9")
        self.assertEqual(self.selected(), [])

    def test_removed_config_skips(self):
        Path(IMAGES[0][0]).unlink()
        self.commit()
        self.assertEqual(self.selected(), [])

    def test_versions_compare_numerically_and_reject_downgrades(self):
        self.write_version(0, "0.1.9")
        before = self.commit()
        self.write_version(0, "0.1.10")
        after = self.commit()
        self.assertEqual(self.selected(before, after)[0]["version"], "0.1.10")
        with self.assertRaisesRegex(ValueError, "must increase"):
            self.selected(after, before)

    def test_invalid_versions_fail(self):
        for value in (
            "",
            "v0.1.1",
            "01.2.3",
            "0.2",
            "0.1.1-beta",
            "latest",
            "1.2." + "1" * 129,
        ):
            with self.subTest(version=value):
                self.write_version(0, value)
                self.commit()
                with self.assertRaisesRegex(ValueError, "major.minor.patch"):
                    self.selected()

    def test_missing_before_commit_fails_instead_of_releasing_everything(self):
        with self.assertRaises(subprocess.CalledProcessError):
            self.selected("1" * 40, self.baseline)

    def test_cli_github_outputs_and_input_validation(self):
        self.write_version(0, "0.1.1")
        after = self.commit()
        output = Path("github-output.txt")
        result = subprocess.run(
            [sys.executable, str(SCRIPT), "--before", self.baseline, "--after", after],
            env={**os.environ, "GITHUB_OUTPUT": str(output)},
            text=True,
            capture_output=True,
            check=True,
        )
        self.assertEqual(json.loads(result.stdout)["include"][0]["version"], "0.1.1")
        self.assertIn("has_releases=true\n", output.read_text())
        output.unlink()
        subprocess.run(
            [sys.executable, str(SCRIPT), "--before", after, "--after", after],
            env={**os.environ, "GITHUB_OUTPUT": str(output)},
            check=True,
            capture_output=True,
        )
        self.assertIn("has_releases=false\n", output.read_text())
        result = subprocess.run(
            [sys.executable, str(SCRIPT), "--before", "invalid", "--after", after],
            capture_output=True,
            check=False,
        )
        self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
