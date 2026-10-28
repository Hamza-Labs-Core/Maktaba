"""Epic 47 (stories 47.8 and 47.9): YouTube sources, the error table and ``youtube_fetch``.

yt-dlp is faked (:mod:`tests.stages.yt_fake`) with recorded output per error class;
nothing reaches YouTube. The opt-in smoke test at the end runs the real yt-dlp when
``MAKTABA_TEST_YTDLP=1``.
"""

from __future__ import annotations

import os
import uuid
from collections.abc import Iterator
from datetime import UTC, date, datetime, timedelta
from pathlib import Path
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.dispatch import dispatch_job
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import EPIC47_PRIORITIES, GRAPH
from maktaba_pipeline.engine.reconcile import reconcile_once
from maktaba_pipeline.engine.sql import dumps, is_pg, loads, parse_ts, to_ts
from maktaba_pipeline.engine.stages import _youtube_drain as drain
from maktaba_pipeline.engine.watchers import watch_once
from maktaba_pipeline.integrations import youtube_download as ytd
from maktaba_pipeline.integrations.youtube_errors import classify
from maktaba_pipeline.library_mgmt.verify import VerifyResult
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library
from tests.extensions.harness import error_of, run_stage, setup_runtime, summary_of
from tests.stages.yt_fake import STDERR, FakeYtDlp, entry

CHANNEL = "UCchan0000000000000000"
CHANNEL_URL = f"https://www.youtube.com/channel/{CHANNEL}"
VIDEOS = CHANNEL_URL + "/videos"
ADDED = datetime(2024, 6, 1, tzinfo=UTC)


def yid(n: int) -> str:
    return f"vid{n:08d}"


# ------------------------------------------------------------------ fixtures


