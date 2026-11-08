"""What a plugin's runner may publish through the worker (Epic 54, the engine relay's
``publish`` op).

A first-party plugin's runner has no database: its job's live events go back to the worker,
which inserts them on the ``jobs`` channel that every web client and app listens to. The
worker publishes one only when

1. its kind is in :data:`KINDS` (the engine's own lifecycle events, ``job.started``,
   ``job.finished`` and ``job.failed``, are the worker's alone);
2. its payload, as JSON, is at most :data:`MAX_PAYLOAD_BYTES`;
3. its payload matches the kind's schema (no unknown key, every required one, each of its
   type);
4. it is about the plugin's own topic, its running job: the job id and stage it names are the
   job's;
5. every id in it is inside the job's scope: its subject and library, compared as uuids
   (as the plugin API's statements compare them, ``sameID``).

Anything else is refused with :class:`PublishRefused` (the runner's code sees the error,
nothing is published) and logged with the plugin, the kind, the reason and the payload's
size, never its body (a job's label and log message can carry a user's titles).
"""

from __future__ import annotations

import json
from typing import Any
from uuid import UUID

from ..log import get_logger

__all__ = [
    "KINDS",
    "MAX_PAYLOAD_BYTES",
    "PublishRefused",
    "check_publish",
    "payload_size",
]

_log = get_logger()

#: The cap on one published payload, as compact JSON. The largest real one is a
#: ``job.progress`` whose job ref carries a long label and a progress document of a few
#: dozen steps (about 2 KB), or a ``job.log`` carrying a stage's ``data`` (a hundred queued
#: stages, about 10 KB); 64 KiB is well above both.
MAX_PAYLOAD_BYTES = 64 * 1024

_STR: dict[str, Any] = {"type": "string", "maxLength": 4096}
_OPT_STR: dict[str, Any] = {"type": ["string", "null"], "maxLength": 4096}
_INT: dict[str, Any] = {"type": "integer"}

#: ``JobRef`` (plan 06-11 section 7.2, ``engine.events.job_ref``)
JOB_REF: dict[str, Any] = {
    "type": "object",
    "additionalProperties": False,
    "required": ["id", "stage", "state", "subject_kind", "subject_id"],
    "properties": {
        "id": _INT,
        "stage": _STR,
        "state": _STR,
        "subject_kind": _STR,
        "subject_id": _STR,
        "subject_label": _OPT_STR,
        "library_id": _OPT_STR,
        "priority": _INT,
        "attempts": _INT,
        "max_attempts": _INT,
        "outcome": _OPT_STR,
        "error_cause": _OPT_STR,
        "created_at": _OPT_STR,
        "started_at": _OPT_STR,
        "finished_at": _OPT_STR,
        "progress": {"type": ["object", "null"]},
    },
}

#: The kinds a plugin may publish, each with its payload's schema (a subset of JSON Schema:
#: ``type``, ``properties``, ``required``, ``additionalProperties``, ``enum``,
#: ``maxLength``).
KINDS: dict[str, dict[str, Any]] = {
    # the job's progress (engine.context.StageContext.progress)
    "job.progress": {
        "type": "object",
        "additionalProperties": False,
        "required": ["job"],
        "properties": {"job": JOB_REF},
    },
    # one line of the job's log (engine.events.log_event's mirror)
    "job.log": {
        "type": "object",
        "additionalProperties": False,
        "required": ["job_id", "stage", "level", "kind", "message"],
        "properties": {
            "job_id": _INT,
            "stage": _STR,
            "subject_label": _OPT_STR,
            "level": {"type": "string", "enum": ["debug", "info", "warn", "error"]},
            "kind": _STR,
            "message": _STR,
            "data": {"type": ["object", "null"]},
        },
    },
}


class PublishRefused(Exception):
    """A publish the worker refused: ``reason`` is one of ``unknown_kind``, ``oversize``,
    ``schema``, ``topic`` or ``scope``."""

    def __init__(self, reason: str, detail: str) -> None:
        super().__init__(f"plugin_publish_refused: {reason}: {detail}")
        self.reason = reason
        self.detail = detail


