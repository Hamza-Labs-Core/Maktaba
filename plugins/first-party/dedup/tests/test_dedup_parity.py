"""tv.maktaba.dedup does exactly what its stages did in-process (Epic 54, stories 54.26,
54.32): two copies of one lecture found, merged into the copy kept (the other to the
library's trash), restored from the trash, and the trash emptied, on two copies of one
database, in-process on one and through the plugin (the light runner, the real plugin API,
its statements and its role) on the other, each side starting from the same files. The
merge's re-pointing of every table that references a video is the worker's on both sides
(dedup_commit). The library and its trash are compared byte for byte, and the rows."""

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
STAGES = ["dedup_scan", "dedup_apply", "file_restore", "trash_purge"]
KEYS = {
    "duplicate_groups": ("id",),
    "duplicate_members": ("video_id", "path"),
    "file_actions": ("id",),
    "videos": ("id",),
    "video_copies": ("video_id", "path"),
}


@pytest.fixture
def template(pg_template: str) -> str:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    if shutil.which("ffmpeg") is None:
        pytest.skip("ffmpeg is needed to make the library's files")
    return pg_template


def seeder(root: Path, lecture: bytes) -> Any:
    from maktaba_pipeline.engine.sql import dumps
    from tests.library_mgmt.dedup_world import mark_converted, probe_into_db
    from tests.library_mgmt.epic47_world import World

    async def seed(db: Any) -> None:
        with pytest.MonkeyPatch.context() as mp:
            mp.setattr(uuid, "uuid4", SameIds(5435))
            lib = uuid.uuid4()
            await db.execute(
                "INSERT INTO libraries (id, name, roots, settings) VALUES ($1, $2, $3, $4)",
                str(lib), "مكتبة المكرر", [str(root)], dumps({"trash_days": 0}),
            )  # fmt: skip
            w = World(db=db, root=root, library_id=lib)
            vid, path = await w.video("دروس/محاضرة.mp4", lecture)
            await probe_into_db(db, vid, path)
            # a byte-identical copy, as the scanner records it (one row, a copy of it)
            copy = root / "نسخ" / "محاضرة (1).mp4"
            copy.parent.mkdir(parents=True, exist_ok=True)
            copy.write_bytes(lecture)
            await db.execute(
                "INSERT INTO video_copies (video_id, path, size_bytes, mtime, seen_at)"
                " VALUES ($1, $2, $3, now(), now())",
                str(vid), str(copy), len(lecture),
            )  # fmt: skip
            await mark_converted(db, vid)
            await enqueue_job(db, subject_kind="library", subject_id=lib, stage="dedup_scan")

    return seed


@pytest.mark.asyncio
async def test_dedup_wave_5_is_identical(
    template: str, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    from maktaba_pipeline.library_mgmt import duplicates, ledger
    from maktaba_pipeline.plugins import firstparty
    from tests.library_mgmt.dedup_world import lecture

    lec = lecture(tmp_path / "lecture.mp4").read_bytes()
    ids = SameIds(5436)
    same = types.SimpleNamespace(uuid4=ids, UUID=uuid.UUID)
    monkeypatch.setattr(ledger, "uuid", same)
    monkeypatch.setattr(duplicates, "uuid", same)  # a group's id
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
        seed=seeder(root, lec),
        files={"library": root},
        monkeypatch=monkeypatch,
    ) as p:
        # 1. found and merged: the copy goes to the trash
        await p.run(before_side=ids.reset)
        assert {"dedup_scan", "dedup_apply"} <= set(p.b.ran), p.b.ran
        made = files_tree(p.tmp / "files-a" / "library")
        assert any("/.maktaba/trash/" in "/" + k for k in made), "nothing went to the trash"
        rows = await p.compare(tables, ignore=ignore, perf=False)
        assert rows == [], "\n".join(rows[:30])
        # 2. restored from the trash, then the trash emptied (each side's own action)
        for side in (p.a, p.b):
            lib = await side.db.fetchval("SELECT id FROM libraries")
            act = await side.db.fetchval("SELECT id FROM file_actions WHERE kind = 'dedup'")
            await enqueue_job(
                side.db, subject_kind="library", subject_id=lib, stage="file_restore",
                payload={"action_ids": [str(act)]},
            )  # fmt: skip
            await enqueue_job(side.db, subject_kind="library", subject_id=lib, stage="trash_purge")
        p.a.ran.clear()
        p.b.ran.clear()
        await p.run(before_side=ids.reset)
        assert {"file_restore", "trash_purge"} <= set(p.b.ran), p.b.ran
        rows = await p.compare(tables, ignore=ignore, perf=False)
        assert rows == [], "\n".join(rows[:30])
        n = await p.b.db.fetchval(
            "SELECT count(*) FROM plugin_invocations WHERE plugin_id = 'tv.maktaba.dedup'"
        )
        assert n >= len(STAGES), f"only {n} jobs went through the plugin"