@pytest.fixture
def fake(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Iterator[FakeYtDlp]:
    f = FakeYtDlp()
    monkeypatch.setenv(drain.WORK_ENV, str(tmp_path / "work"))
    previous = ytd.set_runner(f)

    async def ok(path: str, duration: float | None, full: bool, audio: bool) -> VerifyResult:
        return VerifyResult(True, [], {"full": full})

    prev_verify = drain.set_verifier(ok)

    async def no_sleep(_s: float) -> None:
        return None

    prev_sleep = drain.set_sleep(no_sleep)
    yield f
    ytd.set_runner(previous)
    drain.set_verifier(prev_verify)
    drain.set_sleep(prev_sleep)


async def make_lib(db: Any, tmp_path: Path, settings: dict[str, Any] | None = None) -> UUID:
    lib = await make_library(db)
    root = tmp_path / "lib"
    root.mkdir(exist_ok=True)
    roots = [str(root)]
    await db.execute(
        "UPDATE libraries SET roots = $2, settings = $3 WHERE id = $1",
        str(lib),
        roots if is_pg(db) else dumps(roots),
        dumps(settings or {}),
    )
    return lib


async def add_source(
    db: Any,
    lib: UUID,
    *,
    kind: str = "channel",
    external_id: str = CHANNEL,
    url: str = CHANNEL_URL,
    mode: str = "new_and_backfill",
    added_at: datetime = ADDED,
    folder: str | None = None,
) -> UUID:
    sid = uuid.uuid4()
    await db.execute(
        "INSERT INTO youtube_sources (id, library_id, kind, url, external_id, mode, added_at,"
        " folder) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)",
        str(sid),
        str(lib),
        kind,
        url,
        external_id,
        mode,
        to_ts(db, added_at),
        folder,
    )
    return sid


async def want(db: Any, lib: UUID, sid: UUID, ids: list[str], *, backfill: bool = False) -> None:
    for n, i in enumerate(ids):
        await db.execute(
            "INSERT INTO youtube_downloads (library_id, youtube_id, source_id, state, backfill,"
            " upload_date) VALUES ($1,$2,$3,'wanted',$4,$5)",
            str(lib),
            i,
            str(sid),
            backfill if is_pg(db) else int(backfill),
            date(2024, 7, 1) + timedelta(days=n) if is_pg(db) else f"2024-07-{n + 1:02d}",
        )


async def link(db: Any, lib: UUID, youtube_id: str) -> None:
    from tests.engine.factory import make_video

    v = await make_video(db, lib)
    await db.execute(
        "INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, mapped,"
        " confidence, is_accepted) VALUES ($1,$2,'youtube',$3,$4,1.0,$5)",
        str(uuid.uuid4()),
        str(v),
        youtube_id,
        "{}",
        True,
    )


async def rows(db: Any, lib: UUID) -> dict[str, Any]:
    got = await db.fetch("SELECT * FROM youtube_downloads WHERE library_id = $1", str(lib))
    return {str(r["youtube_id"]): r for r in got}


async def run_all(db: Any, rt: Any, lib: UUID, stage: str = "youtube_fetch") -> list[Any]:
    """Queue ``stage`` for the library and run its jobs (it queues itself, one video a
    job) until none is left; the finished rows."""
    await enqueue_job(
        db, subject_kind="library", subject_id=lib, stage=stage,
        priority=EPIC47_PRIORITIES[stage],
    )  # fmt: skip
    done: list[Any] = []
    for _ in range(100):
        job = await claim_for_pool(db, worker_id="w1", pool="network_yt", stages=[stage])
        if job is None:
            return done
        await dispatch_job(db, job, runtime=rt, legacy_handlers={})
        done.append(await db.fetchrow("SELECT * FROM processing_jobs WHERE id = $1", job.id))
    raise AssertionError("the stage kept queueing itself")


async def live_job(db: Any, stage: str) -> Any:
    return await db.fetchrow(
        "SELECT * FROM processing_jobs WHERE stage = $1 AND state = 'pending'", stage
    )


# ------------------------------------------------------------------ pure pieces


@pytest.mark.parametrize(
    ("key", "action", "reason"),
    [
        ("bot_check", "bot_check", "bot_check"),
        ("bot_check_curly", "bot_check", "bot_check"),
        ("unavailable", "gone", "removed"),
        ("rate_limited", "rate_limited", "rate_limited"),
        ("members_only", "skipped", "members_only"),
        ("private", "skipped", "private"),
        ("age_gate", "skipped", "age_gate"),
        ("geo", "skipped", "geo"),
        ("removed", "gone", "removed"),
        ("terminated", "gone", "removed"),
        ("network", "transient", "network"),
        ("http_503", "transient", "network"),
        ("format", "format", "format"),
        ("unknown", "unknown", "error"),
    ],
)
def test_error_table_classes(key: str, action: str, reason: str) -> None:
    got = classify("[youtube] Extracting URL\n" + STDERR[key])
    assert (got.action, got.reason) == (action, reason)
    assert got.message.startswith("ERROR")


def test_download_argv_is_the_spec(tmp_path: Path) -> None:
    argv = ytd.download_argv(
        "AAAAAAAAAAA",
        tmp_path,
        archive="/lib/.maktaba/yt-dlp-archive.txt",
        options=ytd.DownloadOptions.from_json(
            {"cookies_from_browser": "safari", "limit_rate": "2M", "max_height": 720}
        ),
    )
    joined = " ".join(argv)
    for piece in (
        "--no-playlist", "--download-archive /lib/.maktaba/yt-dlp-archive.txt",
        "--merge-output-format mp4", "--write-info-json", "--write-thumbnail",
        "--convert-thumbnails jpg", "--write-subs", "--write-auto-subs", "--sub-langs ar,en",
        "--convert-subs srt", "--retries 10", "--fragment-retries 10", "--sleep-requests 1",
        "--sleep-interval 5", "--max-sleep-interval 30", "--limit-rate 2M",
        "--cookies-from-browser safari", "--newline", "--progress-template download:%(progress)j",
    ):  # fmt: skip
        assert piece in joined, piece
    assert argv[argv.index("--match-filter") + 1] == "!is_live & live_status!=is_upcoming"
    assert "[height<=720]" in argv[argv.index("-f") + 1]
    assert argv[argv.index("-o") + 1] == str(tmp_path / "v-AAAAAAAAAAA.%(ext)s")
    assert argv[-1] == "https://www.youtube.com/watch?v=AAAAAAAAAAA"
    fallback = ytd.download_argv("AAAAAAAAAAA", tmp_path, archive=None, fallback_format=True)
    assert fallback[fallback.index("-f") + 1] == "b"
    # no cookies by default; bad option values keep the defaults
    plain = ytd.DownloadOptions.from_json({"cookies_from_browser": "x; rm -rf /", "max_height": 9})
    assert plain == ytd.DEFAULT_OPTIONS


def test_names_are_sanitised() -> None:
    assert ytd.sanitize_name(' a/b:c*?"<>| . ') == "a_b_c______"
    assert ytd.sanitize_name("...") == "Unknown"
    long = ytd.sanitize_name("ب" * 300)
    assert len(long.encode()) <= 180
    name = ytd.video_filename("AAAAAAAAAAA", "حلقة 1: البداية", date(2024, 1, 15), "mp4")
    assert name == "2024-01-15 حلقة 1_ البداية [AAAAAAAAAAA].mp4"


# ------------------------------------------------------------------ youtube_sync: marking


@pytest.mark.asyncio
async def test_sync_marks_only_new_in_range_items(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib)
    await add_source(db, lib, external_id="UCoff00000000000000000", mode="off",
                     url="https://www.youtube.com/channel/UCoff00000000000000000")  # fmt: skip
    await link(db, lib, yid(5))
    fake.listings[VIDEOS] = [
        entry(yid(1), "new", upload_date="20240701"),
        entry(yid(2), "members", upload_date="20240702", availability="subscriber_only"),
        entry(yid(3), "upcoming", live_status="is_upcoming"),
        entry(yid(4), "old", upload_date="20240101"),
        entry(yid(5), "linked", upload_date="20230101"),
        entry(yid(6), "undated"),
    ]
    rt = await setup_runtime(db, [])
    job = await run_stage(db, rt, "youtube_sync", "library", lib)
    assert job["state"] == "done", error_of(job)
    got = await rows(db, lib)
    assert set(got) == {yid(n) for n in range(1, 7)}
    assert (got[yid(5)]["state"], got[yid(5)]["reason"]) == ("done", "library")  # seeded
    assert got[yid(1)]["state"] == "wanted" and not got[yid(1)]["backfill"]
    assert got[yid(2)]["state"] == "skipped" and got[yid(2)]["reason"] == "members_only"
    assert got[yid(3)]["state"] == "skipped" and got[yid(3)]["reason"] == "live"
    assert got[yid(4)]["state"] == "wanted" and got[yid(4)]["backfill"]
    assert got[yid(6)]["backfill"]  # no date, first listing: back catalogue
    assert str(got[yid(1)]["source_id"]) == str(sid)
    # only the enabled source was listed, every tab, in full the first time
    assert {u for u, _ in fake.listed} == {
        VIDEOS,
        CHANNEL_URL + "/shorts",
        CHANNEL_URL + "/streams",
    }
    assert not any(brk for _, brk in fake.listed)
    src = await db.fetchrow("SELECT * FROM youtube_sources WHERE id = $1", str(sid))
    assert src["status"] == "ok" and src["last_checked_at"] is not None
    fetch = await live_job(db, "youtube_fetch")
    assert fetch is not None and fetch["priority"] == 120
    s = summary_of(job)["youtube_sources"]
    assert (s["new"], s["backfill"], s["skipped"], s["local"]) == (1, 2, 2, 1)


@pytest.mark.asyncio
async def test_new_only_skips_the_back_catalogue(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    await add_source(db, lib, mode="new_only")
    fake.listings[VIDEOS] = [
        entry(yid(1), upload_date="20240701"),
        entry(yid(2), upload_date="20240101"),
    ]
    rt = await setup_runtime(db, [])
    await run_stage(db, rt, "youtube_sync", "library", lib)
    got = await rows(db, lib)
    assert got[yid(1)]["state"] == "wanted"
    assert got[yid(2)]["state"] == "skipped" and got[yid(2)]["reason"] == "too_old"


@pytest.mark.asyncio
async def test_playlist_source_is_listed_in_full(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    url = "https://www.youtube.com/playlist?list=PLlist00001"
    await add_source(db, lib, kind="playlist", external_id="PLlist00001", url=url,
                     added_at=datetime(2020, 1, 1, tzinfo=UTC))  # fmt: skip
    fake.listings[url] = [
        {**entry(yid(1), upload_date="20240701"), "playlist_title": "Season 1"},
        entry(yid(2), upload_date="20240702"),
    ]
    rt = await setup_runtime(db, [])
    await run_stage(db, rt, "youtube_sync", "library", lib)
    await run_stage(db, rt, "youtube_sync", "library", lib)
    assert fake.listed == [(url, False), (url, False)]  # no head to stop at
    src = await db.fetchrow("SELECT folder FROM youtube_sources WHERE external_id = 'PLlist00001'")
    assert src["folder"] == "Season 1"
    assert set(await rows(db, lib)) == {yid(1), yid(2)}


# ------------------------------------------------------------------ youtube_fetch


@pytest.mark.asyncio
async def test_fetch_places_the_file_and_sidecars_and_scans_it(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib, folder="Chan")
    await want(db, lib, sid, [yid(1)])
    fake.meta[yid(1)] = ("حلقة: 1", "20240715", 60)
    rt = await setup_runtime(db, [])
    job = await run_stage(db, rt, "youtube_fetch", "library", lib)
    assert job["state"] == "done", error_of(job)
    folder = tmp_path / "lib" / "Chan"
    stem = f"2024-07-15 حلقة_ 1 [{yid(1)}]"
    assert sorted(p.name for p in folder.iterdir()) == sorted(
        [f"{stem}.mp4", f"{stem}.info.json", f"{stem}.jpg", f"{stem}.ar.srt"]
    )
    row = (await rows(db, lib))[yid(1)]
    assert row["state"] == "done" and row["video_id"] is not None and row["done_at"] is not None
    video = await db.fetchrow("SELECT * FROM videos WHERE id = $1", str(row["video_id"]))
    assert video["path"] == str(folder / f"{stem}.mp4") and video["library_id"] is not None
    action = await db.fetchrow("SELECT * FROM file_actions WHERE kind = 'youtube_fetch'")
    assert action["state"] == "done" and action["new_path"] == video["path"]
    archive = (tmp_path / "lib" / ".maktaba" / "yt-dlp-archive.txt").read_text()
    assert f"youtube {yid(1)}" in archive
    assert not (tmp_path / "work" / str(lib) / f"v-{yid(1)}").exists()  # cleaned
    assert summary_of(job)["downloaded"] == 1
    convert = await db.fetchrow(
        "SELECT * FROM processing_jobs WHERE stage = 'convert_mp4' AND state = 'pending'"
    )
    assert convert is not None and convert["priority"] == 150
    assert (convert["subject_kind"], str(convert["subject_id"])) == ("video", str(row["video_id"]))
    # the download asked for the library's archive
    argv = next(a for a in fake.calls if "--flat-playlist" not in a)
    assert argv[argv.index("--download-archive") + 1].endswith(".maktaba/yt-dlp-archive.txt")


@pytest.mark.asyncio
async def test_fetch_takes_new_items_newest_first(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib)
    await want(db, lib, sid, [yid(1), yid(2), yid(3)])  # dated 07-01, 07-02, 07-03
    await want(db, lib, sid, [yid(9)], backfill=True)
    rt = await setup_runtime(db, [])
    jobs = await run_all(db, rt, lib)
    assert fake.downloaded == [yid(3), yid(2), yid(1)]  # never the back catalogue
    assert len(jobs) == 3  # one video a job
    assert (await rows(db, lib))[yid(9)]["state"] == "wanted"


@pytest.mark.asyncio
async def test_each_error_class_lands_right(db: Database, tmp_path: Path, fake: FakeYtDlp) -> None:
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib)
    plan = {
        yid(1): ["members_only"],
        yid(2): ["geo"],
        yid(3): ["removed"],
        yid(4): ["network", "network", "ok"],
        yid(5): ["format_ok"],
        yid(6): ["age_gate"],
        yid(7): ["ok"],  # verification fails for this one
        yid(8): ["private"],
        yid(9): ["unknown"],
    }
    fake.downloads.update(plan)
    await want(db, lib, sid, list(plan))

    async def verify(path: str, duration: float | None, full: bool, audio: bool) -> VerifyResult:
        bad = yid(7) in path
        return VerifyResult(not bad, ["decode errors: broken"] if bad else [], {})

    drain.set_verifier(verify)
    rt = await setup_runtime(db, [])
    jobs = await run_all(db, rt, lib)
    assert all(j["state"] == "done" for j in jobs), [error_of(j) for j in jobs]
    got = await rows(db, lib)
    states = {k: (r["state"], r["reason"]) for k, r in got.items()}
    assert states[yid(1)] == ("skipped", "members_only")
    assert states[yid(2)] == ("skipped", "geo")
    assert states[yid(3)] == ("gone", "removed")
    assert states[yid(4)][0] == "done" and got[yid(4)]["attempts"] == 3
    assert states[yid(5)][0] == "done"
    assert states[yid(6)] == ("skipped", "age_gate")
    assert states[yid(7)] == ("failed", "verify")
    assert got[yid(7)]["failure_class"] == "convert_verify"
    assert states[yid(8)] == ("skipped", "private")
    assert states[yid(9)][0] == "failed" and got[yid(9)]["failure_class"] == "unknown"
    # the format retry asked for -f b
    fmts = [a[a.index("-f") + 1] for a in fake.calls if a[-1].endswith(yid(5))]
    assert fmts[0] != "b" and fmts[1] == "b"
    # the failed check keeps its work file for a look
    assert (tmp_path / "work" / str(lib) / f"v-{yid(7)}" / f"v-{yid(7)}.mp4").exists()
    tot = {
        k: sum(summary_of(j)[k] for j in jobs) for k in ("downloaded", "skipped", "gone", "failed")
    }
    assert tuple(tot.values()) == (2, 4, 1, 2)


@pytest.mark.asyncio
async def test_network_errors_give_up_after_five_tries(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib)
    fake.downloads[yid(1)] = ["network"]
    fake.downloads[yid(2)] = ["ok"]
    await want(db, lib, sid, [yid(1), yid(2)])
    rt = await setup_runtime(db, [])
    jobs = await run_all(db, rt, lib)
    got = await rows(db, lib)
    assert fake.tries[yid(1)] == 5
    assert got[yid(1)]["state"] == "failed" and got[yid(1)]["failure_class"] == "transient"
    assert got[yid(2)]["state"] == "done" and all(j["state"] == "done" for j in jobs)


@pytest.mark.asyncio
async def test_bot_check_parks_the_job_and_keeps_the_item(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib)
    fake.downloads[yid(2)] = ["bot_check"]
    await want(db, lib, sid, [yid(1), yid(2)])
    rt = await setup_runtime(db, [])
    job = await run_stage(db, rt, "youtube_fetch", "library", lib)
    assert job["state"] == "pending" and job["parked_on"] == "dependency:youtube"
    assert error_of(job)["kind"] == "youtube_bot_check"
    got = await rows(db, lib)
    assert got[yid(2)]["state"] == "wanted" and got[yid(2)]["failure_class"] == "bot_check"
    assert got[yid(1)]["state"] == "wanted"  # never tried: the job stopped first
    src = await db.fetchrow("SELECT status FROM youtube_sources WHERE id = $1", str(sid))
    assert src["status"] == "bot_check"


@pytest.mark.asyncio
async def test_rate_limit_parks_for_an_hour(db: Database, tmp_path: Path, fake: FakeYtDlp) -> None:
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib)
    fake.downloads[yid(1)] = ["rate_limited"]
    await want(db, lib, sid, [yid(1)])
    rt = await setup_runtime(db, [])
    job = await run_stage(db, rt, "youtube_fetch", "library", lib)
    assert job["state"] == "pending" and job["parked_on"] == "resource:youtube_rate_limit"
    row = (await rows(db, lib))[yid(1)]
    wait = parse_ts(row["not_before"])
    assert row["state"] == "wanted" and wait is not None
    assert wait - datetime.now(UTC) > timedelta(minutes=55)

    # the watchers release it once the hour has passed, not before
    graph = {s.name: s for s in GRAPH}

    async def no_probes(db: Any, graph: Any, cfg: Any, at: datetime) -> dict[str, Any]:
        from maktaba_pipeline.engine.watchers import probe_all

        out = await probe_all(db, graph, cfg, at)
        return {k: v for k, v in out.items() if k.startswith("resource:youtube")}

    soon = await watch_once(
        db, graph, at=datetime.now(UTC) + timedelta(minutes=10), probes=no_probes
    )
    assert "resource:youtube_rate_limit" not in soon.released
    later = await watch_once(
        db, graph, at=datetime.now(UTC) + timedelta(minutes=61), probes=no_probes
    )
    assert later.released.get("resource:youtube_rate_limit") == 1


@pytest.mark.asyncio
async def test_three_failures_in_a_row_stop_the_job(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib)
    ids = [yid(n) for n in range(1, 7)]
    for i in ids:
        fake.downloads[i] = ["unknown"]
    await want(db, lib, sid, ids)
    rt = await setup_runtime(db, [])
    job = await run_stage(db, rt, "youtube_fetch", "library", lib)
    assert job["state"] == "failed" and error_of(job)["kind"] == "youtube_unknown"
    states = sorted(r["state"] for r in (await rows(db, lib)).values())
    assert states == ["failed"] * 3 + ["wanted"] * 3  # a broken yt-dlp is one failure


@pytest.mark.asyncio
async def test_cancel_kills_the_download_and_keeps_the_item(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib)
    fake.downloads[yid(1)] = ["stop"]
    await want(db, lib, sid, [yid(1)])

    async def cancel(_yid: str) -> None:
        await db.execute(
            "UPDATE processing_jobs SET cancel_requested = $1 WHERE stage = 'youtube_fetch'",
            True if is_pg(db) else 1,
        )

    fake.before_download = cancel
    rt = await setup_runtime(db, [])
    job = await run_stage(db, rt, "youtube_fetch", "library", lib)
    assert job["state"] == "cancelled"
    row = (await rows(db, lib))[yid(1)]
    assert row["state"] == "wanted" and row["attempts"] == 0


@pytest.mark.asyncio
async def test_second_refresh_downloads_nothing(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    await add_source(db, lib, added_at=datetime(2020, 1, 1, tzinfo=UTC))
    fake.listings[VIDEOS] = [
        entry(yid(2), upload_date="20240702"),
        entry(yid(1), upload_date="20240701"),
    ]
    rt = await setup_runtime(db, [])
    await run_stage(db, rt, "youtube_sync", "library", lib)
    fetch = await live_job(db, "youtube_fetch")
    assert fetch is not None
    await run_all(db, rt, lib)
    assert sorted(fake.downloaded) == [yid(1), yid(2)]

    fake.listed.clear()
    again = await run_stage(db, rt, "youtube_sync", "library", lib)
    assert again["state"] == "done", error_of(again)
    # the refresh stopped at the first id it knew, on every tab, and queued nothing
    assert all(brk for _, brk in fake.listed)
    assert summary_of(again)["youtube_sources"]["listed"] == 0
    assert await live_job(db, "youtube_fetch") is None
    assert sorted(fake.downloaded) == [yid(1), yid(2)]

    # a new upload: only it is listed and fetched
    head = fake.listings[VIDEOS]
    assert isinstance(head, list)
    fake.listings[VIDEOS] = [entry(yid(3), upload_date="20240703"), *head]
    third = await run_stage(db, rt, "youtube_sync", "library", lib)
    assert summary_of(third)["youtube_sources"]["new"] == 1
    fetch = await live_job(db, "youtube_fetch")
    assert fetch is not None
    await run_all(db, rt, lib)
    assert sorted(fake.downloaded) == [yid(1), yid(2), yid(3)]


@pytest.mark.asyncio
async def test_listing_bot_check_parks_the_sync(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib)
    fake.listings[VIDEOS] = "bot_check"
    rt = await setup_runtime(db, [])
    job = await run_stage(db, rt, "youtube_sync", "library", lib)
    assert job["state"] == "pending" and job["parked_on"] == "dependency:youtube"
    src = await db.fetchrow("SELECT status FROM youtube_sources WHERE id = $1", str(sid))
    assert src["status"] == "bot_check"


@pytest.mark.asyncio
async def test_existing_channels_get_an_off_source(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    from maktaba_pipeline.library_mgmt.youtube_sources import ensure_channel_sources

    lib = await make_lib(db, tmp_path)
    await db.execute(
        "INSERT INTO youtube_channels (channel_id, library_id, url) VALUES ($1,$2,$3)",
        CHANNEL,
        str(lib),
        CHANNEL_URL,
    )
    assert await ensure_channel_sources(db, lib) == 1
    assert await ensure_channel_sources(db, lib) == 0
    row = await db.fetchrow(
        "SELECT mode, kind FROM youtube_sources WHERE library_id = $1", str(lib)
    )
    assert (row["mode"], row["kind"]) == ("off", "channel")


# ------------------------------------------------------------------ the schedule


@pytest.mark.asyncio
async def test_refresh_schedule_reads_the_library_setting(db: Database, tmp_path: Path) -> None:
    graph = {s.name: s for s in GRAPH}
    await setup_runtime(db, [])
    daily = await make_lib(db, tmp_path, {"youtube": {"refresh_every_hours": 24}})
    await add_source(db, daily)
    manual = await make_lib(db, tmp_path, {"youtube": {"refresh_every_hours": 0}})
    await add_source(db, manual)
    off = await make_lib(db, tmp_path)
    await add_source(db, off, mode="off")  # nothing on, no channels: no refresh
    rep = await reconcile_once(db, graph)
    assert rep.yt_refresh == 1
    job = await live_job(db, "youtube_sync")
    assert str(job["subject_id"]) == str(daily) and job["priority"] == 110
    again = await reconcile_once(db, graph)
    assert again.yt_refresh == 0


# ------------------------------------------------------------------ real media / real yt-dlp


@pytest.mark.asyncio
async def test_real_verification_of_a_download(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    from tests.library_mgmt.media_fixtures import mp4_moov_at_end, needs_ffmpeg

    if needs_ffmpeg.args[0]:
        pytest.skip("needs ffmpeg")
    drain.set_verifier(None)
    fake.media_source = mp4_moov_at_end(tmp_path / "src.mp4", seconds=3.0)
    fake.meta[yid(1)] = ("Real", "20240701", 3)
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib, folder="Chan")
    await want(db, lib, sid, [yid(1)])
    rt = await setup_runtime(db, [])
    job = await run_stage(db, rt, "youtube_fetch", "library", lib)
    assert job["state"] == "done", error_of(job)
    row = (await rows(db, lib))[yid(1)]
    assert row["state"] == "done", row["error"]
    action = await db.fetchrow("SELECT verify FROM file_actions WHERE kind = 'youtube_fetch'")
    assert loads(action["verify"])["measured"]["faststart"] is True  # remuxed to the front


@pytest.mark.skipif(os.environ.get("MAKTABA_TEST_YTDLP") != "1", reason="real yt-dlp: opt in")
@pytest.mark.asyncio
async def test_real_ytdlp_smoke(
    db: Database, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Downloads a short Creative Commons video for real (``MAKTABA_TEST_YTDLP=1``)."""
    video = os.environ.get("MAKTABA_TEST_YTDLP_ID", "BaW_jenozKc")  # yt-dlp's own test video
    monkeypatch.setenv(drain.WORK_ENV, str(tmp_path / "work"))
    ytd.reset_runner()
    drain.set_verifier(None)
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib, folder="Smoke")
    await want(db, lib, sid, [video])
    rt = await setup_runtime(db, [])
    job = await run_stage(db, rt, "youtube_fetch", "library", lib)
    row = (await rows(db, lib))[video]
    assert job["state"] == "done" and row["state"] == "done", (error_of(job), row["error"])
    files = list((tmp_path / "lib" / "Smoke").iterdir())
    assert any(p.suffix == ".mp4" for p in files) and any(
        p.name.endswith(".info.json") for p in files
    )


@pytest.mark.asyncio
async def test_each_video_goes_on_to_convert_while_the_next_downloads(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    """Owner, 2026-10-28: download, convert, dedup, one item at a time, through the graph:
    video 1's convert job exists while video 2 is still downloading."""
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib)
    await want(db, lib, sid, [yid(1), yid(2)])  # newest first: yid(2), then yid(1)
    seen: dict[str, int] = {}

    async def during(y: str) -> None:
        seen[y] = int(
            await db.fetchval(
                "SELECT count(*) FROM processing_jobs WHERE stage = 'convert_mp4'"
                " AND state = 'pending' AND subject_kind = 'video'"
            )
        )

    fake.before_download = during
    rt = await setup_runtime(db, [])
    await run_all(db, rt, lib)
    assert fake.downloaded == [yid(2), yid(1)]
    assert seen == {yid(2): 0, yid(1): 1}  # the first one's convert waits while yid(1) downloads
    got = await rows(db, lib)
    converts = await db.fetch(
        "SELECT subject_id FROM processing_jobs WHERE stage = 'convert_mp4' ORDER BY id"
    )
    assert [str(r["subject_id"]) for r in converts] == [
        str(got[yid(2)]["video_id"]),
        str(got[yid(1)]["video_id"]),
    ]
    # the second hop (convert_mp4 -> dedup_scan) is the dedup track's edge (PR #421):
    # checked here once the graph has it
    by = {s.name: s for s in GRAPH}
    if any(e.to == "dedup_scan" for e in by["convert_mp4"].edges):
        from maktaba_pipeline.engine.propagate import resolve_edge

        edge = next(e for e in by["convert_mp4"].edges if e.to == "dedup_scan")
        job = await claim_for_pool(db, worker_id="w1", pool="convert", stages=["convert_mp4"])
        assert job is not None
        targets = await resolve_edge(db, edge, job, [])
        assert ("library", lib) in targets or any(k == "video" for k, _ in targets)