def payload_size(payload: Any) -> int:
    """The payload's size as the compact JSON the worker would store."""
    return len(json.dumps(payload, separators=(",", ":"), default=str).encode())


def _type_of(v: Any) -> str:
    if v is None:
        return "null"
    if isinstance(v, bool):
        return "boolean"
    if isinstance(v, int):
        return "integer"
    if isinstance(v, float):
        return "number"
    if isinstance(v, str):
        return "string"
    if isinstance(v, list):
        return "array"
    if isinstance(v, dict):
        return "object"
    return "unknown"


def _schema_errors(n: dict[str, Any], v: Any, path: str) -> str | None:
    """The first way ``v`` does not match ``n``, as ``path: what``; None when it does."""
    t = _type_of(v)
    wants = n.get("type")
    if wants is not None:
        allowed = [wants] if isinstance(wants, str) else wants
        if t not in allowed and not (t == "integer" and "number" in allowed):
            return f"{path or '/'}: type {t}"
    if "enum" in n and v not in n["enum"]:
        return f"{path or '/'}: not one of the allowed values"
    if t == "string" and len(v) > n.get("maxLength", len(v)):
        return f"{path or '/'}: longer than {n['maxLength']}"
    if t == "object":
        props: dict[str, Any] = n.get("properties", {})
        for r in n.get("required", []):
            if r not in v:
                return f"{path}/{r}: missing"
        for k in sorted(v):
            if k not in props:
                if n.get("additionalProperties") is False:
                    return f"{path}/{k}: unknown key"
                continue
            err = _schema_errors(props[k], v[k], f"{path}/{k}")
            if err is not None:
                return err
    return None


def _same_id(a: Any, b: Any) -> bool:
    """Two ids name one row: both uuids and equal (never compared as text, as the plugin
    API's ``sameID``); None only matches None."""
    if a is None or b is None:
        return a is None and b is None
    try:
        return UUID(str(a)) == UUID(str(b))
    except ValueError:
        return False


def _topic_and_scope(kind: str, payload: dict[str, Any], job: Any) -> None:
    """The job and ids the payload names are the running job's own."""
    if kind == "job.progress":
        ref = payload["job"]
        job_id, stage = ref["id"], ref["stage"]
    else:
        ref = None
        job_id, stage = payload["job_id"], payload["stage"]
    if job_id != int(job.id) or stage != str(job.stage):
        raise PublishRefused("topic", f"job {job_id} of {str(stage)[:100]} is not this job")
    if ref is None:
        return
    if ref["subject_kind"] != str(job.subject_kind):
        raise PublishRefused(
            "scope", f"subject_kind {str(ref['subject_kind'])[:100]} is not this job's"
        )
    if not _same_id(ref["subject_id"], job.subject.id):
        raise PublishRefused("scope", "subject_id is not this job's subject")
    if not _same_id(ref.get("library_id"), getattr(job, "library_id", None)):
        raise PublishRefused("scope", "library_id is not this job's library")


def check_publish(plugin_id: str, kind: Any, payload: Any, job: Any) -> None:
    """Refuse (and log) a publish a plugin's runner may not make; return when it may."""
    size = payload_size(payload)
    try:
        if not isinstance(kind, str) or kind not in KINDS:
            raise PublishRefused(
                "unknown_kind", f"{str(kind)[:100]!r} is not a kind a plugin publishes"
            )
        if size > MAX_PAYLOAD_BYTES:
            raise PublishRefused("oversize", f"{size} bytes, over {MAX_PAYLOAD_BYTES}")
        err = _schema_errors(KINDS[kind], payload, "")
        if err is not None:
            raise PublishRefused("schema", err)
        _topic_and_scope(kind, payload, job)
    except PublishRefused as exc:
        _log.warning(
            "plugin_publish_refused",
            plugin=plugin_id,
            kind=str(kind)[:100],
            reason=exc.reason,
            detail=exc.detail,
            size=size,
            job_id=int(job.id),
        )
        raise
