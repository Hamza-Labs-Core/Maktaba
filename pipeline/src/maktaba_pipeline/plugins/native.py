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
   plugin did not register (``unregistered``), or a file its sandbox refused
   (``sandbox_refused``), runs the job in-process instead, logged;
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

from maktaba_plugin_sdk.native.codec import decode, encode

from maktaba_plugin_runner import socket_path

from ..engine.context import JobCancelled, JobDeferred, StageFailure
from ..engine.graph import GraphViolation, Request, StageResult
from ..log import get_logger
from . import firstparty

__all__ = [
    "FALLBACKS",
    "note_fallback",
    "ImplChoice",
    "SandboxRefused",
    "Unregistered",
    "choose",
    "hello",
    "result_from_wire",
    "result_to_wire",
    "run_native",
    "serving",
    "await_runner",
]

_log = get_logger()

MAX_FRAME = 256 << 20
#: how long a runner's hello is believed
HELLO_TTL = 15.0
#: how long "the runner did not answer" is believed: a moment, so a runner that has just
#: started (on demand, after an update) is used by the next job
HELLO_DOWN_TTL = 1.0
#: how long a job waits for a runner this worker asked the supervisor for, before it runs
#: in-process (a shared group's runner is up in seconds)
RUNNER_START_WAIT_SEC = 30.0
#: how often the job waiting for it asks the runner's socket
RUNNER_POLL_SEC = 0.25


#: (stage, why) -> how many of a plugin stage's jobs ran in the worker instead (a statement
#: the plugin did not register, a file its sandbox refused, or choose() declining: the
#: runner not serving, the role unusable …). A parity run fails on any for its stages.
FALLBACKS: dict[tuple[str, str], int] = {}


def note_fallback(stage: str, why: str) -> None:
    FALLBACKS[(stage, why)] = FALLBACKS.get((stage, why), 0) + 1


class Unregistered(Exception):
    """The plugin ran a statement it does not have: the job runs in-process instead."""


class SandboxRefused(Exception):
    """The runner's sandbox refused a file the job needed (a folder added since the runner
    started, before it restarts on the new areas): the job runs in-process instead."""


def sandbox_refused(exc: BaseException) -> bool:
    """Whether a permission refusal is in the exception's chain (a stage that wraps an I/O
    error in a StageFailure keeps it as the cause)."""
    import errno

    seen: BaseException | None = exc
    for _ in range(10):
        if seen is None:
            return False
        if isinstance(seen, PermissionError) or (
            isinstance(seen, OSError) and seen.errno in (errno.EPERM, errno.EACCES)
        ):
            return True
        seen = seen.__cause__ or seen.__context__
    return False


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


async def _probe(group: str) -> dict[str, Any] | None:
    """One ``hello`` on the group's socket: None when nothing answers."""
    with contextlib.suppress(
        OSError, asyncio.TimeoutError, asyncio.IncompleteReadError, ValueError
    ):
        r, w = await asyncio.wait_for(asyncio.open_unix_connection(str(socket_path(group))), 2)
        try:
            await write_frame(w, {"type": "hello"})
            msg = await asyncio.wait_for(read_frame(r), 5)
            if msg.get("type") == "hello":
                return msg
        finally:
            w.close()
    return None


async def hello(group: str, *, fresh: bool = False) -> dict[str, Any] | None:
    """The group's ``hello`` (an answer cached for a few seconds, no answer for a moment):
    None when it does not answer. ``fresh``: asked now, whatever the cache holds."""
    now = time.monotonic()
    hit = _HELLO.get(group)
    if (
        not fresh
        and hit is not None
        and now - hit[0] < (HELLO_TTL if hit[1] is not None else HELLO_DOWN_TTL)
    ):
        return hit[1]
    out = await _probe(group)
    if out is not None and out.get("bundle") != firstparty.bundle_digest():
        # a runner from another build (an update half done): never trusted with a job
        _log.warning("plugin_runner_bundle_mismatch", group=group, runner=out.get("bundle"))
        out = None
    _HELLO[group] = (now, out)
    if out is not None:
        _START_FAILED.discard(group)
    return out


def forget_hello() -> None:
    _HELLO.clear()


