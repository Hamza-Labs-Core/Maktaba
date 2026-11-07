"""components-chaos (Epic 51, story 51.06, N6): random enable / disable / hold / start flips
of four stage-group components while 2,000 jobs run on real sleepy workers. At the end,
with everything on and the queue drained, every job is done: none failed or was cancelled,
and none needed a second attempt (a drained job's claim is not a new attempt).

    cd pipeline && uv run --extra dev pytest ../tests/components/test_chaos.py

``MAKTABA_CHAOS_FLIPS`` (100), ``MAKTABA_CHAOS_MIN_S`` (3) and ``MAKTABA_CHAOS_MAX_S`` (20)
shape the run; ``MAKTABA_CHAOS_SEED`` repeats one. A failure keeps the whole job history
(``components-chaos-jobs.json``) beside the workers' logs.
"""

from __future__ import annotations

import asyncio
import json
import os
import random
import time
from typing import Any

import pytest

from components import rig as R

pytestmark = pytest.mark.skipif(not R.PG_URL, reason="MAKTABA_TEST_PG_URL not set")

#: component -> the stage of it the workers serve, and that stage's subject kind
TARGETS: dict[str, tuple[str, str]] = {
    "transcription": ("transcribe", "video"),
    "media.thumbnails": ("thumb_small", "video"),
    "classify": ("detect", "library"),
    "search.semantic": ("index", "video"),
}
SERVED = tuple(st for st, _ in TARGETS.values()) + ("component_catchup",)
JOBS = 2000
FLIPS = int(os.environ.get("MAKTABA_CHAOS_FLIPS", "100"))
MIN_S = float(os.environ.get("MAKTABA_CHAOS_MIN_S", "3"))
MAX_S = float(os.environ.get("MAKTABA_CHAOS_MAX_S", "20"))
SEED = int(os.environ.get("MAKTABA_CHAOS_SEED", str(int(time.time()))))
ACTIONS = ("enable", "disable", "hold", "start")


def test_components_chaos() -> None:
    asyncio.run(_chaos())


async def _chaos() -> None:
    m: dict[str, Any] = {"seed": SEED, "flips": []}
    async with R.rig() as r:
        try:
            await _run(r, m)
        except BaseException:
            await r.dump_jobs("components-chaos")
            raise
        finally:
            R.LOGS.mkdir(parents=True, exist_ok=True)
            (R.LOGS / "components-chaos.json").write_text(json.dumps(m, indent=1), "utf-8")


async def _run(r: R.Rig, m: dict[str, Any]) -> None:
    rnd = random.Random(SEED)  # noqa: S311 - a repeatable test schedule
    per = JOBS // len(TARGETS)
    videos = await r.videos(per)
    libraries = await r.libraries(per)
    for stage, kind in TARGETS.values():
        await r.enqueue(stage, libraries if kind == "library" else videos, kind=kind)
    for i in (1, 2):
        r.start(f"chaos-{i}", SERVED, sleep=0.3)
    await r.wait_online(2)

    for n in range(FLIPS):
        await asyncio.sleep(rnd.uniform(MIN_S, MAX_S))
        cid = rnd.choice(list(TARGETS))
        action = rnd.choice(ACTIONS)
        if action == "disable":
            await r.want(cid, "installed")
        elif action == "hold":
            await r.want(cid, "enabled", hold=True)
        else:  # enable, start: on and not held
            await r.want(cid, "enabled")
        m["flips"].append({"n": n, "component": cid, "action": action})
        assert await r.lost() == 0, f"a job failed or was cancelled after flip {n}"
        for w in r.workers:
            assert w.proc.poll() is None, f"worker {w.worker_id} exited: see {w.log}"

    for cid in TARGETS:
        await r.want(cid, "enabled")
    took = await R.until(lambda: _drained(r), 900, step=1.0)
    m["drain_after_flips_s"] = took

    assert await r.lost() == 0
    m["max_attempts"] = int(await r.db.fetchval("SELECT max(attempts) FROM processing_jobs") or 0)
    assert m["max_attempts"] <= 1
    m["jobs"] = await r.states()


async def _drained(r: R.Rig) -> bool:
    n = await r.db.fetchval(
        "SELECT count(*) FROM processing_jobs WHERE stage = ANY($1::text[]) AND state <> 'done'",
        list(SERVED),
    )
    return int(n or 0) == 0
