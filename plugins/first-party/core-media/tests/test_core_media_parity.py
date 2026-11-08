"""tv.maktaba.core-media does exactly what its stages did in-process (Epic 54, story 54.36
§1, T01): a library of real files scanned on two copies of one database, the plugin's
stages in-process on one and through the plugin on the other (the light runner, the real
plugin API, its statements and its role), each side from the same files. The scan finds
the videos, probe reads their streams (ffprobe a fake that answers by file name: a talk
with two audio tracks, a silent one, one it cannot read), and what probe's edges queue runs
(thumb_hash, link from a yt-dlp ``.info.json``); then each video's identity, fingerprint,
style sample and access check. ``videos.state`` is written by the engine on both sides: in
the runner, probe's transition is the worker's (the relayed advance). Every row, job and
log line is compared, then the job times (N16)."""

from __future__ import annotations

import json
import os
import sys
import types
import uuid
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.media import reset_runner
from tests.plugins.firstparty.harness import SameIds, parity, plugin_api_dev
from tests.stages.world import make_fake_media, models_off

pytestmark = [pytest.mark.integration]

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")
HERE = Path(__file__).parent
PLUGIN = "tv.maktaba.core-media"
#: the stages wave 9 moved into tv.maktaba.core-media that this world runs (fp_gate needs a
#: season's candidates and storage_canonicalize a share map: their own tests prove them)
STAGES = [
    "scan", "probe", "thumb_hash", "link", "content_hash", "fingerprint", "style_sample",
    "access_check",
]  # fmt: skip
#: and tv.maktaba.thumbnails's thumbnail (wave 9 too, same runner), which content_hash's edge
#: queues: its state transition is the second the worker writes for a plugin's job
ALSO = ["thumbnail"]
#: the stages queued by hand for each video once the scan and probe's edges have run
LATER = ("content_hash", "fingerprint", "style_sample", "access_check")
KEYS = {
    "videos": ("id",),
    "media_info": ("video_id",),
    "audio_tracks": ("video_id", "track_index"),
    "video_signals": ("video_id",),
    "media_metadata_enrichment": ("video_id", "provider"),
    "youtube_videos": ("youtube_id",),
    "youtube_channels": ("channel_id",),
}
LIBRARY = uuid.UUID(int=5436)
FILES = ("talk_a.mp4", "talk_b.mp4", "silent_c.mp4", "broken_d.mkv")
#: the scan's new videos take their id from the table's default (``gen_random_uuid()`` on
#: Postgres): on each side a sequence instead, so the rows compare by id. The plugin's role
#: inserts the rows on the plugin side, so it may use the sequence.
SAME_VIDEO_IDS = (
    "CREATE SEQUENCE parity_video_ids",
    "GRANT USAGE ON SEQUENCE parity_video_ids TO PUBLIC",
    "ALTER TABLE videos ALTER COLUMN id SET DEFAULT"
    " (lpad(to_hex(nextval('parity_video_ids')), 32, '0'))::uuid",
)


def selected() -> list[str]:
    want = [s for s in os.environ.get("MAKTABA_PARITY_STAGES", "").split(",") if s]
    return [s for s in STAGES + ALSO if not want or s in want]


@pytest.fixture
def template(pg_template: str) -> str:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    if not selected():
        pytest.skip("no stage of tv.maktaba.core-media selected")
    # the plugin API's dev server is built here, in setup, not in the timed call
    plugin_api_dev()
    return pg_template


