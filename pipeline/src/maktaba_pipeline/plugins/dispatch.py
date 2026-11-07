"""Plugin stage dispatch (Epic 54, story 54.06 §3): the handler of every plugin stage.

The pipeline still owns the queue (claim, pools, retries, failure classes, drain). A plugin
stage's handler does no work itself: it

1. asks the API for a token (``POST /internal/plugins/invocations``, this machine's own
   per-boot local token; the grants come from the API's table, never from here);
2. sends ``Run`` to maktaba-plugind on its unix socket, with the token, the job and the
   plugin's settings, and relays ``progress`` frames (the Queues page's %) and checkpoints;
3. maps the host's ``result`` to a :class:`StageResult` or a classified failure:

   ========================  =============================================================
   ``ok``                    the output hash, the summary, the requests the API allowed
   ``trap`` / ``timeout``    ``plugin_trap`` / ``plugin_timeout`` (transient: one retry)
   ``memory``                ``plugin_memory`` (item_permanent)
   ``refused``               ``plugin_capability`` / ``plugin_scope`` / ``plugin_quota``
   ``failed``                ``plugin_failed`` (the plugin's own error)
   ``refused`` plugin_loop   ``plugin_loop`` (a request or event would loop, 54.40)
   cancelled, host gone      the job goes back to the queue, no attempt spent
   ========================  =============================================================

   The requests and the published events (54.40) become jobs only when the job ended
   well, each with the job's lineage extended by its own (stage, subject): its origin, its
   hop count and its path. The API refused already what would pass the hop cap or re-enter
   a pair; the same rule is checked here again, as the second line.

4. ends the token in a ``finally``.

The worker never imports plugin code: the code runs in the host, sandboxed.
"""

from __future__ import annotations

import asyncio
import base64
import contextlib
import json
import os
import struct
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any
from uuid import UUID

from ..engine.context import JobDeferred, StageFailure
from ..engine.graph import Request, StageResult
from ..log import get_logger
from .loader import plugin_of

__all__ = ["HostResultError", "api_url", "plugind_socket", "result_to_stage", "run_plugin_stage"]

_log = get_logger()

#: the largest frame either side accepts (plugind's protocol.rs MAX_FRAME)
MAX_FRAME = 4 << 20
#: the payload key that holds a job's lineage (the API's LineageKey); never the guest's
LINEAGE_KEY = "_lineage"
#: the lineage hop cap (README §4.7)
MAX_HOPS = 16
LOCAL_TOKEN_HEADER = "X-Maktaba-Local-Token"


async def _connect(path: str) -> tuple[asyncio.StreamReader, asyncio.StreamWriter]:
    """The host's dispatch socket (a seam: tests give in-memory streams)."""
    return await asyncio.open_unix_connection(path)


class HostResultError(Exception):
    """The host's answer could not be read."""


def api_url() -> str:
    return (os.environ.get("MAKTABA_API_URL") or "http://127.0.0.1:8080").rstrip("/")


def plugind_socket() -> str:
    """The host's dispatch socket: ``MAKTABA_PLUGIND_SOCKET``, else under the data folder."""
    explicit = os.environ.get("MAKTABA_PLUGIND_SOCKET", "").strip()
    if explicit:
        return explicit
    data = os.environ.get("MAKTABA_DATA_DIR", "").strip() or "."
    return str(Path(data) / "run" / "plugins" / "plugind.sock")


def package_path(plugin_id: str, home_path: str) -> str:
    """Where the host finds a package: the home's own folder, or on a joined machine
    (MAKTABA_PLUGINS_ROOT, story 54.42 §2) this machine's copy of it, the same version
    folder under its own plugins folder."""
    root = os.environ.get("MAKTABA_PLUGINS_ROOT", "").strip()
    if not root:
        return home_path
    return str(Path(root) / plugin_id / Path(home_path).name)


def _local_token() -> str:
    path = os.environ.get("MAKTABA_SUPERVISOR_TOKEN_FILE", "").strip()
    if not path:
        return ""
    try:
        return Path(path).read_text().strip()
    except OSError:
        return ""


