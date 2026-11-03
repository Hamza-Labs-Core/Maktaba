"""components-live (Epic 51, story 51.06, README section 11): real sleepy workers on Postgres,
a component switched off, held and on again, and the numbers N1, N2, N4 and N5 measured.

    cd pipeline && uv run --extra dev pytest ../tests/components/test_live.py

N3 (a process component) is the supervisor's (51.04): its scenario joins this job there.
The measurements go to ``components-live.json`` beside the workers' logs.
"""

from __future__ import annotations

import asyncio
import json
from typing import Any

import pytest

from tests.components import rig as R

pytestmark = pytest.mark.skipif(not R.PG_URL, reason="MAKTABA_TEST_PG_URL not set")

STAGES = ("content_hash", "transcribe", "component_catchup")
#: the catalog's drain grace of transcription (catalog.toml)
TRANSCRIPTION_GRACE_S = 120


async def _in_flight(r: R.Rig, stage: str) -> int:
    s = await r.states(stage)
    return sum(v for k, v in s.items() if k in ("claimed", "running", "resuming"))


async def _for_subjects(r: R.Rig, stage: str, subjects: list[str]) -> int:
    return int(
        await r.db.fetchval(
            "SELECT count(*) FROM processing_jobs"
            " WHERE stage = $1 AND subject_id = ANY($2::uuid[])",
            stage,
            subjects,
        )
    )


def _scenario(metrics: dict[str, Any]) -> None:
    R.LOGS.mkdir(parents=True, exist_ok=True)
    (R.LOGS / "components-live.json").write_text(json.dumps(metrics, indent=1), encoding="utf-8")


def test_components_live() -> None:
    asyncio.run(_live())


async def _live() -> None:
    m: dict[str, Any] = {}
    async with R.rig() as r:
        try:
            await _run(r, m)
        except BaseException:
            await r.dump_jobs("components-live")
            raise
        finally:
            _scenario(m)


async def _run(r: R.Rig, m: dict[str, Any]) -> None:
    queued = await r.videos(300)
    await r.enqueue("transcribe", queued)
    for i in (1, 2):
        r.start(f"live-{i}", STAGES, sleep=1.0)
    await r.wait_online(2)
    await R.until(lambda: _running(r, "transcribe", 2), 60)

    # -- disable: N4 (stop claiming), N5 (drain), nothing lost ----------------------
    before = await r.states("transcribe")
    t_off = await r.want("transcription", "installed")
    rows_s = await R.until(lambda: _is(r.stage_row("transcribe"), (False, "component_off")), 10)
    m["N4_rows_s"] = rows_s
    drained = await R.until(lambda: _zero(_in_flight(r, "transcribe")), TRANSCRIPTION_GRACE_S + 5)
    m["N5_drain_s"] = drained
    assert drained <= TRANSCRIPTION_GRACE_S + 2
    await asyncio.sleep(3)
    late = await r.claims_after(["transcribe"], t_off + 2)
    m["N4_claims_after_2s"] = late
    assert late == 0
    after = await r.states("transcribe")
    # every job is still there: done (claimed before the stop, or finished within the
    # grace), paused (drained) or queued as it was
    assert sum(after.values()) == sum(before.values())
    assert set(after) <= {"pending", "paused", "done"}
    m["queued_after_disable"] = after
    assert await r.lost() == 0

    # -- edges into it while off are skipped --------------------------------------
    fresh = await r.videos(50)
    await r.enqueue("content_hash", fresh)
    await R.until(lambda: _done(r, "content_hash", fresh), 120)
    assert await _for_subjects(r, "transcribe", fresh) == 0

    # -- enable: N2 (claimable again), the catch-up queues exactly the skipped ---------
    t_on = await r.want("transcription", "enabled")
    n2 = await R.until(lambda: _claimed_after(r, "transcribe", t_on), 30)
    m["N2_enable_to_claim_s"] = n2
    assert n2 <= 5
    await R.until(lambda: _done_stage(r, "component_catchup"), 60)
    m["catchup_queued"] = await _for_subjects(r, "transcribe", fresh)
    assert m["catchup_queued"] == len(fresh)

    # -- hold: new jobs queue and wait, none is claimed -----------------------------
    t_hold = await r.want("transcription", "enabled", hold=True)
    await R.until(lambda: _is(r.stage_row("transcribe"), (False, "component_held")), 10)
    await R.until(lambda: _zero(_in_flight(r, "transcribe")), TRANSCRIPTION_GRACE_S + 5)
    held = await r.videos(10)
    got = await r.enqueue("transcribe", held)
    assert got.get("inserted") == len(held)
    await asyncio.sleep(3)
    assert await r.claims_after(["transcribe"], t_hold + 2) == 0
    await r.want("transcription", "enabled")
    await R.until(lambda: _done(r, "transcribe", held), 120)

    assert await r.lost() == 0
    attempts = await r.db.fetchval("SELECT max(attempts) FROM processing_jobs")
    m["max_attempts"] = int(attempts or 0)
    assert m["max_attempts"] <= 1


async def _is(coro: Any, want: Any) -> bool:
    return bool(await coro == want)


async def _zero(coro: Any) -> bool:
    return bool(await coro == 0)


async def _running(r: R.Rig, stage: str, n: int) -> bool:
    return await _in_flight(r, stage) >= n


async def _claimed_after(r: R.Rig, stage: str, at: float) -> bool:
    return await r.claims_after([stage], at) > 0


async def _done(r: R.Rig, stage: str, subjects: list[str]) -> bool:
    n = await r.db.fetchval(
        "SELECT count(*) FROM processing_jobs WHERE stage = $1 AND subject_id = ANY($2::uuid[])"
        " AND state = 'done'",
        stage,
        subjects,
    )
    return int(n or 0) >= len(subjects)


async def _done_stage(r: R.Rig, stage: str) -> bool:
    s = await r.states(stage)
    return s.get("done", 0) > 0 and not any(
        s.get(k, 0) for k in ("pending", "claimed", "running", "resuming", "paused")
    )
