"""A home can read GitHub's release list (the incident of 2026-11-24: every home's update check had
failed for over a day on a list of releases longer than its cap, and nothing outside a home read
the list the way a home does). github-release-list.yml reads it hourly through the home's own code
(api/cmd/release-list-check) and opens an issue on a failure; release.yml and desktop-release.yml
each end with a `release-list-check` job that runs tools/release/release-list-check.sh once their publish step
has run, and fails the release's workflow when a home could not read the list with it. The
script is run here against a fake `gh` and a fake check.

Run: uv run --no-project --with pyyaml==6.0.3 --with pytest==8.4.2 pytest tools/ci/tests
"""

from __future__ import annotations

import os
import subprocess
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/release/release-list-check.sh"
PUBLISHERS = {
    ".github/workflows/release.yml": "sign-and-release",
    ".github/workflows/desktop-release.yml": "publish",
}


def load(path: str) -> dict:
    return yaml.safe_load((ROOT / path).read_text())


def needs_of(job: dict) -> list[str]:
    n = job.get("needs", [])
    return [n] if isinstance(n, str) else list(n)


@pytest.mark.parametrize(("path", "publisher"), PUBLISHERS.items())
def test_the_list_check_follows_the_publish_step_and_can_fail_the_release(
    path: str, publisher: str
) -> None:
    job = load(path)["jobs"]["release-list-check"]
    assert needs_of(job) == [publisher]
    assert "!cancelled()" in job["if"] and f"needs.{publisher}.result" in job["if"]
    # it fails loudly: never continue-on-error
    assert "continue-on-error" not in job
    assert job["permissions"] == {"contents": "read"}
    run = [s for s in job["steps"] if "run" in s]
    assert len(run) == 1
    assert run[0]["run"] == 'tools/release/release-list-check.sh "${TAG}"'
    assert run[0]["env"]["TAG"] == "${{ github.ref_name }}"


def test_the_hourly_check_reads_as_a_home_and_opens_an_issue() -> None:
    wf = load(".github/workflows/github-release-list.yml")
    assert wf[True]["schedule"][0]["cron"].split()[1:] == ["*", "*", "*", "*"]  # hourly
    job = wf["jobs"]["list"]
    assert job["permissions"] == {"contents": "read", "issues": "write"}
    steps = {s.get("name"): s for s in job["steps"]}
    check = steps["Read the list as a home does"]
    assert "go run ./cmd/release-list-check" in check["run"] and "pipefail" in check["run"]
    issue = steps["Open or update the issue"]
    assert issue["if"] == "failure() && steps.check.outcome == 'failure'"
    assert "gh issue create" in issue["run"] and "gh issue comment" in issue["run"]
    assert "gh issue close" in steps["Close the issue once the list reads again"]["run"]


def run(
    tmp_path: Path, draft: str, results: list[tuple[int, str]]
) -> tuple[int, str, int]:
    """The script with a fake gh (`release view` answers isDraft) and a fake check that answers
    each of results in turn (its exit status and output). Returns the status, the output and how
    many times the check ran."""
    fake = tmp_path / "bin"
    fake.mkdir(exist_ok=True)
    (fake / "gh").write_text(f'#!/usr/bin/env bash\necho "{draft}"\n')
    (fake / "gh").chmod(0o755)
    count = tmp_path / "count"
    count.write_text("0")
    lines = ["#!/usr/bin/env bash", f'n=$(cat "{count}")', f'echo $((n + 1)) > "{count}"']
    for i, (code, out) in enumerate(results):
        lines.append(f'if [ "$n" = {i} ]; then printf "%s\\n" "{out}"; exit {code}; fi')
    lines.append("exit 9")
    check = tmp_path / "check"
    check.write_text("\n".join(lines) + "\n")
    check.chmod(0o755)
    env = {
        **os.environ,
        "PATH": f"{fake}:{os.environ['PATH']}",
        "GITHUB_REPOSITORY": "Hamza-Labs-Core/Maktaba",
        "LIST_CHECK_CMD": str(check),
        "LIST_CHECK_DELAYS": "0 0",
        "GH_RETRY_DELAYS": "0",
    }
    p = subprocess.run(
        ["bash", str(SCRIPT), "v0.3.162"], check=False, env=env, capture_output=True, text=True
    )
    return p.returncode, p.stdout + p.stderr, int(count.read_text())


def test_a_draft_is_checked_by_the_workflow_that_publishes_it(tmp_path: Path) -> None:
    code, out, ran = run(tmp_path, "true", [(1, "never")])
    assert code == 0, out
    assert ran == 0 and "still a draft" in out


def test_a_list_a_home_reads_passes(tmp_path: Path) -> None:
    code, out, ran = run(tmp_path, "false", [(0, "A home reads this list with room to spare.")])
    assert code == 0, out
    assert ran == 1 and "room to spare" in out


def test_a_release_not_listed_yet_is_asked_for_again(tmp_path: Path) -> None:
    code, out, ran = run(
        tmp_path,
        "false",
        [(1, "- v0.3.162 is not in the 30 releases a home reads"), (0, "room to spare")],
    )
    assert code == 0, out
    assert ran == 2


def test_a_list_a_home_cannot_read_fails_the_release_at_once(tmp_path: Path) -> None:
    code, out, ran = run(
        tmp_path,
        "false",
        [(1, "- page 1 of the release list is 12000000 bytes, 72% of the 16777216 a home reads")],
    )
    assert code == 1
    assert ran == 1
    assert "::error title=GitHub release list::v0.3.162 is published, but a home cannot read" in out


def test_a_release_never_listed_fails_after_the_retries(tmp_path: Path) -> None:
    missing = (1, "- v0.3.162 is not in the 30 releases a home reads")
    code, out, ran = run(tmp_path, "false", [missing, missing, missing])
    assert code == 1
    assert ran == 3