#: when this worker last asked the supervisor for each runner group (group -> monotonic)
_ASKED: dict[str, float] = {}
#: the asks in flight (a task with no reference may be collected before it runs)
_ASKING: set[asyncio.Task[None]] = set()
#: how often a group not up is asked for again
ASK_EVERY_SEC = 60.0
#: the groups whose last ask the supervisor refused (its component off, no such runner)
_ASK_REFUSED: set[str] = set()
#: the groups asked for that did not come up within RUNNER_START_WAIT_SEC: their jobs run
#: in-process without waiting, until the runner answers again (a runner that cannot start
#: costs one wait, not one per job)
_START_FAILED: set[str] = set()


def want_runner(group: str) -> None:
    """Ask the supervisor to start a shared group's runner (it starts on demand and stops
    when idle, so an idle home runs none). In the background: the job that found it down
    runs in the worker meanwhile, and the next ones go to the runner once it is up. At most
    once a minute per group; nothing without a supervisor (tests, a bare worker)."""
    base = os.environ.get("MAKTABA_SUPERVISOR_URL", "").strip()
    if not base:
        return
    now = time.monotonic()
    if now - _ASKED.get(group, -ASK_EVERY_SEC) < ASK_EVERY_SEC:
        return
    _ASKED[group] = now
    _ASK_REFUSED.discard(group)

    def post() -> None:
        import urllib.request

        req = urllib.request.Request(base.rstrip("/") + f"/runners/{group}/start", method="POST")
        try:
            with urllib.request.urlopen(req, timeout=5) as res:  # noqa: S310 - the local supervisor
                res.read()
        except Exception:  # noqa: BLE001 - asked again later; the worker runs the job
            _ASK_REFUSED.add(group)
            _log.debug("plugin_runner_ask_failed", group=group, exc_info=True)

    task = asyncio.ensure_future(asyncio.to_thread(post))
    _ASKING.add(task)
    task.add_done_callback(_ASKING.discard)


async def await_runner(group: str) -> dict[str, Any] | None:
    """The hello of a runner this worker asked the supervisor for, once it is up: waited
    for until RUNNER_START_WAIT_SEC after the ask. None at once with no ask (no supervisor),
    a refused one, or a runner that already failed to come up (:data:`_START_FAILED`)."""
    asked = _ASKED.get(group)
    if asked is None or group in _START_FAILED:
        return None
    while group not in _ASK_REFUSED and time.monotonic() - asked < RUNNER_START_WAIT_SEC:
        await asyncio.sleep(RUNNER_POLL_SEC)
        h = await hello(group, fresh=True)
        if h is not None:
            return h
    if group not in _ASK_REFUSED:
        _START_FAILED.add(group)
        _log.warning("plugin_runner_start_timeout", group=group, waited=RUNNER_START_WAIT_SEC)
    return None


async def serving(stage: str) -> tuple[bool, str | None]:
    """Whether the stage's runner serves it here, and its unavailable reason if it said one.
    A runner that is not up is asked for (:func:`want_runner`) and waited for
    (:func:`await_runner`): a shared group's runner starts on demand, so the first jobs
    after it stopped (idle, or an update that restarted every process) would otherwise run
    in-process beside a runner that serves seconds later."""
    group = firstparty.runner_instance(stage)
    if group is None:
        return False, None
    h = await hello(group)
    if h is None:
        want_runner(group)
        h = await await_runner(group)
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
    if h is None:
        want_runner(group)
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
    except Exception:  # noqa: BLE001 - a database before slot 0198 has no column: in-process
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
    if not await role_usable(db, stage):
        return ImplChoice("inproc", "role_unusable")
    if stage in CHROMA_STAGES and not await _postgres_index(db):
        return ImplChoice("inproc", "index_store_chroma")
    if files_stage(stage):
        group = firstparty.runner_instance(stage) or ""
        h = await hello(group)
        if not _AREAS_WRITTEN or (h or {}).get("areas") != _AREAS_WRITTEN[-1]:
            # the runner's sandbox is from other folders than this machine's now (a library
            # was added): this job runs here while the runner restarts on the new areas
            return ImplChoice("inproc", "runner_areas_stale")
    return ImplChoice("plugin")


