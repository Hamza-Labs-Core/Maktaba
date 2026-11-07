"""tools/ci/integration-go.sh runs each module with -json (issue #855): its log reads as
go test's without it (each package's line, a failed test's output, a build error), then the
module's ten slowest tests, and the tier fails when a module fails."""

from __future__ import annotations

import os
import shutil
import subprocess
from pathlib import Path

import pytest

SCRIPT = Path(__file__).resolve().parents[1] / "integration-go.sh"

pytestmark = pytest.mark.skipif(
    shutil.which("go") is None or shutil.which("jq") is None, reason="needs go and jq"
)


def module(root: Path, files: dict[str, str]) -> Path:
    root.mkdir()
    (root / "go.mod").write_text("module fake\n\ngo 1.21\n")
    for name, body in files.items():
        p = root / name
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(body)
    return root


def run(*mods: Path) -> subprocess.CompletedProcess[str]:
    env = {
        **os.environ,
        "GOTOOLCHAIN": "local",
        "GOFLAGS": "",
        "MAKTABA_INTEGRATION_JOBS": "2",
    }
    return subprocess.run(
        ["bash", str(SCRIPT), *map(str, mods)],
        capture_output=True,
        text=True,
        env=env,
        check=False,
    )


def test_a_failing_module_shows_its_failure_and_fails_the_tier(tmp_path: Path) -> None:
    bad = module(
        tmp_path / "bad",
        {
            "a/a_test.go": 'package a\nimport "testing"\nfunc TestOK(t *testing.T) {}\n'
            'func TestBad(t *testing.T) { t.Log("why it failed"); t.Fatal("boom") }\n',
            "b/b_test.go": 'package b\nimport "testing"\nfunc TestB(t *testing.T) { undefined() }\n',
        },
    )
    good = module(
        tmp_path / "good",
        {
            "c/c_test.go": 'package c\nimport ("testing"; "time")\n'
            "func TestSlow(t *testing.T) { time.Sleep(300 * time.Millisecond) }\n"
            "func TestFast(t *testing.T) {}\n"
        },
    )
    got = run(bad, good)
    out = got.stdout
    assert got.returncode == 1, out
    # the build error and the failed test's own lines, as go test prints them
    assert "undefined: undefined" in out
    assert "FAIL\tfake/b [build failed]" in out
    assert "why it failed" in out and "--- FAIL: TestBad" in out
    assert f"FAIL: go test in {bad} (exit 1)" in out
    # a passing test's output is not printed, nor the -json stream's bare PASS lines
    assert "=== RUN   TestOK" not in out and "=== RUN   TestSlow" not in out
    assert "\nPASS\n" not in out
    # the passing module: its package's line, then its slowest tests, slowest first
    good_part = out[out.index(f"({good},") :]
    assert "ok  \tfake/c\t" in good_part
    slowest = good_part[good_part.index("--> the ten slowest tests") :].splitlines()[
        1:3
    ]
    assert slowest[0].endswith("\tfake/c\tTestSlow"), slowest
    assert slowest[1].endswith("\tfake/c\tTestFast"), slowest
    assert f"FAIL: go test in {good}" not in out


def test_passing_modules_pass_the_tier(tmp_path: Path) -> None:
    good = module(
        tmp_path / "good",
        {"c/c_test.go": 'package c\nimport "testing"\nfunc TestOK(t *testing.T) {}\n'},
    )
    got = run(good)
    assert got.returncode == 0, got.stdout
    assert "ok  \tfake/c\t" in got.stdout
