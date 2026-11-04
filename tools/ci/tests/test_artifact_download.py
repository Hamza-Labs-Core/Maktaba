"""Every artifact download in .github/workflows retries, and says so when it never arrives.

A runner that cannot reach the artifacts API once (`Failed to ListArtifacts: ... ETIMEDOUT`)
must not fail a job. Each `actions/download-artifact` step is a chain: a first attempt with an
`id` and `continue-on-error`, a wait, a second attempt run only when the first failed, a longer
wait, a third attempt run only when the second failed, then a check that fails the job with an
`::error::` naming the artifact when the third failed too (a `::warning::` where the original
download was optional). Only downloads retry: no test step and no job does.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[3]
WORKFLOWS = sorted((ROOT / ".github/workflows").glob("*.yml"))
DOWNLOAD = "actions/download-artifact@"


def _jobs() -> list[tuple[str, str, list[dict[str, Any]]]]:
    found = []
    for path in WORKFLOWS:
        doc = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
        for name, job in (doc.get("jobs") or {}).items():
            found.append((path.name, name, job.get("steps") or []))
    return found


def _failed(step_id: str) -> str:
    return f"steps.{step_id}.outcome == 'failure'"


def _chains() -> list[tuple[str, list[dict[str, Any]]]]:
    """Each download chain: where it is, and its six steps from the first attempt on."""
    chains = []
    for workflow, job, steps in _jobs():
        for i, step in enumerate(steps):
            if not str(step.get("uses", "")).startswith(DOWNLOAD):
                continue
            if "outcome == 'failure'" in str(step.get("if", "")):
                continue  # a retry: checked with its chain
            chains.append((f"{workflow} {job} step {i}", steps[i : i + 6]))
    return chains


def test_there_are_downloads() -> None:
    assert len(_chains()) >= 20


def test_every_download_retries_twice_with_a_backoff() -> None:
    for where, chain in _chains():
        first, wait1, second, wait2, third, check = chain + [{}] * (6 - len(chain))
        sid = first.get("id")
        assert sid, f"{where}: the first download attempt has no id"
        assert first.get("continue-on-error") is True, f"{where}: the first attempt must not fail the job"
        assert wait1.get("if") == _failed(sid) and "sleep" in wait1.get("run", ""), f"{where}: no wait after attempt 1"
        assert str(second.get("uses", "")).startswith(DOWNLOAD), f"{where}: no second attempt"
        assert second.get("if") == _failed(sid) and second.get("continue-on-error") is True, where
        assert second.get("with") == first.get("with"), f"{where}: attempt 2 downloads something else"
        assert wait2.get("if") == _failed(second["id"]) and "sleep" in wait2.get("run", ""), f"{where}: no wait after attempt 2"
        assert str(third.get("uses", "")).startswith(DOWNLOAD), f"{where}: no third attempt"
        assert third.get("if") == _failed(second["id"]) and third.get("continue-on-error") is True, where
        assert third.get("with") == first.get("with"), f"{where}: attempt 3 downloads something else"
        assert check.get("if") == _failed(third["id"]), f"{where}: no check after the last attempt"


def test_a_download_that_never_arrives_names_the_artifact() -> None:
    for where, chain in _chains():
        first, check = chain[0], chain[5]
        artifact = first["with"].get("name") or first["with"].get("pattern")
        assert check.get("env", {}).get("ARTIFACT") == artifact, f"{where}: the check does not name {artifact}"
        run = check.get("run", "")
        if "::warning::" in run:
            # optional before the retry too: the job went on without it, and still does
            assert "exit 1" not in run, where
        else:
            assert "::error::" in run and "$ARTIFACT" in run and "exit 1" in run, f"{where}: the check must fail loudly"


def test_only_downloads_retry() -> None:
    for workflow, job, steps in _jobs():
        downloads = {s["id"] for s in steps if str(s.get("uses", "")).startswith(DOWNLOAD) and "id" in s}
        for step in steps:
            if not any(step.get("if") == _failed(d) for d in downloads):
                continue
            uses, run = str(step.get("uses", "")), str(step.get("run", ""))
            retry = uses.startswith(DOWNLOAD)
            wait = run.strip().startswith("sleep ")
            check = "never downloaded" in run
            assert retry or wait or check, f"{workflow} {job}: {step.get('name')} runs again on a failure"


def test_the_player_ui_shard_says_the_app_never_downloaded() -> None:
    doc = yaml.safe_load((ROOT / ".github/workflows/_apple-apps.yml").read_text(encoding="utf-8"))
    for job in doc["jobs"].values():
        steps = job.get("steps") or []
        ids = {s.get("id") for s in steps}
        if "artifact-player-ui-build-check" not in ids:
            continue
        (log,) = [s for s in steps if s.get("name", "").startswith("Read and check the app's log")]
        assert log["env"]["APP_DOWNLOAD"] == "${{ steps.artifact-player-ui-build-check.outcome }}"
        assert "never downloaded" in log["run"] and "$APP_ARTIFACT" in log["run"]
        return
    raise AssertionError("the Player UI shard's download has no check step")