#: the stages that open the search index: while it is Chroma (a folder one process may
#: open), they stay in the worker beside the query server; once search reads Postgres
#: (semantic_store_state), the runner writes it like any table (story 54.30)
CHROMA_STAGES = frozenset({"index", "chapter_infer"})


async def _postgres_index(db: Any) -> bool:
    from ..search.pgstore import store_state

    try:
        st = await store_state(db)
    except Exception:  # noqa: BLE001 - unreadable: the worker keeps the stage
        return False
    return st.read_postgres and not st.write_chroma


# role -> (checked at, usable): a home whose migrations could not make or grant the
# plugin's role (no right to create roles; a role another database's owner made on a
# shared Postgres) runs the stage in-process; checked again every minute, so a later
# migration run that grants it is picked up.
_ROLE_OK: dict[str, tuple[float, bool]] = {}
_ROLE_TTL = 60.0


async def role_usable(db: Any, stage: str) -> bool:
    """Whether this machine's login may SET ROLE to the stage's plugin role (the API that
    runs the plugin's statements logs in as the same role as this worker)."""
    fp = firstparty.by_stage(stage)
    if fp is None:
        return False
    role = "maktaba_fp_" + fp.id.removeprefix("tv.maktaba.").replace("-", "_").replace(".", "_")
    now = time.monotonic()
    hit = _ROLE_OK.get(role)
    if hit is not None and now - hit[0] < _ROLE_TTL:
        return hit[1]
    try:
        ok = bool(
            await db.fetchval(
                "SELECT to_regrole($1::text) IS NOT NULL"
                " AND pg_has_role(current_user, $1::text, 'MEMBER')",
                role,
            )
        )
    except Exception:  # noqa: BLE001 - an unreadable answer is "not usable", never a failure
        ok = False
    _ROLE_OK[role] = (now, ok)
    return ok


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
        return {
            "status": "failure",
            "kind": exc.kind,
            "message": exc.message,
            "retryable": exc.retryable,
        }
    if isinstance(exc, JobDeferred):
        return {"status": "deferred", "reason": exc.reason, "retry_after_sec": exc.retry_after_sec}
    if isinstance(exc, JobCancelled):
        return {"status": "cancelled", "message": str(exc)}
    if isinstance(exc, GraphViolation):
        return {"status": "graph_violation", "message": str(exc)}
    return {"status": "error", "error_type": type(exc).__name__, "message": str(exc)}


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
    if st == "sandbox_refused":
        raise SandboxRefused(str(d.get("message") or ""))
    if st == "refused":
        raise StageFailure(
            str(d.get("code") or "plugin_capability"), str(d.get("message") or ""), retryable=False
        )
    # any other exception: one of the same class name and message, so the failure's kind
    # and cause are the in-process handler's (run.py names a failure by its class)
    name = str(d.get("error_type") or "Exception")
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
        status, body = await asyncio.to_thread(
            _api, "POST", "/internal/plugins/invocations", {"job_id": job_id}
        )
    except OSError as exc:
        raise JobDeferred(f"plugin_api_unavailable: {exc}", 30) from None
    if status != 200:
        raise JobDeferred(f"plugin_held: {body.get('code', status)}", 60)
    return body


async def _end(invocation_id: str) -> None:
    from .dispatch import _api

    with contextlib.suppress(Exception):
        await asyncio.to_thread(_api, "DELETE", f"/internal/plugins/invocations/{invocation_id}")


#: the token ends in flight (kept so a task is not collected before it ran)
_ENDING: set[asyncio.Future[None]] = set()

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
        if op == "enqueue":
            return await _relayed_enqueue(db, ctx, {k: decode_any(v) for k, v in a.items()})
        if op == "dedup_uncovered":
            from ..library_mgmt.dedup_merge import uncovered_foreign_keys

            return [list(x) for x in await uncovered_foreign_keys(db)]
        if op == "unsupersede":
            return await _relayed_unsupersede(db, ctx, {k: decode_any(v) for k, v in a.items()})
        if op == "dedup_commit":
            return await _relayed_dedup_commit(db, ctx, {k: decode_any(v) for k, v in a.items()})
        if op in ("tv_commit", "tv_rollback"):
            return await _relayed_tv(db, ctx, op)
        if op == "service_request":
            return await _relayed_service(ctx, a)
        raise ValueError(f"engine op {op!r}")

    return handle


