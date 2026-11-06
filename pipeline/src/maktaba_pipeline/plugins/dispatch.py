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
   cancelled, host gone      the job goes back to the queue, no attempt spent
   ========================  =============================================================

4. ends the token in a ``finally``.

The worker never imports plugin code: the code runs in the host, sandboxed.
"""

from __future__ import annotations

import asyncio
import base64
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
    if status != 200:
        # the plugin is off, held, not installed (any more), or its grants are waiting:
        # the job waits and spends no attempt (the runner stops claiming it, 54.07)
        raise JobDeferred(f"plugin_held: {body.get('code', status)}", 60)
    return body


async def _end(invocation_id: str) -> None:
    try:
        await asyncio.to_thread(_api, "DELETE", f"/internal/plugins/invocations/{invocation_id}")
    except Exception:  # noqa: BLE001 - the token expires on its own anyway
        _log.warning("plugin_invocation_end_failed", invocation=invocation_id)


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


def _requests(res: dict[str, Any], plugin_id: str) -> tuple[Request, ...]:
    from . import manifest as pm

    out = []
    for r in res.get("requests") or []:
        sub = r.get("subject") or {}
        try:
            sid = UUID(str(sub.get("id")))
        except ValueError:
            continue
        payload = None
        try:
            payload = json.loads(r.get("payload") or "{}") or None
        except ValueError:
            payload = None
        out.append(
            Request(
                stage=pm.queue_name(plugin_id, str(r["stage"])),
                subject_kind=str(sub.get("kind", "video")),  # type: ignore[arg-type]
                subject_id=sid,
                reason=f"plugin:{plugin_id}",
                payload=payload,
            )
        )
    return tuple(out)


def result_to_stage(res: dict[str, Any], plugin_id: str) -> StageResult:
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
            requests=_requests(res, plugin_id),
        )
    if outcome == "trap":
        raise StageFailure("plugin_trap", detail, retryable=True)
    if outcome == "timeout":
        raise StageFailure("plugin_timeout", detail, retryable=True)
    if outcome == "memory":
        raise StageFailure("plugin_memory", detail, retryable=False)
    if outcome == "refused" and code == "cancelled":
        raise JobDeferred("plugin_cancelled", 5)
    if outcome == "refused" and code in ("plugin_capability", "plugin_scope", "plugin_quota"):
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
    minted = await _mint(int(job.id))
    try:
        from .loader import load_installed

        installs = {p.plugin_id: p for p in await load_installed(db)}
        install = installs.get(plugin_id)
        if install is None:
            raise JobDeferred("plugin_held: not installed", 60)
        sdoc = install.stage_doc(job.stage) or {}
        settings = await _settings(db, plugin_id)
        subject = job.subject
        run = {
            "type": "run",
            "invocation_id": minted["invocation_id"],
            "plugin_id": plugin_id,
            "version": install.version,
            "package_path": install.path,
            "export": sdoc.get("export", ""),
            "job": {
                "id": str(job.id),
                "stage": sdoc.get("name", job.stage),
                "subject": {"kind": str(subject.kind), "id": str(subject.id)},
                "payload": json.dumps(job.payload or {}),
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
                    return result_to_stage(msg, plugin_id)
                raise StageFailure(
                    "plugin_failed", f"host answered {msg.get('type')}", retryable=True
                )
        finally:
            w.close()
    finally:
        await _end(minted["invocation_id"])


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
