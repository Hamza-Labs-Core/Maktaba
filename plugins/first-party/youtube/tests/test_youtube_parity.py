"""tv.maktaba.youtube does exactly what its stages did in-process (Epic 54, stories 54.26,
54.33): a channel listed and its videos downloaded into a library on two copies of one
database, in-process on one and through the plugin (the net runner, the real plugin API, its
statements and its role) on the other, each side starting from the same files. yt-dlp is the
recorded fake the stage tests use, the same on both sides (the runner runs in this test's
process); the files and the rows are compared."""

from __future__ import annotations

import os
import types
import uuid
from collections.abc import Iterator
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

import pytest
from maktaba_plugin_sdk.parity import ParityConfig, files_tree

from maktaba_pipeline.engine.enqueue import enqueue_job
from tests.plugins.firstparty.harness import SameIds, parity
from tests.stages.yt_fake import FakeYtDlp, entry

pytestmark = [pytest.mark.integration]

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")
HERE = Path(__file__).parent
STAGES = ["youtube_sync", "youtube_fetch", "youtube_backfill"]
KEYS = {
    "youtube_downloads": ("library_id", "youtube_id"),
    "youtube_videos": ("youtube_id",),
    "youtube_sources": ("id",),
    "videos": ("id",),
    "file_actions": ("id",),
}
CHANNEL = "UCchan0000000000000000"
CHANNEL_URL = f"https://www.youtube.com/channel/{CHANNEL}"


@pytest.fixture
def template(pg_template: str) -> str:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    return pg_template


@pytest.fixture
def fake(tmp_path: Path) -> Iterator[FakeYtDlp]:
    from maktaba_fp_youtube.stages import _youtube_drain as drain

    from maktaba_pipeline.integrations import youtube_download as ytd
    from maktaba_pipeline.library_mgmt.verify import VerifyResult

    f = FakeYtDlp()
    f.listings[CHANNEL_URL + "/videos"] = [
        entry("vid00000001", "درس جديد", upload_date="20240701"),
        entry("vid00000002", "درس قديم", upload_date="20240101"),
    ]
    f.meta["vid00000001"] = ("درس جديد", "20240701", 60)
    f.meta["vid00000002"] = ("درس قديم", "20240101", 90)
    prev = ytd.set_runner(f)

    async def ok(path: str, duration: float | None, full: bool, audio: bool) -> VerifyResult:
        return VerifyResult(True, [], {"full": full})

    async def no_sleep(_s: float) -> None:
        return None

    prev_verify, prev_sleep = drain.set_verifier(ok), drain.set_sleep(no_sleep)
    yield f
    ytd.set_runner(prev)
    drain.set_verifier(prev_verify)
    drain.set_sleep(prev_sleep)


_SAME_VIDEO_IDS = (
    "CREATE SEQUENCE parity_video_seq",
    "GRANT USAGE ON SEQUENCE parity_video_seq TO PUBLIC",
    "CREATE FUNCTION parity_video_id() RETURNS uuid LANGUAGE sql AS"
    " $$SELECT md5('5439-' || nextval('parity_video_seq'))::uuid$$",
    "ALTER TABLE videos ALTER COLUMN id SET DEFAULT parity_video_id()",
)


def seeder(root: Path) -> Any:
    from maktaba_pipeline.engine.sql import dumps, to_ts

    async def seed(db: Any) -> None:
        # a downloaded file's video row takes Postgres's own id (gen_random_uuid()): each
        # test database numbers them the same instead, in the order they are made
        for sql in _SAME_VIDEO_IDS:
            await db.execute(sql)
        with pytest.MonkeyPatch.context() as mp:
            mp.setattr(uuid, "uuid4", SameIds(5437))
            lib, sid = uuid.uuid4(), uuid.uuid4()
            await db.execute(
                "INSERT INTO libraries (id, name, roots, settings) VALUES ($1, $2, $3, $4)",
                str(lib), "مكتبة يوتيوب", [str(root)], dumps({}),
            )  # fmt: skip
            await db.execute(
                "INSERT INTO youtube_sources (id, library_id, kind, url, external_id, mode,"
                " added_at, folder) VALUES ($1, $2, 'channel', $3, $4, 'new_and_backfill', $5,"
                " 'القناة')",
                str(sid), str(lib), CHANNEL_URL, CHANNEL,
                to_ts(db, datetime(2024, 6, 1, tzinfo=UTC)),
            )  # fmt: skip
            await enqueue_job(db, subject_kind="library", subject_id=lib, stage="youtube_sync")

    return seed


@pytest.mark.asyncio
async def test_youtube_wave_6_is_identical(
    template: str, fake: FakeYtDlp, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    from maktaba_fp_youtube.stages import _youtube_drain as drain

    from maktaba_pipeline.library_mgmt import ledger
    from maktaba_pipeline.plugins import firstparty

    ids = SameIds(5438)
    monkeypatch.setattr(ledger, "uuid", types.SimpleNamespace(uuid4=ids, UUID=uuid.UUID))

    monkeypatch.setenv(drain.WORK_ENV, str(tmp_path / "work"))
    root = tmp_path / "lib"
    root.mkdir()
    cfg = ParityConfig.load(HERE.parent / "parity.toml")
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
        await p.run(before_side=ids.reset)
        assert p.a.ran == p.b.ran, "the two sides ran different jobs"
        for s in ("youtube_sync", "youtube_fetch"):
            assert s in p.b.ran, f"{s} never ran"
        made = files_tree(p.tmp / "files-a" / "library")
        assert any(k.endswith(".mp4") for k in made), "nothing was downloaded"
        n = await p.b.db.fetchval(
            "SELECT count(*) FROM plugin_invocations WHERE plugin_id = 'tv.maktaba.youtube'"
        )
        assert n >= 3, f"only {n} jobs went through the plugin"
        tables = [(t, KEYS[t]) for t in cfg.tables]
        ignore = {k: list(v) for k, v in cfg.ignore.items()}
        rows = await p.compare(tables, ignore=ignore, perf=False)
        assert rows == [], "\n".join(rows[:30])
