"""Running a first-party stage in its plugin runner (Epic 54, stories 54.23, 54.25 §3).

``job_stages.impl`` says, per stage, where it runs: ``inproc`` (the stage's code imported
here, on the worker's database) or ``plugin`` (the same code in ``maktaba-plugin-runner``,
which reaches the database only through the plugin API by its named statements). The
worker reads it for each job (:func:`choose`); a stage whose runner does not serve on this
machine (a Windows machine, a runner that is starting or failed its sandbox) runs here
in-process instead, which writes the same data (the parity proof, story 54.26), so a
mixed cluster or a machine without a runner never stops a stage.

:func:`run_native` is the plugin path of one job:

1. a token from the API (``POST /internal/plugins/invocations {job_id}``: a first-party
   job's, with its plugin's statements and settings);
2. ``run`` on the group's socket with the job, the token and the stage's entry;
3. while it runs, the runner asks for what only the worker writes (the job's progress, its
   log, its events, whether it was cancelled: ``engine`` frames), answered here;
4. the ``result``: the :class:`StageResult`, or the failure exactly as the in-process
   handler would have raised it (the same kind, message and retry class); a statement the
   plugin did not register (``unregistered``) runs the job in-process instead, logged;
5. the token ends in a ``finally``.
"""

from __future__ import annotations

import asyncio
import contextlib
import dataclasses
import json
import os
import struct
import time
from collections.abc import Awaitable, Callable
from typing import Any
from uuid import UUID

from maktaba_plugin_runner import socket_path
from maktaba_plugin_sdk.native.codec import decode, encode

from ..engine.context import JobCancelled, JobDeferred, StageFailure
from ..engine.graph import GraphViolation, Request, StageResult
from ..log import get_logger
from . import firstparty

__all__ = [
    "ImplChoice",
    "Unregistered",
    "choose",
    "hello",
    "result_from_wire",
    "result_to_wire",
    "run_native",
    "serving",
]

_log = get_logger()

MAX_FRAME = 256 << 20
#: how long a runner's hello is believed
HELLO_TTL = 15.0


class Unregistered(Exception):
    """The plugin ran a statement it does not have: the job runs in-process instead."""


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


# --- the runners serving here -----------------------------------------------------------

_HELLO: dict[str, tuple[float, dict[str, Any] | None]] = {}


async def hello(group: str) -> dict[str, Any] | None:
    """The group's ``hello`` (cached for a few seconds): None when it does not answer."""
    now = time.monotonic()
    hit = _HELLO.get(group)
    if hit is not None and now - hit[0] < HELLO_TTL:
        return hit[1]
    out: dict[str, Any] | None = None
    with contextlib.suppress(OSError, asyncio.TimeoutError, asyncio.IncompleteReadError, ValueError):
        r, w = await asyncio.wait_for(asyncio.open_unix_connection(str(socket_path(group))), 2)
        try:
            await write_frame(w, {"type": "hello"})
            msg = await asyncio.wait_for(read_frame(r), 5)
            if msg.get("type") == "hello":
                out = msg
        finally:
            w.close()
    if out is not None and out.get("bundle") != firstparty.bundle_digest():
        # a runner from another build (an update half done): never trusted with a job
        _log.warning("plugin_runner_bundle_mismatch", group=group, runner=out.get("bundle"))
        out = None
    _HELLO[group] = (now, out)
    return out


def forget_hello() -> None:
    _HELLO.clear()


async def serving(stage: str) -> tuple[bool, str | None]:
    """Whether the stage's runner serves it here, and its unavailable reason if it said one."""
    group = firstparty.runner_group(stage)
    if group is None:
        return False, None
    h = await hello(group)
    if h is None:
        return False, None
    stages = h.get("stages") or {}
    if stage not in stages:
        return False, None
    reason = stages.get(stage)
    return True, (str(reason) if reason else None)


async def serving_group(group: str, stage: str) -> tuple[bool, str | None]:
    """Whether ``group`` (``fp``, the fingerprint runner) serves the stage here."""
    h = await hello(group)
    if h is None or stage not in (h.get("stages") or {}):
        return False, None
    reason = (h.get("stages") or {}).get(stage)
    return True, (str(reason) if reason else None)