@pytest.fixture
def media(monkeypatch: pytest.MonkeyPatch, tmp_path: Path) -> Iterator[None]:
    """ffmpeg (chromaprint, frames, style) answers by the file's family, ffprobe by its
    name, the poster and sprite by the file's content: the same on both sides, as the
    runner is in this process."""
    from maktaba_fp_thumbnails.stages import thumbnail as thumbnail_stage

    from maktaba_pipeline.thumbnail import handler as thumbnail_handler
    from maktaba_pipeline.thumbnail.generator import ThumbnailSet, thumbnail_dir_for

    make_fake_media({})
    probe_mod = sys.modules["maktaba_pipeline.audio.probe"]

    async def fake_frames(
        src: str, *, duration_sec: float, content_hash: str, chapters: Any = (), **_: Any
    ) -> ThumbnailSet:
        d = thumbnail_dir_for(content_hash, root=str(tmp_path / "thumbs"))
        d.mkdir(parents=True, exist_ok=True)
        poster, sprite = d / "poster.jpg", d / "sprite.jpg"
        poster.write_bytes(b"poster " + content_hash.encode())
        sprite.write_bytes(b"sprite " + content_hash.encode())
        return ThumbnailSet(poster=poster, sprite=sprite)

    monkeypatch.setattr(thumbnail_handler, "generate_thumbnails", fake_frames)
    monkeypatch.setattr(thumbnail_stage, "generate", fake_frames)

    async def fake_ffprobe(path: str, **_: Any) -> dict[str, Any]:
        name = Path(path).name
        if name.startswith("broken"):
            raise probe_mod.ProbeError(f"ffprobe could not read {name}")
        streams: list[dict[str, Any]] = [
            {"index": 0, "codec_type": "video", "codec_name": "h264", "width": 1280,
             "height": 720, "avg_frame_rate": "25/1"},
        ]  # fmt: skip
        if not name.startswith("silent"):
            streams += [
                {"index": 1, "codec_type": "audio", "codec_name": "aac", "channels": 2,
                 "sample_rate": "48000", "tags": {"language": "ara"},
                 "disposition": {"default": 1}},
                {"index": 2, "codec_type": "audio", "codec_name": "ac3", "channels": 6,
                 "sample_rate": "48000", "tags": {"language": "eng", "title": "5.1"},
                 "disposition": {"default": 0}},
            ]  # fmt: skip
        return {
            "streams": streams,
            "format": {"format_name": "mov,mp4", "duration": "1500.25", "bit_rate": "1200000"},
        }

    monkeypatch.setattr(probe_mod, "run_ffprobe", fake_ffprobe)
    yield
    reset_runner()


def library(root: Path) -> Path:
    """Four videos of one show, a thumbnail beside one and the yt-dlp sidecar of another."""
    show = root / "lib" / "دروس التفسير"
    for name in FILES:
        p = show / name
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_bytes(f"video {name}".encode() * 64)
    (show / "talk_a.jpg").write_bytes(b"\xff\xd8\xff\xe0core\xff\xd9")
    (show / "talk_b.info.json").write_text(
        json.dumps(
            {
                "id": "ytCoreMed01", "title": "تفسير سورة الفاتحة", "channel": "قناة الدروس",
                "channel_id": "UCcoremedia", "upload_date": "20240105", "duration": 1500,
                "webpage_url": "https://www.youtube.com/watch?v=ytCoreMed01",
            },
            ensure_ascii=False,
        ),
        encoding="utf-8",
    )  # fmt: skip
    return root / "lib"


def seeder(lib_dir: Path) -> Any:
    async def seed(db: Any) -> None:
        await models_off(db)
        # transcription is another plugin's (its own parity proves it): switched off here, as
        # on a home without it, so probe's edges queue no transcribe, which would run ffmpeg
        # on these stand-in files (absent on CI: each job failing and retried, 4x the time)
        await db.execute(
            "UPDATE job_stages SET enabled = FALSE, disabled_reason = 'component_off'"
            " WHERE stage IN ('transcribe', 'extract')"
        )
        await db.execute(
            "INSERT INTO libraries (id, name, roots) VALUES ($1, 'مكتبة الأساس', $2)",
            str(LIBRARY), [str(lib_dir)],
        )  # fmt: skip
        await enqueue_job(db, subject_kind="library", subject_id=LIBRARY, stage="scan")

    return seed


