"""The cloud's integration tests start from the last release's migrations too (a new database hides what
a late lower slot breaks): each workflow that runs them fetches those first."""

from __future__ import annotations

import re
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


def test_the_newest_release_tag_is_taken(tmp_path: Path) -> None:
    def git(*args: str) -> None:
        subprocess.run(["git", "-C", str(tmp_path / "up"), *args], check=True, capture_output=True)

    (tmp_path / "up/cloud/migrations").mkdir(parents=True)
    git("init", "-q")
    for tag in ("v0.9.2", "v0.10.0", "v0.10.0-rc1", "vnext"):
        (tmp_path / "up/cloud/migrations/00000001_a.sql").write_text(tag)
        git("add", "-A")
        git("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", tag)
        git("tag", tag)
    subprocess.run(["git", "clone", "-q", "--no-tags", "--depth", "1", f"file://{tmp_path}/up", str(tmp_path / "down")], check=True, capture_output=True)
    env = tmp_path / "env"
    p = subprocess.run(
        ["bash", str(ROOT / "tools/ci/last_release_migrations.sh"), str(tmp_path / "out")],
        cwd=tmp_path / "down", env={"PATH": "/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin", "GITHUB_ENV": str(env), "HOME": str(tmp_path)},
        capture_output=True, text=True, check=False,
    )
    assert p.returncode == 0 and "v0.10.0: 1" in p.stdout, p.stderr
    assert (tmp_path / "out/cloud/migrations/00000001_a.sql").read_text() == "v0.10.0"
    assert re.fullmatch(rf"MAKTABA_LAST_RELEASE_MIGRATIONS={re.escape(str(tmp_path))}/out/cloud/migrations\n", env.read_text())