@dataclasses.dataclass(frozen=True)
class ImplChoice:
    impl: str  # "inproc" | "plugin"
    why: str = ""


async def stage_impl(db: Any, stage: str) -> str:
    try:
        v = await db.fetchval("SELECT impl FROM job_stages WHERE stage = $1", stage)
    except Exception:  # noqa: BLE001 - a database before slot 0196 has no column: in-process
        return "inproc"
    return "plugin" if v == "plugin" else "inproc"


async def choose(db: Any, stage: str) -> ImplChoice:
    """Where this job of ``stage`` runs on this machine."""
    if firstparty.by_stage(stage) is None or getattr(db, "dialect", "") != "postgres":
        return ImplChoice("inproc")
    if await stage_impl(db, stage) != "plugin":
        return ImplChoice("inproc")
    ok, _ = await serving(stage)
    if not ok:
        return ImplChoice("inproc", "runner_not_serving_here")
    return ImplChoice("plugin")


# --- the wire format of a job and its result ---------------------------------------------


def job_to_wire(job: Any) -> dict[str, Any]:
    return {f.name: encode_any(getattr(job, f.name)) for f in dataclasses.fields(job)}


def job_from_wire(d: dict[str, Any]) -> Any:
    from ..db.jobs import Job

    names = {f.name for f in dataclasses.fields(Job)}
    return Job(**{k: decode_any(v) for k, v in d.items() if k in names})


def encode_any(v: Any) -> Any:
    if isinstance(v, dict):
        return {"$t": "map", "v": json.dumps({k: encode_any(x) for k, x in v.items()})}
    if isinstance(v, list | tuple):
        return [encode_any(x) for x in v]
    return encode(v)


def decode_any(v: Any) -> Any:
    if isinstance(v, dict) and v.get("$t") == "map":
        return {k: decode_any(x) for k, x in json.loads(v["v"]).items()}
    if isinstance(v, list):
        return [decode_any(x) for x in v]
    return decode(v)


def result_to_wire(r: StageResult) -> dict[str, Any]:
    return {
        "output_hash": r.output_hash,
        "outcome": r.outcome,
        "changed_subjects": [[k, str(i)] for k, i in r.changed_subjects],
        "requests": [
            {
                "stage": q.stage,
                "subject_kind": q.subject_kind,
                "subject_id": str(q.subject_id),
                "reason": q.reason,
                "payload": encode_any(q.payload) if q.payload is not None else None,
                "priority": q.priority,
            }
            for q in r.requests
        ],
        "summary": encode_any(r.summary),
        "log": [[lv, msg, encode_any(data)] for lv, msg, data in r.log],
        "input_hash": r.input_hash,
    }


def result_from_wire(d: dict[str, Any]) -> StageResult:
    return StageResult(
        output_hash=str(d["output_hash"]),
        outcome=d.get("outcome"),
        changed_subjects=tuple((str(k), UUID(i)) for k, i in d.get("changed_subjects") or []),  # type: ignore[misc]
        requests=tuple(
            Request(
                stage=q["stage"],
                subject_kind=q["subject_kind"],
                subject_id=UUID(q["subject_id"]),
                reason=q["reason"],
                payload=decode_any(q["payload"]) if q.get("payload") is not None else None,
                priority=q.get("priority"),
            )
            for q in d.get("requests") or []
        ),
        summary=decode_any(d.get("summary") or {"$t": "map", "v": "{}"}),
        log=tuple((lv, msg, decode_any(data)) for lv, msg, data in d.get("log") or []),
        input_hash=d.get("input_hash"),
    )


def failure_to_wire(exc: BaseException) -> dict[str, Any]:
    """An exception the stage's code raised, so the worker raises the same one."""
    if isinstance(exc, StageFailure):
        return {"status": "failure", "kind": exc.kind, "message": exc.message, "retryable": exc.retryable}
    if isinstance(exc, JobDeferred):
        return {"status": "deferred", "reason": exc.reason, "retry_after_sec": exc.retry_after_sec}
    if isinstance(exc, JobCancelled):
        return {"status": "cancelled", "message": str(exc)}
    if isinstance(exc, GraphViolation):
        return {"status": "graph_violation", "message": str(exc)}
    return {"status": "error", "type": type(exc).__name__, "message": str(exc)}


