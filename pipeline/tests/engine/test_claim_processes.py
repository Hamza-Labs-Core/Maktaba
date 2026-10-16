"""A global pool holds across real processes (plan 06-11 section 13, Postgres only)."""

from __future__ import annotations

import asyncio
import json
import os
import sys
from pathlib import Path

import pytest

from maktaba_pipeline.engine.enqueue import enqueue_job
from tests.engine.factory import make_library, make_video

SRC = str(Path(__file__).resolve().parents[2] / "src")

WORKER = """
import asyncio, json, sys, time
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.engine.claim import claim_for_pool

async def main(url, wid, start_at):
    db = await Database.connect(url)
    while time.time() < start_at:
        await asyncio.sleep(0.005)
    got = []
    for _ in range(8):
        job = await claim_for_pool(db, worker_id=wid, pool="nas_read", stages=["probe"])
        if job is not None:
            got.append(job.id)
    await db.close()
    print(json.dumps(got))

asyncio.run(main(sys.argv[1], sys.argv[2], float(sys.argv[3])))
"""


@pytest.mark.integration
@pytest.mark.asyncio
async def test_three_worker_processes_never_exceed_the_global_pool_size(db) -> None:
    if db.dialect != "postgres":
        pytest.skip("real processes need a shared server")
    lib = await make_library(db)
    for _ in range(10):
        v = await make_video(db, lib)
        await enqueue_job(db, subject_kind="video", subject_id=v, stage="probe")
    assert await db.fetchval("SELECT size FROM worker_pools WHERE pool = 'nas_read'") == 2

    import time

    start = time.time() + 3.0
    env = {**os.environ, "PYTHONPATH": SRC}
    procs = [
        await asyncio.create_subprocess_exec(
            sys.executable, "-c", WORKER, db._dsn, f"host{i}/{i}", str(start),
            stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE, env=env,
        )
        for i in range(3)
    ]  # fmt: skip
    outs = await asyncio.gather(*[p.communicate() for p in procs])
    for p, (_, err) in zip(procs, outs, strict=True):
        assert p.returncode == 0, err.decode()[-2000:]
    claimed = [json.loads(o[0].decode().strip().splitlines()[-1]) for o in outs]
    flat = [j for c in claimed for j in c]
    assert len(flat) == len(set(flat)) == 2  # exactly the pool size, no job twice
    busy = await db.fetchval("SELECT count(*) FROM processing_jobs WHERE state = 'claimed'")
    assert busy == 2
