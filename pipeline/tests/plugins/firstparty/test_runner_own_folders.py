"""The runners' grants name Maktaba's own data folders as they really are (Epic 59).

A runner's sandbox grants only folders that exist when it starts (Landlock opens each one;
a missing one is no rule). In the server image ``HOME`` is the data volume, and on a fresh
volume nothing had made ``~/.maktaba/cache`` yet: the first report a runner wrote
(``review_pages``) was refused (``plugin_sandbox_refused``, ``/var/lib/maktaba/.maktaba/cache``)
and the stage fell back to the worker. The worker now makes its own folders before it names
them, by their real paths, wherever ``HOME`` puts them; a library's folders are never made.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import textwrap
import uuid
from pathlib import Path

import pytest

from maktaba_pipeline.engine.sql import dumps, is_pg
from maktaba_pipeline.plugins import native
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.thumbnail import small

pytestmark = pytest.mark.asyncio

OWN = ("data.thumbnails", "data.cache", "data.index", "data.review")


@pytest.fixture
def fresh_home(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    """An empty home reached through a symlink (a NAS's ``/var/services/homes``), nothing of
    Maktaba's in it, and no setting naming another folder."""
    real = tmp_path / "volume" / "maktaba"
    real.mkdir(parents=True)
    home = tmp_path / "home"
    home.symlink_to(real)
    monkeypatch.setenv("HOME", str(home))
    for k in ("MAKTABA_REVIEWS_ROOT", "MAKTABA_CHROMA_DIR", "MAKTABA_YT_WORK"):
        monkeypatch.delenv(k, raising=False)
    monkeypatch.setattr(small, "ROOT", None)
    return home


async def test_a_fresh_home_gets_its_folders_before_they_are_granted(
    db: Database, fresh_home: Path, tmp_path: Path
) -> None:
    media = tmp_path / "Media" / "دروس"  # a library root that is not there (a share not up)
    roots = [str(media)]
    await db.execute(
        "INSERT INTO libraries (id, name, roots) VALUES ($1, 'مكتبة', $2)",
        str(uuid.uuid4()),
        roots if is_pg(db) else dumps(roots),
    )
    doc = await native.areas_doc(db)
    real = os.path.realpath(fresh_home)
    cache = os.path.join(real, ".maktaba", "cache")
    assert doc["data.thumbnails"] == os.path.join(cache, "thumbnails")
    assert doc["data.review"] == os.path.join(cache, "reviews")
    assert doc["data.cache"].split(";") == [
        os.path.join(real, ".maktaba", "work", "yt"),
        os.path.join(cache, "audio"),
    ]
    assert doc["data.index"] == os.path.join(real, ".maktaba", "chroma")
    for area in OWN:
        for folder in doc[area].split(";"):
            assert os.path.isdir(folder), (area, folder)
            assert folder.startswith(real + os.sep), (area, folder)  # the real path
    # a library's folders (and their trash) are named, never made
    assert not media.exists() and not (tmp_path / "Media").exists()
    assert doc["library"] == str(media)


async def test_a_setting_that_names_a_folder_is_granted_that_folder(
    db: Database, fresh_home: Path, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("MAKTABA_REVIEWS_ROOT", str(tmp_path / "state" / "reports"))
    monkeypatch.setenv("MAKTABA_CHROMA_DIR", str(tmp_path / "state" / "chroma"))
    doc = await native.areas_doc(db)
    assert doc["data.review"] == os.path.realpath(tmp_path / "state" / "reports")
    assert doc["data.index"] == os.path.realpath(tmp_path / "state" / "chroma")
    assert (tmp_path / "state" / "reports").is_dir()


WRITE_REPORT = textwrap.dedent(
    """
    import json, os, sys
    from maktaba_plugin_runner import sandbox
    reports, run_dir = sys.argv[1], sys.argv[2]
    out = {}
    try:
        out["applied"] = sandbox.apply(sandbox.Policy(write=[reports], run_dir=run_dir)).level
    except sandbox.SandboxUnavailable as exc:
        out["applied"] = "unavailable:" + exc.step
    try:
        tmp = os.path.join(reports, "lib.part.html")
        with open(tmp, "w") as f:
            f.write("<p>تقرير</p>")
        os.replace(tmp, os.path.join(reports, "lib.html"))
        out["report"] = "allowed"
    except OSError as exc:
        out["report"] = "denied:" + type(exc).__name__
    print(json.dumps(out))
    """
)


@pytest.mark.skipif(
    not (sys.platform == "darwin" or sys.platform.startswith("linux")),
    reason="runners run on macOS and Linux (the stages run in the worker on Windows)",
)
async def test_a_runner_on_a_fresh_home_writes_its_report(
    db: Database, fresh_home: Path, tmp_path: Path
) -> None:
    """What the runner does with the areas: its sandbox from them, then a report written
    the way ``review_pages`` writes one."""
    doc = await native.areas_doc(db)
    run_dir = tmp_path / "run"
    run_dir.mkdir()
    out = subprocess.run(
        [sys.executable, "-c", WRITE_REPORT, doc["data.review"], str(run_dir.resolve())],
        capture_output=True, text=True, timeout=60,
    )  # fmt: skip
    assert out.returncode == 0, out.stderr
    got = json.loads(out.stdout.strip().splitlines()[-1])
    if str(got["applied"]).startswith("unavailable"):
        pytest.skip(f"this machine cannot apply the sandbox: {got['applied']}")
    if got["applied"] == "partial" and sys.platform.startswith("linux"):
        pytest.skip("no Landlock on this kernel: files are not limited")
    assert got["report"] == "allowed", got
    assert (Path(doc["data.review"]) / "lib.html").read_text() == "<p>تقرير</p>"
