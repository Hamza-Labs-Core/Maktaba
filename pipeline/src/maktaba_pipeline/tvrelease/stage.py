"""Producers that make TV changes instead of changing the air (story 33.12), and the
stage step every change goes through (design 11.2 ``draft -> staged``).

* :func:`stage_write_channels` - ``write_channels`` with staging on: its write plan
  becomes a change's pending edits; ``write_mode = review`` parks it as a ``draft``
  (the Changes page shows its diff; its Approve is the queue's Apply), ``auto`` (or an
  approved digest) stages it at once;
* :func:`stale_to_draft` - the scheduler's ``stale`` flag with staging on: the channel
  goes into the open change; the live generation is never rewritten;
* :func:`join` - every update joins the one open change until it is approved
  (``merge.py`` has the rules); joined after it was checked, it goes back to draft and
  is checked again (:func:`collapse_open` brings a second open change into line);
* :func:`stage_change` - build the release and generation N+1 of a draft (the
  ``tv_stage`` job, and the producers above in auto mode).
"""

from __future__ import annotations

from datetime import datetime
from typing import Any

from .. import rules
from ..db import schema_compat
from ..engine.sql import is_pg, now
from ..log import get_logger
from .build import build_release
from .generation import build_generation, planned_swap
from .merge import live_defs, merge_pending, plan_pending, with_plan_layer
from .store import (
    Change,
    create_change,
    draft_with_digest,
    event,
    get_change,
    in_flight,
    open_changes,
    reopen,
    transition,
    update_change,
)

__all__ = [
    "ChangeInFlight",
    "auto_restage",
    "collapse_open",
    "enabled",
    "join",
    "stage_change",
    "stage_write_channels",
    "stale_to_draft",
    "write_plan_pending",
]

_log = get_logger()


class ChangeInFlight(RuntimeError):
    """Another change is in ``staged … swapping`` (README R9.4)."""

    def __init__(self, change_id: str) -> None:
        super().__init__(f"change_in_flight: {change_id}")
        self.change_id = change_id


async def enabled(db: Any) -> bool:
    """``rules.tv_staging.enabled`` (on by default: owner decision)."""
    cfg, _, _ = await rules.read(db, "tv_staging")
    return bool(cfg.enabled)


async def stage_change(
    db: Any, change_id: str, *, clock: datetime | None = None, actor_source: str = "system"
) -> str:
    """Build the release and schedule generation of a ``draft`` change and move it to
    ``staged``. Returns the change's state afterwards: ``staged``, ``draft`` (another
    change is in flight: it queues), or ``cancelled`` (it airs exactly what is live)."""
    clock = clock or now()
    ch = await get_change(db, change_id)
    if ch is None:
        raise LookupError(f"no TV change {change_id}")
    if ch.state != "draft":
        return ch.state
    other = await in_flight(db)
    if other is not None and other.id != change_id:
        await event(
            db, change_id, from_state="draft", to_state="draft", action="queued",
            actor_source=actor_source, detail={"in_flight": other.id},
        )  # fmt: skip
        raise ChangeInFlight(other.id)
    cfg, _, _ = await rules.read(db, "tv_staging")
    pending = ch.pending
    for _attempt in range(_STAGE_ATTEMPTS):
        rel, diff, kinds = await build_release(db, pending, created_by=ch.row.get("created_by"))
        boundaries: dict[str, str] = {}
        at = planned_swap(ch.row.get("swap_at"), int(cfg.verify_soak_seconds), clock)
        if rel is not None:
            # the draft names its release while the generation is built, so the daily
            # clean-up (which keeps the drafts' generations) never takes it half way
            await update_change(db, change_id, release_id=rel.id)
            boundaries = await build_generation(db, rel, at=at, pending=pending, clock=clock)
        async with db.transaction():
            # the drafts' lock (the API's tvchange.Drafts takes it for every save): an
            # edit saved into this draft while it was being built is not overwritten
            # by the built pending, and a second change cannot go in flight meanwhile
            if is_pg(db):
                await db.execute("SELECT pg_advisory_xact_lock(hashtext('tv_change_draft'))")
            cur = await get_change(db, change_id)
            if cur is None or cur.state != "draft":
                if cur is None:
                    raise LookupError(f"no TV change {change_id}")
                return cur.state
            if cur.pending != pending:
                pending = cur.pending  # edited while it was built: build it again
                continue
            other = await in_flight(db)
            if other is not None and other.id != change_id:
                raise ChangeInFlight(other.id)
            if rel is None:
                await transition(
                    db, change_id, from_states=("draft",), to="cancelled", action="noop",
                    actor_source=actor_source,
                    detail={"why": "the change airs exactly what is live"},
                    end_reason="noop", ended_at=clock, kinds=kinds,
                )  # fmt: skip
                return "cancelled"
            for s in diff.get("schedules") or []:
                b = boundaries.get(str(s.get("id")))
                if b:
                    s["divergence_at"] = b
            live = await db.fetchval(
                "SELECT live_release_id FROM tv_routing WHERE scope = 'cluster'"
            )
            await transition(
                db, change_id, from_states=("draft",), to="staged", action="stage",
                actor_source=actor_source,
                detail={"release_id": rel.id, "sched_gen": rel.sched_gen, "reused": rel.reused},
                release_id=rel.id, base_release_id=None if live is None else str(live),
                kinds=kinds, diff={**diff, "pending": pending}, anchor_at=at,
                # when it became staged, not when the job began: the build takes minutes
                # (4.5 on 2026-10-27), and the controller times the staging slot's start
                # from it, so a staged_at from the job's start failed the slot at once
                staged_at=now(),
                auto_rule=_strictest(cfg, kinds, cur.source.get("kind", "")),
            )  # fmt: skip
        _log.info(
            "tv_change_staged", change_id=change_id, release=rel.id, gen=rel.sched_gen,
            kinds=kinds,
        )  # fmt: skip
        return "staged"
    # still being edited: it stays a draft; the controller asks for the stage again
    await event(
        db, change_id, from_state="draft", to_state="draft", action="stage_retry",
        actor_source=actor_source, detail={"why": "edited while it was being staged"},
    )  # fmt: skip
    return "draft"


