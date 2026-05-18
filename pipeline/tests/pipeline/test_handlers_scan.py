"""Gap-closure (HLB-257/255): the real library-scoped SCAN adapter.

``handlers.scan_handler`` is the thin wrapper that turns a
library-scoped ``processing_jobs`` row (slot 0058: ``library_id`` set,
``video_id`` null) into a real bootstrap walk: discover files, UPSERT
``videos`` rows, enqueue a per-video PROBE for each new one, and flip
the job ``done``. The heavy logic lives in
:class:`maktaba_pipeline.scanner.Scanner` (exercised by
``tests/scanner/test_scanner.py``); these tests pin the *adapter +
SQL-store + enqueue* seams end to end against the canonical
``FakeAudioDB`` extended with a ``libraries`` table and a real
``tmp_path`` root.

Async work is driven via :func:`asyncio.run` rather than
``@pytest.mark.asyncio``, and these tests are intentionally NOT
``unit``-marked: ``asyncio.run`` opens the event loop's self-pipe
socketpair which the unit-tier netguard (Story 20.1 AC1) forbids — the
same caveat documented in ``tests/pipeline/test_handlers_probe.py`` and
``tests/db/test_jobs_enqueue.py`` and shared by every async DB test in
the suite.

Asserted outcomes (not internal calls):

- a SCAN job is claimable with null ``video_id`` + set ``library_id``;
- the scanner discovers files under the library's real root;
- ``videos`` rows are created and per-video PROBE jobs enqueued;
- a re-scan of an unchanged tree creates no duplicate videos and no
  duplicate PROBE jobs (idempotent);
- ``enqueue_scan`` enforces one live scan per library;
- the job transitions claimed → done.
"""

from __future__ import annotations

import asyncio
import dataclasses
from pathlib import Path
from typing import Any

from maktaba_pipeline.db.jobs import JobState, Stage, enqueue_scan
from maktaba_pipeline.db.jobs_claim import claim_one
from maktaba_pipeline.handlers import scan_handler
from maktaba_pipeline.scanner import ScanStore, SqlScanStore
from tests.audio._fake_audio_db import FakeAudioDB


def _store_factory(db: Any) -> ScanStore:
    """Build the production :class:`SqlScanStore` over the test fake.

    ``FakeAudioDB`` satisfies the store's ``_ScanDB`` protocol
    (transaction/fetchrow/execute); the ``Any`` param keeps the seam
    test-only and side-steps the ``DBConn`` (no ``execute``) vs.
    ``_ScanDB`` variance the production ``_default_scan_store`` handles
    via the runtime ``Database`` facade."""
    return SqlScanStore(db)


def _touch(root: Path, rel: str, *, content: bytes) -> None:
    p = root / rel
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_bytes(content)


def test_scan_job_claimable_with_null_video_id(tmp_path: Path) -> None:
    async def scenario() -> None:
        db = FakeAudioDB(dialect="postgres")
        lib_id = db.add_library(roots=[str(tmp_path)])

        res = await enqueue_scan(db, library_id=lib_id)
        assert res.outcome == "inserted"

        job = await claim_one(db, worker_id="w1", supported_stages=[Stage.SCAN])
        assert job is not None
        assert job.stage is Stage.SCAN
        assert job.video_id is None
        assert job.library_id == lib_id
        assert job.state is JobState.CLAIMED

    asyncio.run(scenario())


def test_scan_discovers_files_and_enqueues_per_video_probe(tmp_path: Path) -> None:
    async def scenario() -> None:
        db = FakeAudioDB(dialect="postgres")
        lib_id = db.add_library(roots=[str(tmp_path)])
        for i in range(3):
            _touch(tmp_path, f"v{i}.mp4", content=f"clip-{i}".encode())

        await enqueue_scan(db, library_id=lib_id)
        job = await claim_one(db, worker_id="w1", supported_stages=[Stage.SCAN])
        assert job is not None

        await scan_handler(db, job, make_store=_store_factory)

        vids = [v for v in db.videos.values() if v.library_id == lib_id]
        assert len(vids) == 3
        assert {Path(v.path).name for v in vids if v.path} == {
            "v0.mp4",
            "v1.mp4",
            "v2.mp4",
        }

        probes = [
            pj
            for pj in db.processing_jobs.values()
            if pj.stage == "probe" and pj.state == "pending"
        ]
        assert len(probes) == 3
        assert {pj.video_id for pj in probes} == {v.id for v in vids}

        scan_row = db.processing_jobs[job.id]
        assert scan_row.state == "done"
        assert scan_row.video_id is None
        assert scan_row.library_id == lib_id

    asyncio.run(scenario())


def test_rescan_is_idempotent_no_dup_videos_or_probes(tmp_path: Path) -> None:
    async def scenario() -> None:
        db = FakeAudioDB(dialect="postgres")
        lib_id = db.add_library(roots=[str(tmp_path)])
        _touch(tmp_path, "movie.mp4", content=b"unchanged-bytes")

        await enqueue_scan(db, library_id=lib_id)
        j1 = await claim_one(db, worker_id="w1", supported_stages=[Stage.SCAN])
        assert j1 is not None
        await scan_handler(db, j1, make_store=_store_factory)

        videos_after_first = len(db.videos)
        probes_after_first = sum(
            1 for pj in db.processing_jobs.values() if pj.stage == "probe"
        )
        assert videos_after_first == 1
        assert probes_after_first == 1

        await enqueue_scan(db, library_id=lib_id)
        j2 = await claim_one(db, worker_id="w1", supported_stages=[Stage.SCAN])
        assert j2 is not None
        await scan_handler(db, j2, make_store=_store_factory)

        # No duplicate video, no duplicate PROBE — the (size, mtime)
        # signature matched so the orchestrator skipped the rehash.
        assert len(db.videos) == videos_after_first
        assert (
            sum(1 for pj in db.processing_jobs.values() if pj.stage == "probe")
            == probes_after_first
        )
        assert db.processing_jobs[j2.id].state == "done"

    asyncio.run(scenario())


def test_enqueue_scan_one_live_per_library() -> None:
    async def scenario() -> None:
        db = FakeAudioDB(dialect="postgres")
        lib_id = db.add_library(roots=["/does/not/matter"])

        a = await enqueue_scan(db, library_id=lib_id)
        b = await enqueue_scan(db, library_id=lib_id)

        assert a.outcome == "inserted"
        assert b.outcome == "reused"
        assert a.id == b.id
        live = [
            pj
            for pj in db.processing_jobs.values()
            if pj.stage == "scan" and pj.library_id == lib_id
        ]
        assert len(live) == 1

        other = db.add_library(roots=["/other"])
        c = await enqueue_scan(db, library_id=other)
        assert c.outcome == "inserted"
        assert c.id != a.id

    asyncio.run(scenario())


def test_scan_missing_library_id_is_non_retryable(tmp_path: Path) -> None:
    async def scenario() -> None:
        db = FakeAudioDB(dialect="postgres")
        # A scan job with no library_id should never exist (slot 0058
        # CHECK rejects it), but the adapter defends against it:
        # terminal failure, not a retry — a re-run can't invent scope.
        lib_id = db.add_library(roots=[str(tmp_path)])
        await enqueue_scan(db, library_id=lib_id)
        job = await claim_one(db, worker_id="w1", supported_stages=[Stage.SCAN])
        assert job is not None
        bad_job = dataclasses.replace(job, library_id=None)

        await scan_handler(db, bad_job)

        row = db.processing_jobs[job.id]
        assert row.state == "failed"
        assert row.error is not None and "scan_missing_library" in row.error

    asyncio.run(scenario())