def _entry_for(stage: str, kind: str) -> bool:
    """Whether ``stage`` is where the graph starts a new subject of ``kind`` (``probe`` for a
    video): a stage that makes one (a download) starts its pipeline."""
    from ..db.jobs import Stage
    from ..engine.graph import GRAPH

    if kind == "video" and stage == Stage.PROBE:
        return True  # where the scanner starts a new file (scanner/store.py)
    return any(s.name == stage and kind in (s.entry_for or ()) for s in GRAPH)


def _neighbours(a: str, b: str) -> bool:
    """Whether an edge of the graph joins the two stages, either way (``dedup_scan`` queues
    ``convert_mp4``, whose edge leads to it, for a video never planned for conversion)."""
    from ..engine.graph import GRAPH

    for s in GRAPH:
        for e in s.edges:
            if (s.name, e.to) in ((a, b), (b, a)):
                return True
    return False


class EnqueueRefused(Exception):
    """A plugin asked to queue what its stage may not (story 54.32: the plugin's scope)."""


async def _relayed_enqueue(
    db: Any, ctx: Any, a: dict[str, Any], *, hold: bool = True
) -> dict[str, Any]:
    """A job a plugin's stage queues, as the worker enqueues it: only the stage itself or a
    stage it may request (``may_request``), and only for a subject of the job's library (a
    video of it, or the library)."""
    from uuid import UUID

    from ..engine.enqueue import enqueue_job
    from ..engine.sql import uid

    job, sdef = ctx.job, ctx.stage
    stage = str(a["stage"])
    kind, subject = str(a["subject_kind"]), str(a["subject_id"])
    if (
        stage != sdef.name
        and stage not in (sdef.may_request or ())
        and not _neighbours(sdef.name, stage)
        and not _entry_for(stage, kind)
    ):
        raise EnqueueRefused(f"plugin_scope: {sdef.name} may not queue {stage}")
    lib = getattr(job, "library_id", None)
    if lib is None:
        raise EnqueueRefused("plugin_scope: this job has no library")
    if kind == "library":
        ok = str(lib) == str(UUID(subject))
    elif kind == "video":
        owner = await db.fetchval("SELECT library_id FROM videos WHERE id = $1", uid(subject))
        if owner is None and hold:
            # a video this job made in its own transaction, which this worker cannot see yet
            # (a download): queued, after the same checks, once the job's writes are committed
            held = getattr(ctx, "held_enqueues", None)
            if held is None:
                held = []
                ctx.held_enqueues = held
            held.append(dict(a))
            return {"job_id": None, "outcome": "held"}
        ok = owner is not None and str(owner) == str(lib)
    else:
        ok = False
    if not ok:
        raise EnqueueRefused(f"plugin_scope: {kind} {subject} is not of this job's library")
    res = await enqueue_job(
        db,
        subject_kind=kind,
        subject_id=subject,
        stage=stage,
        priority=a.get("priority"),
        force=bool(a.get("force")),
        force_cascade=bool(a.get("force_cascade")),
        reason=a.get("reason"),
        triggered_by=a.get("triggered_by"),
        payload=a.get("payload"),
        max_attempts=a.get("max_attempts"),
    )
    return {"job_id": res.job_id, "outcome": res.outcome}


async def _relayed_unsupersede(db: Any, ctx: Any, a: dict[str, Any]) -> None:
    """A restored duplicate's video back in its state, by the worker for ``file_restore``:
    only a superseded video of the job's library (the state is checked by the table's own
    constraint)."""
    from ..engine.sql import uid
    from ..library_mgmt.swap import unsupersede

    vid = str(a["video_id"])
    lib = getattr(ctx.job, "library_id", None)
    ok = lib is not None and await db.fetchval(
        "SELECT 1 FROM videos WHERE id = $1 AND library_id = $2 AND state = 'superseded'",
        uid(vid),
        uid(lib),
    )
    if not ok:
        raise EnqueueRefused(f"plugin_scope: video {vid} is not a superseded video of this library")
    await unsupersede(db, vid, str(a["state"]))