#: how many times a draft edited while it is being built is built again in one job
_STAGE_ATTEMPTS = 3


def _strictest(cfg: Any, kinds: list[str], source_kind: str = "") -> str:
    from .config import auto_rule

    return auto_rule(cfg, kinds, source_kind)


# --- the open change ----------------------------------------------------------------


async def auto_restage(db: Any) -> bool:
    """Whether the write mode stages a change by itself (``write_channels`` in
    ``auto``, the default): a change an update reopened is then staged again."""
    try:
        row = await db.fetchrow("SELECT write_mode FROM job_stages WHERE stage = 'write_channels'")
    except Exception as exc:  # noqa: BLE001 - a database without job_stages: the default
        schema_compat.fallback(_log, "write_channels_mode_unavailable", exc)
        return True
    return row is None or str(row["write_mode"]) == "auto"


async def join(
    db: Any,
    draft: Change,
    pending: dict[str, Any],
    *,
    actor_source: str,
    detail: dict[str, Any],
    fields: dict[str, Any] | None = None,
) -> None:
    """An update joins the open change (merge.py): its pending edits merged into the
    change's, ``updated_at`` bumped, an ``append`` event naming the update. The
    change keeps its id and number. A change joined after it was checked (``staged``
    … ``ready``) goes back to draft first (:func:`.store.reopen`), staged again when
    the write mode stages by itself."""
    if draft.state != "draft":
        draft = await reopen(
            db, draft, restage=await auto_restage(db), actor_source=actor_source,
            detail={"update": detail},
        )  # fmt: skip
    old = with_plan_layer(draft.pending, draft.row.get("digest"))
    merged = merge_pending(old, pending, await live_defs(db))
    await update_change(
        db, draft.id, **(fields or {}), diff={**(draft.row.get("diff") or {}), "pending": merged}
    )
    await event(
        db, draft.id, from_state="draft", to_state="draft", action="append",
        actor_source=actor_source, detail=detail,
    )  # fmt: skip


async def collapse_open(db: Any, *, actor_source: str = "system") -> Change | None:
    """The open change, with any other change no one approved joined into it: the
    oldest is the open change; a newer one (opened before the rule, or while the
    oldest was in flight) joins it - merged by merge.py, ended ``superseded`` with
    ``end_reason = joined_change`` - the API's ``Store.CollapseOpen``. The oldest goes
    back to draft when it was checked (:func:`.store.reopen`). Callers hold the
    drafts' lock. Idempotent: with one open change it only returns it."""
    opens = await open_changes(db)
    if len(opens) < 2:
        return opens[0] if opens else None
    target = await reopen(
        db, opens[0], restage=await auto_restage(db), actor_source=actor_source,
        detail={"joined": [n.id for n in opens[1:]]},
    )  # fmt: skip
    pending = with_plan_layer(target.pending, target.row.get("digest"))
    live = await live_defs(db)
    fields: dict[str, Any] = {}
    stamp = now()
    for n in opens[1:]:
        pending = merge_pending(pending, with_plan_layer(n.pending, n.row.get("digest")), live)
        if n.row.get("digest"):
            # the newer plan's: its approval is the queue's Apply of that digest
            fields.update(digest=n.row["digest"], source=n.source)
            if n.row.get("change_set"):
                fields["change_set"] = n.row["change_set"]
        await transition(
            db, n.id, from_states=(n.state,), to="superseded", action="supersede",
            actor_source=actor_source, detail={"joined": target.id, "number": target.number},
            superseded_by=target.id, ended_at=stamp, end_reason="joined_change",
        )  # fmt: skip
        await event(
            db, target.id, from_state="draft", to_state="draft", action="append",
            actor_source=actor_source,
            detail={"what": "joined_change", "kind": n.source.get("kind", ""), "change_id": n.id,
                    "number": n.number},
        )  # fmt: skip
    await update_change(
        db, target.id, **fields, diff={**(target.row.get("diff") or {}), "pending": pending}
    )
    _log.info("tv_change_collapsed", change_id=target.id, joined=[n.id for n in opens[1:]])
    return await get_change(db, target.id)


