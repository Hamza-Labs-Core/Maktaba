"""maktaba.tv's download page follows the release (Epic 60 story 60.11, T07): release.yml and
desktop-release.yml each end with a `website` job that runs tools/release/website-dispatch.sh
after their publish step, which starts website-deploy.yml on main for the tag once it is GitHub's
latest release. The job can never hold back or fail a release: nothing needs it, it continues on
error, and it may only dispatch workflows. The script is run here against a fake `gh`.

Run: uv run --no-project --with pyyaml==6.0.3 --with pytest==8.4.2 pytest tools/ci/tests
"""

from __future__ import annotations

import os
import subprocess
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = ROOT / "tools/release/website-dispatch.sh"
PUBLISHERS = {
    ".github/workflows/release.yml": "sign-and-release",
    ".github/workflows/desktop-release.yml": "publish",
}


def jobs(path: str) -> dict:
    return yaml.safe_load((ROOT / path).read_text())["jobs"]


def needs_of(job: dict) -> list[str]:
    n = job.get("needs", [])
    return [n] if isinstance(n, str) else list(n)


@pytest.mark.parametrize(("path", "publisher"), PUBLISHERS.items())
def test_the_website_job_follows_the_publish_step(path: str, publisher: str) -> None:
    job = jobs(path)["website"]
    assert needs_of(job) == [publisher]
    cond = job["if"]
    assert "!cancelled()" in cond and f"needs.{publisher}.result" in cond
    run = [s for s in job["steps"] if "run" in s]
    assert len(run) == 1
    assert run[0]["run"] == 'tools/release/website-dispatch.sh "${TAG}"'
    assert run[0]["env"] == {
        "GH_TOKEN": "${{ github.token }}",
        "TAG": "${{ github.ref_name }}",
    }


@pytest.mark.parametrize("path", PUBLISHERS)
def test_the_website_job_blocks_nothing(path: str) -> None:
    all_jobs = jobs(path)
    for name, job in all_jobs.items():
        assert "website" not in needs_of(job), f"{name} waits on the website job"
    job = all_jobs["website"]
    assert job["continue-on-error"] is True
    assert job["timeout-minutes"] <= 10
    # it may dispatch a workflow and read the checkout: nothing else
    assert job["permissions"] == {"contents": "read", "actions": "write"}


def test_the_deploy_takes_the_tag() -> None:
    deploy = yaml.safe_load((ROOT / ".github/workflows/website-deploy.yml").read_text())
    inputs = deploy[True]["workflow_dispatch"]["inputs"]
    assert inputs["release"]["type"] == "string" and inputs["release"]["default"] == ""
    assert deploy["jobs"]["build"]["with"]["release"] == "${{ inputs.release || '' }}"
    site = yaml.safe_load((ROOT / ".github/workflows/_website.yml").read_text())
    assert "release" in site[True]["workflow_call"]["inputs"]
    build = next(
        s for s in site["jobs"]["website"]["steps"] if s.get("name") == "Build"
    )
    assert build["env"] == {
        "GITHUB_TOKEN": "${{ github.token }}",
        "WEBSITE_EXPECT_RELEASE": "${{ inputs.release }}",
    }


def run(
    tmp_path: Path, tag: str, latest: str, fail_api: bool = False
) -> tuple[int, str, list[str]]:
    """The script with a fake gh on PATH: `gh api .../releases/latest` answers `latest`, and every
    `gh workflow run` call is recorded."""
    log = tmp_path / "gh.log"
    fake = tmp_path / "bin"
    fake.mkdir(exist_ok=True)
    (fake / "gh").write_text(
        "#!/usr/bin/env bash\n"
        f'echo "$*" >> "{log}"\n'
        'if [ "$1" = api ]; then\n'
        + ("  exit 1\n" if fail_api else f'  echo "{latest}"\n')
        + "fi\n"
    )
    (fake / "gh").chmod(0o755)
    env = {
        **os.environ,
        "PATH": f"{fake}:{os.environ['PATH']}",
        "GITHUB_REPOSITORY": "Hamza-Labs-Core/Maktaba",
    }
    env.pop("GITHUB_STEP_SUMMARY", None)
    p = subprocess.run(
        ["bash", str(SCRIPT), tag], check=False, env=env, capture_output=True, text=True
    )
    calls = log.read_text().splitlines() if log.exists() else []
    return (
        p.returncode,
        p.stdout + p.stderr,
        [c for c in calls if c.startswith("workflow ")],
    )


def test_T07_the_latest_release_dispatches_the_deploy_with_its_tag(
    tmp_path: Path,
) -> None:
    code, out, calls = run(tmp_path, "v0.3.140", "v0.3.140")
    assert code == 0, out
    assert calls == [
        "workflow run website-deploy.yml --repo Hamza-Labs-Core/Maktaba --ref main -f release=v0.3.140"
    ]


@pytest.mark.parametrize(
    ("tag", "latest"),
    [
        (
            "v0.3.140",
            "v0.3.139",
        ),  # published, but not (yet) the latest: a feed is incomplete
        ("v0.3.139", "v0.3.140"),  # a re-run of a tag a newer release has passed
        ("v0.4.0-rc.1", "v0.3.140"),  # a pre-release
    ],
)
def test_anything_but_the_latest_dispatches_nothing(
    tmp_path: Path, tag: str, latest: str
) -> None:
    code, out, calls = run(tmp_path, tag, latest)
    assert code == 0, out
    assert calls == []


def test_an_unreadable_api_dispatches_nothing_and_says_so(tmp_path: Path) -> None:
    code, out, calls = run(tmp_path, "v0.3.140", "", fail_api=True)
    assert code == 0
    assert calls == []
    assert "is not the latest release (the latest: none)" in out


def test_a_branch_name_is_refused(tmp_path: Path) -> None:
    code, out, calls = run(tmp_path, "main", "v0.3.140")
    assert code == 1 and calls == []
    assert "is not a release tag" in out