#: the TV commits the worker runs for a stage in its runner (story 54.34): the op, the stage
#: that may ask for it, and what tvrelease.for_job runs
_TV_OPS = {"tv_commit": ("tv_swap", "commit"), "tv_rollback": ("tv_rollback", "rollback")}


async def _relayed_tv(db: Any, ctx: Any, op: str) -> dict[str, Any]:
    """A TV change's swap commit or rollback, by the worker for ``tv_swap`` / ``tv_rollback``
    in its runner: only for that stage's job, and only for the change in the job's own
    payload (nothing the plugin sends is used)."""
    from ..tvrelease.for_job import run_for_job

    stage, kind = _TV_OPS[op]
    if ctx.job.stage != stage:
        raise EnqueueRefused(f"plugin_scope: {op} is {stage}'s, not {ctx.job.stage}'s")
    got = await run_for_job(db, kind, int(ctx.job.id), ctx.job.payload)
    return {k: encode_any(v) for k, v in got.items()}


async def _relayed_dedup_commit(db: Any, ctx: Any, a: dict[str, Any]) -> dict[str, Any]:
    """A duplicate group's merge, committed by the worker for ``dedup_apply`` in its runner:
    only this job's group (its subject), and only the group's own members."""
    from ..library_mgmt.dedup_merge import MergeRefused, commit_merge

    gid = str(a["group_id"])
    if str(ctx.job.subject.id) != gid:
        raise EnqueueRefused(f"plugin_scope: group {gid} is not this job's")
    members = {
        str(r["video_id"])
        for r in await db.fetch("SELECT video_id FROM duplicate_members WHERE group_id = $1", gid)
    }
    keeper, losers = str(a["keeper"]), [str(x) for x in a.get("losers") or []]
    if keeper not in members or not set(losers) <= members or keeper in losers:
        raise EnqueueRefused(f"plugin_scope: not the members of group {gid}")
    try:
        moved = await commit_merge(
            db,
            gid,
            keeper,
            losers,
            keeper_library=str(a["keeper_library"]),
            decided_by=a.get("decided_by"),
        )
    except MergeRefused as exc:
        return {"refused": [list(x) for x in exc.uncovered]}
    return {"moved": moved}


def _services_of(stage: str) -> tuple[str, ...]:
    p = firstparty.by_stage(stage)
    return tuple(str(s) for s in (p.trusted.get("service") or ())) if p is not None else ()


def _service_frame(stage: str, ctx: Any) -> dict[str, Any]:
    """What a plugin's job gets for the services it reaches (story 54.35): the extensions'
    settings with no secret and no service address, and ``service_via``: ``api`` on the home
    (the plugin API's door), ``worker`` on a joined machine (this worker's channel to the
    home's door, as for its own Plex stages)."""
    from .. import relay
    from .service import holds_secret, plugin_settings

    services = _services_of(stage)
    if not services:
        return {}  # no service: the job gets no extension settings, as before
    settings = getattr(ctx, "settings", None) or {}
    # only a service with a secret is relayed (a machine without it asks the home's door);
    # dizqueTV keeps none and is home-only (issue #827)
    via = (
        "worker"
        if any(relay.relayed(settings.get(s, {})) for s in services if holds_secret(s))
        else "api"
    )
    return {"extension_settings": plugin_settings(settings, services), "service_via": via}


async def _relayed_service(ctx: Any, a: dict[str, Any]) -> dict[str, Any]:
    """A plugin's service call on a joined machine (story 54.35): through this worker's
    channel to the home's door, which checks it against the allow-list and adds the secret.
    Only a service the stage's plugin lists."""
    import base64

    from .. import relay

    service = str(a.get("service") or "")
    if service not in _services_of(str(ctx.job.stage)):
        raise EnqueueRefused(f"plugin_capability: {ctx.job.stage} does not reach {service}")
    t = relay.RelayTransport(service)
    headers = {
        "Accept": str(a.get("accept") or ""),
        "Content-Type": str(a.get("content_type") or ""),
    }
    status, data = await asyncio.to_thread(
        t.request,
        str(a.get("method") or "GET"),
        relay.RELAY_BASE + service + str(a.get("target") or "/"),
        {k: v for k, v in headers.items() if v},
        base64.b64decode(str(a.get("body") or "")) or None,
        float(a.get("timeout_ms") or 30000) / 1000.0,
    )
    return {"status": int(status), "body": base64.b64encode(data).decode()}


