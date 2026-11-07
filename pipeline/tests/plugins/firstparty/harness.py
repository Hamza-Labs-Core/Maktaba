"""The parity proof's harness (Epic 54, story 54.26 §2).

Two copies of one seeded database: A runs the stages in-process (``impl = inproc``), B
through their first-party plugin (``impl = plugin``): the worker's real native path, the
runner's real server, and the real plugin API (``api/tools/plugin-api-dev``: the Go
statements executor, the plugin's Postgres role and its tokens) on B. Then
:meth:`Parity.compare` reads both and names every difference
(:mod:`maktaba_plugin_sdk.parity`).

The runner runs in the test's process without its OS sandbox (the sandbox has its own
tests, story 54.23 T01); everything between the runner and Postgres is the release's.
"""

from __future__ import annotations

import asyncio
import contextlib
import os
import shutil
import subprocess
import tempfile
import time
import uuid
from collections.abc import AsyncIterator, Awaitable, Callable, Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from maktaba_plugin_sdk.parity import JOB_COLUMNS, diff_rows, perf_check

from maktaba_pipeline.engine.dispatch import EngineRuntime, dispatch_job
from maktaba_pipeline.engine.graph import StageDef, bound_graph
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.runtime import Database

ROOT = Path(__file__).resolve().parents[4]
API_DIR = ROOT / "api"

#: the plugin path's fixed cost of one job that N16 does not count (README §15.12)
PERF_FLOOR_S = 0.025

Seed = Callable[[Database], Awaitable[None]]


def plugin_api_dev() -> str | None:
    """The dev API binary (``MAKTABA_PLUGIN_API_DEV``), built once with go when unset."""
    explicit = os.environ.get("MAKTABA_PLUGIN_API_DEV", "").strip()
    if explicit:
        return explicit
    go = shutil.which("go")
    if go is None:
        return None
    out = Path(tempfile.gettempdir()) / "maktaba-plugin-api-dev"
    if not out.exists():
        subprocess.run(
            [go, "build", "-o", str(out), "./tools/plugin-api-dev"], cwd=API_DIR, check=True
        )
    return str(out)


#: the keys of the queue's JSON columns that hold when something happened (an enqueue's
#: reason): two runs a moment apart differ there and nowhere else
_TIME_KEYS = frozenset({"at", "ts", "enqueued_at", "started_at", "finished_at"})


def _untimed(v: Any) -> Any:
    import json

    if isinstance(v, str) and v[:1] in "[{":
        try:
            return _untimed(json.loads(v))
        except ValueError:
            return v
    if isinstance(v, dict):
        return {k: ("<time>" if k in _TIME_KEYS else _untimed(x)) for k, x in v.items()}
    if isinstance(v, list):
        return [_untimed(x) for x in v]
    return v


def _swap_db(url: str, name: str) -> str:
    base, _, _ = url.rpartition("/")
    return f"{base}/{name}"


@dataclass
class Side:
    db: Database
    times: dict[str, list[float]] = field(default_factory=dict)
    ran: list[str] = field(default_factory=list)


