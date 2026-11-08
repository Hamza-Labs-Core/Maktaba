"""The runner's dispatch socket (Epic 54, story 54.23 §2).

Frames are a 4-byte big-endian length and JSON, as ``maktaba-plugind``'s. The queue worker
connects for each request:

=================  =========================================================================
``hello``          → ``{type: hello, group, bundle, stages: {name: unavailable reason|null},
                   concurrency}``: what this runner serves on this machine
``run``            one job; the runner answers ``engine`` frames while it runs (the job's
                   progress, log, events, cancel flag: only the worker writes those) and
                   ends with one ``result``
``cancel``         on a ``run``'s own connection: the job's task is cancelled
``fingerprints``   ``{stage, kind: input|output, subjects: [[kind, id, library]…], token}``
                   → ``{hashes: […]}`` (the fingerprint runner, read statements only)
=================  =========================================================================
"""

from __future__ import annotations

import asyncio
import contextlib
import importlib
import itertools
import json
import os
import struct
import sys
import time
from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any
from uuid import UUID

from maktaba_plugin_sdk.native import (
    PluginRefused,
    StatementDB,
    StatementUnregistered,
    UnixTransport,
)

from . import sandbox, winfiles

__all__ = ["Runner", "load_impl"]

MAX_FRAME = 256 << 20


async def write_frame(w: asyncio.StreamWriter, msg: dict[str, Any]) -> None:
    body = json.dumps(msg, separators=(",", ":"), ensure_ascii=False).encode()
    w.write(struct.pack(">I", len(body)) + body)
    await w.drain()


async def read_frame(r: asyncio.StreamReader) -> dict[str, Any]:
    (n,) = struct.unpack(">I", await r.readexactly(4))
    if n > MAX_FRAME:
        raise ConnectionError("frame too large")
    out: dict[str, Any] = json.loads(await r.readexactly(n))
    return out


def load_impl(entry: str) -> Any:
    module, _, attr = entry.partition(":")
    return getattr(importlib.import_module(module), attr)


def _use_plan_exporters(msg: dict[str, Any]) -> str:
    """The worker's plan exporters for this job (story 54.34): its extensions add data to
    the channel plan, and this process loads none, so the names it sent are resolved here
    from the extensions it discovers (the same code). Every job of one worker sends the same
    names, so concurrent jobs agree. Answers why it could not (the job then runs in the
    worker, as for an unregistered statement), else ""."""
    names = msg.get("plan_exporters")
    if names is None:
        return ""
    from maktaba_pipeline.schedule.plan_inputs import use_plan_exporters

    missing = use_plan_exporters(str(n) for n in names)
    return f"plan exporters this runner cannot load: {', '.join(missing)}" if missing else ""


class Relay:
    """The engine relay of one ``run``: asks the worker on the run's connection."""

    def __init__(self, r: asyncio.StreamReader, w: asyncio.StreamWriter) -> None:
        self.r, self.w = r, w
        self._ids = itertools.count(1)
        self._lock = asyncio.Lock()

    async def ask(self, op: str, **args: Any) -> Any:
        from maktaba_pipeline.plugins.native import encode_any

        async with self._lock:
            n = next(self._ids)
            await write_frame(
                self.w,
                {
                    "type": "engine",
                    "id": n,
                    "op": op,
                    "args": {k: encode_any(v) for k, v in args.items()},
                },
            )
            while True:
                msg = await read_frame(self.r)
                if msg.get("type") == "cancel":
                    raise asyncio.CancelledError
                if msg.get("type") == "engine_reply" and msg.get("id") == n:
                    if "error" in msg:
                        raise RuntimeError(f"the worker: {msg['error']}")
                    return msg.get("value")

    # maktaba_plugin_sdk.native.EngineRelay
    async def log_event(self, **kw: Any) -> None:
        await self.ask("log_event", **kw)

    async def publish(self, type_: str, payload: dict[str, Any]) -> None:
        await self.ask("publish", type=type_, payload=payload)

    async def job_ref(self, job_id: int) -> dict[str, Any] | None:
        from maktaba_pipeline.plugins.native import decode_any

        v = await self.ask("job_ref", job_id=job_id)
        return None if v is None else decode_any(v)

    async def enqueue(self, **args: Any) -> dict[str, Any]:
        """Queue a job: the worker enqueues it (only the queue engine writes the queue), and
        only a stage this job's stage may request, for a subject of the job's library."""
        v = await self.ask("enqueue", **args)
        return dict(v or {})