def _plan_exporter_names() -> list[str]:
    """The plan exporters the worker's extensions registered (story 54.34): a runner loads
    no extension, so it resolves these names itself (schedule.plan_inputs)."""
    from ..schedule.plan_inputs import plan_exporters

    return sorted(plan_exporters())


async def run_native(db: Any, job: Any, ctx: Any, *, connect: Any = None) -> StageResult:
    """One job of a first-party stage in its runner (``impl = 'plugin'``)."""
    stage = job.stage
    group = firstparty.runner_instance(stage)
    entry = firstparty.impl_entry(stage)
    if group is None or entry is None:
        raise StageFailure("plugin_failed", f"{stage} is not a first-party stage", retryable=False)
    minted = await _mint(int(job.id))
    handle = engine_handler(db, ctx)
    try:
        opener = connect or (
            lambda: asyncio.open_unix_connection(str(socket_path(group)), limit=1 << 20)
        )
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
                    "plan_exporters": _plan_exporter_names(),
                    # story 54.35: the extensions' settings without secrets or a service's
                    # address, and which way the job's service calls go
                    **_service_frame(stage, ctx),
                    "path_map": [
                        list(e) for e in getattr(getattr(ctx, "path_map", None), "entries", ())
                    ],
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
                        result = result_from_wire(msg["result"])
                        # the job's own writes are committed now: what it queued for a video
                        # it made (not yet visible to this worker then) is checked and queued
                        for held in getattr(ctx, "held_enqueues", ()):
                            await _relayed_enqueue(db, ctx, held, hold=False)
                        return result
                    raise_failure(msg)
                raise StageFailure("plugin_failed", f"runner sent {kind!r}", retryable=True)
        except asyncio.CancelledError:
            with contextlib.suppress(Exception):
                await write_frame(w, {"type": "cancel"})
            raise
        finally:
            w.close()
    finally:
        # the token ends on its own when the job is done; nothing waits for the answer
        _ENDING.add(t := asyncio.ensure_future(_end(str(minted["invocation_id"]))))
        t.add_done_callback(_ENDING.discard)


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
        _api,
        "POST",
        "/internal/plugins/invocations",
        {"plugin_id": plugin_id, "purpose": "fingerprints"},
    )
    if status != 200:
        raise ConnectionError(f"fingerprint token: {body.get('code', status)}")
    _FP_TOKENS[plugin_id] = (now, str(body["token"]))
    return str(body["token"])


