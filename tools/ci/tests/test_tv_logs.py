"""Story 58.16: the TV jobs' shared pieces.

T02: every TV job uploads its app logs (30 days, linked from the job summary) through
.github/actions/tv-logs, whether it passed or failed. T01: the TV areas' path groups in
.github/ci-paths.yml, and a change in a TV area with no job yet runs no other TV job."""

from __future__ import annotations

import sys
from pathlib import Path
from typing import Any

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools" / "ci"))

import ci_paths  # noqa: E402

ACTION = ROOT / ".github" / "actions" / "tv-logs" / "action.yml"
WORKFLOWS = ROOT / ".github" / "workflows"
USES = "./.github/actions/tv-logs"

# The TV jobs' reusable workflows (story 58.16's table): the ones that exist are checked.
TV_WORKFLOWS = (
    "_android-tv.yml",
    "_tv-web.yml",
    "_tv-web-emulators.yml",
    "_cast-receiver.yml",
    "_roku.yml",
    "_vega.yml",
)

# ci.yml's jobs that build a TV app
TV_JOBS = {"android-tv", "apple-apps", "tv-web", "tv-web-emulators", "cast-receiver", "roku", "vega"}

# each TV area's group, and a file in it
GROUPS = {
    "_tv_web": ["apps/tv/web/launcher/src/main.ts", "web/src/bigscreen/focus.ts", "web/vite.config.ts"],
    "_cast_receiver": ["web/packages/castreceiver/src/receiver.ts"],
    "_roku": ["apps/tv/roku/source/main.bs", "apps/tv/roku/components/theme.brs"],
    "_vega": ["apps/tv/vega/src/App.tsx"],
}


def _load(path: Path) -> dict[str, Any]:
    data = yaml.safe_load(path.read_text(encoding="utf-8"))
    assert isinstance(data, dict)
    return data


def test_the_action_uploads_for_30_days_and_links_the_summary() -> None:
    action = _load(ACTION)
    steps = action["runs"]["steps"]
    upload = next(s for s in steps if str(s.get("uses", "")).startswith("actions/upload-artifact@"))
    assert upload["with"]["retention-days"] == 30
    assert upload["with"]["name"] == "${{ inputs.name }}"
    assert upload["with"]["path"] == "${{ inputs.path }}"
    summary = next(s for s in steps if "GITHUB_STEP_SUMMARY" in str(s.get("run", "")))
    assert summary["env"]["URL"] == f"${{{{ steps.{upload['id']}.outputs.artifact-url }}}}"
    # the inputs reach the shell through env, never spliced into the script
    assert "${{" not in summary["run"]


def _tv_workflows() -> list[Path]:
    found = [WORKFLOWS / w for w in TV_WORKFLOWS if (WORKFLOWS / w).exists()]
    assert WORKFLOWS / "_android-tv.yml" in found
    return found


@pytest.mark.parametrize("workflow", _tv_workflows(), ids=lambda p: p.name)
def test_every_tv_job_ends_with_its_app_logs(workflow: Path) -> None:
    jobs = _load(workflow)["jobs"]
    names: list[str] = []
    for job_id, job in jobs.items():
        if "uses" in job:  # a job that calls another workflow: that one is checked
            continue
        logs = [s for s in job.get("steps", []) if s.get("uses") == USES]
        assert logs, f"{workflow.name}: job {job_id} has no app-log step ({USES})"
        assert len(logs) == 1, f"{workflow.name}: job {job_id} uploads its logs twice"
        step = logs[0]
        assert "always()" in str(step.get("if", "")), f"{workflow.name}: {job_id}'s app logs run only on success"
        names.append(step["with"]["name"])
    assert len(names) == len(set(names)), f"{workflow.name}: two jobs share an artifact name"


def test_the_tv_groups_hold_their_areas() -> None:
    filters = ci_paths.load_filters()
    for group, files in GROUPS.items():
        assert group in filters, group
        for f in files:
            assert any(ci_paths.matches(g, f) for g in filters[group]), f"{group} misses {f}"
        assert ".github/actions/tv-logs/**" in filters[group], group


@pytest.mark.parametrize("group", [g for g in GROUPS if g != "_tv_web"])
def test_a_tv_area_runs_no_other_tv_job(group: str) -> None:
    """Given a change only in one TV area, no other platform's TV job runs (once its own job
    exists, only that one: 58.16's acceptance)."""
    runs = ci_paths.which(GROUPS[group])
    ran = {j for j, r in runs.items() if r is not None}
    own = group.strip("_").replace("_", "-")
    assert ran & TV_JOBS <= {own}, f"{group}: {sorted(ran & TV_JOBS)}"


def test_the_shaping_vectors_run_every_client_checked_against_them() -> None:
    """Story 58.15: Roku's VisualText, /tv/ and the Cast receiver are each held against
    shared/i18n/visual-vectors.json (one reading order on every TV), so a change to it runs the
    three jobs; the browser tests' helper beside it runs the two web ones."""
    def ran(paths: list[str]) -> set[str]:
        return {j for j, r in ci_paths.which(paths).items() if r is not None} & TV_JOBS

    assert ran(["shared/i18n/visual-vectors.json"]) == {"roku", "tv-web", "cast-receiver"}
    assert ran(["shared/i18n/visual_order.mjs"]) == {"tv-web", "cast-receiver"}
