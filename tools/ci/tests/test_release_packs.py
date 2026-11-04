"""A release never ships without some platform's component packs (Epic 51, story 51.15):
release.yml's sign-and-release waits for every leg of package-packs and runs only when all
passed, the packs' download cannot fail quietly, every catalog platform has a leg, and the
index is written for the whole release (gen-components-index refuses a missing pack)."""

from __future__ import annotations

import tomllib
from pathlib import Path
from typing import Any

import yaml

ROOT = Path(__file__).resolve().parents[3]
RELEASE = ROOT / ".github/workflows/release.yml"
CATALOG = ROOT / "shared/components/catalog.toml"


def _jobs() -> dict[str, Any]:
    workflow: dict[str, Any] = yaml.safe_load(RELEASE.read_text(encoding="utf-8"))
    jobs: dict[str, Any] = workflow["jobs"]
    return jobs


def _step(job: dict[str, Any], name: str) -> dict[str, Any]:
    step: dict[str, Any] = next(s for s in job["steps"] if s.get("name") == name)
    return step


def _pack_platforms() -> set[str]:
    """The platforms of every asset the catalog ships as its own pack (not in the payload,
    its source there ``pack:<id>``): componentsindex.Packs, read from the file."""
    cat = tomllib.loads(CATALOG.read_text(encoding="utf-8"))
    out: set[str] = set()
    for c in cat["component"]:
        if c.get("kind") != "asset" or c.get("in_payload", False):
            continue
        for p in c.get("platforms", []):
            if c.get("sources", {}).get(p, c.get("source")) == f"pack:{c['id']}":
                out.add(p)
    return out


def test_sign_and_release_needs_every_pack_leg_to_pass() -> None:
    job = _jobs()["sign-and-release"]
    assert "package-packs" in job["needs"]
    cond = " ".join(str(job["if"]).split())
    assert "needs.package-packs.result == 'success'" in cond
    # no other wording that would let a failed or skipped leg through
    assert "needs.package-packs.result != " not in cond


def test_the_packs_download_cannot_fail_quietly() -> None:
    job = _jobs()["sign-and-release"]
    step = _step(job, "Download the component packs")
    assert "if" not in step
    assert step["with"]["pattern"] == "release-packs-*"
    # a failed attempt retries (test_artifact_download.py); when the last one fails
    # too, the check fails the release, never a warning that lets it go on
    check = _step(job, "Check release-packs-* downloaded")
    assert check["if"] == f"steps.{step['id']}-3.outcome == 'failure'"
    assert "continue-on-error" not in check
    assert "::error::" in check["run"] and "exit 1" in check["run"]


def test_every_catalog_platform_has_a_leg_that_must_upload() -> None:
    job = _jobs()["package-packs"]
    platforms = set(job["strategy"]["matrix"]["platform"])
    want = _pack_platforms()
    assert len(want) >= 4, want
    assert platforms == want
    upload = _step(job, "Upload the packs")
    assert upload["with"]["if-no-files-found"] == "error"
    assert "continue-on-error" not in upload


def test_the_index_covers_the_whole_release() -> None:
    step = _step(_jobs()["sign-and-release"], "Write the component index")
    run = str(step["run"])
    assert "go run ./cmd/gen-components-index" in run
    # --platform and --only index part of a release; a release indexes all of it
    assert "--platform" not in run and "--only" not in run
    assert "continue-on-error" not in step
    assert "||" not in run