@pytest.mark.asyncio
@pytest.mark.usefixtures("media")
async def test_core_media_wave_9_is_identical(
    template: str, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    from maktaba_fp_core_media.stages import link
    from maktaba_plugin_sdk.parity import ParityConfig

    from maktaba_pipeline.plugins import firstparty
    from maktaba_pipeline.thumbnail import small

    # the link rows get the same ids on both sides (the videos': SAME_VIDEO_IDS)
    ids = SameIds(5436)
    monkeypatch.setattr(link, "uuid", types.SimpleNamespace(uuid4=ids, UUID=uuid.UUID))
    lib_dir = library(tmp_path)
    thumbs = tmp_path / "thumbs"
    thumbs.mkdir()
    monkeypatch.setattr(small, "ROOT", str(thumbs))
    cfg = ParityConfig.load(HERE.parent / "parity.toml")
    tables = [(t, KEYS[t]) for t in cfg.tables]
    ignore = {k: list(v) for k, v in cfg.ignore.items()}
    stages = selected()
    async with parity(
        PG_URL,
        template,
        bundle=firstparty._raw(),
        stages=stages,
        group="light",
        setup_sql=SAME_VIDEO_IDS,
        seed=seeder(lib_dir),
        # a stage the wave did not move writes too (thumb_small): each side starts from the
        # same files
        files={"library": lib_dir, "data.thumbnails": thumbs},
        monkeypatch=monkeypatch,
    ) as p:
        # 1. the scan, probe, and what probe's edges queue
        await p.run(before_side=ids.reset)
        assert p.a.ran == p.b.ran, "the two sides ran different jobs"
        rows = await p.compare(tables, ignore=ignore, perf=False)
        assert rows == [], "\n".join(rows[:30])
        # 2. each video's identity, fingerprint, style sample and access check, and the
        # thumbnail its identity queues; one video is indexed (as transcription leaves it),
        # so its thumbnail moves it on (INDEXED -> THUMBNAILED), the worker's write
        for side in (p.a, p.b):
            await side.db.execute(
                "UPDATE videos SET state = 'indexed' WHERE filename = 'talk_a.mp4'"
            )
            for vid in await side.db.fetch("SELECT id FROM videos ORDER BY path"):
                for stage in LATER:
                    await enqueue_job(
                        side.db, subject_kind="video", subject_id=vid["id"], stage=stage
                    )
        await p.run(before_side=ids.reset)
        assert p.a.ran == p.b.ran, "the two sides ran different jobs"
        for s in stages:
            assert s in p.b.ran, f"{s} never ran"
        rows = await p.compare(tables, ignore=ignore, perf=False)
        assert rows == [], "\n".join(rows[:30])
        # the world is what it says: probe's transition written by the engine (the relayed
        # advance) on the plugin side, as in-process; one video probe could not read
        states = {
            r["filename"]: r["state"]
            for r in await p.b.db.fetch("SELECT filename, state FROM videos")
        }
        assert set(states) == set(FILES), states
        assert states == {
            "talk_a.mp4": "thumbnailed" if "thumbnail" in stages else "indexed",
            "talk_b.mp4": "probed", "silent_c.mp4": "ready_no_audio",
            "broken_d.mkv": "discovered",
        }, states  # fmt: skip
        probes = {
            r["filename"]: r["state"]
            for r in await p.b.db.fetch(
                "SELECT v.filename, j.state FROM processing_jobs j"
                " JOIN videos v ON v.id::text = j.subject_id::text WHERE j.stage = 'probe'"
            )
        }
        assert probes["broken_d.mkv"] == "failed" and probes["talk_a.mp4"] == "done", probes
        assert await p.b.db.fetchval("SELECT count(*) FROM youtube_videos") == 1
        n = await p.b.db.fetchval(
            "SELECT count(*) FROM plugin_invocations WHERE plugin_id = $1", PLUGIN
        )
        assert n >= len(stages), f"only {n} jobs went through the plugin"
        problems = await p.compare_n16(tables, ignore=ignore, stages=stages, perf=cfg.perf)
        slowest = p.report(PLUGIN)
    assert problems == [], "\n".join(problems[:20]) + "\nslowest plugin jobs:\n" + slowest
