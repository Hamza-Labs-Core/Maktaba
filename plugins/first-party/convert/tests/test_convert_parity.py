"""tv.maktaba.convert does exactly what its stage did in-process (Epic 54, stories 54.26,
54.32): a library of real files converted on two copies of one database, in-process on one
and through the plugin (the light runner, the real plugin API, its statements and its role)
on the other, each side starting from the same files; the library and its trash are
compared byte for byte, and the rows, the queue and the job logs."""

from __future__ import annotations

import os
import shutil
import types
import uuid
from pathlib import Path
from typing import Any

import pytest
from maktaba_plugin_sdk.parity import ParityConfig, files_tree

from maktaba_pipeline.engine.enqueue import enqueue_job
from tests.plugins.firstparty.harness import SameIds, parity

pytestmark = [pytest.mark.integration]

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")
HERE = Path(__file__).parent
STAGES = ["convert_mp4"]
KEYS = {
    "file_actions": ("id",),
    "videos": ("id",),
    "video_copies": ("video_id", "path"),
    "audio_tracks": ("video_id", "track_index"),
}


@pytest.fixture
def template(pg_template: str) -> str:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    if shutil.which("ffmpeg") is None:
        pytest.skip("ffmpeg is needed to make the library's files")
    return pg_template


def seeder(root: Path, media: dict[str, bytes]) -> Any:
    from maktaba_pipeline.engine.sql import dumps
    from tests.library_mgmt.epic47_world import World

    async def seed(db: Any) -> None:
        with pytest.MonkeyPatch.context() as mp:
            mp.setattr(uuid, "uuid4", SameIds(5433))
            lib = uuid.uuid4()
            await db.execute(
                "INSERT INTO libraries (id, name, roots, settings) VALUES ($1, $2, $3, $4)",
                str(lib), "مكتبة التحويل", [str(root)], dumps({}),
            )  # fmt: skip
            w = World(db=db, root=root, library_id=lib)
            for rel, content in media.items():
                vid, _ = await w.video(rel, content)
                await db.execute(
                    "INSERT INTO audio_tracks (video_id, track_index, codec, language)"
                    " VALUES ($1, 1, 'opus', 'ar')",
                    str(vid),
                )
            await enqueue_job(db, subject_kind="library", subject_id=lib, stage="convert_mp4")

    return seed


@pytest.mark.asyncio
async def test_convert_wave_5_is_identical(
    template: str, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    from maktaba_pipeline.library_mgmt import convert as cv
    from maktaba_pipeline.library_mgmt import ledger
    from maktaba_pipeline.plugins import firstparty
    from maktaba_pipeline.thumbnail import small
    from tests.library_mgmt.test_convert import mkv_h264_opus

    src = tmp_path / "src"
    src.mkdir()
    media = {
        "دروس/درس 01.mkv": mkv_h264_opus(src / "a.mkv").read_bytes(),
        "دروس/درس 02.mkv": mkv_h264_opus(src / "b.mkv", seconds=2.0).read_bytes(),
    }
    ids = SameIds(5434)
    monkeypatch.setattr(ledger, "uuid", types.SimpleNamespace(uuid4=ids, UUID=uuid.UUID))
    monkeypatch.setattr(cv, "WORK_ROOT", str(tmp_path / "work"))
    monkeypatch.setattr(small, "ROOT", str(tmp_path / "thumbs"))
    root = tmp_path / "lib"
    root.mkdir()
    cfg = ParityConfig.load(HERE.parent / "parity.toml")
    async with parity(
        PG_URL,
        template,
        bundle=firstparty._raw(),
        stages=STAGES,
        group="files",
        seed=seeder(root, media),
        files={"library": root, "thumbs": tmp_path / "thumbs"},
        monkeypatch=monkeypatch,
    ) as p:
        await p.run(before_side=ids.reset)
        assert p.a.ran == p.b.ran, "the two sides ran different jobs"
        assert "convert_mp4" in p.b.ran
        n = await p.b.db.fetchval(
            "SELECT count(*) FROM plugin_invocations WHERE plugin_id = 'tv.maktaba.convert'"
        )
        assert n >= 3, f"only {n} jobs went through the plugin"
        tables = [(t, KEYS[t]) for t in cfg.tables]
        ignore = {k: list(v) for k, v in cfg.ignore.items()}
        rows = await p.compare(tables, ignore=ignore, perf=False)
        assert rows == [], "\n".join(rows[:30])
        base = files_tree(p.tmp / "files-base" / "library")
        made = files_tree(p.tmp / "files-a" / "library")
        assert any(k.endswith(".mp4") for k in set(made) - set(base)), "nothing was converted"
        assert any("/.maktaba/trash/" in "/" + k for k in made), "no original in the trash"
        problems = await p.compare_n16(
            [], ignore=ignore, stages=STAGES, perf=cfg.perf, timing_only=True
        )
        slowest = p.report("tv.maktaba.convert")
    assert problems == [], "\n".join(problems[:20]) + "\nslowest plugin jobs:\n" + slowest
