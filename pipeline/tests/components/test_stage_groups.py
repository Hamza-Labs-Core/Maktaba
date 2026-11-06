"""A component's stage group stops without losing work (Epic 51, story 51.06): the gates,
the selection and the stage rows (T01, T02), the enqueue rules (T03, T04), the skipped
edges, and the drain of this worker's jobs (T05-T07)."""

from __future__ import annotations

import asyncio
import dataclasses
from typing import Any

import pytest

from maktaba_pipeline.cluster import stages as cluster_stages
from maktaba_pipeline.cluster.stages import select_stages
from maktaba_pipeline.components.catalog import default
from maktaba_pipeline.components.stages import (
    HELD,
    OFF,
    ON,
    gates,
    home_reasons,
    machine_reasons,
)
from maktaba_pipeline.components.store import Desired, Hold
from maktaba_pipeline.engine.claim import row_to_job
from maktaba_pipeline.engine.enqueue import enqueue_job, merge_payload
from maktaba_pipeline.engine.graph import CATALOG_ID, GRAPH
from maktaba_pipeline.engine.propagate import SKIPPED_COMPONENT_OFF, enqueue_dependents
from maktaba_pipeline.engine.sql import is_pg, loads, now
from maktaba_pipeline.engine.sync import apply_gates, stage_enabled_state, sync_graph
from maktaba_pipeline.pipeline.shutdown import drain_stages
from maktaba_pipeline.runtime import Database
from tests.components._helpers import set_stage, stage_row
from tests.engine.factory import make_library, make_video

WORKER = "w-test"
STAGES = {s.name: s for s in GRAPH}


def _d(cid: str, want: str = "enabled", *, hold: bool = False) -> Desired:
    return Desired(
        id=cid, want=want, generation=1, hold=Hold(by="admin:1", at=now()) if hold else None
    )


# -- gates ------------------------------------------------------------------------


def test_a_component_with_no_row_and_core_are_on() -> None:
    gs = gates({"core.pipeline": _d("core.pipeline", "absent")})
    assert gs["core.pipeline"].state == ON  # core is never switched
    assert gs["transcription"].state == ON  # no row: as it runs today
    assert machine_reasons(gs) == {}


def test_off_and_held_and_their_dependents() -> None:
    gs = gates({"transcription": _d("transcription", "installed")})
    assert gs["transcription"].state == OFF
    assert (gs["subtitles"].state, gs["subtitles"].reason) == (OFF, "dependency_off")
    assert gs["subtitles"].params == {"component": "transcription"}
    r = machine_reasons(gs)
    assert r["transcribe"] == "component_off:transcription"
    assert r["extract"] == "component_off:transcription"
    assert r["subtitle_gen"] == "component_off:subtitles"
    assert home_reasons(gs)["transcribe"] == "component_off"

    held = gates({"transcription": _d("transcription", hold=True)})
    assert held["transcription"].state == HELD
    assert held["subtitles"].state == HELD
    assert machine_reasons(held)["transcribe"] == "component_held:transcription"
    assert home_reasons(held)["subtitle_gen"] == "component_held"


def test_an_any_group_needs_one_and_an_asset_needs_only_installing() -> None:
    models = ("model.whisper-turbo", "model.whisper-large-v3", "model.whisper-medium")
    desired = {m: _d(m, "absent") for m in models}
    assert gates(desired)["transcription"].reason == "dependency_off"
    desired["model.whisper-medium"] = _d("model.whisper-medium", "installed")
    assert gates(desired)["transcription"].state == ON


# -- T01: the selection --------------------------------------------------------------