async def fingerprints(
    stage: str, kind: str, subjects: list[Any], *, group: str = "fp"
) -> list[str | None]:
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
                "plan_exporters": _plan_exporter_names(),
                "subjects": [
                    [s.kind, str(s.id), str(s.library_id) if s.library_id else None]
                    for s in subjects
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


# --- the runners' file areas (story 54.23 §3) --------------------------------------------

AREAS_FILE = "areas.json"


async def areas_doc(db: Any, path_map: Any = None) -> dict[str, str]:
    """This machine's folders of each ``[trusted]`` file area, as the worker sees them: the
    library roots through this machine's share-path mapping, and the data folders the
    stages' own settings name. A runner applies its sandbox from them."""
    from ..engine.stages.review_pages import reviews_dir
    from ..thumbnail import small
    from ..thumbnail.generator import thumbnail_dir_for

    roots: set[str] = set()
    try:
        rows = await db.fetch("SELECT roots FROM libraries")
    except Exception:  # noqa: BLE001 - a database without libraries: no library area
        rows = []
    for r in rows:
        raw = r["roots"]
        if isinstance(raw, str):
            try:
                raw = json.loads(raw)
            except ValueError:
                raw = [raw]
        for root in raw or ():
            local = path_map.to_local(str(root)) if path_map is not None else str(root)
            roots.add(os.path.realpath(local) if os.path.isabs(local) else local)
    # the cache the thumbnail stages write (maktaba_pipeline.thumbnail.small.ROOT)
    thumbs = str(thumbnail_dir_for("x", root=small.ROOT).parent)
    lib = ";".join(sorted(roots))
    # library.subtitles is the library's folders, written only by subtitle names
    return {
        "library": lib,
        "library.subtitles": lib,
        # each root's trash (library_mgmt.trash.TRASH_DIR)
        "trash": ";".join(f"{r}/.maktaba/trash" for r in sorted(roots)),
        "data.thumbnails": thumbs,
        "models": ";".join(_model_folders()),
        # the YouTube downloads' work folder (MAKTABA_YT_WORK, else ~/.maktaba/work/yt)
        "data.cache": os.path.realpath(
            os.path.expanduser(os.environ.get("MAKTABA_YT_WORK") or "~/.maktaba/work/yt")
        ),
        "data.index": os.path.realpath(
            os.path.expanduser(os.environ.get("MAKTABA_CHROMA_DIR") or "~/.maktaba/chroma")
        ),
        # the reports folder (Reports: the channel plan's page, engine.plan_page)
        "data.review": os.path.realpath(str(reviews_dir())),
        # the channels' art: what an admin uploaded (<data dir>/channel-art, the API's
        # channels.ArtDirName) and the folders the planner's rules name (schedule.tv_channel)
        "data.channels": ";".join(await _channel_art_folders(db)),
    }


async def _channel_art_folders(db: Any) -> list[str]:
    from ..rules import read as read_rules

    out: set[str] = set()
    data = (os.environ.get("MAKTABA_DATA_DIR") or "").strip()
    if data:
        out.add(os.path.join(data, "channel-art"))
    try:
        cfg = (await read_rules(db, "planner"))[0]
    except Exception:  # noqa: BLE001 - no rules table yet: only the uploads
        cfg = None
    for key in ("dz_logo_dir", "dz_watermark_dir"):
        if folder := str(getattr(cfg, key, "") or "").strip():
            out.add(folder)
    if wm := str(getattr(cfg, "dz_watermark_file", "") or "").strip():
        out.add(os.path.dirname(wm))
    return sorted(os.path.realpath(os.path.expanduser(x)) for x in out if x)


def _model_folders() -> list[str]:
    """Where this machine's models are: the Hugging Face cache (``HF_HOME``, else its
    default), CAMeL Tools' data, the model cache root and the share's models folder."""
    env = os.environ
    hf = env.get("HF_HOME") or os.path.join(
        env.get("XDG_CACHE_HOME") or os.path.expanduser("~/.cache"), "huggingface"
    )
    out = {hf}
    for k in ("CAMELTOOLS_DATA", "MAKTABA_MODEL_CACHE_DIR", "MAKTABA_MODELS_DIR"):
        if (env.get(k) or "").strip():
            out.add(env[k].strip())
    return sorted(os.path.realpath(os.path.expanduser(x)) for x in out)


def areas_digest(doc: dict[str, str]) -> str:
    import hashlib

    return hashlib.sha256(json.dumps(doc, sort_keys=True).encode()).hexdigest()[:16]


_AREAS_WRITTEN: list[str] = []


async def write_areas(db: Any, path_map: Any = None) -> str:
    """Write the areas for the runners (``<runner dir>/areas.json``) when they changed; a
    runner that started with others restarts itself (story 54.23 §3). Answers the digest."""
    from maktaba_plugin_runner import runner_dir

    doc = await areas_doc(db, path_map)
    digest = areas_digest(doc)
    if _AREAS_WRITTEN and _AREAS_WRITTEN[-1] == digest:
        return digest
    d = runner_dir()
    with contextlib.suppress(OSError):
        d.mkdir(parents=True, exist_ok=True)
        tmp = d / (AREAS_FILE + ".tmp")
        tmp.write_text(json.dumps({"digest": digest, "areas": doc}, sort_keys=True))
        os.replace(tmp, d / AREAS_FILE)
        _AREAS_WRITTEN.append(digest)
    return digest


def files_stage(stage: str) -> bool:
    """Whether a first-party stage reads or writes files (its runner must have this
    machine's current areas to serve it)."""
    p = firstparty.by_stage(stage)
    files = (p.trusted.get("files") or {}) if p is not None else {}
    return bool(files.get("read") or files.get("write"))