_ERRORS: dict[str, type[Exception]] = {}


def raise_failure(d: dict[str, Any]) -> None:
    st = d.get("status")
    if st == "failure":
        raise StageFailure(str(d["kind"]), str(d["message"]), retryable=bool(d["retryable"]))
    if st == "deferred":
        raise JobDeferred(str(d["reason"]), float(d["retry_after_sec"]))
    if st == "cancelled":
        raise JobCancelled(str(d.get("message") or "cancelled"))
    if st == "graph_violation":
        raise GraphViolation(str(d["message"]))
    if st == "unregistered":
        raise Unregistered(str(d.get("message") or ""))
    if st == "refused":
        raise StageFailure(str(d.get("code") or "plugin_capability"), str(d.get("message") or ""), retryable=False)
    # any other exception: one of the same class name and message, so the failure's kind
    # and cause are the in-process handler's (run.py names a failure by its class)
    name = str(d.get("type") or "Exception")
    cls = _ERRORS.get(name)
    if cls is None:
        cls = type(name, (Exception,), {})
        _ERRORS[name] = cls
    raise cls(str(d.get("message") or ""))


# --- one job in the runner -------------------------------------------------------------


def _api_url() -> str:
    return (os.environ.get("MAKTABA_API_URL") or "http://127.0.0.1:8080").rstrip("/")


async def _mint(job_id: int) -> dict[str, Any]:
    from .dispatch import _api

    try:
        status, body = await asyncio.to_thread(_api, "POST", "/internal/plugins/invocations", {"job_id": job_id})
    except OSError as exc:
        raise JobDeferred(f"plugin_api_unavailable: {exc}", 30) from None
    if status != 200:
        raise JobDeferred(f"plugin_held: {body.get('code', status)}", 60)
    return body


async def _end(invocation_id: str) -> None:
    from .dispatch import _api

    with contextlib.suppress(Exception):
        await asyncio.to_thread(_api, "DELETE", f"/internal/plugins/invocations/{invocation_id}")


EngineHandler = Callable[[str, dict[str, Any]], Awaitable[Any]]


def engine_handler(db: Any, ctx: Any) -> EngineHandler:
    """What the runner may ask of the worker while one job runs: the job's own progress,
    log, events and cancel flag, written by the worker with its own database."""
    from ..engine import events

    async def handle(op: str, a: dict[str, Any]) -> Any:
        if op == "progress":
            await ctx.progress(
                str(a["step"]),
                int(a["done"]),
                int(a["total"]),
                a.get("message"),
                steps=a.get("steps"),
                force=bool(a.get("force")),
            )
            return None
        if op == "log":
            await ctx.log(str(a["level"]), str(a["message"]), decode_any(a.get("data")))
            return None
        if op == "check_cancel":
            try:
                await ctx.check_cancel()
            except JobCancelled:
                return True
            return False
        if op == "log_event":
            kw = {k: decode_any(v) for k, v in a.items()}
            await events.log_event(db, **kw)
            return None
        if op == "publish":
            await events.publish(db, str(a["type"]), decode_any(a["payload"]))
            return None
        if op == "job_ref":
            ref = await events.job_ref(db, int(a["job_id"]))
            return encode_any(ref) if ref is not None else None
        if op == "review_digest":
            ctx.review_digests.append(str(a["digest"]))
            return None
        raise ValueError(f"engine op {op!r}")

    return handle


