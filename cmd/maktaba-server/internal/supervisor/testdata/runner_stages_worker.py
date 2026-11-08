"""The worker's side of the first-party stages' Windows end to end (story 54.23, CI's
runner-stages job only, never a developer's or the owner's machine).

Run with the pipeline's own interpreter, outside every sandbox, as the worker runs: it
migrates the scratch database it is given, seeds a library on a local folder and one on an
SMB share, writes this machine's areas for the runners (``native.write_areas``), asks the
supervisor for the runners (``native.want_runner``, its real ``/runners/{group}/start``) and
waits for their hello, then runs each case's job through the worker's own dispatch
(``dispatch_job`` -> ``native.choose`` -> ``native.run_native`` through the supervisor's
pipes). What each job did is recorded as it happened: every choice ``choose`` made, every
``run_native`` and how it ended, the fallbacks the worker counted, the job's row and its
tokens. The test (runners_windows_stages_e2e_test.go) judges the report; this script judges
nothing.

    python runner_stages_worker.py <config.json>

The config: ``dsn``, ``pipeline`` (the pipeline's folder: its tests' migration helper and
factories), ``out`` (the report's path), ``groups`` (the runners to wait for),
``wait_sec``, ``libraries`` (``{name: root}``) and ``cases`` (``[{name, stage, library,
file, payload, share_runners}]``). A case with ``share_runners`` false runs with
``MAKTABA_WINDOWS_SHARE_RUNNERS`` unset, as a home does by default: native.choose's Windows
share-library guard then keeps it in the worker.
"""

from __future__ import annotations

import asyncio
import json
import os
import sys
import time
import traceback
from typing import Any


def _jsonable(v: Any) -> Any:
    try:
        json.dumps(v)
        return v
    except TypeError:
        return str(v)


