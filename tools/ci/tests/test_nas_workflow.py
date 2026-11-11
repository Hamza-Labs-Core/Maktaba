"""nas-shapes runs for what it tests and only then (story 59.15, T01 and T02).

.github/workflows/nas.yml starts on a pull request touching the image, the server, streaming's
ffmpeg, the pipeline's runner, scanner and speech code, plugind, the NAS install files or the
harness; its plan job's `cells` filter then decides whether the cells run (a change to the
checklists alone runs only their lint). A pull request touching only web/ starts nothing. The
push trigger reads the same paths, and every tracked file the harness needs is under them.
"""

from __future__ import annotations

import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools" / "ci"))

import ci_paths  # noqa: E402

NAS = ROOT / ".github/workflows/nas.yml"


def _doc() -> dict:
    doc = yaml.safe_load(NAS.read_text(encoding="utf-8"))
    return doc.get("on") or doc[True]  # PyYAML reads the bare key `on` as True


def _plan_filters() -> dict[str, list[str]]:
    jobs = yaml.safe_load(NAS.read_text(encoding="utf-8"))["jobs"]
    step = next(s for s in jobs["plan"]["steps"] if str(s.get("uses", "")).startswith("dorny/paths-filter@"))
    return yaml.safe_load(step["with"]["filters"])


def _any(globs: list[str], path: str) -> bool:
    return any(ci_paths.matches(g, path) for g in globs)


def test_the_dockerfile_runs_every_cell() -> None:
    on = _doc()
    f = "deploy/docker/server/Dockerfile"
    assert _any(on["pull_request"]["paths"], f)
    assert _any(_plan_filters()["cells"], f)
    assert _any(_plan_filters()["arm64"], f)


def test_web_only_does_not_start_it() -> None:
    on = _doc()
    for f in ("web/src/App.tsx", "web/packages/devportal/scripts/apps.mjs", "apps/apple/Maktaba/App.swift"):
        assert not _any(on["pull_request"]["paths"], f), f
        assert not _any(on["push"]["paths"], f), f


def test_push_and_pull_request_read_the_same_paths() -> None:
    on = _doc()
    assert on["push"]["paths"] == on["pull_request"]["paths"]
    assert on["push"]["branches"] == ["main", "release/**"]


def test_the_story_paths_start_the_cells() -> None:
    cells = _plan_filters()["cells"]
    for f in (
        "cmd/maktaba-server/cmd_container.go",
        "streaming/internal/ffmpeg/hwaccel.go",
        "pipeline/src/maktaba_pipeline/scanner/walker.py",
        "pipeline/src/maktaba_pipeline/stt/whisper.py",
        "plugind/crates/host/src/lib.rs",
        "deploy/nas/unraid/maktaba.xml",
        "cloud/installers/docker/docker-compose.yml",
        "tests/nas/cell.py",
        "tests/nas/cells.json",
        ".github/workflows/nas.yml",
    ):
        assert _any(cells, f), f
        assert _any(_doc()["pull_request"]["paths"], f), f


def test_the_dev_compose_runs_only_the_plan() -> None:
    # its images are checked by the plan job (story 59.11); it is not the image the cells run
    f = "deploy/compose/docker-compose.yml"
    assert _any(_doc()["pull_request"]["paths"], f)
    assert not _any(_plan_filters()["cells"], f)
    jobs = yaml.safe_load(NAS.read_text(encoding="utf-8"))["jobs"]
    runs = "\n".join(str(s.get("run", "")) for s in jobs["plan"]["steps"])
    assert "tests/nas/install_files.py --docker" in runs


def test_the_checklists_alone_run_only_their_lint() -> None:
    f = "docs/nas/checklists/synology.md"
    assert _any(_doc()["pull_request"]["paths"], f)
    assert not _any(_plan_filters()["cells"], f)


def test_the_plan_filters_are_inside_the_trigger() -> None:
    trigger = _doc()["pull_request"]["paths"]
    for name, globs in _plan_filters().items():
        for g in globs:
            assert g in trigger, f"{name}: {g} is not in the pull_request paths (it would never be seen)"


def test_every_harness_file_is_tracked_by_the_trigger() -> None:
    trigger = _doc()["pull_request"]["paths"]
    for p in (ROOT / "tests/nas").rglob("*"):
        if p.is_file() and "__pycache__" not in p.parts:
            assert _any(trigger, str(p.relative_to(ROOT))), p


def test_the_verdict_needs_every_job() -> None:
    jobs = yaml.safe_load(NAS.read_text(encoding="utf-8"))["jobs"]
    assert jobs["nas-shapes"]["needs"] == ["plan", "image", "cell", "truenas-lint", "truenas-deploy"]
    assert jobs["nas-shapes"]["if"] == "always()"
    # logs are kept pass or fail
    keep = next(s for s in jobs["cell"]["steps"] if s.get("name") == "Keep the logs")
    assert keep["if"] == "always()"


def test_the_truenas_app_runs_its_catalog_jobs_only_on_its_changes() -> None:
    """Story 59.10: truenas/apps' tooling runs on a change to the app, its harness or its
    healthcheck, and not on another change that starts the cells."""
    truenas = _plan_filters()["truenas"]
    for f in (
        "deploy/nas/truenas/ix-dev/community/maktaba/questions.yaml",
        "deploy/nas/truenas/custom-app.yaml",
        "tests/nas/truenas_app.py",
        "tests/nas/truenas/catalog.env",
        "tools/release/truenas_app.py",
        "cmd/maktaba-server/cmd_healthcheck.go",
    ):
        assert _any(truenas, f), f
        assert _any(_doc()["pull_request"]["paths"], f), f
    for f in ("deploy/docker/server/Dockerfile", "streaming/internal/ffmpeg/hwaccel.go", "plugind/crates/host/src/lib.rs"):
        assert not _any(truenas, f), f
    jobs = yaml.safe_load(NAS.read_text(encoding="utf-8"))["jobs"]
    assert "truenas" in jobs["truenas-lint"]["if"]
    assert "image" in jobs["truenas-deploy"]["needs"]
    assert {"truenas-lint", "truenas-deploy"} <= set(jobs["nas-shapes"]["needs"])
