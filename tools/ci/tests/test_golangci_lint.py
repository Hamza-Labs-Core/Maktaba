"""tools/ci/golangci-lint.sh: the lint jobs' golangci-lint, pinned and fetched by fetch-pinned.sh.

golangci-lint-action's own download (github.com's release URL, no retry) failed main's lint
with a 504 on 2026-11-08. Every use of the action runs the binary golangci-lint.sh put on
the PATH (`install-mode: none`), after the step that runs it, and pins no version of its own.
"""

from __future__ import annotations

import os
import re
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/ci/golangci-lint.sh"
WORKFLOWS = sorted((ROOT / ".github/workflows").glob("*.yml"))
USES = "uses: golangci/golangci-lint-action@"


def _steps_using_the_action(text: str) -> list[str]:
    """Each step that uses golangci-lint-action, from its `- name:` or `- uses:` line to the next step."""
    steps = re.split(r"\n(?=\s*- (?:name|uses|id):)", text)
    return [s for s in steps if USES in s]


def test_the_key_names_the_pinned_release() -> None:
    env = {**os.environ, "RUNNER_OS": "Linux"}
    r = subprocess.run(["bash", str(SCRIPT), "--key"], capture_output=True, text=True, env=env, timeout=30)
    assert r.returncode == 0, r.stderr
    version = re.search(r"^VERSION=(\S+)$", SCRIPT.read_text(encoding="utf-8"), re.M)
    sha = re.search(r"^SHA256=([0-9a-f]{64})$", SCRIPT.read_text(encoding="utf-8"), re.M)
    assert version and sha
    assert r.stdout.strip() == f"pinned-linux-golangci-lint-{version[1]}-linux-amd64.tar.gz-{sha[1][:16]}"


def test_a_wrong_usage_fails() -> None:
    r = subprocess.run(["bash", str(SCRIPT)], capture_output=True, text=True, timeout=30)
    assert r.returncode == 2
    assert "usage" in r.stderr


@pytest.mark.parametrize("workflow", [w for w in WORKFLOWS if USES in w.read_text(encoding="utf-8")], ids=lambda p: p.name)
def test_every_use_runs_the_pinned_binary(workflow: Path) -> None:
    text = workflow.read_text(encoding="utf-8")
    install = text.find("run: tools/ci/golangci-lint.sh build/ci-pinned/golangci-lint")
    assert install != -1, f"{workflow.name} uses golangci-lint-action without tools/ci/golangci-lint.sh"
    assert install < text.find(USES), f"{workflow.name} runs the action before golangci-lint.sh"
    steps = _steps_using_the_action(text)
    assert steps
    for step in steps:
        assert "install-mode: none" in step, f"{workflow.name}: a golangci-lint-action step downloads its own binary"
        assert not re.search(r"^\s*version:", step, re.M), f"{workflow.name}: a golangci-lint-action step pins a version"


def test_some_workflow_uses_the_action() -> None:
    # the positive control for the test above: it found the lint jobs
    names = {w.name for w in WORKFLOWS if USES in w.read_text(encoding="utf-8")}
    assert {"_lint.yml", "relay.yml"} <= names