def _api(method: str, path: str, body: dict[str, Any] | None = None) -> tuple[int, dict[str, Any]]:
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(api_url() + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    req.add_header(LOCAL_TOKEN_HEADER, _local_token())
    try:
        with urllib.request.urlopen(req, timeout=15) as res:  # noqa: S310 - our own loopback API
            return res.status, json.loads(res.read() or b"{}")
    except urllib.error.HTTPError as exc:
        try:
            return exc.code, json.loads(exc.read() or b"{}")
        except ValueError:
            return exc.code, {}


async def _mint(job_id: int) -> dict[str, Any]:
    try:
        status, body = await asyncio.to_thread(
            _api, "POST", "/internal/plugins/invocations", {"job_id": job_id}
        )
    except (OSError, urllib.error.URLError) as exc:
        raise JobDeferred(f"plugin_api_unavailable: {exc}", 30) from None
    if status == 409 and body.get("code") == "plugin_loop":
        # the job itself closes a loop through data (54.40 §3): not retried
        raise StageFailure("plugin_loop", str(body.get("detail") or "")[:2000], retryable=False)
    if status != 200:
        # the plugin is off, held, not installed (any more), or its grants are waiting:
        # the job waits and spends no attempt (the runner stops claiming it, 54.07)
        raise JobDeferred(f"plugin_held: {body.get('code', status)}", 60)
    return body


async def _end(invocation_id: str) -> int:
    """End the token; the plugin API refusals the job met (the breaker counts them)."""
    try:
        _, body = await asyncio.to_thread(
            _api, "DELETE", f"/internal/plugins/invocations/{invocation_id}"
        )
    except Exception:  # noqa: BLE001 - the token expires on its own anyway
        _log.warning("plugin_invocation_end_failed", invocation=invocation_id)
        return 0
    try:
        return int(body.get("refusals") or 0)
    except (TypeError, ValueError):
        return 0


async def _cancel(invocation_id: str) -> None:
    """Ask the host to stop a job (a drain's deadline: 54.07 §1); it answers with the
    checkpoint on the run's own connection, which is gone by then, so this one is ignored."""
    try:
        r, w = await _connect(plugind_socket())
    except OSError:
        return
    try:
        await write_frame(w, {"type": "cancel", "invocation_id": invocation_id})
        with contextlib.suppress(Exception):
            await asyncio.wait_for(read_frame(r), 5)
    finally:
        w.close()


async def write_frame(w: asyncio.StreamWriter, msg: dict[str, Any]) -> None:
    body = json.dumps(msg, separators=(",", ":")).encode()
    w.write(struct.pack(">I", len(body)) + body)
    await w.drain()


async def read_frame(r: asyncio.StreamReader) -> dict[str, Any]:
    head = await r.readexactly(4)
    (n,) = struct.unpack(">I", head)
    if n > MAX_FRAME:
        raise HostResultError("frame too large")
    out: dict[str, Any] = json.loads(await r.readexactly(n))
    return out


def next_lineage(lineage: dict[str, Any] | None, stage: str, kind: str, sid: str) -> dict[str, Any]:
    """The lineage of a job this one starts on (stage, kind:sid); StageFailure plugin_loop
    when it would pass the hop cap or re-enter a pair already in the path."""
    lin = lineage if isinstance(lineage, dict) and lineage.get("hops") else {}
    path = [list(p) for p in lin.get("path") or []]
    pair = [stage, f"{kind}:{sid}"]
    hops = int(lin.get("hops") or 0) + 1
    if hops > MAX_HOPS or pair in path:
        raise StageFailure("plugin_loop", f"{stage} on {pair[1]} would loop", retryable=False)
    origin = lin.get("origin") or {"kind": "plugin"}
    return {"origin": origin, "hops": hops, "path": [*path, pair]}


def _requests(
    res: dict[str, Any], plugin_id: str, lineage: dict[str, Any] | None = None
) -> tuple[Request, ...]:
    from . import manifest as pm

    out = []
    for r in res.get("requests") or []:
        sub = r.get("subject") or {}
        try:
            sid = UUID(str(sub.get("id")))
        except ValueError:
            continue
        payload: dict[str, Any] = {}
        try:
            got = json.loads(r.get("payload") or "{}")
            payload = got if isinstance(got, dict) else {"value": got}
        except ValueError:
            payload = {}
        payload.pop(LINEAGE_KEY, None)
        stage = pm.queue_name(plugin_id, str(r["stage"]))
        kind = str(sub.get("kind", "video"))
        payload[LINEAGE_KEY] = next_lineage(lineage, stage, kind, str(sid))
        out.append(
            Request(
                stage=stage,
                subject_kind=kind,  # type: ignore[arg-type]
                subject_id=sid,
                reason=f"plugin:{plugin_id}",
                payload=payload,
            )
        )
    for ev in res.get("events") or []:
        sub = ev.get("subject") or {}
        try:
            sid = UUID(str(sub.get("id")))
        except ValueError:
            continue
        try:
            data = json.loads(ev.get("payload") or "{}")
        except ValueError:
            data = {}
        kind = str(sub.get("kind", "video"))
        for stage in ev.get("stages") or []:
            out.append(
                Request(
                    stage=str(stage),
                    subject_kind=kind,  # type: ignore[arg-type]
                    subject_id=sid,
                    reason=f"plugin-event:{plugin_id}/{ev.get('name', '')}",
                    payload={
                        "event": str(ev.get("name", "")),
                        "from": plugin_id,
                        "payload": data,
                        LINEAGE_KEY: next_lineage(lineage, str(stage), kind, str(sid)),
                    },
                )
            )
    return tuple(out)


def result_to_stage(
    res: dict[str, Any], plugin_id: str, lineage: dict[str, Any] | None = None
) -> StageResult:
    """The host's ``result`` frame as a StageResult, or the classified failure."""
    outcome = res.get("outcome")
    code = str(res.get("error_code") or "")
    detail = str(res.get("detail") or "")[:2000]
    if outcome == "ok":
        try:
            summary = json.loads(res.get("summary") or "{}")
        except ValueError:
            summary = {}
        if not isinstance(summary, dict):
            summary = {"value": summary}
        summary["plugin"] = {
            "fuel": res.get("fuel_used"),
            "peak_memory": res.get("peak_memory"),
            "wall_ms": res.get("wall_ms"),
        }
        return StageResult(
            output_hash=str(res.get("output_hash") or ""),
            summary=summary,
            requests=_requests(res, plugin_id, lineage),
        )
    if outcome == "trap":
        raise StageFailure("plugin_trap", detail, retryable=True)
    if outcome == "timeout":
        raise StageFailure("plugin_timeout", detail, retryable=True)
    if outcome == "memory":
        raise StageFailure("plugin_memory", detail, retryable=False)
    if outcome == "refused" and code == "cancelled":
        raise JobDeferred("plugin_cancelled", 5)
    if outcome == "refused" and code in (
        "plugin_capability",
        "plugin_scope",
        "plugin_quota",
        "plugin_loop",
    ):
        raise StageFailure(code, detail, retryable=False)
    if outcome == "failed":
        raise StageFailure("plugin_failed", detail, retryable=False)
    # a package the host would not load (bad manifest, forbidden import…): not retried
    raise StageFailure(code or "plugin_failed", detail, retryable=False)


async def _save_checkpoint(db: Any, job_id: int, checkpoint: str) -> None:
    try:
        await db.execute(
            "UPDATE processing_jobs SET progress = jsonb_set(COALESCE(progress, '{}'::jsonb),"
            " '{plugin_checkpoint}', to_jsonb($2::text)) WHERE id = $1",
            job_id,
            checkpoint,
        )
    except Exception:  # noqa: BLE001 - a lost checkpoint costs a rerun, never the job
        _log.warning("plugin_checkpoint_not_saved", job_id=job_id)


async def _last_checkpoint(db: Any, job_id: int) -> str | None:
    try:
        row = await db.fetchrow(
            "SELECT progress->>'plugin_checkpoint' AS cp FROM processing_jobs WHERE id = $1", job_id
        )
    except Exception:  # noqa: BLE001
        return None
    return None if row is None else row["cp"]


async def run_plugin_stage(db: Any, job: Any, ctx: Any) -> StageResult:
    """The handler of every plugin stage (``impls()`` of every :class:`PluginExtension`)."""
    stage = ctx.stage if hasattr(ctx, "stage") else None
    ext = getattr(stage, "extension", None)
    plugin_id = plugin_of(ext)
    if plugin_id is None:
        raise StageFailure("plugin_failed", f"{job.stage} is not a plugin stage", retryable=False)
    from .loader import MIGRATE_STAGE_ARG, load_installed

    installs = {p.plugin_id: p for p in await load_installed(db)}
    install = installs.get(plugin_id)
    if install is None:
        raise JobDeferred("plugin_held: not installed", 60)
    migrate = job.stage == install.migrate_stage
    payload = {k: v for k, v in (job.payload or {}).items() if k != LINEAGE_KEY}
    if migrate:
        # story 54.07 §5 step 4: the data's version is the row's; nothing to do once it is
        # up (a second job, or one a rollback left behind), and a plugin with no migrate
        # step has its data taken as it is
        if not install.migrating:
            return StageResult(output_hash=f"data-{install.target_data_version}")
        if not install.doc.get("migrate"):
            await record_migrated(db, install)
            return StageResult(output_hash=f"data-{install.target_data_version}")
        payload = {"from": install.data_version, "to": install.target_data_version}
    elif install.migrating:
        # its other stages wait until the migrate job brings the data up
        await ensure_migrate_job(db, install)
        raise JobDeferred(
            f"plugin_migrating: data {install.data_version} -> {install.target_data_version}", 30
        )
    minted = await _mint(int(job.id))
    cause: str | None = None
    res_msg: dict[str, Any] = {}
    try:
        sdoc = install.stage_doc(job.stage) or {}
        settings = await _settings(db, plugin_id)
        subject = job.subject
        run = {
            "type": "run",
            "invocation_id": minted["invocation_id"],
            "plugin_id": plugin_id,
            "version": install.version,
            "package_path": package_path(plugin_id, install.path),
            "export": sdoc.get("export", ""),
            "job": {
                "id": str(job.id),
                "stage": MIGRATE_STAGE_ARG if migrate else sdoc.get("name", job.stage),
                "subject": {"kind": str(subject.kind), "id": str(subject.id)},
                # the lineage is Maktaba's: the guest reads its payload without it
                "payload": json.dumps(payload),
                "attempt": int(job.attempts),
                "checkpoint": await _last_checkpoint(db, int(job.id)),
            },
            "settings": settings,
            "grants": minted.get("grants") or [],
            "token": minted["token"],
            "limits": {},
        }
        try:
            r, w = await _connect(plugind_socket())
        except OSError as exc:
            # no host (it is starting, or crashed and the supervisor restarts it): back to
            # the queue without an attempt
            raise JobDeferred(f"plugin_host_unavailable: {exc}", 30) from None
        try:
            await write_frame(w, run)
            while True:
                try:
                    msg = await read_frame(r)
                except (asyncio.IncompleteReadError, ConnectionError) as exc:
                    raise JobDeferred(f"plugin_host_lost: {exc}", 10) from None
                if msg.get("type") == "progress":
                    if msg.get("checkpoint"):
                        await _save_checkpoint(db, int(job.id), str(msg["checkpoint"]))
                    else:
                        await ctx.progress("plugin", int(msg.get("pct", 0)), 100)
                    continue
                if msg.get("type") == "result":
                    if msg.get("checkpoint"):
                        await _save_checkpoint(db, int(job.id), str(msg["checkpoint"]))
                    res_msg = msg
                    try:
                        out = result_to_stage(msg, plugin_id, minted.get("lineage"))
                    except StageFailure as f:
                        cause = f.kind
                        raise
                    cause = ""
                    if migrate:
                        # the guest's run("__migrate") succeeded: the data is up to date,
                        # and the plugin's other stages run again
                        await record_migrated(db, install)
                        return StageResult(output_hash=f"data-{install.target_data_version}")
                    return out
                raise StageFailure(
                    "plugin_failed", f"host answered {msg.get('type')}", retryable=True
                )
        except asyncio.CancelledError:
            # drained (its component off or held, past the grace): the host stops the job
            # and keeps its checkpoint; the job goes back to the queue
            await asyncio.shield(_cancel(minted["invocation_id"]))
            raise
        finally:
            w.close()
    finally:
        refused = await _end(minted["invocation_id"])
        if cause is not None or refused:
            from . import breaker

            await breaker.record(
                db,
                plugin_id,
                cause or "",
                ms=int(res_msg.get("wall_ms") or 0),
                memory=int(res_msg.get("peak_memory") or 0),
                refusals=refused,
            )


async def _settings(db: Any, plugin_id: str) -> str:
    try:
        row = await db.fetchrow(
            "SELECT value FROM app_settings WHERE key = $1", f"plugins.{plugin_id}"
        )
    except Exception:  # noqa: BLE001 - no settings table in a fake
        return "{}"
    if row is None or row["value"] is None:
        return "{}"
    v = row["value"]
    return v if isinstance(v, str) else json.dumps(v)


def decode_checkpoint(cp: str | None) -> bytes | None:
    return None if not cp else base64.b64decode(cp)


async def ensure_migrate_job(db: Any, install: Any) -> None:
    """Enqueue the plugin's one migrate job (catalog subject; enqueueing twice is a no-op)."""
    from ..engine.enqueue import enqueue_job
    from ..engine.graph import CATALOG_ID

    await enqueue_job(
        db,
        subject_kind="catalog",
        subject_id=CATALOG_ID,
        stage=install.migrate_stage,
        reason={"why": "plugin_migrate", "by": "system", "plugin": install.plugin_id},
    )


async def record_migrated(db: Any, install: Any) -> None:
    """The data is at the installed version's data_version now (story 54.07 §5 step 4):
    clear the row's mark, only if it is still the version and data this job migrated."""
    await db.execute(
        "UPDATE plugin_installs SET data_version = NULL"
        " WHERE plugin_id = $1 AND version = $2 AND data_version = $3",
        install.plugin_id,
        install.version,
        install.data_version,
    )
    _log.info(
        "plugin_data_migrated",
        plugin=install.plugin_id,
        version=install.version,
        to=install.target_data_version,
    )