@dataclass
class Parity:
    a: Side
    b: Side
    graph: dict[str, StageDef]
    tmp: Path

    async def drain(self, side: Side, *, limit: int = 500) -> None:
        """Run every claimable job of the graph on one side, timing each."""
        from maktaba_pipeline.engine.claim import claim_for_pool

        rt = EngineRuntime(graph=self.graph)
        pools = sorted({s.pool for s in self.graph.values()})
        for _ in range(limit):
            job = None
            for pool in pools:
                stages = [s.name for s in self.graph.values() if s.pool == pool and not s.legacy]
                job = await claim_for_pool(
                    side.db, worker_id="parity", pool=pool, stages=stages, scope="per_worker"
                )
                if job is not None:
                    break
            if job is None:
                # a debounce or back-off window elapses once nothing else is claimable,
                # as tests.stages.world.drain_core does
                waiting = await side.db.fetchval(
                    "SELECT count(*) FROM processing_jobs WHERE state = 'pending'"
                    " AND not_before IS NOT NULL AND parked_on IS NULL"
                )
                if not waiting:
                    return
                await side.db.execute(
                    "UPDATE processing_jobs SET not_before = NULL WHERE state = 'pending'"
                    " AND parked_on IS NULL"
                )
                continue
            t0 = time.perf_counter()
            await dispatch_job(side.db, job, runtime=rt, legacy_handlers={})
            side.times.setdefault(job.stage, []).append(time.perf_counter() - t0)
            side.ran.append(job.stage)
        raise AssertionError(f"parity: no quiet after {limit} jobs")

    async def run(self) -> None:
        await self.drain(self.a)
        await self.drain(self.b)

    async def bench(self, stages: Sequence[str], *, rounds: int = 7) -> None:
        """N16's samples: each stage's job run again ``rounds`` times on each side, A and B
        taking turns (a busy machine slows both alike), forced so it does its work. The
        samples replace the drain's one run per job for the comparison."""
        from maktaba_pipeline.engine.claim import claim_for_pool
        from maktaba_pipeline.engine.enqueue import enqueue_job

        rt = EngineRuntime(graph=self.graph)
        for side in (self.a, self.b):
            for st in stages:
                side.times[st] = []
        for st in stages:
            row = await self.a.db.fetchrow(
                "SELECT subject_kind, subject_id FROM processing_jobs WHERE stage = $1"
                " AND state = 'done' ORDER BY id LIMIT 1",
                st,
            )
            if row is None:
                continue
            for _ in range(rounds):
                for side in (self.a, self.b):
                    await enqueue_job(
                        side.db,
                        subject_kind=row["subject_kind"],
                        subject_id=row["subject_id"],
                        stage=st,
                        force=True,
                    )
                    await side.db.execute(
                        "UPDATE processing_jobs SET not_before = NULL WHERE stage = $1"
                        " AND state = 'pending'",
                        st,
                    )
                    pool = self.graph[st].pool
                    job = await claim_for_pool(
                        side.db, worker_id="parity", pool=pool, stages=[st], scope="per_worker"
                    )
                    if job is None:
                        continue
                    t0 = time.perf_counter()
                    await dispatch_job(side.db, job, runtime=rt, legacy_handlers={})
                    side.times[st].append(time.perf_counter() - t0)

    async def compare(
        self,
        tables: Sequence[tuple[str, Sequence[str]]],
        *,
        ignore: dict[str, Sequence[str]] | None = None,
        stages: Sequence[str] = (),
        perf: bool = True,
    ) -> list[str]:
        """Every difference: the plugin's tables (``(table, key columns)``), the queue's
        record of each job, its log, and the job times of ``stages`` (N16)."""
        ignore = ignore or {}
        out: list[str] = []
        for table, key in tables:
            ra = [dict(r.items()) for r in await self.a.db.fetch(f"SELECT * FROM {table}")]  # noqa: S608
            rb = [dict(r.items()) for r in await self.b.db.fetch(f"SELECT * FROM {table}")]  # noqa: S608
            out += diff_rows(table, ra, rb, key, ignore.get(table, ()))
        cols = ", ".join(JOB_COLUMNS)
        q = f"SELECT {cols} FROM processing_jobs ORDER BY id"  # noqa: S608
        ja, jb = await self.a.db.fetch(q), await self.b.db.fetch(q)

        def keyed(rows: Sequence[Any]) -> list[dict[str, Any]]:
            seen: dict[tuple[Any, ...], int] = {}
            res = []
            for r in rows:
                d = {c: _untimed(v) for c, v in r.items()}
                k = (d["stage"], d["subject_kind"], str(d["subject_id"]))
                seen[k] = seen.get(k, 0) + 1
                d["n"] = seen[k]
                res.append(d)
            return res

        out += diff_rows(
            "processing_jobs", keyed(ja), keyed(jb), ("stage", "subject_kind", "subject_id", "n")
        )
        q = (
            "SELECT j.stage, e.kind, e.level, e.message FROM job_events e JOIN processing_jobs j ON j.id = e.job_id"  # noqa: E501
            " WHERE e.kind NOT IN ('claimed') ORDER BY e.id"
        )
        ea, eb = await self.a.db.fetch(q), await self.b.db.fetch(q)

        def counted(rows: Sequence[Any]) -> list[dict[str, Any]]:
            seen: dict[tuple[Any, ...], int] = {}
            res = []
            for r in rows:
                d = dict(r.items())
                k = tuple(d.values())
                seen[k] = seen.get(k, 0) + 1
                res.append({**d, "n": seen[k]})
            return res

        out += diff_rows(
            "job_events", counted(ea), counted(eb), ("stage", "kind", "level", "message", "n")
        )
        if perf:
            for s in stages:
                # N16 counts the job's own work: a difference under PERF_FLOOR_S is the plugin
                # path's fixed cost of a job (its token, the hop to the runner), README §15.12
                out += perf_check(
                    s, self.a.times.get(s, []), self.b.times.get(s, []), floor_s=PERF_FLOOR_S
                )
        return out

    async def fingerprints(self, stage: str, subjects: Sequence[Any]) -> list[str]:
        """Input fingerprints in-process on A against the fp runner's on B."""
        from maktaba_pipeline.engine.stages.registry import get_impl
        from maktaba_pipeline.plugins import native

        impl = get_impl(stage)
        assert impl is not None and impl.input_fingerprint is not None
        a = [await impl.input_fingerprint(self.a.db, s) for s in subjects]
        b = await native.fingerprints(stage, "input", list(subjects), group="light")
        return [
            f"fingerprint {stage} {s.kind} {s.id}: in-process {x} != plugin {y}"
            for s, x, y in zip(subjects, a, b, strict=True)
            if x != y
        ]