# --- write_channels -----------------------------------------------------------------


async def write_plan_pending(db: Any, wp: Any) -> dict[str, Any]:
    """A ``write_channels`` write plan as a change's pending edits (the channels as they
    will be, their links and the filler pools, applied at the swap)."""
    from .snapshot import DEF_COLUMNS

    channels: dict[str, Any] = {}
    links: list[dict[str, Any]] = []
    for cid, w in wp.update:
        row = await db.fetchrow(f"SELECT {', '.join(DEF_COLUMNS)} FROM channels WHERE id = $1", cid)
        from .snapshot import _def  # noqa: PLC0415 - shared row decoder

        cur = _def(row) if row is not None else {"id": cid}
        channels[cid] = {**cur, **_values(w.values)}
        links.append(
            {"key": w.key, "channel_id": cid, "title": w.values["name"], "digest": w.digest}
        )
    import uuid

    for w in wp.create:
        cid = str(uuid.uuid5(uuid.NAMESPACE_URL, f"maktaba:write_channels:{w.key}:{w.digest}"))
        channels[cid] = {
            "id": cid,
            "library_id": None,
            "slug": w.slug,
            **_values(w.values),
        }
        links.append(
            {"key": w.key, "channel_id": cid, "title": w.values["name"], "digest": w.digest}
        )
    for _, cid in wp.delete:
        channels[cid] = None
    pools = {
        "write": [
            {
                "key": pw.key, "name": pw.name, "items": _touched(pw), "pool_id": pw.pool_id,
                "add": pw.add, "drop": pw.drop, "retime": pw.retime, "rename": pw.rename,
            }
            for pw in wp.pools.write
        ],
        "delete": [list(x) for x in wp.pools.delete],
    }  # fmt: skip
    sched: list[str] = []
    if wp.pools.write or wp.pools.delete:
        # what fills the gaps changed: every channel the tool made plans its filler anew
        from ..engine.stages.write_channels import SUBJECT_KIND, TARGET

        sched = sorted(
            {
                str(r["external_id"])
                for r in await db.fetch(
                    "SELECT external_id FROM external_links"
                    " WHERE target = $1 AND subject_kind = $2",
                    TARGET,
                    SUBJECT_KIND,
                )
            }
            | {c for c, d in channels.items() if d is not None}
        )
    return {
        "channels": channels,
        "links": links,
        "unlinks": [k for k, _ in wp.delete],
        "pools": pools,
        "sched": sched,
    }


def _touched(pw: Any) -> dict[str, int]:
    """A pool write's clip durations the swap needs: the added and retimed clips only.
    The pool as it will be is the live pool with these applied (``pool_overlay``); the
    whole pool (thousands of clips, hundreds of pools per plan) is never stored in the
    change (it made every channel plan's diff ~50 MB)."""
    return {v: pw.items[v] for v in (*pw.add, *pw.retime) if v in pw.items}


def _values(v: dict[str, Any]) -> dict[str, Any]:
    return {
        k: v[k]
        for k in (
            "number", "name", "category", "mode", "mode_config", "transition", "enabled",
            "sort_order", "logo_path",
        )
        if k in v
    }  # fmt: skip


