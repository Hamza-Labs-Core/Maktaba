"""A TV change's swap commit and rollback for one queue job (Epic 54 wave 7, story 54.34).

Both write the stored TV settings (``rules.tv``, ``rules.tv_output``) in the same
transaction as the lineup, and ``app_settings`` is the queue engine's: no plugin role holds
it. So ``tv_swap`` and ``tv_rollback`` in ``tv.maktaba.livetv-slots``' runner ask the worker
(``tv_commit``, ``tv_rollback``), which runs them for the job's own ``payload.change_id``
(and a rollback's ``op_id``, ``reason`` and ``actor``) with its own connection. In-process,
the same functions run directly. The outcome travels as a mapping, so a missing change
(:class:`LookupError`) and a failed commit stay apart on both paths."""

from __future__ import annotations

from typing import Any

__all__ = ["change_id_of", "commit_for_job", "rollback_args", "rollback_for_job", "run_for_job"]


def change_id_of(payload: dict[str, Any] | None) -> str | None:
    cid = (payload or {}).get("change_id")
    return str(cid) if cid else None


def rollback_args(payload: dict[str, Any] | None) -> dict[str, Any]:
    """A rollback job's ``op_id``, ``reason`` and ``actor`` (the front door's operation, why
    and who), as ``rollback_change`` takes them."""
    p = payload or {}
    raw = p.get("op_id")
    op = int(raw) if isinstance(raw, int | str) and str(raw) != "" else None
    return {
        "op_id": op,
        "reason": str(p.get("reason") or "manual"),
        "actor_user_id": p.get("actor") or None,
    }


async def run_for_job(
    db: Any, kind: str, job_id: int, payload: dict[str, Any] | None
) -> dict[str, Any]:
    """The commit (``kind = "commit"``) or the rollback of the job's change, here:
    ``{"report": ...}``, ``{"lookup": message}`` or ``{"failed": message}``."""
    from .commit import commit_change
    from .rollback import rollback_change

    cid = change_id_of(payload)
    if cid is None:
        return {"lookup": "no change_id in the job's payload"}
    try:
        if kind == "commit":
            report = await commit_change(db, cid, actor_source=f"job:{job_id}")
        else:
            report = await rollback_change(
                db, cid, actor_source=f"job:{job_id}", **rollback_args(payload)
            )
    except LookupError as e:
        return {"lookup": str(e)}
    except Exception as e:  # noqa: BLE001 - the change went back to ready: an admin retries
        if kind != "commit":
            raise
        return {"failed": str(e)[:300]}
    return {"report": report}


async def _here_or_relayed(db: Any, kind: str, job: Any) -> dict[str, Any]:
    relay = getattr(db, "engine_relay", None)
    if relay is None:
        return await run_for_job(db, kind, int(job.id), job.payload)
    from ..plugins.native import decode_any

    got = await relay.ask("tv_commit" if kind == "commit" else "tv_rollback")
    return {str(k): decode_any(v) for k, v in dict(got or {}).items()}


async def commit_for_job(db: Any, job: Any) -> dict[str, Any]:
    return await _here_or_relayed(db, "commit", job)


async def rollback_for_job(db: Any, job: Any) -> dict[str, Any]:
    return await _here_or_relayed(db, "rollback", job)