def runner_context(
    db: Any, job: Any, stage: Any, relay: Relay, write_mode: str, path_map: Any
) -> Any:
    """The job's :class:`StageContext` in the runner: what writes queue tables asks the
    worker; ``rules`` reads through the plugin's statements."""
    from maktaba_pipeline.engine.context import JobCancelled, StageContext

    class RunnerContext(StageContext):
        #: a model question goes to the plugin API's compute.generate (story 54.31 §2)
        model_via = "api"

        async def progress(
            self,
            step: str,
            done: int,
            total: int,
            message: str | None = None,
            *,
            steps: list[str] | None = None,
            force: bool = False,
        ) -> None:
            await relay.ask(
                "progress",
                step=step,
                done=done,
                total=total,
                message=message,
                steps=steps,
                force=force,
            )

        async def log(self, level: str, message: str, data: dict[str, Any] | None = None) -> None:
            await relay.ask("log", level=level, message=message, data=data)

        async def check_cancel(self) -> None:
            if await relay.ask("check_cancel"):
                raise JobCancelled(f"job {self.job.id} cancelled")

    ctx = RunnerContext(db=db, job=job, stage=stage, write_mode=write_mode, path_map=path_map)
    return ctx


def library_roots(areas: dict[str, Any]) -> list[str]:
    """The ``library`` area's folders (``;``-separated, as the sandbox reads them)."""
    return [x for x in str(areas.get("library") or "").split(";") if x]


async def job_scope(db: Any, stage: str, job: Any, ctx: Any, roots: list[str]) -> Any:
    """The files a job of a file stage may change (story 54.32 §2), as this machine names
    them, read through the plugin's statements before the job runs; None when its stage
    keeps no scope."""
    from maktaba_pipeline.library_mgmt.scope import JobScope, job_paths

    found = await job_paths(db, stage, job)
    if found is None:
        return None
    sandbox.guard_scope()  # once per process; the runner's main installs it before any job
    return JobScope.of(stage, (ctx.local_path(p) for p in found), roots)


def with_services(ctx: Any, msg: dict[str, Any]) -> Any:
    """The job's extension settings (no secret, no service address) and the way its service
    calls go (story 54.35), from the worker's run frame."""
    settings = msg.get("extension_settings")
    if isinstance(settings, dict):
        ctx.settings = {str(k): dict(v) for k, v in settings.items() if isinstance(v, dict)}
    ctx.service_via = str(msg.get("service_via") or "api")
    return ctx


def job_line(
    group: str,
    stage: str,
    job_id: Any,
    status: str,
    seconds: float,
    broker: dict[str, int] | None = None,
    missing: list[str] | None = None,
) -> None:
    """The runner's own record of a job it ran (its log, which the supervisor keeps): the
    worker's job id, the stage, how it ended (``ok``, ``failure``, ``refused``,
    ``sandbox_refused``, ...), how long the stage's code took and, on Windows with a share
    brokered, what the launcher's broker and the plain look-ups answered for it
    (``winfiles.take_counts``) and the first paths a look-up found missing
    (``winfiles.take_missing``)."""
    line: dict[str, Any] = {
        "event": "plugin_runner_job",
        "service": "plugin-runner",
        "group": group,
        "stage": stage,
        "job_id": job_id,
        "status": status,
        "ms": round(seconds * 1000, 1),
    }
    if broker:
        line["broker"] = broker
    if missing:
        line["missing"] = missing
    sys.stderr.write(json.dumps(line) + "\n")
    sys.stderr.flush()