async def stage_write_channels(
    db: Any,
    *,
    wp: Any,
    changes: Any,
    job: Any,
    write_mode: str,
    approved: frozenset[str],
    plan_id: int | None,
) -> dict[str, Any]:
    """``write_channels`` with staging on (AC1 - AC3): returns the job summary's
    ``tv_change`` part. Nothing on the air changes here."""
    digest = changes.digest()
    source = {
        "kind": "write_channels",
        "job_id": job.id,
        "stage": "write_channels",
        "plan_id": plan_id,
        "digest": digest,
    }
    flying = await in_flight(db)
    if flying is not None and flying.row.get("digest") == digest:
        # this very plan is already staged (a re-run while it waits for approval)
        return {"id": flying.id, "digest": digest, "state": flying.state}
    pending = plan_pending(await write_plan_pending(db, wp))
    title = f"Channel plan #{plan_id}" if plan_id is not None else "Channel plan"
    async with db.transaction():
        if is_pg(db):
            # the drafts' lock (the API's tvchange.Drafts takes the same): one open change
            await db.execute("SELECT pg_advisory_xact_lock(hashtext('tv_change_draft'))")
        # the same plan (the queue's Apply re-running the change it approved), else the
        # open change: a newer plan joins it (merge.py), keeping its number
        draft = await draft_with_digest(db, digest) or await collapse_open(
            db, actor_source="write_channels"
        )
        if draft is None:
            cid = await create_change(
                db, state="draft", title=title, source=source, pending=pending,
                change_set=changes.to_json(), digest=digest, actor_source="write_channels",
            )  # fmt: skip
        else:
            cid = draft.id
            if draft.row.get("digest") != digest:
                fields: dict[str, Any] = {"source": source, "change_set": changes.to_json(),
                                          "digest": digest}  # fmt: skip
                if draft.source.get("kind") == "write_channels":
                    fields["title"] = title
                await join(
                    db, draft, pending, actor_source="write_channels", fields=fields,
                    detail={"what": "channel_plan", "kind": "write_channels", "plan_id": plan_id,
                            "digest": digest, "replaces": draft.row.get("digest")},
                )  # fmt: skip
    out: dict[str, Any] = {"id": cid, "digest": digest}
    if write_mode == "review" and digest not in approved:
        out["state"] = "draft"
        return out
    if digest in approved:
        # the Changes page's Approve was the queue's Apply (AC2): the change is approved
        ch = await get_change(db, cid)
        if ch is not None and not ch.row.get("approved_by"):
            await update_change(db, cid, approved_by=["queue_apply"], approved_at=now())
    try:
        out["state"] = await stage_change(db, cid, actor_source="write_channels")
    except ChangeInFlight as e:
        out["state"] = "draft"
        out["queued_behind"] = e.change_id
    if out["state"] == "cancelled":
        # nothing airs differently: only the bookkeeping (links) is written
        await _write_links(db, pending)
    return out


async def _write_links(db: Any, pending: dict[str, Any]) -> None:
    from ..engine.sql import to_ts
    from ..engine.stages.write_channels import SUBJECT_KIND, TARGET

    stamp = to_ts(db, now())
    for link in pending.get("links") or []:
        if (
            await db.fetchrow("SELECT 1 AS x FROM channels WHERE id = $1", link["channel_id"])
            is None
        ):
            continue
        await db.execute(
            "INSERT INTO external_links (target, subject_kind, subject_id, external_id, title,"
            " content_hash, created_by_tool, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)"
            " ON CONFLICT (target, subject_kind, subject_id) DO UPDATE SET"
            " external_id = EXCLUDED.external_id, title = EXCLUDED.title,"
            " content_hash = EXCLUDED.content_hash, updated_at = EXCLUDED.updated_at",
            TARGET, SUBJECT_KIND, link["key"], link["channel_id"], link["title"], link["digest"],
            True if is_pg(db) else 1, stamp,
        )  # fmt: skip


# --- the stale flag ------------------------------------------------------------------


async def stale_to_draft(db: Any, repo: Any) -> list[str]:
    """With staging on, a ``stale`` live channel is a schedule change (AC7): it goes
    into the open change (created if none) and its flag is cleared, so the
    top-up never rewrites the live generation. Off: nothing (today's behaviour)."""
    try:
        if not await enabled(db):
            return []
        ids = [str(c) for c in await repo.stale_channels()]
    except Exception as exc:  # noqa: BLE001 - a database before slot 0127
        if "no such" in str(exc).lower() or "does not exist" in str(exc):
            return []
        raise
    if not ids:
        return []
    async with db.transaction():
        if is_pg(db):
            # the drafts' lock (the API's tvchange.Drafts takes the same): one open
            # change even with several workers topping up
            await db.execute("SELECT pg_advisory_xact_lock(hashtext('tv_change_draft'))")
        draft = await collapse_open(db, actor_source="scheduler")
        if draft is None:
            await create_change(
                db, state="draft", title="Schedule regeneration",
                source={"kind": "schedule", "by": "scheduler"}, pending={"sched": ids},
                actor_source="scheduler",
            )  # fmt: skip
        else:
            await join(
                db, draft, {"sched": ids}, actor_source="scheduler",
                detail={"what": "schedule", "kind": "schedule", "sched": ids},
            )  # fmt: skip
        for cid in ids:
            from uuid import UUID

            await repo.clear_stale(UUID(cid))
    _log.info("tv_change_stale_drafted", channels=len(ids))
    return ids
