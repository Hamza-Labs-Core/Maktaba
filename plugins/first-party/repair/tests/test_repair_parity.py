"""tv.maktaba.repair does exactly what ``repair_broken`` did in-process (Epic 54, stories 54.26,
54.33): a library's broken downloads read again and fetched again on two copies of one
database, in-process on one and through the plugin (the net runner, the real plugin API, its
statements and its role) on the other, each side starting from the same files. yt-dlp and
ffprobe are the stage tests' fake, the same on both sides (the runner runs in this test's
process); the files, the rows, the queue and the job log are compared, then N16."""

from __future__ import annotations

import os
import uuid
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest
from maktaba_fp_repair.stages import repair_broken
from maktaba_plugin_sdk.parity import ParityConfig, files_tree

from maktaba_pipeline.engine.enqueue import enqueue_job
from tests.plugins.firstparty.harness import SameIds, parity
from tests.stages.test_repair_broken import BROKEN, Tools, set_rules

pytestmark = [pytest.mark.integration]

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")
HERE = Path(__file__).parent
STAGES = ["repair_broken"]
KEYS = {
    "video_repairs": ("video_id",),
    "item_requests": ("video_id", "action"),
    "videos": ("id",),
}


@pytest.fixture
def template(pg_template: str) -> str:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    return pg_template


@pytest.fixture
def tools() -> Iterator[Tools]:
    t = Tools()
    t.unavailable.add("yt000000004")
    previous = repair_broken.set_runner(t)
    yield t
    repair_broken.set_runner(previous)


#: name, content, YouTube id, the probe job's state: one per path through the stage
FILES = (
    ("a.mp4", BROKEN, "yt000000001", "failed"),  # broken: fetched again and swapped in
    ("b.mp4", b"fine now", "yt000000002", "failed"),  # reads fine after all
    ("c.mp4", BROKEN, None, "failed"),  # no YouTube id: reported, never replaced
    ("d.mp4", BROKEN, "yt000000004", "failed"),  # gone from YouTube: failed
    ("e.mp4", b"fine", "yt000000005", "done"),  # an admin's re-download request
)


def seeder(root: Path) -> Any:
    from tests.engine.factory import make_library, make_video

    for name, content, _yid, _probe in FILES:
        (root / name).write_bytes(content)

    async def seed(db: Any) -> None:
        with pytest.MonkeyPatch.context() as mp:
            mp.setattr(uuid, "uuid4", SameIds(5440))
            lib = await make_library(db, "مكتبة الإصلاح")
            await db.execute("UPDATE libraries SET roots = $1 WHERE id = $2", [str(root)], lib)
            for name, _content, yid, probe in FILES:
                v = await make_video(db, lib, filename=name, path=str(root / name))
                if yid:
                    await db.execute(
                        "INSERT INTO media_metadata_enrichment (id, video_id, provider,"
                        " external_id, mapped, confidence, is_accepted)"
                        " VALUES ($1, $2, 'youtube', $3, '{}', 1.0, true)",
                        str(uuid.uuid4()), str(v), yid,
                    )  # fmt: skip
                await db.execute(
                    "INSERT INTO processing_jobs (video_id, stage, state) VALUES ($1, 'probe', $2)",
                    str(v), probe,
                )  # fmt: skip
                if name == "e.mp4":
                    await db.execute(
                        "INSERT INTO item_requests (video_id, action, requested_by)"
                        " VALUES ($1, 'redownload', 'admin')",
                        str(v),
                    )
            await set_rules(db, {"enabled": True})
            await enqueue_job(db, subject_kind="library", subject_id=lib, stage="repair_broken")

    return seed


@pytest.mark.asyncio
async def test_repair_wave_6_is_identical(
    template: str, tools: Tools, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    from maktaba_pipeline.plugins import firstparty

    root = tmp_path / "lib"
    root.mkdir()
    cfg = ParityConfig.load(HERE.parent / "parity.toml")
    tables = [(t, KEYS[t]) for t in cfg.tables]
    ignore = {k: list(v) for k, v in cfg.ignore.items()}
    async with parity(
        PG_URL,
        template,
        bundle=firstparty._raw(),
        stages=STAGES,
        group="net",
        seed=seeder(root),
        files={"library": root},
        monkeypatch=monkeypatch,
    ) as p:
        await p.run()
        assert p.a.ran == p.b.ran, "the two sides ran different jobs"
        assert "repair_broken" in p.b.ran, p.b.ran
        made = files_tree(p.tmp / "files-a" / "library")
        assert any(k.endswith(".broken") for k in made), "nothing was swapped in"
        assert sorted(tools.downloads()) == sorted(["yt000000001", "yt000000004", "yt000000005"] * 2)
        n = await p.b.db.fetchval(
            "SELECT count(*) FROM plugin_invocations WHERE plugin_id = 'tv.maktaba.repair'"
        )
        assert n >= 1, "no job went through the plugin"
        rows = await p.compare(tables, ignore=ignore, perf=False)
        assert rows == [], "\n".join(rows[:30])
        problems = await p.compare_n16(
            [], ignore=ignore, stages=STAGES, perf=cfg.perf, timing_only=True
        )
        slowest = p.report("tv.maktaba.repair")
    assert problems == [], "\n".join(problems[:20]) + "\nslowest plugin jobs:\n" + slowest