@dataclass
class Runner:
    group: str
    api_socket: str
    #: stage name -> its entry (module:IMPL), for the stages this group serves
    stages: dict[str, str]
    #: stage name -> its plugin id
    plugin_of: dict[str, str]
    bundle: str
    concurrency: int = 8
    unavailable: dict[str, str] = field(default_factory=dict)
    #: the digest of the file areas the sandbox was built from (the worker's areas.json)
    areas: str = ""
    #: this machine's library roots (the ``library`` area): where a job's scope applies
    library_roots: list[str] = field(default_factory=list)
    areas_changed: bool = False
    #: the transport a job's database uses (a seam: tests give one in memory)
    transport: Callable[[str], Any] | None = None
    running: dict[int, asyncio.Task[Any]] = field(default_factory=dict)
    #: when it last had work (a run or a fingerprint request; the health check's hello
    #: is not work), for the idle timeout
    last_active: float = field(default_factory=time.monotonic)

    def _transport(self, token: str) -> Any:
        if self.transport is not None:
            return self.transport(token)
        if sys.platform == "win32":
            from .winpipe import API_ENV, PipeTransport

            return PipeTransport(os.environ.get(API_ENV, ""), token)
        return UnixTransport(self.api_socket, token)

    def hello(self) -> dict[str, Any]:
        return {
            "type": "hello",
            "group": self.group,
            "bundle": self.bundle,
            "pid": os.getpid(),
            "concurrency": self.concurrency,
            "areas": self.areas,
            "stages": {s: self.unavailable.get(s) for s in sorted(self.stages)},
        }

    async def handle(self, r: asyncio.StreamReader, w: asyncio.StreamWriter) -> None:
        try:
            msg = await read_frame(r)
            kind = msg.get("type")
            if kind in ("run", "fingerprints"):
                self.last_active = time.monotonic()
            if kind == "hello":
                await write_frame(w, self.hello())
            elif kind == "run":
                await self._run(msg, r, w)
            elif kind == "fingerprints":
                await write_frame(w, await self._fingerprints(msg))
            else:
                await write_frame(w, {"type": "error", "message": f"unknown frame {kind!r}"})
        except (asyncio.IncompleteReadError, ConnectionError):
            pass
        finally:
            w.close()
            with contextlib.suppress(Exception):
                await w.wait_closed()

    async def _run(
        self, msg: dict[str, Any], r: asyncio.StreamReader, w: asyncio.StreamWriter
    ) -> None:
        from maktaba_pipeline.engine.graph import get_stage
        from maktaba_pipeline.engine.paths import PathMap
        from maktaba_pipeline.plugins import firstparty
        from maktaba_pipeline.plugins.native import failure_to_wire, job_from_wire, result_to_wire

        stage = str(msg["stage"])
        if stage not in self.stages or msg.get("entry") != self.stages[stage]:
            await write_frame(
                w,
                {
                    "type": "result",
                    "status": "refused",
                    "code": "plugin_capability",
                    "message": f"{stage} is not served by {self.group}",
                },
            )
            return
        job = job_from_wire(msg["job"])
        started = time.monotonic()
        winfiles.take_counts()  # the broker's answers from here on are this job's
        winfiles.take_missing()
        missing = _use_plan_exporters(msg)
        if missing:
            # the plan would lack what those extensions add: the worker runs the job itself
            await write_frame(w, {"type": "result", "status": "unregistered", "message": missing})
            return
        relay = Relay(r, w)
        transport = self._transport(str(msg["token"]))
        db = StatementDB(firstparty.registry(self.plugin_of[stage]), transport, engine_relay=relay)
        sdef = get_stage(stage)
        path_map = PathMap(tuple((str(a), str(b)) for a, b in msg.get("path_map") or ()))
        ctx = runner_context(db, job, sdef, relay, str(msg.get("write_mode") or "auto"), path_map)
        with_services(ctx, msg)
        impl = load_impl(self.stages[stage])
        scope = None
        try:
            scope = await job_scope(db, stage, job, ctx, self.library_roots)
        except StatementUnregistered as exc:
            await write_frame(w, {"type": "result", "status": "unregistered", "message": str(exc)})
            return
        # the task copies the context: its scope is this job's alone (asyncio.to_thread too)
        token = sandbox.JOB_SCOPE.set(scope)
        try:
            task = asyncio.ensure_future(impl.run(db, job, ctx))
        finally:
            sandbox.JOB_SCOPE.reset(token)
        self.running[job.id] = task
        try:
            result = await task
        except StatementUnregistered as exc:
            out: dict[str, Any] = {"status": "unregistered", "message": str(exc)}
        except PluginRefused as exc:
            out = {"status": "refused", "code": exc.code, "message": exc.detail}
        except asyncio.CancelledError:
            out = {"status": "deferred", "reason": "plugin_cancelled", "retry_after_sec": 0}
        except Exception as exc:  # noqa: BLE001 - every failure goes back to the worker as raised
            import traceback

            sys.stderr.write(traceback.format_exc())
            from maktaba_pipeline.plugins.native import sandbox_refused

            if sandbox_refused(exc):
                out = {"status": "sandbox_refused", "message": str(exc)}
            else:
                out = failure_to_wire(exc)
        else:
            out = {"status": "ok", "result": result_to_wire(result)}
            if ctx.review_digests:
                for d in ctx.review_digests:
                    await relay.ask("review_digest", digest=d)
        finally:
            self.running.pop(job.id, None)
            close = getattr(transport, "close", None)
            if close is not None:
                await close()
        if scope is not None and scope.refused:
            # refused even when the job's code caught the error (a move it reports failed)
            out = {
                "status": "refused",
                "code": "plugin_scope",
                "message": f"{scope.refused[0]}: not a file of this {stage} job's subject",
            }
        job_line(
            self.group,
            stage,
            job.id,
            str(out.get("status")),
            time.monotonic() - started,
            winfiles.take_counts(),
            winfiles.take_missing(),
        )
        await write_frame(w, {"type": "result", **out})

    async def _fingerprints(self, msg: dict[str, Any]) -> dict[str, Any]:
        from maktaba_pipeline.engine.graph import Subject
        from maktaba_pipeline.plugins import firstparty

        stage = str(msg["stage"])
        if stage not in self.stages:
            return {"type": "error", "message": f"{stage} is not served by {self.group}"}
        impl = load_impl(self.stages[stage])
        missing = _use_plan_exporters(msg)
        if missing:
            return {"type": "error", "status": "unregistered", "message": missing}
        fn = impl.input_fingerprint if msg.get("kind") == "input" else impl.output_fingerprint
        if fn is None:
            return {"type": "fingerprints", "hashes": [None for _ in msg.get("subjects") or []]}
        transport = self._transport(str(msg["token"]))
        db = StatementDB(firstparty.registry(self.plugin_of[stage]), transport)
        out: list[str | None] = []
        try:
            for kind, sid, lib in msg.get("subjects") or []:
                subject = Subject(kind, UUID(sid), UUID(lib) if lib else None)
                out.append(await fn(db, subject))
        except StatementUnregistered as exc:
            return {"type": "error", "status": "unregistered", "message": str(exc)}
        except Exception as exc:  # noqa: BLE001 - answered, so the worker fingerprints it itself
            # a fingerprint the sandbox refused (a share path, a folder without a grant) or
            # that failed: an answer, never a dropped connection (the worker then reads its
            # own fingerprint, as for any refusal)
            from maktaba_pipeline.plugins.native import sandbox_refused

            status = "sandbox_refused" if sandbox_refused(exc) else "failed"
            sys.stderr.write(
                json.dumps(
                    {
                        "event": "plugin_runner_fingerprint_failed",
                        "service": "plugin-runner",
                        "group": self.group,
                        "stage": stage,
                        "status": status,
                        "error": f"{type(exc).__name__}: {exc}"[:300],
                    }
                )
                + "\n"
            )
            sys.stderr.flush()
            return {"type": "error", "status": status, "message": f"{type(exc).__name__}: {exc}"}
        finally:
            close = getattr(transport, "close", None)
            if close is not None:
                await close()
        return {"type": "fingerprints", "hashes": out, "calls": db.calls}

    def idle_for(self) -> float:
        """Seconds since it last had work; 0 while a job runs."""
        if self.running:
            self.last_active = time.monotonic()
            return 0.0
        return time.monotonic() - self.last_active

    async def serve(self, path: Path) -> None:
        if sys.platform == "win32":
            # no socket of its own in its AppContainer: the callers the supervisor hands it
            # on its back pipe (story 54.23)
            from . import winpipe

            await winpipe.serve_back(self.handle, os.environ.get(winpipe.BACK_ENV, ""))
            return
        path.parent.mkdir(parents=True, exist_ok=True)
        with contextlib.suppress(FileNotFoundError):
            path.unlink()
        server = await asyncio.start_unix_server(self.handle, path=str(path), limit=1 << 20)
        os.chmod(path, 0o600)
        async with server:
            await server.serve_forever()