@contextlib.asynccontextmanager
async def parity(
    pg_url: str,
    template: str,
    *,
    bundle: bytes,
    stages: Sequence[str],
    extra: Sequence[StageDef] = (),
    setup_sql: Sequence[str] = (),
    roles_sql: str = "",
    seed: Seed,
    group: str = "light",
    monkeypatch: Any,
) -> AsyncIterator[Parity]:
    import asyncpg  # type: ignore[import-untyped]

    from maktaba_pipeline.plugins import firstparty, native
    from maktaba_plugin_runner.server import Runner

    binary = plugin_api_dev()
    if binary is None:
        import pytest

        pytest.skip("go is needed for the plugin API's dev server")
    names = ["maktaba_pa_" + uuid.uuid4().hex[:8], "maktaba_pb_" + uuid.uuid4().hex[:8]]
    admin = await asyncpg.connect(pg_url)
    for n in names:
        await admin.execute(f'CREATE DATABASE "{n}" TEMPLATE "{template}"')
    await admin.close()
    tmp = Path(tempfile.mkdtemp(prefix="mkp-", dir="/tmp"))
    proc: subprocess.Popen[str] | None = None
    task: asyncio.Task[None] | None = None
    sides: list[Database] = []
    try:
        for n in names:
            d = await Database.connect(_swap_db(pg_url, n))
            sides.append(d)
            for sql in setup_sql:
                await d.execute(sql)
        a, b = sides
        if roles_sql:
            await b.execute(roles_sql)
        graph = {s.name: s for s in bound_graph()}
        for s in extra:
            graph[s.name] = s
        for d in sides:
            await sync_graph(d, list(graph.values()))
            await seed(d)
        # A is the in-process side: its graph sync flips first-party stages to their plugin
        # (the flip at update), so they are set back by hand; B runs them as plugins
        await a.execute(
            "UPDATE job_stages SET impl = 'inproc', impl_changed_at = now(),"
            " impl_changed_by = 'parity'"
        )
        for name in stages:
            await b.execute("UPDATE job_stages SET impl = 'plugin' WHERE stage = $1", name)
        (tmp / "bundle.json").write_bytes(bundle)
        api_sock = tmp / "api.sock"
        proc = subprocess.Popen(
            [
                binary,
                "-dsn",
                _swap_db(pg_url, names[1]) + ("&" if "?" in pg_url else "?") + "sslmode=disable",
                "-socket",
                str(api_sock),
                "-bundle",
                str(tmp / "bundle.json"),
            ],
            stdout=subprocess.PIPE,
            text=True,
        )
        assert proc.stdout is not None
        line = await asyncio.to_thread(proc.stdout.readline)
        assert line.startswith("ready "), line
        monkeypatch.setenv("MAKTABA_API_URL", "http://" + line.split()[1])
        run_dir = tmp / "runners"
        monkeypatch.setenv("MAKTABA_RUNNER_DIR", str(run_dir))
        served = {s: (firstparty.impl_entry(s) or "") for s in stages}
        runner = Runner(
            group=group,
            api_socket=str(api_sock),
            stages=served,
            plugin_of={s: firstparty.by_stage(s).id for s in stages},  # type: ignore[union-attr]
            bundle=firstparty.bundle_digest(),
        )
        sock = run_dir / f"runner-{group}.sock"
        task = asyncio.create_task(runner.serve(sock))
        for _ in range(200):
            if sock.exists() and api_sock.exists():
                break
            await asyncio.sleep(0.02)
        native.forget_hello()
        native._FP_TOKENS.clear()
        yield Parity(a=Side(a), b=Side(b), graph=graph, tmp=tmp)
    finally:
        native.forget_hello()
        native._FP_TOKENS.clear()
        if task is not None:
            task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await task
        if proc is not None:
            proc.terminate()
            with contextlib.suppress(Exception):
                proc.wait(5)
        for d in sides:
            await d.close()
        admin = await asyncpg.connect(pg_url)
        for n in names:
            await admin.execute(f'DROP DATABASE IF EXISTS "{n}" WITH (FORCE)')
        await admin.close()
        shutil.rmtree(tmp, ignore_errors=True)