def test_t01_component_reason_sits_after_storage_and_before_switched_off(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setattr(cluster_stages, "_cannot_run", lambda _st, _h: None)
    st = dataclasses.replace(STAGES["transcribe"], pool="nas_read")
    graph = [st]
    comp = {"transcribe": "component_off:transcription"}
    served, off = select_stages(graph, components=comp)
    assert (served, off) == ([], {"transcribe": "component_off:transcription"})
    _, off = select_stages(graph, components=comp, storage={"read": "error"})
    assert off["transcribe"] == "storage"
    _, off = select_stages(graph, components=comp, switches={"transcribe": False})
    assert off["transcribe"] == "component_off:transcription"
    served, _ = select_stages(graph, components={})
    assert served == ["transcribe"]


# -- T02: the stage rows ---------------------------------------------------------------


def test_t02_stage_enabled_state_order() -> None:
    st = STAGES["transcribe"]
    assert stage_enabled_state(st, {}, "component_off") == (False, "component_off")
    assert stage_enabled_state(st, {}, "component_held") == (False, "component_held")
    assert stage_enabled_state(st, {}, None) == (True, None)
    unreleased = dataclasses.replace(st, unreleased=True)
    assert stage_enabled_state(unreleased, {}, "component_off") == (False, "not_configured")


@pytest.mark.asyncio
async def test_t02_gates_are_written_and_an_operator_row_is_kept(db: Database) -> None:
    await sync_graph(db, GRAPH, gates={})
    await set_stage(db, "youtube_fetch", False, "operator")
    off = {st: "component_off" for st in default().get("youtube").stages}  # type: ignore[union-attr]
    changed = await apply_gates(db, GRAPH, off)
    assert {c[0] for c in changed} == set(off) - {"youtube_fetch"}
    assert await stage_row(db, "youtube_sync") == (False, "component_off")
    assert await stage_row(db, "youtube_fetch") == (False, "operator")

    # a sync that does not know the gates leaves them alone
    await sync_graph(db, GRAPH)
    assert await stage_row(db, "youtube_sync") == (False, "component_off")

    # on again: the component's rows come back, the operator's stays off
    back = await apply_gates(db, GRAPH, {})
    assert {c[0] for c in back} == set(off) - {"youtube_fetch"}
    assert await stage_row(db, "youtube_sync") == (True, None)
    assert await stage_row(db, "youtube_fetch") == (False, "operator")

    # the graph sync with the gates known writes them too
    await sync_graph(db, GRAPH, gates={"youtube_sync": "component_held"})
    assert await stage_row(db, "youtube_sync") == (False, "component_held")
    await sync_graph(db, GRAPH, gates={})
    assert await stage_row(db, "youtube_sync") == (True, None)
    assert await stage_row(db, "youtube_fetch") == (False, "operator")


# -- T03, T04: enqueue -------------------------------------------------------------------


@pytest.mark.asyncio
async def test_t03_an_off_stage_makes_no_job(db: Database) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    await set_stage(db, "transcribe", False, "component_off")
    res = await enqueue_job(db, subject_kind="video", subject_id=v, stage="transcribe")
    assert (res.outcome, res.job_id) == ("skipped_component_off", None)
    assert await db.fetchval("SELECT count(*) FROM processing_jobs") == 0
    # an operator's switch keeps its own answer
    await set_stage(db, "transcribe", False, "operator")
    res = await enqueue_job(db, subject_kind="video", subject_id=v, stage="transcribe")
    assert res.outcome == "disabled"


@pytest.mark.asyncio
async def test_t04_a_held_stage_queues_and_the_job_waits(db: Database) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    await set_stage(db, "transcribe", False, "component_held")
    res = await enqueue_job(db, subject_kind="video", subject_id=v, stage="transcribe")
    assert res.outcome == "inserted" and res.job_id is not None
    row = await db.fetchrow("SELECT state FROM processing_jobs WHERE id = $1", res.job_id)
    assert row["state"] == "pending"


def test_payload_merge_unions_components() -> None:
    a = {"components": [{"component": "transcription", "off_since": None}]}
    b = {"components": [{"component": "classify", "off_since": "2026-11-01T00:00:00Z"}]}
    got = merge_payload(a, b)
    assert got is not None
    assert sorted(e["component"] for e in got["components"]) == ["classify", "transcription"]


@pytest.mark.asyncio
async def test_two_components_on_together_share_one_catchup_job(db: Database) -> None:
    first = await enqueue_job(
        db, subject_kind="catalog", subject_id=CATALOG_ID, stage="component_catchup",
        payload={"components": [{"component": "transcription", "off_since": None}]},
    )  # fmt: skip
    second = await enqueue_job(
        db, subject_kind="catalog", subject_id=CATALOG_ID, stage="component_catchup",
        payload={"components": [{"component": "classify", "off_since": None}]},
    )  # fmt: skip
    assert (second.outcome, second.job_id) == ("coalesced", first.job_id)
    row = await db.fetchrow("SELECT payload FROM processing_jobs WHERE id = $1", first.job_id)
    payload = loads(row["payload"])
    assert sorted(e["component"] for e in payload["components"]) == ["classify", "transcription"]


# -- skipped edges -----------------------------------------------------------------------


async def _done_job(db: Database, stage: str, subject: Any, outcome: str = "changed") -> Any:
    res = await enqueue_job(db, subject_kind="video", subject_id=subject, stage=stage)
    assert res.job_id is not None
    await db.execute(
        "UPDATE processing_jobs SET state = 'done', outcome = $2, finished_at = $3 WHERE id = $1",
        res.job_id,
        outcome,
        now() if is_pg(db) else now().isoformat(),
    )
    return await db.fetchrow("SELECT * FROM processing_jobs WHERE id = $1", res.job_id)


@pytest.mark.asyncio
async def test_an_edge_into_an_off_stage_is_skipped_and_a_held_one_queues(db: Database) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    up = await _done_job(db, "content_hash", v)
    await set_stage(db, "thumbnail", False, "component_off")
    trig = await enqueue_dependents(
        db, row_to_job(up), STAGES["content_hash"], STAGES, changed=True, only="thumbnail"
    )
    assert [t["outcome"] for t in trig] == [SKIPPED_COMPONENT_OFF]
    assert await db.fetchval("SELECT count(*) FROM processing_jobs WHERE stage = 'thumbnail'") == 0

    await set_stage(db, "thumbnail", False, "component_held")
    trig = await enqueue_dependents(
        db, row_to_job(up), STAGES["content_hash"], STAGES, changed=True, only="thumbnail"
    )
    assert [t["outcome"] for t in trig] == ["inserted"]


# -- T05-T07: the drain ----------------------------------------------------------------


async def _running(db: Database, stage: str, subject: Any, *, worker: str = WORKER) -> int:
    res = await enqueue_job(db, subject_kind="video", subject_id=subject, stage=stage)
    assert res.job_id is not None
    await db.execute(
        "UPDATE processing_jobs SET state = 'running', claimed_by = $2, attempts = 1,"
        " last_segment_end_sec = 42 WHERE id = $1",
        res.job_id,
        worker,
    )
    return res.job_id


async def _job(db: Database, job_id: int) -> Any:
    return await db.fetchrow(
        "SELECT state, paused_reason, attempts, last_segment_end_sec, paused_at_sec,"
        " pause_requested, claimed_by FROM processing_jobs WHERE id = $1",
        job_id,
    )


@pytest.mark.asyncio
async def test_t05_a_checkpointing_job_pauses_within_the_grace(db: Database) -> None:
    lib = await make_library(db)
    job_id = await _running(db, "transcribe", await make_video(db, lib))

    async def handler_checkpoints() -> None:
        # the stage sees the flag at its next segment and pauses itself
        while not (await _job(db, job_id))["pause_requested"]:
            await asyncio.sleep(0.01)
        await db.execute(
            "UPDATE processing_jobs SET state = 'paused', paused_reason = 'component_off',"
            " pause_requested = $2, claimed_by = NULL, paused_at_sec = last_segment_end_sec"
            " WHERE id = $1",
            job_id,
            False if is_pg(db) else 0,
        )

    helper = asyncio.create_task(handler_checkpoints())
    got = await drain_stages(
        db, worker_id=WORKER, stages={"transcribe", "extract"}, grace_sec=10, poll_sec=0.02
    )
    await helper
    assert got == {"cooperative": 1, "forced": 0}
    row = await _job(db, job_id)
    assert (row["state"], row["paused_reason"], row["attempts"]) == ("paused", "component_off", 1)
    assert float(row["last_segment_end_sec"]) == 42 and float(row["paused_at_sec"]) == 42


@pytest.mark.asyncio
async def test_t06_a_job_ignoring_the_flag_is_force_paused_and_cancelled(db: Database) -> None:
    lib = await make_library(db)
    job_id = await _running(db, "transcribe", await make_video(db, lib))
    cancelled: list[set[str]] = []

    def cancel(stages: Any) -> int:
        cancelled.append(set(stages))
        return 1

    got = await drain_stages(
        db, worker_id=WORKER, stages={"transcribe"}, grace_sec=0.3, poll_sec=0.05, cancel=cancel
    )
    assert got == {"cooperative": 0, "forced": 1}
    assert cancelled == [{"transcribe"}]
    row = await _job(db, job_id)
    assert (row["state"], row["paused_reason"], row["attempts"]) == ("paused", "component_off", 1)
    assert row["claimed_by"] is None and not row["pause_requested"]
    assert float(row["paused_at_sec"]) == 42
    n = await db.fetchval(
        "SELECT count(*) FROM processing_jobs WHERE state IN ('failed', 'cancelled')"
    )
    assert n == 0


@pytest.mark.asyncio
async def test_t07_other_stages_and_other_workers_are_untouched(db: Database) -> None:
    # one job of the drained stage on this worker ignores the flag, so the drain runs to its
    # deadline and force-pauses: neither the ask nor the force-pause may touch the others
    lib = await make_library(db)
    straggler = await _running(db, "transcribe", await make_video(db, lib))
    other_stage = await _running(db, "thumbnail", await make_video(db, lib))
    other_worker = await _running(db, "transcribe", await make_video(db, lib), worker="w-other")
    others = (other_stage, other_worker)
    flagged: list[int] = []

    async def watch_the_others() -> None:
        # the flag must never reach them, not only be clear at the end
        while True:
            for job_id in others:
                if (await _job(db, job_id))["pause_requested"]:
                    flagged.append(job_id)
            await asyncio.sleep(0.01)

    watcher = asyncio.create_task(watch_the_others())
    cancelled: list[set[str]] = []

    def cancel(stages: Any) -> int:
        cancelled.append(set(stages))
        return 1

    try:
        got = await drain_stages(
            db,
            worker_id=WORKER,
            stages={"transcribe"},
            grace_sec=0.3,
            poll_sec=0.02,
            cancel=cancel,
        )
    finally:
        watcher.cancel()
        with pytest.raises(asyncio.CancelledError):
            await watcher
    assert got == {"cooperative": 0, "forced": 1}
    assert cancelled == [{"transcribe"}]
    row = await _job(db, straggler)
    assert (row["state"], row["paused_reason"], row["claimed_by"]) == (
        "paused",
        "component_off",
        None,
    )
    assert flagged == []
    for job_id in others:
        row = await _job(db, job_id)
        assert (row["state"], bool(row["pause_requested"])) == ("running", False)
        assert row["paused_reason"] is None and row["paused_at_sec"] is None
        assert row["claimed_by"] == ("w-other" if job_id == other_worker else WORKER)


@pytest.mark.asyncio
async def test_a_drain_cancelled_by_the_gate_reopening_clears_the_flag(db: Database) -> None:
    lib = await make_library(db)
    job_id = await _running(db, "transcribe", await make_video(db, lib))
    task = asyncio.create_task(
        drain_stages(db, worker_id=WORKER, stages={"transcribe"}, grace_sec=30, poll_sec=0.02)
    )
    while not (await _job(db, job_id))["pause_requested"]:
        await asyncio.sleep(0.01)
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task
    row = await _job(db, job_id)
    assert (row["state"], bool(row["pause_requested"])) == ("running", False)


def test_safe_mode_keeps_all_but_the_safe_set_off(monkeypatch: pytest.MonkeyPatch) -> None:
    """Epic 53 story 53.05 T02b: in safe mode every component but the safe set is off for
    this boot (boot_flag), core.pipeline and transcription too; the desired state is
    only read."""
    from maktaba_pipeline.components import stages as stages_mod
    from maktaba_pipeline.components.store import Desired as D

    monkeypatch.setenv(stages_mod.ENV_SAFE_MODE, "1")
    desired = {"transcription": D(id="transcription", want="enabled", generation=3)}
    g = stages_mod.gates(desired)
    for cid in ("core.pipeline", "transcription", "core.streaming"):
        assert not g[cid].open and g[cid].reason == stages_mod.BOOT_FLAG, cid
        assert g[cid].params == {"flag": "--safe-mode"}
    assert g["transcription"].generation == 3 and g["transcription"].want == "enabled"
    assert g["core.api"].open
    monkeypatch.setenv(stages_mod.ENV_SAFE_MODE, "")
    assert stages_mod.gates(desired)["transcription"].open