async def run_native(db: Any, job: Any, ctx: Any, *, connect: Any = None) -> StageResult:
    """One job of a first-party stage in its runner (``impl = 'plugin'``)."""
    stage = job.stage
    group = firstparty.runner_group(stage)
    entry = firstparty.impl_entry(stage)
    if group is None or entry is None:
        raise StageFailure("plugin_failed", f"{stage} is not a first-party stage", retryable=False)
    minted = await _mint(int(job.id))
    handle = engine_handler(db, ctx)
    try:
        opener = connect or (lambda: asyncio.open_unix_connection(str(socket_path(group)), limit=1 << 20))
        try:
            r, w = await opener()
        except OSError as exc:
            raise JobDeferred(f"plugin_runner_unavailable: {exc}", 30) from None
        try:
            await write_frame(
                w,
                {
                    "type": "run",
                    "stage": stage,
                    "entry": entry,
                    "plugin_id": minted["plugin_id"],
                    "invocation_id": minted["invocation_id"],
                    "token": minted["token"],
                    "job": job_to_wire(job),
                    "write_mode": getattr(ctx, "write_mode", "auto"),
                    "path_map": [list(e) for e in getattr(getattr(ctx, "path_map", None), "entries", ())],
                },
            )
            while True:
                try:
                    msg = await read_frame(r)
                except (asyncio.IncompleteReadError, ConnectionError) as exc:
                    # the runner died mid-job: back to the queue, no attempt spent (§4.9)
                    raise JobDeferred(f"plugin_runner_lost: {exc}", 10) from None
                kind = msg.get("type")
                if kind == "engine":
                    try:
                        value = await handle(str(msg["op"]), msg.get("args") or {})
                        reply = {"type": "engine_reply", "id": msg["id"], "value": value}
                    except Exception as exc:  # noqa: BLE001 - the runner's code sees the error
                        reply = {"type": "engine_reply", "id": msg["id"], "error": str(exc)}
                    await write_frame(w, reply)
                    continue
                if kind == "result":
                    if msg.get("status") == "ok":
                        return result_from_wire(msg["result"])
                    raise_failure(msg)
                raise StageFailure("plugin_failed", f"runner sent {kind!r}", retryable=True)
        except asyncio.CancelledError:
            with contextlib.suppress(Exception):
                await write_frame(w, {"type": "cancel"})
            raise
        finally:
            w.close()
    finally:
        await _end(str(minted["invocation_id"]))


# --- fingerprints through the fp runner (story 54.23 §4) ---------------------------------

_FP_TOKENS: dict[str, tuple[float, str]] = {}
#: a fingerprint token is minted for hours; renewed well before it ends
FP_TOKEN_TTL = 3600.0


async def _fp_token(plugin_id: str) -> str:
    from .dispatch import _api

    hit = _FP_TOKENS.get(plugin_id)
    now = time.monotonic()
    if hit is not None and now - hit[0] < FP_TOKEN_TTL:
        return hit[1]
    status, body = await asyncio.to_thread(
        _api, "POST", "/internal/plugins/invocations", {"plugin_id": plugin_id, "purpose": "fingerprints"}
    )
    if status != 200:
        raise ConnectionError(f"fingerprint token: {body.get('code', status)}")
    _FP_TOKENS[plugin_id] = (now, str(body["token"]))
    return str(body["token"])


async def fingerprints(stage: str, kind: str, subjects: list[Any], *, group: str = "fp") -> list[str | None]:
    """The stage's input or output fingerprints of ``subjects``, in one batch, from the
    runner that serves fingerprints (read statements only). Raises ``ConnectionError``
    when it does not answer; the caller then computes them in-process."""
    p = firstparty.by_stage(stage)
    if p is None:
        raise KeyError(stage)
    token = await _fp_token(p.id)
    r, w = await asyncio.open_unix_connection(str(socket_path(group)), limit=1 << 20)
    try:
        await write_frame(
            w,
            {
                "type": "fingerprints",
                "stage": stage,
                "kind": kind,
                "token": token,
                "subjects": [
                    [s.kind, str(s.id), str(s.library_id) if s.library_id else None] for s in subjects
                ],
            },
        )
        msg = await read_frame(r)
    finally:
        w.close()
    if msg.get("type") != "fingerprints":
        if msg.get("status") == "unregistered":
            raise Unregistered(str(msg.get("message") or ""))
        raise ConnectionError(str(msg.get("message") or "fingerprints refused"))
    return [None if h is None else str(h) for h in msg.get("hashes") or []]


def fingerprint_fn(stage: str, kind: str, inproc: Any) -> Any:
    """The input (or output) fingerprint function of a plugin stage: the fp runner's when it
    answers, else the in-process one (a machine without it, a refusal)."""
    if inproc is None:
        return None

    async def fn(db: Any, subject: Any) -> Any:
        try:
            (h,) = await fingerprints(stage, kind, [subject])
            return h
        except (OSError, ConnectionError, Unregistered, ValueError, KeyError) as exc:
            _log.info("plugin_fingerprint_inproc", stage=stage, why=str(exc)[:200])
            return await inproc(db, subject)

    return fn