async def main(cfg: dict[str, Any]) -> dict[str, Any]:
    sys.path.insert(0, cfg["pipeline"])
    import asyncpg  # type: ignore[import-untyped]

    from maktaba_pipeline.engine.claim import claim_for_pool
    from maktaba_pipeline.engine.dispatch import EngineRuntime, dispatch_job
    from maktaba_pipeline.engine.enqueue import enqueue_job
    from maktaba_pipeline.engine.graph import bound_graph
    from maktaba_pipeline.engine.sync import sync_graph
    from maktaba_pipeline.plugins import native
    from maktaba_pipeline.runtime import Database
    from tests.engine.factory import make_library, make_video
    from tests.engine.migrate import apply_postgres

    report: dict[str, Any] = {"cases": [], "runners": {}}
    conn = await asyncpg.connect(cfg["dsn"])
    try:
        report["migration_problems"] = await apply_postgres(conn)
    finally:
        await conn.close()
    db = await Database.connect(cfg["dsn"])
    try:
        graph = {s.name: s for s in bound_graph()}
        await sync_graph(db, list(graph.values()))
        stages = sorted({c["stage"] for c in cfg["cases"]})
        for st in stages:
            await db.execute(
                "UPDATE job_stages SET impl = 'plugin', impl_changed_at = now(),"
                " impl_changed_by = 'runner-stages-e2e' WHERE stage = $1",
                st,
            )
        videos: dict[tuple[str, str], Any] = {}
        for name, root in cfg["libraries"].items():
            lib = await make_library(db, f"e2e {name}")
            await db.execute("UPDATE libraries SET roots = $1 WHERE id = $2", [root], lib)
            for c in cfg["cases"]:
                if c["library"] != name or (name, c["file"]) in videos:
                    continue
                path = os.path.join(root, c["file"])
                videos[(name, c["file"])] = await make_video(
                    db,
                    lib,
                    filename=os.path.basename(path),
                    title=c["file"],
                    path=path,
                    size=os.path.getsize(path),
                )
        report["areas"] = await native.write_areas(db)

        # the runners, as the worker asks for them; waited for here (a cold AppContainer's
        # first start, its grants included, can take longer than the worker's own wait), so
        # every job below is one the worker sends to a runner that is up
        for group in cfg["groups"]:
            t0 = time.monotonic()
            native.want_runner(group)
            hello = None
            refused = False
            while time.monotonic() - t0 < float(cfg["wait_sec"]):
                hello = await native.hello(group, fresh=True)
                if hello is not None:
                    break
                if group in native._ASK_REFUSED:
                    # the supervisor answered the ask with an error (its log says why at
                    # debug): no runner is coming, so the end to end stops here, saying so
                    refused = True
                    break
                await asyncio.sleep(1)
            report["runners"][group] = {
                "hello": hello,
                "seconds": round(time.monotonic() - t0, 1),
                "ask_refused": refused,
            }
            if hello is None:
                return report  # the test fails on the first runner that is not up

        choices: list[dict[str, Any]] = []
        runs: list[dict[str, Any]] = []
        choose, run_native = native.choose, native.run_native

        async def recorded_choose(db: Any, stage: str) -> Any:
            c = await choose(db, stage)
            choices.append({"stage": stage, "impl": c.impl, "why": c.why})
            return c

        async def recorded_run_native(db: Any, job: Any, ctx: Any, **kw: Any) -> Any:
            t0 = time.monotonic()
            try:
                result = await run_native(db, job, ctx, **kw)
            except BaseException as exc:
                runs.append(
                    {
                        "job_id": job.id,
                        "status": "raised",
                        "error": f"{type(exc).__name__}: {exc}",
                        "seconds": round(time.monotonic() - t0, 3),
                    }
                )
                raise
            # run_native answers only for the runner's "result" frame with status ok
            runs.append(
                {"job_id": job.id, "status": "ok", "seconds": round(time.monotonic() - t0, 3)}
            )
            return result

        native.choose = recorded_choose  # type: ignore[assignment]
        native.run_native = recorded_run_native  # type: ignore[assignment]
        rt = EngineRuntime(graph=graph)
        for c in cfg["cases"]:
            choices.clear()
            runs.clear()
            native.FALLBACKS.clear()
            entry: dict[str, Any] = {"name": c["name"], "stage": c["stage"]}
            asked = os.environ.get(native.SHARE_RUNNERS_ENV)
            if c.get("share_runners", True) is False:
                os.environ.pop(native.SHARE_RUNNERS_ENV, None)
            try:
                vid = videos[(c["library"], c["file"])]
                await enqueue_job(
                    db,
                    subject_kind="video",
                    subject_id=vid,
                    stage=c["stage"],
                    payload=c.get("payload") or None,
                    force=True,
                )
                job = await claim_for_pool(
                    db,
                    worker_id="runner-stages-e2e",
                    pool=graph[c["stage"]].pool,
                    stages=[c["stage"]],
                    scope="per_worker",
                )
                if job is None:
                    entry["error"] = "no job to claim"
                else:
                    entry["job_id"] = job.id
                    t0 = time.monotonic()
                    await dispatch_job(db, job, runtime=rt, legacy_handlers={})
                    entry["seconds"] = round(time.monotonic() - t0, 3)
                    row = await db.fetchval(
                        "SELECT to_jsonb(j)::text FROM processing_jobs j WHERE id = $1", job.id
                    )
                    entry["job"] = json.loads(row) if row else None
                    entry["invocations"] = await db.fetchval(
                        "SELECT count(*) FROM plugin_invocations WHERE job_id = $1", job.id
                    )
            except Exception:  # noqa: BLE001 - recorded; the test fails on it
                entry["error"] = traceback.format_exc()
            finally:
                if asked is not None:
                    os.environ[native.SHARE_RUNNERS_ENV] = asked
            entry["choices"] = list(choices)
            entry["runs"] = list(runs)
            entry["fallbacks"] = {f"{s}:{w}": n for (s, w), n in native.FALLBACKS.items()}
            report["cases"].append({k: _jsonable(v) for k, v in entry.items()})
    finally:
        await db.close()
    return report


if __name__ == "__main__":
    with open(sys.argv[1], encoding="utf-8") as f:
        config = json.load(f)
    out = asyncio.run(main(config))
    with open(config["out"], "w", encoding="utf-8") as f:
        json.dump(out, f, indent=1, default=str)
