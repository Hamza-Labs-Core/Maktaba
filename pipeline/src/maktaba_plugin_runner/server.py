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


def runner_context(
    db: Any, job: Any, stage: Any, relay: Relay, write_mode: str, path_map: Any
) -> Any:
    """The job's :class:`StageContext` in the runner: what writes queue tables asks the
    worker; ``rules`` reads through the plugin's statements."""
    from maktaba_pipeline.engine.context import JobCancelled, StageContext

    class RunnerContext(StageContext):
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
    #: the transport a job's database uses (a seam: tests give one in memory)
    transport: Callable[[str], Any] | None = None
    running: dict[int, asyncio.Task[Any]] = field(default_factory=dict)

    def _transport(self, token: str) -> Any:
        if self.transport is not None:
            return self.transport(token)
        return UnixTransport(self.api_socket, token)

    def hello(self) -> dict[str, Any]:
        return {
            "type": "hello",
            "group": self.group,
            "bundle": self.bundle,
            "pid": os.getpid(),
            "concurrency": self.concurrency,
            "stages": {s: self.unavailable.get(s) for s in sorted(self.stages)},
        }

    async def handle(self, r: asyncio.StreamReader, w: asyncio.StreamWriter) -> None:
        try:
            msg = await read_frame(r)
            kind = msg.get("type")
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
        relay = Relay(r, w)
        transport = self._transport(str(msg["token"]))
        db = StatementDB(firstparty.registry(self.plugin_of[stage]), transport, engine_relay=relay)
        sdef = get_stage(stage)
        path_map = PathMap(tuple((str(a), str(b)) for a, b in msg.get("path_map") or ()))
        ctx = runner_context(db, job, sdef, relay, str(msg.get("write_mode") or "auto"), path_map)
        impl = load_impl(self.stages[stage])
        task = asyncio.ensure_future(impl.run(db, job, ctx))
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
        await write_frame(w, {"type": "result", **out})

    async def _fingerprints(self, msg: dict[str, Any]) -> dict[str, Any]:
        from maktaba_pipeline.engine.graph import Subject
        from maktaba_pipeline.plugins import firstparty

        stage = str(msg["stage"])
        if stage not in self.stages:
            return {"type": "error", "message": f"{stage} is not served by {self.group}"}
        impl = load_impl(self.stages[stage])
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
        finally:
            close = getattr(transport, "close", None)
            if close is not None:
                await close()
        return {"type": "fingerprints", "hashes": out, "calls": db.calls}

    async def serve(self, path: Path) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        with contextlib.suppress(FileNotFoundError):
            path.unlink()
        server = await asyncio.start_unix_server(self.handle, path=str(path), limit=1 << 20)
        os.chmod(path, 0o600)
        async with server:
            await server.serve_forever()
