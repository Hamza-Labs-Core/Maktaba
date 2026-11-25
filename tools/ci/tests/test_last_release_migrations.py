"""The cloud's integration tests start from the last release's migrations too (a new database hides what
a late lower slot breaks): each workflow that runs them fetches those first."""

from __future__ import annotations

import subprocess
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
STEP = 'tools/ci/last_release_migrations.sh "$RUNNER_TEMP/last-release"'


@pytest.mark.parametrize("workflow", ["_integration.yml", "relay.yml"])
def test_the_step_comes_before_the_clouds_integration_tests(workflow: str) -> None:
    seen = 0
    for job in yaml.safe_load((ROOT / ".github/workflows" / workflow).read_text())["jobs"].values():
        steps = job.get("steps") or []
        tests = [
            i
            for i, s in enumerate(steps)
            if s.get("working-directory") == "cloud" and "-tags=integration" in str(s.get("run", ""))
        ]
        if tests:
            seen += 1
            fetch = [i for i, s in enumerate(steps) if s.get("run") == STEP]
            assert fetch and fetch[0] < tests[0], job.get("name")
            assert steps[fetch[0]].get("if") == steps[tests[0]].get("if")
    assert seen == 1


def repository(tmp: Path) -> Path:
    """A clone, with its whole history, of a repository with a few release tags."""

    def git(*args: str) -> None:
        subprocess.run(["git", "-C", str(tmp / "up"), *args], check=True, capture_output=True)

    (tmp / "up/cloud/migrations").mkdir(parents=True)
    git("init", "-q")
    for tag in ("v0.9.2", "v0.10.0", "v0.10.0-rc1", "vnext"):
        (tmp / "up/cloud/migrations/00000001_a.sql").write_text(tag)
        git("add", "-A")
        git("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", tag)
        git("tag", tag)
    subprocess.run(["git", "clone", "-q", "--no-tags", f"file://{tmp}/up", str(tmp / "down")], check=True, capture_output=True)
    return tmp / "down"


def run(tmp: Path, clone: Path, **env: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["bash", str(ROOT / "tools/ci/last_release_migrations.sh"), str(tmp / "out")],
        cwd=clone,
        env={"PATH": "/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin", "GITHUB_ENV": str(tmp / "env"), "HOME": str(tmp), **env},
        capture_output=True,
        text=True,
        check=False,
    )


@pytest.mark.parametrize(
    ("ref", "want"),
    [
        ({}, "v0.10.0"),
        ({"GITHUB_REF_TYPE": "branch", "GITHUB_REF_NAME": "v0.10.0"}, "v0.10.0"),
        # a tag's own run starts from the release before it
        ({"GITHUB_REF_TYPE": "tag", "GITHUB_REF_NAME": "v0.10.0"}, "v0.9.2"),
    ],
)
def test_the_newest_release_is_taken_and_the_clone_left_whole(tmp_path: Path, ref: dict[str, str], want: str) -> None:
    clone = repository(tmp_path)
    p = run(tmp_path, clone, GITHUB_ACTIONS="true", **ref)
    assert p.returncode == 0 and f"{want}: 1" in p.stdout, p.stderr
    assert (tmp_path / "out/cloud/migrations/00000001_a.sql").read_text() == want
    assert (tmp_path / "env").read_text() == f"MAKTABA_LAST_RELEASE_MIGRATIONS={tmp_path}/out/cloud/migrations\n"
    assert not (clone / ".git/shallow").exists(), "the clone was made shallow"


def test_it_is_refused_outside_a_runner(tmp_path: Path) -> None:
    clone = repository(tmp_path)
    p = run(tmp_path, clone)
    assert p.returncode != 0 and "on a CI runner only" in p.stderr
    assert not (tmp_path / "out").exists() and not (clone / ".git/shallow").exists()
